import { sharedLink } from '../../utils/sharedLinks'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { Folder, AlertCircle, Loader2, ChevronRight, ChevronDown, Trash2, MessageSquare, Upload, Plus, MoreHorizontal, Move, Download, CheckSquare, Edit2, Link, Check } from 'lucide-react'
import type { PlannerFile } from '../../services/api-types'
import { isProtectedWorkspaceEntry } from '../../utils/workspaceSelection'
import { Tooltip, TooltipContent, TooltipTrigger, TooltipProvider } from '../ui/tooltip'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { copyToClipboard } from '../../utils/textUtils'
import { FileTypeIcon } from './fileTypeIcon'
import { useFileGitStore } from './FileGitContext'
import { type GitDecoration } from '../../stores/useWorkspaceGitStore'
import {
  flattenVisiblePlannerFiles,
  WORKSPACE_SCROLL_TO_FILE_EVENT,
  type WorkspaceScrollToFileDetail,
} from '../../utils/plannerFileTree'

interface PlannerFileListProps {
  readOnly?: boolean
  gitDecorations?: boolean
  selectedPath?: string | null
  files: PlannerFile[]
  loading: boolean
  error: string | null
  onFolderClick: (folder: PlannerFile) => void
  onFileClick: (file: PlannerFile) => void
  onFileDelete: (file: PlannerFile) => void
  onFolderDelete: (folder: PlannerFile) => void
  onDeleteAllFilesInFolder?: (folder: PlannerFile) => void
  onRetry: () => void
  expandedFolders: Set<string>
  chatFileContext: Array<{name: string, path: string, type: 'file' | 'folder'}>
  addFileToContext: (file: {name: string, path: string, type: 'file' | 'folder'}) => void
  highlightedFile?: string | null
  onFolderUpload?: (folderPath: string) => void
  onCreateFolder?: (parentFolder?: PlannerFile | string) => void
  onFileMove?: (file: PlannerFile) => void
  onFolderMove?: (folder: PlannerFile) => void
  onFileRename?: (file: PlannerFile) => void
  onFolderRename?: (folder: PlannerFile) => void
  onFileDownload?: (file: PlannerFile) => void
  downloadingFilePath?: string
  hideAddToChat?: boolean
  hideRootActions?: boolean
  protectedRootPath?: string
  isSelectionMode?: boolean
  selectedFiles?: Set<string>
  onToggleFileSelection?: (file: PlannerFile) => void
  onSelectFileAndEnterSelectionMode?: (file: PlannerFile) => void
  forceExpandFolders?: boolean
  scrollContainerRef?: RefObject<HTMLDivElement | null>
}

const VIRTUALIZE_FILE_COUNT = 200

// VS Code-style source-control marks: a letter and a colour per status.
const GIT_MARKS: Record<GitDecoration['status'], { letter: string; text: string; title: string }> = {
  modified: { letter: 'M', text: 'text-amber-500', title: 'Modified' },
  added: { letter: 'A', text: 'text-emerald-500', title: 'Added' },
  untracked: { letter: 'U', text: 'text-emerald-500', title: 'Untracked' },
  deleted: { letter: 'D', text: 'text-destructive', title: 'Deleted' },
  renamed: { letter: 'R', text: 'text-sky-500', title: 'Renamed' },
  conflict: { letter: '!', text: 'text-destructive', title: 'Merge conflict' },
}
const FILE_ROW_HEIGHT = 28
const FILE_ROW_OVERSCAN = 8

export default function PlannerFileList({
  readOnly = false,
  gitDecorations = !readOnly,
  selectedPath,
  files,
  loading,
  error,
  onFolderClick,
  onFileClick,
  onFileDelete,
  onFolderDelete,
  onDeleteAllFilesInFolder,
  onRetry,
  expandedFolders,
  chatFileContext,
  addFileToContext,
  highlightedFile,
  onFolderUpload,
  onCreateFolder,
  onFileMove,
  onFolderMove,
  onFileRename,
  onFolderRename,
  onFileDownload,
  downloadingFilePath,
  hideAddToChat = false,
  hideRootActions = false,
  protectedRootPath,
  isSelectionMode = false,
  selectedFiles = new Set(),
  onToggleFileSelection,
  onSelectFileAndEnterSelectionMode,
  forceExpandFolders = false,
  scrollContainerRef,
}: PlannerFileListProps) {
  const scrollToFile = useWorkspaceStore(state => state.scrollToFile)
  const workspaceOpenFilePath = useWorkspaceStore(state => state.showFileContent ? state.selectedFile?.path ?? null : null)
  const openFilePath = readOnly ? selectedPath : workspaceOpenFilePath
  const [copiedPath, setCopiedPath] = useState<string | null>(null)
  const [openActionsPath, setOpenActionsPath] = useState<string | null>(null)
  // Keyboard cursor in the tree (VS Code style): arrows move, Enter opens.
  const [focusedPath, setFocusedPath] = useState<string | null>(null)
  const gitFileStatus = useFileGitStore(state => state.fileStatus)
  const gitChangedDirs = useFileGitStore(state => state.changedDirs)
  const listRef = useRef<HTMLDivElement | null>(null)
  const [viewport, setViewport] = useState({ scrollTop: 0, height: 0, listTop: 0 })
  const visibleRows = useMemo(
    () => flattenVisiblePlannerFiles(files, expandedFolders, forceExpandFolders),
    [expandedFolders, files, forceExpandFolders],
  )

  useEffect(() => {
    const container = scrollContainerRef?.current
    if (!container) return

    const updateViewport = () => {
      setViewport(current => {
        const containerTop = container.getBoundingClientRect().top
        const listTop = listRef.current
          ? listRef.current.getBoundingClientRect().top - containerTop + container.scrollTop
          : 0
        const next = {
          scrollTop: container.scrollTop,
          height: container.clientHeight,
          listTop,
        }
        return current.scrollTop === next.scrollTop &&
          current.height === next.height &&
          current.listTop === next.listTop
          ? current
          : next
      })
    }
    let animationFrame: number | null = null
    const scheduleViewportUpdate = () => {
      if (animationFrame !== null) return
      animationFrame = window.requestAnimationFrame(() => {
        animationFrame = null
        updateViewport()
      })
    }
    const resizeObserver = typeof ResizeObserver === 'undefined'
      ? null
      : new ResizeObserver(scheduleViewportUpdate)
    resizeObserver?.observe(container)
    container.addEventListener('scroll', scheduleViewportUpdate, { passive: true })
    updateViewport()
    return () => {
      if (animationFrame !== null) window.cancelAnimationFrame(animationFrame)
      resizeObserver?.disconnect()
      container.removeEventListener('scroll', scheduleViewportUpdate)
    }
  }, [scrollContainerRef])

  useLayoutEffect(() => {
    const container = scrollContainerRef?.current
    const list = listRef.current
    if (!container || !list) return

    const containerTop = container.getBoundingClientRect().top
    const next = {
      scrollTop: container.scrollTop,
      height: container.clientHeight,
      listTop: list.getBoundingClientRect().top - containerTop + container.scrollTop,
    }
    setViewport(current => (
      current.scrollTop === next.scrollTop &&
      current.height === next.height &&
      current.listTop === next.listTop
        ? current
        : next
    ))
  }, [loading, scrollContainerRef, visibleRows.length])

  useEffect(() => {
    const container = scrollContainerRef?.current
    if (!container) return

    const revealFile = (event: Event) => {
      const filepath = (event as CustomEvent<WorkspaceScrollToFileDetail>).detail?.filepath
      if (!filepath) return
      const rowIndex = visibleRows.findIndex(row => (
        row.file.filepath === filepath || row.file.originalFilepath === filepath
      ))
      if (rowIndex < 0) return

      const rowTop = viewport.listTop + rowIndex * FILE_ROW_HEIGHT
      const centeredTop = rowTop - Math.max(0, (container.clientHeight - FILE_ROW_HEIGHT) / 2)
      container.scrollTo({ top: Math.max(0, centeredTop), behavior: 'smooth' })
    }

    window.addEventListener(WORKSPACE_SCROLL_TO_FILE_EVENT, revealFile)
    return () => window.removeEventListener(WORKSPACE_SCROLL_TO_FILE_EVENT, revealFile)
  }, [scrollContainerRef, viewport.listTop, visibleRows])

  // Render a single item (file or folder) with proper hierarchy
  const renderFileItem = (file: PlannerFile, depth: number = 0) => {
    const isExpanded = forceExpandFolders || expandedFolders.has(file.filepath)
    const isClickable = true // backend determines if content is viewable; binary files show error after fetch
    const fileName = file.filepath.split('/').pop() || file.filepath
    // Check both filepath (adjusted for display) and originalFilepath (original path)
    // This ensures workspace tool events can highlight files even when paths are adjusted in workflow mode
    const isHighlighted = !!highlightedFile && (highlightedFile === file.filepath || highlightedFile === file.originalFilepath)
    const isInContext = chatFileContext.some(ctx => ctx.path === file.filepath)
    const actionMenuPath = file.originalFilepath || file.filepath
    const isActionMenuOpen = openActionsPath === actionMenuPath
    
    const isSelected = selectedFiles.has(file.filepath)
    const isSelectable = !isProtectedWorkspaceEntry(file, protectedRootPath) && !(hideRootActions && !protectedRootPath && depth === 0 && file.type === 'folder')
    const isOpenFile = !!openFilePath && file.type !== 'folder' && (openFilePath === file.filepath || openFilePath === file.originalFilepath)
    const isFocused = focusedPath === file.filepath
    const gitKey = (file.originalFilepath || file.filepath).replace(/^\/+/, '')
    const gitMark = !gitDecorations || file.type === 'folder' ? undefined : gitFileStatus.get(gitKey)
    const gitFolderStatus = gitDecorations && file.type === 'folder' ? gitChangedDirs.get(gitKey) : undefined
    const gitStyle = gitMark ? GIT_MARKS[gitMark.status] : gitFolderStatus ? GIT_MARKS[gitFolderStatus] : undefined
    const hasActionMenu = file.type === 'folder'
      ? (!hideRootActions || depth > 0) && !!(onCreateFolder || onFolderUpload || onFolderMove)
      : !!(onFileMove || onFileDownload)

    return (
      <div key={file.filepath} className="group h-7 select-none">
        <div
          className={`
            flex h-7 items-center gap-1.5 rounded-sm px-2 transition-colors
            ${isSelectionMode ? 'cursor-default' : isClickable ? 'cursor-pointer hover:bg-muted' : 'cursor-default'}
            ${isOpenFile ? 'bg-primary/15 hover:bg-primary/20' : ''}
            ${isFocused ? 'ring-1 ring-inset ring-primary/60' : ''}
            ${isHighlighted ? 'bg-primary/10 ring-1 ring-inset ring-primary/40' : ''}
            ${isInContext ? 'bg-emerald-500/10 border-l-2 border-emerald-500' : ''}
            ${isSelected && isSelectionMode ? 'bg-primary/10' : ''}
          `}
          style={{ paddingLeft: `${depth * 12 + 6}px` }}
          aria-current={isOpenFile ? 'true' : undefined}
          data-filepath={file.filepath}
          data-original-filepath={file.originalFilepath || undefined}
          data-highlighted={isHighlighted ? 'true' : 'false'}
          onContextMenu={(event) => {
            if (isSelectionMode || !hasActionMenu) return
            event.preventDefault()
            setFocusedPath(file.filepath)
            setOpenActionsPath(actionMenuPath)
          }}
          onClick={() => {
            setFocusedPath(file.filepath)
            if (isSelectionMode && onToggleFileSelection) {
              if (isSelectable) onToggleFileSelection(file)
              else if (file.type === 'folder') onFolderClick(file)
            } else {
              if (file.type === 'folder') {
                onFolderClick(file)
              } else {
                onFileClick(file)
              }
            }
          }}
        >
          {/* Checkbox for selection mode */}
          {isSelectionMode && (
            <div className="flex-shrink-0" onClick={(e) => e.stopPropagation()}>
              <input
                type="checkbox"
                aria-label={`Select ${fileName}`}
                disabled={!isSelectable}
                checked={isSelected}
                onChange={() => onToggleFileSelection?.(file)}
                className="h-4 w-4 accent-primary cursor-pointer"
              />
            </div>
          )}
          
          {/* File/Folder Icon with expansion indicator */}
          <div className="flex w-4 flex-shrink-0 justify-center">
            {file.type === 'folder' && (isExpanded
              ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
              : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />)}
          </div>
          <FileTypeIcon name={fileName} folder={file.type === 'folder'} open={isExpanded} image={file.is_image} />

          {/* File Name - with reserved space for icons */}
          <div className="flex-1 min-w-0">
            <span className={`block truncate text-[13px] ${gitStyle ? gitStyle.text : isOpenFile ? 'font-medium text-foreground' : 'text-foreground/90'}`}>
              {fileName}
            </span>
          </div>
          {gitMark && gitStyle && (
            <span title={`${gitStyle.title}${gitMark?.staged ? ' (staged)' : ''}`} className={`w-3 shrink-0 text-center text-[11px] font-semibold ${gitStyle.text}`}>{gitStyle.letter}</span>
          )}
          {gitFolderStatus && (
            <span title="Contains changes" aria-label="Contains changes" className={`h-1.5 w-1.5 shrink-0 rounded-full bg-current ${GIT_MARKS[gitFolderStatus].text}`} />
          )}

          {/* Action buttons container - compact space */}
          <div className="flex items-center gap-1 flex-shrink-0">
            {/* Send to Chat button - hidden in workspace/workflow mode */}
            {!hideAddToChat && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    aria-label={`Send ${file.type || 'file'} to chat context`}
                    onClick={(e) => {
                      e.stopPropagation()
                      // Use the filepath as-is for context
                      addFileToContext({
                        name: fileName,
                        path: file.filepath,
                        type: (file.type || 'file') as 'file' | 'folder'
                      })
                      
                      // Auto-scroll to the file in workspace
                      scrollToFile(file.filepath)
                    }}
                    className="p-1 hover:bg-primary/10 rounded text-primary"
                  >
                    <MessageSquare className="w-3 h-3" />
                  </button>
                </TooltipTrigger>
                <TooltipContent>
                  <p>Send {file.type || 'file'} to chat context</p>
                </TooltipContent>
              </Tooltip>
            )}

            {/* More actions dropdown for folders */}
            {file.type === 'folder' && (!hideRootActions || depth > 0) && (onCreateFolder || onFolderUpload || onFolderMove) && (
              <div className="relative">
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setOpenActionsPath(current => current === actionMenuPath ? null : actionMenuPath)
                      }}
                      aria-label={`More actions for ${fileName}`}
                      aria-expanded={isActionMenuOpen}
                      aria-haspopup="menu"
                      className="p-1 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      <MoreHorizontal className="w-3 h-3" />
                    </button>
                  </TooltipTrigger>
                  <TooltipContent>
                    <p>More actions</p>
                  </TooltipContent>
                </Tooltip>
                
                {/* Dropdown menu */}
                <div
                  role="menu"
                  className={`absolute right-0 top-full mt-1 w-32 bg-card border border-border rounded-md shadow-md z-50 ${isActionMenuOpen ? 'block' : 'hidden'}`}
                >
                  <div className="py-1">
                    {onCreateFolder && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onCreateFolder(file)
                        }}
                        className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                      >
                        <Plus className="w-3 h-3" />
                        Create Folder
                      </button>
                    )}
                    {onFolderUpload && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onFolderUpload(file.originalFilepath || file.filepath)
                        }}
                        className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                      >
                        <Upload className="w-3 h-3" />
                        Upload File
                      </button>
                    )}
                    {onFolderMove && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onFolderMove(file)
                        }}
                        className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                      >
                        <Move className="w-3 h-3" />
                        Move
                      </button>
                    )}
                    {onFolderRename && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onFolderRename(file)
                        }}
                        className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                      >
                        <Edit2 className="w-3 h-3" />
                        Rename
                      </button>
                    )}
                    {onSelectFileAndEnterSelectionMode && (
                      <>
                        <div className="border-t border-border my-1"></div>
                        <button
                          onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onSelectFileAndEnterSelectionMode(file)
                          }}
                          className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                        >
                          <CheckSquare className="w-3 h-3" />
                          Select
                        </button>
                      </>
                    )}
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setOpenActionsPath(null)
                        const uid = useAuthStore.getState().user?.id || ''
                        const shareUrl = sharedLink(window.location.origin, 'folder', file.originalFilepath || file.filepath, uid)
                        copyToClipboard(shareUrl).then((ok) => {
                          if (ok) {
                            setCopiedPath(file.filepath)
                            setTimeout(() => setCopiedPath(null), 2000)
                          }
                        })
                      }}
                      className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                    >
                      {copiedPath === file.filepath
                        ? <><Check className="w-3 h-3 text-emerald-500" /><span className="text-emerald-600 dark:text-emerald-400">Copied!</span></>
                        : <><Link className="w-3 h-3" />Copy Share Link</>
                      }
                    </button>
                    {onDeleteAllFilesInFolder && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onDeleteAllFilesInFolder(file)
                        }}
                        className="w-full px-3 py-1 text-left text-xs text-destructive hover:bg-destructive/10 flex items-center gap-2"
                      >
                        <Trash2 className="w-3 h-3" />
                        Delete All Contents
                      </button>
                    )}
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setOpenActionsPath(null)
                        onFolderDelete(file)
                      }}
                      className="w-full px-3 py-1 text-left text-xs text-destructive hover:bg-destructive/10 flex items-center gap-2"
                    >
                      <Trash2 className="w-3 h-3" />
                      Delete
                    </button>
                  </div>
                </div>
              </div>
            )}

            {/* More actions dropdown for files */}
            {file.type !== 'folder' && (onFileMove || onFileDownload) && (
              <div className="relative">
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setOpenActionsPath(current => current === actionMenuPath ? null : actionMenuPath)
                      }}
                      aria-label={`More actions for ${fileName}`}
                      aria-expanded={isActionMenuOpen}
                      aria-haspopup="menu"
                      className="p-1 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      <MoreHorizontal className="w-3 h-3" />
                    </button>
                  </TooltipTrigger>
                  <TooltipContent>
                    <p>More actions</p>
                  </TooltipContent>
                </Tooltip>
                
                {/* Dropdown menu */}
                <div
                  role="menu"
                  className={`absolute right-0 top-full mt-1 w-40 bg-card border border-border rounded-md shadow-md z-50 ${isActionMenuOpen ? 'block' : 'hidden'}`}
                >
                  <div className="py-1">
                    {onFileDownload && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onFileDownload(file)
                        }}
                        disabled={downloadingFilePath === (file.originalFilepath || file.filepath)}
                        className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2 disabled:cursor-wait disabled:opacity-60"
                      >
                        {downloadingFilePath === (file.originalFilepath || file.filepath)
                          ? <><Loader2 className="w-3 h-3 animate-spin" />Downloading…</>
                          : <><Download className="w-3 h-3" />Download</>
                        }
                      </button>
                    )}
                    {onFileMove && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onFileMove(file)
                        }}
                        className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                      >
                        <Move className="w-3 h-3" />
                        Move
                      </button>
                    )}
                    {onFileRename && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onFileRename(file)
                        }}
                        className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                      >
                        <Edit2 className="w-3 h-3" />
                        Rename
                      </button>
                    )}
                    {onSelectFileAndEnterSelectionMode && (
                      <>
                        <div className="border-t border-border my-1"></div>
                        <button
                          onClick={(e) => {
                          e.stopPropagation()
                          setOpenActionsPath(null)
                          onSelectFileAndEnterSelectionMode(file)
                          }}
                          className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                        >
                          <CheckSquare className="w-3 h-3" />
                          Select
                        </button>
                      </>
                    )}
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setOpenActionsPath(null)
                        const uid = useAuthStore.getState().user?.id || ''
                        const shareUrl = sharedLink(window.location.origin, 'file', file.originalFilepath || file.filepath, uid)
                        copyToClipboard(shareUrl).then((ok) => {
                          if (ok) {
                            setCopiedPath(file.filepath)
                            setTimeout(() => setCopiedPath(null), 2000)
                          }
                        })
                      }}
                      className="w-full px-3 py-1 text-left text-xs text-foreground hover:bg-muted flex items-center gap-2"
                    >
                      {copiedPath === file.filepath
                        ? <><Check className="w-3 h-3 text-emerald-500" /><span className="text-emerald-600 dark:text-emerald-400">Copied!</span></>
                        : <><Link className="w-3 h-3" />Copy Share Link</>
                      }
                    </button>
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setOpenActionsPath(null)
                        onFileDelete(file)
                      }}
                      className="w-full px-3 py-1 text-left text-xs text-destructive hover:bg-destructive/10 flex items-center gap-2"
                    >
                      <Trash2 className="w-3 h-3" />
                      Delete
                    </button>
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>

      </div>
    )
  }

  const ensureRowVisible = (rowIndex: number) => {
    const container = scrollContainerRef?.current
    if (!container) return
    const rowTop = viewport.listTop + rowIndex * FILE_ROW_HEIGHT
    if (rowTop < container.scrollTop) container.scrollTop = rowTop
    else if (rowTop + FILE_ROW_HEIGHT > container.scrollTop + container.clientHeight) {
      container.scrollTop = rowTop + FILE_ROW_HEIGHT - container.clientHeight
    }
  }

  const handleTreeKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (isSelectionMode || visibleRows.length === 0) return
    if (!['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Enter', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    let index = visibleRows.findIndex(row => row.file.filepath === focusedPath)
    if (index < 0) index = Math.max(0, visibleRows.findIndex(row => row.file.filepath === openFilePath || row.file.originalFilepath === openFilePath))
    const row = visibleRows[index]
    const isFolder = row.file.type === 'folder'
    const isOpen = forceExpandFolders || expandedFolders.has(row.file.filepath)
    let next = index
    switch (event.key) {
      case 'ArrowDown': next = Math.min(visibleRows.length - 1, index + 1); break
      case 'ArrowUp': next = Math.max(0, index - 1); break
      case 'Home': next = 0; break
      case 'End': next = visibleRows.length - 1; break
      case 'ArrowRight':
        if (isFolder && !isOpen) { onFolderClick(row.file); return }
        if (isFolder) next = Math.min(visibleRows.length - 1, index + 1)
        break
      case 'ArrowLeft': {
        if (isFolder && isOpen) { onFolderClick(row.file); return }
        const parent = row.file.filepath.split('/').slice(0, -1).join('/')
        const parentIndex = visibleRows.findIndex(candidate => candidate.file.filepath === parent)
        if (parentIndex >= 0) next = parentIndex
        break
      }
      case 'Enter':
        if (isFolder) onFolderClick(row.file)
        else onFileClick(row.file)
        return
    }
    setFocusedPath(visibleRows[next].file.filepath)
    ensureRowVisible(next)
  }

  if (loading && files.length === 0) {
    return (
      <div className="flex items-center justify-center p-8">
        <Loader2 className="w-6 h-6 animate-spin text-muted-foreground" />
        <span className="ml-2 text-sm text-muted-foreground">Loading files...</span>
      </div>
    )
  }

  if (error && files.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center">
        <AlertCircle className="w-8 h-8 text-destructive mb-2" />
        <p className="text-sm text-destructive mb-4">{error}</p>
        <button
          onClick={onRetry}
          className="px-4 py-2 text-sm bg-destructive text-destructive-foreground rounded-md hover:bg-destructive/90 transition-colors"
        >
          Retry
        </button>
      </div>
    )
  }

  if (files.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center">
        <Folder className="w-8 h-8 text-muted-foreground mb-2" />
        <p className="text-sm text-muted-foreground">No files found</p>
      </div>
    )
  }

  const shouldVirtualize = visibleRows.length >= VIRTUALIZE_FILE_COUNT && viewport.height > 0
  const localScrollTop = Math.max(0, viewport.scrollTop - viewport.listTop)
  const firstVisibleIndex = shouldVirtualize
    ? Math.max(0, Math.floor(localScrollTop / FILE_ROW_HEIGHT) - FILE_ROW_OVERSCAN)
    : 0
  const lastVisibleIndex = shouldVirtualize
    ? Math.min(
      visibleRows.length,
      Math.ceil((localScrollTop + viewport.height) / FILE_ROW_HEIGHT) + FILE_ROW_OVERSCAN,
    )
    : visibleRows.length
  const renderedRows = visibleRows.slice(firstVisibleIndex, lastVisibleIndex)

  return (
    <TooltipProvider>
      <div
        ref={listRef}
        role="tree"
        aria-label="Files"
        tabIndex={0}
        onKeyDown={handleTreeKeyDown}
        className={`outline-none ${shouldVirtualize ? 'relative' : ''}`}
        style={shouldVirtualize ? { height: visibleRows.length * FILE_ROW_HEIGHT } : undefined}
      >
        {renderedRows.map((row, renderedIndex) => {
          if (!shouldVirtualize) return renderFileItem(row.file, row.depth)
          const absoluteIndex = firstVisibleIndex + renderedIndex
          return (
            <div
              key={row.file.filepath}
              className="absolute inset-x-0 hover:z-50 focus-within:z-50"
              style={{ height: FILE_ROW_HEIGHT, transform: `translateY(${absoluteIndex * FILE_ROW_HEIGHT}px)` }}
            >
              {renderFileItem(row.file, row.depth)}
            </div>
          )
        })}
      </div>
    </TooltipProvider>
  )
}
