import React, { useState, useEffect, useRef, useCallback, useMemo, useId } from 'react'
import { File, Folder, ChevronRight, ChevronDown } from 'lucide-react'
import type { PlannerFile } from '../services/api-types'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

import { isPlainPickerKey } from '../utils/composerReferences'
import { useComposerPicker, isComposerPickerEvent, type ComposerPickerProps } from '../hooks/useComposerPicker'

interface FileSelectionDialogProps extends ComposerPickerProps {
  isOpen: boolean
  onClose: () => void
  onSelectFile: (file: PlannerFile) => void
  /** When user presses Alt+→ on a folder, call with folder path so parent can set search context to that folder */
  onNavigateIntoFolder?: (folderPath: string) => void
  searchQuery: string
  position: { top: number; left: number }
  /** Extra top-level files/folders to show alongside workspace files (e.g., Chats/) */
  extraFiles?: PlannerFile[]
  /** Replaces the workspace tree entirely (product-profile chats list their own project). */
  files?: PlannerFile[]
}

export const FileSelectionDialog: React.FC<FileSelectionDialogProps> = ({
  isOpen,
  onClose,
  onSelectFile,
  onNavigateIntoFolder,
  searchQuery,
  position,
  extraFiles, files: filesOverride, inputRef, listId, onActiveOptionChange
}) => {
  const generatedId = useId()
  const optionListId = listId ?? generatedId
  const { files: workspaceFiles } = useWorkspaceStore()

  // Merge workspace files with extra files (deduplicated by filepath)
  const files = useMemo(() => {
    if (filesOverride) return filesOverride
    if (!extraFiles || extraFiles.length === 0) return workspaceFiles
    const existingPaths = new Set(workspaceFiles.map(f => f.filepath))
    const newFiles = extraFiles.filter(f => !existingPaths.has(f.filepath))
    return [...workspaceFiles, ...newFiles]
  }, [workspaceFiles, extraFiles, filesOverride])
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [filteredFiles, setFilteredFiles] = useState<PlannerFile[]>([])
  const [expandedFolders, setExpandedFolders] = useState<Set<string>>(new Set())
  const dialogRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  // Calculate fuzzy match score (how close the characters are together)
  const calculateFuzzyScore = useCallback((filepath: string, query: string): number => {
    let score = 0
    let queryIndex = 0
    let lastMatchIndex = -1
    
    for (let i = 0; i < filepath.length && queryIndex < query.length; i++) {
      if (filepath[i] === query[queryIndex]) {
        // Bonus for consecutive matches
        if (i === lastMatchIndex + 1) {
          score += 10
        }
        // Bonus for matches at word boundaries (after / or at start)
        if (i === 0 || filepath[i - 1] === '/') {
          score += 5
        }
        // Penalty for distance from last match
        if (lastMatchIndex >= 0) {
          score += Math.max(0, 10 - (i - lastMatchIndex))
        }
        
        lastMatchIndex = i
        queryIndex++
      }
    }
    
    return score
  }, [])

  // Memoize flattened files for search to avoid re-calculating on every search query change
  const allFlattenedFiles = useMemo(() => {
    const result: PlannerFile[] = []
    const flatten = (fileList: PlannerFile[]) => {
      for (const file of fileList) {
        result.push(file)
        if (file.children && file.children.length > 0) {
          flatten(file.children)
        }
      }
    }
    flatten(files)
    return result
  }, [files])

  // Flatten hierarchical structure while respecting expanded folders (for non-search view)
  const flattenWithExpandedFolders = useCallback((files: PlannerFile[], expandedFolders: Set<string>): PlannerFile[] => {
    const result: PlannerFile[] = []
    
    const flatten = (fileList: PlannerFile[], depth = 0) => {
      for (const file of fileList) {
        // Add the file/folder to result
        result.push({ ...file, depth })
        
        // If it's a folder and it's expanded, add its children
        if (file.type === 'folder' && expandedFolders.has(file.filepath) && file.children) {
          flatten(file.children, depth + 1)
        }
      }
    }
    
    flatten(files)
    return result
  }, [])

  // Filter files based on search query with VS Code-style fuzzy matching
  useEffect(() => {
    if (!searchQuery.trim()) {
      // Show hierarchical structure when no search, respecting expanded folders
      const flattened = flattenWithExpandedFolders(files, expandedFolders)
      setFilteredFiles(flattened)
      setSelectedIndex(0)
      return
    }

    const query = searchQuery.toLowerCase().trim()
    
    let filtered = allFlattenedFiles.filter(file => {
      // Filter by filepath with fuzzy matching (like VS Code) - includes both files and folders
      const filepath = file.filepath.toLowerCase()
      
      // VS Code-style fuzzy search: find query characters in order within the filepath
      let queryIndex = 0
      for (let i = 0; i < filepath.length && queryIndex < query.length; i++) {
        if (filepath[i] === query[queryIndex]) {
          queryIndex++
        }
      }
      
      // All query characters must be found in order
      const fuzzyMatch = queryIndex === query.length
      
      // Fallback: simple substring match for better user experience
      const substringMatch = filepath.includes(query)
      
      return fuzzyMatch || substringMatch
    })

    // When query ends with / we're "inside" a folder — show only items under that path
    if (query.endsWith('/')) {
      filtered = filtered.filter(file => file.filepath.toLowerCase().startsWith(query))
    }
    
    // Sort by relevance: exact matches first, then partial matches
    const sorted = filtered.sort((a, b) => {
      const aPath = a.filepath.toLowerCase()
      const bPath = b.filepath.toLowerCase()
      
      // Exact match bonus
      const aExact = aPath === query ? 1 : 0
      const bExact = bPath === query ? 1 : 0
      
      // Starts with query bonus
      const aStartsWith = aPath.startsWith(query) ? 1 : 0
      const bStartsWith = bPath.startsWith(query) ? 1 : 0
      
      // Filename match bonus (last part of path)
      const aFileName = aPath.split('/').pop() || ''
      const bFileName = bPath.split('/').pop() || ''
      const aFileNameMatch = aFileName.includes(query) ? 1 : 0
      const bFileNameMatch = bFileName.includes(query) ? 1 : 0
      
      // Fuzzy match score (how close the characters are together)
      const aFuzzyScore = calculateFuzzyScore(aPath, query)
      const bFuzzyScore = calculateFuzzyScore(bPath, query)
      
      // Calculate final score
      const aScore = aExact * 100 + aStartsWith * 50 + aFileNameMatch * 25 + aFuzzyScore * 10
      const bScore = bExact * 100 + bStartsWith * 50 + bFileNameMatch * 25 + bFuzzyScore * 10
      
      return bScore - aScore
    })

    setFilteredFiles(sorted)
    setSelectedIndex(0) // Reset selection when filtering
  }, [files, allFlattenedFiles, searchQuery, expandedFolders, flattenWithExpandedFolders, calculateFuzzyScore])

  // Limit displayed results to improve performance
  const displayLimit = 50
  const displayedFiles = useMemo(() => {
    return filteredFiles.slice(0, displayLimit)
  }, [filteredFiles, displayLimit])
  const remainingCount = Math.max(0, filteredFiles.length - displayLimit)

  useEffect(() => { setSelectedIndex(0) }, [isOpen])
  useEffect(() => {
    if (isOpen) onActiveOptionChange?.(displayedFiles[selectedIndex] ? `${optionListId}-${selectedIndex}` : undefined)
  }, [isOpen, displayedFiles, selectedIndex, optionListId, onActiveOptionChange])
  useComposerPicker(isOpen, dialogRef, inputRef, onClose)

  // Handle keyboard navigation
  const handleKeyDown = useCallback((e: KeyboardEvent) => {
    if (!isOpen || !isComposerPickerEvent(e, inputRef) || e.isComposing || e.keyCode === 229) return
    const folderKey = e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey && ['ArrowLeft', 'ArrowRight'].includes(e.key)
    if (!folderKey && !isPlainPickerKey(e)) return
    if (['ArrowLeft', 'ArrowRight'].includes(e.key) && !folderKey) return
    if (e.key === 'Tab') { onClose(); return }

    const visibleCount = displayedFiles.length

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setSelectedIndex(prev => 
          prev < visibleCount - 1 ? prev + 1 : 0
        )
        break
      case 'ArrowUp':
        e.preventDefault()
        setSelectedIndex(prev => 
          prev > 0 ? prev - 1 : Math.max(0, visibleCount - 1)
        )
        break
      case 'ArrowLeft':
        e.preventDefault()
        if (searchQuery.trim().endsWith('/') && onNavigateIntoFolder) {
          const path = searchQuery.replace(/\/$/, '').split('/').filter(Boolean)
          if (path.length > 0) {
            const parentPath = path.length === 1 ? '' : path.slice(0, -1).join('/') + '/'
            onNavigateIntoFolder(parentPath)
          }
        }
        break
      case 'ArrowRight': {
        e.preventDefault()
        const selectedItem = displayedFiles[selectedIndex]
        if (!selectedItem) break
        if (selectedItem.type === 'folder') {
          if (searchQuery.trim() && onNavigateIntoFolder) {
            // Go inside folder: parent updates input so search context becomes this folder
            onNavigateIntoFolder(selectedItem.filepath + '/')
          } else if (!searchQuery.trim()) {
            // Hierarchical view: toggle folder expansion
            setExpandedFolders(prev => {
              const newSet = new Set(prev)
              if (newSet.has(selectedItem.filepath)) {
                newSet.delete(selectedItem.filepath)
              } else {
                newSet.add(selectedItem.filepath)
              }
              return newSet
            })
          }
        }
        break
      }
      case 'Enter':
        e.preventDefault()
        if (displayedFiles[selectedIndex]) {
          onSelectFile(displayedFiles[selectedIndex])
        } else { onClose() }
        break
      case 'Escape':
        e.preventDefault()
        onClose()
        break
    }
  }, [isOpen, displayedFiles, selectedIndex, searchQuery, onNavigateIntoFolder, onSelectFile, onClose, inputRef])


  // Add keyboard event listeners
  useEffect(() => {
    if (isOpen) {
      document.addEventListener('keydown', handleKeyDown)
      return () => document.removeEventListener('keydown', handleKeyDown)
    }
  }, [isOpen, handleKeyDown])

  // Scroll selected item into view
  useEffect(() => {
    if (listRef.current && selectedIndex >= 0) {
      const selectedElement = listRef.current.children[selectedIndex] as HTMLElement
      if (selectedElement) {
        selectedElement.scrollIntoView({
          block: 'nearest',
          behavior: 'smooth'
        })
      }
    }
  }, [selectedIndex])

  if (!isOpen) return null

  const getFileIcon = (file: PlannerFile) => {
    if (file.type === 'folder') {
      return <Folder className="w-4 h-4 text-primary" />
    }
    
    // Get file extension for icon styling
    const extension = file.filepath.split('.').pop()?.toLowerCase()
    
    // Color code by file type using theme colors
    if (['js', 'ts', 'jsx', 'tsx'].includes(extension || '')) {
      return <File className="w-4 h-4 text-warning" />
    } else if (['py'].includes(extension || '')) {
      return <File className="w-4 h-4 text-success" />
    } else if (['go'].includes(extension || '')) {
      return <File className="w-4 h-4 text-primary" />
    } else if (['md', 'txt'].includes(extension || '')) {
      return <File className="w-4 h-4 text-muted-foreground" />
    } else if (['json', 'yaml', 'yml'].includes(extension || '')) {
      return <File className="w-4 h-4 text-primary" />
    } else {
      return <File className="w-4 h-4 text-muted-foreground" />
    }
  }

  const highlightMatch = (text: string, query: string) => {
    if (!query.trim()) return text

    const queryLower = query.toLowerCase()

    // Split query into parts for highlighting
    const queryParts = queryLower.split(/[/\\]/).filter(part => part.length > 0)

    if (queryParts.length === 0) return text

    // Create a capturing regex so split() places matches at odd indices (avoiding /g lastIndex state bugs).
    const regex = new RegExp(`(${queryParts.map(part => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'gi')
    const parts = text.split(regex)

    return parts.map((part, index) =>
      index % 2 === 1 ? (
        <mark key={index} className="bg-warning/20 text-warning px-0.5 rounded">
          {part}
        </mark>
      ) : part
    )
  }

  // For deep paths: show basename + truncated parent path (from the left so the end is visible)
  const maxDirChars = 42
  const formatPathParts = (filepath: string): { basename: string; dirDisplay: string | null } => {
    const lastSlash = filepath.lastIndexOf('/')
    if (lastSlash === -1) return { basename: filepath, dirDisplay: null }
    const basename = filepath.slice(lastSlash + 1)
    const dirPath = filepath.slice(0, lastSlash)
    const dirDisplay = dirPath.length > maxDirChars ? `…${dirPath.slice(-(maxDirChars - 1))}` : dirPath
    return { basename, dirDisplay }
  }

  return (
    <div
      ref={dialogRef}
      className="fixed z-50 bg-background border border-border rounded-lg shadow-lg flex flex-col overflow-hidden"
      style={{
        top: Math.max(8, position.top),
        left: Math.max(8, Math.min(position.left, window.innerWidth - Math.min(420, window.innerWidth - 16) - 8)),
        width: 'min(420px, calc(100vw - 16px))',
        maxHeight: `min(320px, calc(100vh - ${Math.max(8, position.top) + 8}px))`
      }}
    >
      {/* Header - compact: one line for title + filter, one for shortcuts */}
      <div className="px-3 py-1.5 border-b border-border bg-secondary">
        <div className="flex items-center gap-2 text-sm flex-wrap">
          <span className="text-muted-foreground">Add file or folder</span>
          {searchQuery ? (
            <>
              <span className="text-muted-foreground">·</span>
              <span
                className="font-mono text-xs px-2 py-0.5 rounded bg-muted/80 text-foreground truncate max-w-[180px]"
                title={searchQuery.includes('/') ? `${searchQuery}\n\nUse / in paths — it won’t open slash commands.` : searchQuery}
              >
                {searchQuery || ' '}
              </span>
              <span className="text-muted-foreground text-xs">{filteredFiles.length} results</span>
            </>
          ) : (
            <span className="text-muted-foreground text-xs">(type @ then path to filter)</span>
          )}
          <span className="text-[11px] text-muted-foreground ml-auto flex items-center gap-1.5 flex-shrink-0">
            <kbd className="px-1 py-0.5 bg-muted rounded font-mono">↑</kbd><kbd className="px-1 py-0.5 bg-muted rounded font-mono">↓</kbd>
            <kbd className="px-1 py-0.5 bg-muted rounded font-mono">Alt+→</kbd>
            <kbd className="px-1 py-0.5 bg-muted rounded font-mono">Enter</kbd>
            <kbd className="px-1 py-0.5 bg-muted rounded font-mono">Esc</kbd>
          </span>
        </div>
      </div>

      {/* File List */}
      <div 
        ref={listRef}
        id={optionListId}
        role="listbox"
        aria-label="Files and folders"
        className="overflow-y-auto min-h-0 flex-1"
      >
        {displayedFiles.length === 0 ? (
          <div className="px-3 py-5 text-center text-muted-foreground text-sm space-y-1">
            {searchQuery ? (
              <>
                <p>No files or folders match “{searchQuery}”</p>
                <p className="text-xs">Try a shorter path or different spelling. Paths can include <kbd className="px-1 py-0.5 bg-muted rounded font-mono text-[10px]">/</kbd>.</p>
              </>
            ) : (
              <p>No files available. Type after <kbd className="px-1 py-0.5 bg-muted rounded font-mono text-[10px]">@</kbd> to filter by path.</p>
            )}
          </div>
        ) : (
          <>
            {displayedFiles.map((file, index) => {
              const { basename, dirDisplay } = formatPathParts(file.filepath)
              return (
                <div
                  key={file.filepath}
                  id={`${optionListId}-${index}`}
                  role="option"
                  aria-selected={index === selectedIndex}
                  onMouseDown={event => event.preventDefault()}
                  title={file.filepath}
                  className={`px-3 py-2.5 cursor-pointer flex items-center gap-2 text-sm transition-colors ${
                    index === selectedIndex
                      ? 'bg-primary/10 text-primary border-l-2 border-primary'
                      : 'hover:bg-secondary'
                  }`}
                  onClick={() => onSelectFile(file)}
                  style={{ paddingLeft: `${12 + (file.depth || 0) * 16}px` }}
                >
                  {getFileIcon(file)}
                  <div className="flex-1 min-w-0">
                    {dirDisplay !== null ? (
                      <>
                        <div className="truncate font-medium">
                          {highlightMatch(basename, searchQuery)}
                        </div>
                        <div className="truncate text-xs text-muted-foreground mt-0.5">
                          {highlightMatch(dirDisplay, searchQuery)}
                        </div>
                      </>
                    ) : (
                      <div className="truncate">
                        {highlightMatch(file.filepath, searchQuery)}
                      </div>
                    )}
                  </div>
                  {file.type === 'folder' && (
                    expandedFolders.has(file.filepath) ? (
                      <ChevronDown className="w-3 h-3 text-muted-foreground flex-shrink-0" />
                    ) : (
                      <ChevronRight className="w-3 h-3 text-muted-foreground flex-shrink-0" />
                    )
                  )}
                </div>
              )
            })}
            {remainingCount > 0 && (
              <div className="px-3 py-2 text-center text-xs text-muted-foreground bg-secondary/30 italic border-t border-border/50">
                ...and {remainingCount} more results. Keep typing to narrow down.
              </div>
            )}
          </>
        )}
      </div>

      {/* Footer - repeat shortcuts for visibility when list is long */}
      <div className="px-3 py-2 border-t border-border bg-secondary text-xs text-muted-foreground">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="flex items-center gap-1.5 flex-wrap">
            <kbd className="px-1 py-0.5 bg-muted dark:bg-muted/80 rounded font-mono text-[11px]">↑</kbd>
            <kbd className="px-1 py-0.5 bg-muted dark:bg-muted/80 rounded font-mono text-[11px]">↓</kbd>
            navigate
            <span className="text-border">·</span>
            <kbd className="px-1 py-0.5 bg-muted dark:bg-muted/80 rounded font-mono text-[11px]">Alt+→</kbd>
            expand
            <span className="text-border">·</span>
            <kbd className="px-1 py-0.5 bg-muted dark:bg-muted/80 rounded font-mono text-[11px]">Enter</kbd>
            select
            <span className="text-border">·</span>
            <kbd className="px-1 py-0.5 bg-muted dark:bg-muted/80 rounded font-mono text-[11px]">Esc</kbd>
            close
          </span>
        </div>
      </div>
    </div>
  )
}

export default FileSelectionDialog
