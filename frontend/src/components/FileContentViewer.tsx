import { sharedLink } from '../utils/sharedLinks'
import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ArrowLeft, Download, Edit, FileText, Github, Link, Loader2, MoreHorizontal, Save, X } from 'lucide-react'
import { WorkspaceViewHeader } from './workflow/WorkspaceViewHeader'
import { MarkdownRenderer, MermaidDiagram } from './ui/MarkdownRenderer'
import { CsvRenderer } from './ui/CsvRenderer'
import { HtmlRenderer } from './ui/HtmlRenderer'
import { ConversationRenderer, isConversationJSON } from './ui/ConversationRenderer'
import { DiffRenderer } from './ui/DiffRenderer'
import { RenderedContentSearchBar, RenderedContentSearchButton, useRenderedContentSearch } from './ui/RenderedContentSearch'
import LazyModalFallback from './ui/LazyModalFallback'
import ConfirmationDialog from './ui/ConfirmationDialog'
import { useWorkspaceStore, useChatStore } from '../stores'
import { useAuthStore } from '../stores/useAuthStore'
import { isValidJSON } from '../utils/event-helpers'
import { prepareDomForPdfExport } from '../utils/pdfExport'
import { convertToSlackMarkdown } from '../utils/slackMarkdown'
import { isDiffFilePath, looksLikeDiffContent } from '../utils/diff'
import { copyToClipboard } from '../utils/textUtils'
import { isCodeFile } from '../utils/codeFileLanguage'
import {
  AUDIO_MIME_TYPES,
  VIDEO_MIME_TYPES,
  isAudioPath,
  isOfficeOrPdfPath,
  isTallSurfacePath,
  isVideoPath,
  mimeForExtension,
} from '../utils/fileTypes'

const FileEditor = lazy(() => import('./workspace/FileEditor'))
const PushToGistDialog = lazy(() => import('./workspace/PushToGistDialog'))
const XlsxRenderer = lazy(() => import('./ui/XlsxRenderer').then(module => ({ default: module.XlsxRenderer })))
const DocxRenderer = lazy(() => import('./ui/DocxRenderer').then(module => ({ default: module.DocxRenderer })))
const PdfRenderer = lazy(() => import('./ui/PdfRenderer').then(module => ({ default: module.PdfRenderer })))

const FileSurfaceFallback = () => (
  <div className="flex h-full min-h-40 items-center justify-center text-muted-foreground">
    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
    Loading viewer...
  </div>
)

// Builds (and revokes) one object URL for the media bytes the store holds.
// One hook serves audio and video; previously each had its own copy.
function useMediaObjectUrl(mimeType: string | null, data: ArrayBuffer | null): string | null {
  const [url, setUrl] = useState<string | null>(null)

  useEffect(() => {
    if (!mimeType || !data) {
      setUrl(current => {
        if (current) URL.revokeObjectURL(current)
        return null
      })
      return
    }

    const next = URL.createObjectURL(new Blob([data], { type: mimeType }))
    setUrl(current => {
      if (current) URL.revokeObjectURL(current)
      return next
    })

    return () => {
      URL.revokeObjectURL(next)
    }
  }, [mimeType, data])

  return url
}

const ICON_BUTTON_CLASS =
  'flex items-center p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted rounded-md transition-colors'

const CopyIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <rect x="9" y="9" width="13" height="13" rx="2" ry="2" strokeWidth={2} />
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1" />
  </svg>
)

const SlackIcon = () => (
  <svg className="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
    <path d="M5.042 15.165a2.528 2.528 0 0 1-2.52 2.523A2.528 2.528 0 0 1 0 15.165a2.527 2.527 0 0 1 2.522-2.52h2.52v2.52zm1.271 0a2.527 2.527 0 0 1 2.521-2.52 2.527 2.527 0 0 1 2.521 2.52v6.313A2.528 2.528 0 0 1 8.834 24a2.528 2.528 0 0 1-2.521-2.522v-6.313zM8.834 5.042a2.528 2.528 0 0 1-2.521-2.52A2.528 2.528 0 0 1 8.834 0a2.528 2.528 0 0 1 2.521 2.522v2.52H8.834zm0 1.271a2.528 2.528 0 0 1 2.521 2.521 2.528 2.528 0 0 1-2.521 2.521H2.522A2.528 2.528 0 0 1 0 8.834a2.528 2.528 0 0 1 2.522-2.521h6.312zM18.956 8.834a2.528 2.528 0 0 1 2.522-2.521A2.528 2.528 0 0 1 24 8.834a2.528 2.528 0 0 1-2.522 2.521h-2.522V8.834zm-1.27 0a2.528 2.528 0 0 1-2.523 2.521 2.527 2.527 0 0 1-2.52-2.521V2.522A2.527 2.527 0 0 1 15.163 0a2.528 2.528 0 0 1 2.523 2.522v6.312zM15.163 18.956a2.528 2.528 0 0 1 2.523 2.522A2.528 2.528 0 0 1 15.163 24a2.527 2.527 0 0 1-2.52-2.522v-2.522h2.52zm0-1.27a2.527 2.527 0 0 1-2.52-2.523 2.527 2.527 0 0 1 2.52-2.52h6.315A2.528 2.528 0 0 1 24 15.163a2.528 2.528 0 0 1-2.522 2.523h-6.315z"/>
  </svg>
)

const PdfIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M7 21h10a2 2 0 002-2V9l-5-5H7a2 2 0 00-2 2v13a2 2 0 002 2z" />
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M14 4v5h5" />
    <text x="7" y="18" fontSize="6" fontWeight="bold" fill="currentColor" stroke="none">PDF</text>
  </svg>
)

type PaneAction = {
  key: string
  label: string
  icon: React.ReactNode
  onSelect: () => void
  disabled?: boolean
}

/** The secondary header actions collapsed into one menu, so the header fits a
 * 240px pane without wrapping. Closes on select, outside click, and Escape. */
function PaneActionsMenu({ actions }: { actions: PaneAction[] }) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onMouseDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen(prev => !prev)}
        aria-label="More actions"
        aria-expanded={open}
        title="More actions"
        className={`${ICON_BUTTON_CLASS} ${open ? 'bg-muted text-foreground' : ''}`}
      >
        <MoreHorizontal className="w-4 h-4" />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 top-full z-50 mt-1 w-52 rounded-md border border-border bg-card p-1 shadow-md">
          {actions.map(action => (
            <button
              key={action.key}
              type="button"
              role="menuitem"
              disabled={action.disabled}
              onClick={() => {
                setOpen(false)
                action.onSelect()
              }}
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm text-foreground hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
            >
              {action.icon}
              <span>{action.label}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * File viewer/editor body: images, video, audio, PDF/XLSX/DOCX, CSV, HTML,
 * Mermaid, conversation logs, JSON, diffs, and markdown (with inline editing,
 * save+commit, PDF export, and Gist push). Reads everything
 * from `useWorkspaceStore` (selectedFile, fileContent, showFileContent, ...).
 *
 * It fills whatever box it's given. The workspace pane's Files view swaps
 * the file tree for this body while a file is open. The header collapses its
 * secondary actions into a menu so it fits a narrow pane, tall surfaces (PDF,
 * video, HTML) size to the pane, and keyboard shortcuts only fire while focus
 * is inside the viewer so Ctrl+E / Ctrl+S / Esc typed in the chat next to it
 * are left alone.
 */
export function FileContentViewerBody({ headerAction }: { headerAction?: React.ReactNode }) {
  const rootRef = useRef<HTMLDivElement>(null)
  const {
    selectedFile,
    fileContent,
    loadingFileContent,
    showFileContent,
    setShowFileContent,
    isEditMode,
    setIsEditMode,
    editedContent,
    setEditedContent,
    isSaving,
    getHasUnsavedChanges,
    saveFile,
    binaryFileData,
  } = useWorkspaceStore(useShallow(state => ({
    selectedFile: state.selectedFile,
    fileContent: state.fileContent,
    loadingFileContent: state.loadingFileContent,
    showFileContent: state.showFileContent,
    setShowFileContent: state.setShowFileContent,
    isEditMode: state.isEditMode,
    setIsEditMode: state.setIsEditMode,
    editedContent: state.editedContent,
    setEditedContent: state.setEditedContent,
    isSaving: state.isSaving,
    getHasUnsavedChanges: state.getHasUnsavedChanges,
    saveFile: state.saveFile,
    binaryFileData: state.binaryFileData,
  })))

  const videoObjectUrl = useMediaObjectUrl(
    selectedFile?.path ? mimeForExtension(selectedFile.path, VIDEO_MIME_TYPES) : null,
    binaryFileData,
  )
  const audioObjectUrl = useMediaObjectUrl(
    selectedFile?.path ? mimeForExtension(selectedFile.path, AUDIO_MIME_TYPES) : null,
    binaryFileData,
  )

  const [commitMessage, setCommitMessage] = useState('')
  const [showCommitDialog, setShowCommitDialog] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const addToast = useChatStore(state => state.addToast)
  const [pendingConfirm, setPendingConfirm] = useState<'discard-changes' | 'save-large-file' | 'close-viewer' | null>(null)
  const [isExportingPdf, setIsExportingPdf] = useState(false)
  const [showPushToGistDialog, setShowPushToGistDialog] = useState(false)
  const [shareCopied, setShareCopied] = useState(false)
  const [contentCopied, setContentCopied] = useState(false)
  const [slackCopied, setSlackCopied] = useState(false)
  const [failedImagePath, setFailedImagePath] = useState<string | null>(null)
  const markdownContentRef = useRef<HTMLDivElement>(null)
  const selectedFilePathLower = selectedFile?.path?.toLowerCase() || ''
  const isOfficeOrPdf = isOfficeOrPdfPath(selectedFilePathLower)
  // Edit is hidden for binary documents and for code files (code is written by the agent).
  const canEdit = !isOfficeOrPdf && !isCodeFile(selectedFile?.path || '')
  // Parsed once per render: the dispatch below used to re-parse the whole
  // file up to four times (search gate, conversation check, JSON check).
  const parsedJsonContent: unknown = useMemo(() => {
    try {
      return JSON.parse(fileContent) as unknown
    } catch {
      return undefined
    }
  }, [fileContent])
  const isRenderedMarkdownSearchAvailable = (
    showFileContent &&
    !loadingFileContent &&
    !isEditMode &&
    !!fileContent &&
    !fileContent.startsWith('data:image/') &&
    !isCodeFile(selectedFile?.path || '') &&
    !binaryFileData &&
    !selectedFilePathLower.endsWith('.csv') &&
    !selectedFilePathLower.endsWith('.html') &&
    !selectedFilePathLower.endsWith('.htm') &&
    !selectedFilePathLower.endsWith('.mmd') &&
    !selectedFilePathLower.endsWith('.mermaid') &&
    parsedJsonContent === undefined &&
    !looksLikeDiffContent(fileContent)
  )
  const renderedContentSearch = useRenderedContentSearch({
    targetRef: markdownContentRef,
    contentKey: `${selectedFile?.path || ''}:${fileContent.length}`,
    enabled: isRenderedMarkdownSearchAvailable,
  })

  // Initialize editedContent when entering edit mode
  useEffect(() => {
    if (isEditMode && editedContent === '' && fileContent) {
      setEditedContent(fileContent)
    }
  }, [isEditMode, fileContent, editedContent, setEditedContent])

  // Handle edit mode toggle
  const handleEdit = useCallback(() => {
    if (!canEdit) return
    setEditedContent(fileContent)
    setIsEditMode(true)
    setSaveError(null)
  }, [canEdit, fileContent, setEditedContent, setIsEditMode])

  // Handle download
  const handleDownload = useCallback(() => {
    if (!selectedFile || loadingFileContent) return

    // Binary previews keep their original bytes separately from text content.
    // Images already contain a downloadable data URL; keep that encoding intact.
    const isImageDataUrl = !binaryFileData && fileContent.startsWith('data:image/')
    const blob = new Blob([binaryFileData ?? fileContent], {
      type: binaryFileData
        ? (selectedFile.path.toLowerCase().endsWith('.pdf') ? 'application/pdf' : 'application/octet-stream')
        : 'text/plain;charset=utf-8',
    })
    const url = isImageDataUrl ? fileContent : URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = selectedFile.path.split('/').pop() || 'download'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    // Give the browser time to start reading the download before releasing it.
    if (!isImageDataUrl) setTimeout(() => URL.revokeObjectURL(url), 60_000)
  }, [selectedFile, fileContent, binaryFileData, loadingFileContent])

  // Handle cancel edit
  const handleCancelEdit = useCallback(() => {
    if (getHasUnsavedChanges()) {
      setPendingConfirm('discard-changes')
    } else {
      setEditedContent('')
      setIsEditMode(false)
      setSaveError(null)
    }
  }, [getHasUnsavedChanges, setEditedContent, setIsEditMode])

  const confirmPendingAction = useCallback(() => {
    if (pendingConfirm === 'discard-changes') {
      setEditedContent('')
      setIsEditMode(false)
      setSaveError(null)
    } else if (pendingConfirm === 'save-large-file') {
      setShowCommitDialog(true)
    } else if (pendingConfirm === 'close-viewer') {
      setEditedContent('')
      setIsEditMode(false)
      setShowFileContent(false)
    }
    setPendingConfirm(null)
  }, [pendingConfirm, setEditedContent, setIsEditMode, setShowFileContent])

  // Handle save
  const handleSave = useCallback(async () => {
    // Validate JSON if it's a JSON file
    if (selectedFile?.path?.toLowerCase().endsWith('.json') || isValidJSON(editedContent)) {
      try {
        JSON.parse(editedContent)
      } catch {
        setSaveError('Invalid JSON. Please fix the syntax errors before saving.')
        return
      }
    }

    // Check file size (warn if > 1MB)
    if (editedContent.length > 1024 * 1024) {
      setPendingConfirm('save-large-file')
      return
    }

    setShowCommitDialog(true)
  }, [selectedFile?.path, editedContent])

  // Handle save with commit message
  const handleSaveWithCommit = async () => {
    setSaveError(null)
    const result = await saveFile(commitMessage || undefined)
    if (result.success) {
      setShowCommitDialog(false)
      setCommitMessage('')
      setSaveError(null)
    } else {
      setSaveError(result.error || 'Failed to save file')
      // Keep dialog open on error
    }
  }


  // Handle export to PDF
  const handleExportPdf = useCallback(async () => {
    if (!markdownContentRef.current || !selectedFile) return
    setIsExportingPdf(true)
    const filename = (selectedFile.name || selectedFile.path?.split('/').pop() || 'document')
      .replace(/\.[^.]+$/, '') + '.pdf'
    const isElectron = !!window.electronAPI?.printToPDF

    try {
      const { restore } = await prepareDomForPdfExport(markdownContentRef.current)
      try {
        if (isElectron) {
          // Electron: printToPDF via IPC → direct file save
          await window.electronAPI?.printToPDF?.(filename)
        } else {
          // Web: clone content into a top-level wrapper for clean full-page printing
          const printTarget = markdownContentRef.current
          const clone = printTarget.cloneNode(true) as HTMLElement
          const wrapper = document.createElement('div')
          wrapper.id = 'pdf-print-wrapper'
          wrapper.style.cssText = 'position:absolute;top:0;left:0;width:100%;background:white;padding:40px;z-index:99999;'
          wrapper.appendChild(clone)
          // Set document title to filename for the PDF name
          const prevTitle = document.title
          document.title = filename.replace(/\.pdf$/, '')
          const style = document.createElement('style')
          style.textContent = `@media print {
            body > *:not(#pdf-print-wrapper) { display: none !important; }
            #pdf-print-wrapper { position: static !important; width: 100% !important; }
            #pdf-print-wrapper * { max-width: 100% !important; }
            html, body { overflow: visible !important; height: auto !important; }
          }`
          document.head.appendChild(style)
          document.body.appendChild(wrapper)
          await new Promise<void>((resolve) => {
            window.addEventListener('afterprint', () => resolve(), { once: true })
            window.print()
          })
          document.body.removeChild(wrapper)
          document.head.removeChild(style)
          document.title = prevTitle
        }
      } finally {
        restore()
      }
    } catch (err) {
      console.error('PDF export failed:', err)
      addToast('Could not export this file as PDF.', 'error')
    } finally {
      setIsExportingPdf(false)
    }
  }, [selectedFile, addToast])

  // Keyboard shortcuts
  useEffect(() => {
    if (!showFileContent) return

    const handleKeyDown = (e: KeyboardEvent) => {
      // The viewer sits next to the chat; only act on shortcuts typed while
      // focus is inside the viewer.
      const active = document.activeElement
      if (!(active instanceof Node) || !rootRef.current?.contains(active)) return
      // Ctrl+S or Cmd+S: Save
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault()
        if (isEditMode && getHasUnsavedChanges()) {
          handleSave()
        }
      }
      // Ctrl+E or Cmd+E: Toggle edit mode
      if ((e.ctrlKey || e.metaKey) && e.key === 'e') {
        e.preventDefault()
        if (isEditMode) {
          handleCancelEdit()
        } else {
          handleEdit()
        }
      }
      // Esc: leave edit mode, confirming first when there are unsaved
      // changes. Skipped while the commit dialog is open (it handles its
      // own Escape) so the two dialogs don't stack.
      if (e.key === 'Escape' && isEditMode && !showCommitDialog) {
        if (getHasUnsavedChanges()) {
          setPendingConfirm('discard-changes')
        } else {
          handleCancelEdit()
        }
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [showFileContent, showCommitDialog, isEditMode, getHasUnsavedChanges, handleSave, handleEdit, handleCancelEdit])

  const copyContent = useCallback(async () => {
    if (!fileContent) return
    if (await copyToClipboard(fileContent)) {
      setContentCopied(true)
      setTimeout(() => setContentCopied(false), 2000)
    }
  }, [fileContent])

  const copyAsSlack = useCallback(async () => {
    if (!fileContent) return
    const slack = convertToSlackMarkdown(fileContent)
    if (await copyToClipboard(slack)) {
      setSlackCopied(true)
      setTimeout(() => setSlackCopied(false), 2000)
    }
  }, [fileContent])

  const copyShareLink = useCallback(() => {
    if (!selectedFile?.path) return
    const uid = useAuthStore.getState().user?.id || ''
    const shareUrl = sharedLink(window.location.origin, 'file', selectedFile.path, uid)
    copyToClipboard(shareUrl).then((ok) => {
      if (ok) {
        setShareCopied(true)
        setTimeout(() => setShareCopied(false), 2000)
      }
    })
  }, [selectedFile?.path])

  const isMarkdownFile = selectedFilePathLower.endsWith('.md') || selectedFilePathLower.endsWith('.markdown')
  // PDF, video, and HTML surfaces fill the pane.
  const isTallSurface = isTallSurfacePath(selectedFile?.path || '')
  const tallSurfaceClass = 'h-full min-h-0'

  const paneActions: PaneAction[] = [
    { key: 'copy', label: contentCopied ? 'Copied!' : 'Copy content', icon: <CopyIcon />, onSelect: () => { void copyContent() } },
    { key: 'slack', label: slackCopied ? 'Copied!' : 'Copy as Slack format', icon: <SlackIcon />, onSelect: () => { void copyAsSlack() } },
    { key: 'share', label: shareCopied ? 'Copied!' : 'Copy share link', icon: <Link className="w-4 h-4" />, onSelect: copyShareLink },
    ...(isMarkdownFile ? [
      { key: 'pdf', label: isExportingPdf ? 'Exporting…' : 'Export as PDF', icon: isExportingPdf ? <Loader2 className="w-4 h-4 animate-spin" /> : <PdfIcon />, onSelect: () => { void handleExportPdf() }, disabled: isExportingPdf },
      { key: 'gist', label: 'Push to GitHub Gist', icon: <Github className="w-4 h-4" />, onSelect: () => setShowPushToGistDialog(true) },
    ] : []),
  ]

  // Prevent navigation with unsaved changes
  useEffect(() => {
    if (!showFileContent || !getHasUnsavedChanges()) return

    const handleBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }

    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [showFileContent, getHasUnsavedChanges])

  return (
    <>
      <div
        ref={rootRef}
        className="flex h-full min-h-0 flex-col bg-background"
        data-ui-file-path={selectedFile?.path || undefined}
        data-ui-file-ready={showFileContent && !loadingFileContent ? 'true' : 'false'}
      >
        <WorkspaceViewHeader
          icon={FileText}
          helpTopic="File"
          title={selectedFile?.path ? (
            <span className="inline-flex max-w-full items-center gap-1.5">
              <button
                type="button"
                onClick={() => {
                  if (getHasUnsavedChanges()) {
                    setPendingConfirm('close-viewer')
                  } else {
                    setShowFileContent(false)
                  }
                }}
                aria-label="Back to files"
                title="Back to files"
                className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                <ArrowLeft className="h-4 w-4" />
              </button>
              <span className="truncate">{selectedFile.path.split('/').pop() || selectedFile.path}</span>
            </span>
          ) : 'File'}
          subtitle={selectedFile?.path ? <span title={selectedFile.path}>{selectedFile.path}</span> : undefined}
          context={getHasUnsavedChanges() && (
            <span className="text-[10px] text-amber-500" title="Unsaved changes">●</span>
          )}
          actions={<>
            {!isEditMode ? (
              <div className="flex items-center gap-0.5">
                {canEdit && (
                  <button onClick={handleEdit} className={ICON_BUTTON_CLASS} title="Edit file (Ctrl+E)">
                    <Edit className="w-4 h-4" />
                  </button>
                )}
                <button onClick={handleDownload} disabled={loadingFileContent || !selectedFile} className={`${ICON_BUTTON_CLASS} disabled:opacity-50 disabled:cursor-not-allowed`} title="Download file">
                  <Download className="w-4 h-4" />
                </button>
                {isRenderedMarkdownSearchAvailable && (
                  <RenderedContentSearchButton search={renderedContentSearch} className={ICON_BUTTON_CLASS} />
                )}
                <PaneActionsMenu actions={paneActions} />
              </div>
            ) : (
              <>
                <button
                  onClick={handleCancelEdit}
                  className="flex items-center gap-1 px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-muted rounded-md transition-colors"
                  title="Cancel edit (Esc)"
                  disabled={isSaving}
                >
                  <X className="w-4 h-4" />
                  Cancel
                </button>
                {getHasUnsavedChanges() && (
                  <button
                    onClick={handleSave}
                    disabled={isSaving}
                    className="flex items-center gap-1 px-3 py-1.5 text-sm text-primary-foreground bg-primary hover:bg-primary/90 rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                    title="Save file (Ctrl+S)"
                  >
                    {isSaving ? (
                      <Loader2 className="w-4 h-4 animate-spin" />
                    ) : (
                      <Save className="w-4 h-4" />
                    )}
                    Save
                  </button>
                )}
              </>
            )}
            {headerAction}
          </>}
        />

        {isRenderedMarkdownSearchAvailable && (
          <RenderedContentSearchBar search={renderedContentSearch} />
        )}

        {/* Save Error Message */}
        {saveError && (
          <div className="px-4 py-2 bg-destructive/10 border-b border-destructive/20">
            <p className="text-sm text-destructive">{saveError}</p>
          </div>
        )}

        {/* Commit Message Dialog */}
        {showCommitDialog && (
          <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
            <div className="bg-card rounded-md shadow-md border border-border p-6 w-full max-w-md">
              <h3 className="text-sm font-semibold text-foreground mb-4">
                Save File
              </h3>
              <div className="mb-4">
                <label className="block text-sm font-medium text-foreground mb-2">
                  Commit Message (optional)
                </label>
                <input
                  type="text"
                  value={commitMessage}
                  onChange={(e) => setCommitMessage(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      handleSaveWithCommit()
                    } else if (e.key === 'Escape') {
                      setShowCommitDialog(false)
                      setCommitMessage('')
                    }
                  }}
                  className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  placeholder="Enter commit message..."
                  autoFocus
                />
              </div>
              <div className="flex justify-end gap-2">
                <button
                  onClick={() => {
                    setShowCommitDialog(false)
                    setCommitMessage('')
                  }}
                  className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground"
                >
                  Cancel
                </button>
                <button
                  onClick={handleSaveWithCommit}
                  disabled={isSaving}
                  className="px-4 py-2 text-sm text-primary-foreground bg-primary hover:bg-primary/90 rounded-md disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {isSaving ? 'Saving...' : 'Save'}
                </button>
              </div>
            </div>
          </div>
        )}

        {/* Discard-changes / large-file confirmations */}
        <ConfirmationDialog
          isOpen={pendingConfirm !== null}
          onClose={() => setPendingConfirm(null)}
          onConfirm={confirmPendingAction}
          title={pendingConfirm === 'save-large-file' ? 'Save large file?' : 'Discard changes?'}
          message={
            pendingConfirm === 'save-large-file'
              ? 'This file is larger than 1MB. Continue saving?'
              : 'You have unsaved changes. They will be lost.'
          }
          confirmText={pendingConfirm === 'save-large-file' ? 'Save' : 'Discard'}
          cancelText="Cancel"
          type="warning"
          ignoreWorkspaceAutoCollapse
        />

        {/* Scrollable Content */}
        <div className="min-h-0 flex-1 overflow-y-auto">
          {loadingFileContent ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <div className="w-8 h-8 border-4 border-border border-t-primary rounded-full animate-spin mx-auto mb-4"></div>
                <p className="text-muted-foreground">Loading file content...</p>
              </div>
            </div>
          ) : (
            <>
              {fileContent.startsWith('data:image/') ? (
                <div className="flex flex-col items-center justify-center h-full p-4">
                  {failedImagePath === selectedFile?.path ? (
                    <div className="rounded-md border border-border bg-muted p-8 text-center">
                      <p className="text-sm font-medium text-foreground">Couldn't load this image</p>
                      <p className="mt-1 text-xs text-muted-foreground">The file may be damaged. Nothing was changed.</p>
                    </div>
                  ) : (
                    <img
                      src={fileContent}
                      alt="File content"
                      className="max-w-full max-h-full object-contain rounded-md shadow-md"
                      onError={() => setFailedImagePath(selectedFile?.path ?? null)}
                    />
                  )}
                  <p className="text-sm text-muted-foreground mt-2">Image file</p>
                </div>
              ) : isEditMode ? (
                <div className="h-full overflow-hidden">
                  <Suspense fallback={<FileSurfaceFallback />}>
                    <FileEditor
                      value={editedContent}
                      filepath={selectedFile?.path || ''}
                      readOnly={false}
                      onChange={(value) => setEditedContent(value || '')}
                      height="100%"
                    />
                  </Suspense>
                </div>
              ) : (selectedFile?.path && isCodeFile(selectedFile.path) && !/\.html?$/i.test(selectedFile.path)) ? (
                <div className="h-full overflow-hidden">
                  <Suspense fallback={<FileSurfaceFallback />}>
                    <FileEditor
                      value={fileContent}
                      filepath={selectedFile.path}
                      readOnly={true}
                      height="100%"
                    />
                  </Suspense>
                </div>
              ) : (
                <div className={`${isTallSurface ? 'h-full min-h-0 ' : ''}${/\.(pdf|html?)$/i.test(selectedFile?.path || '') ? '' : 'p-6'}`}>
                  {(() => {
                    const filePath = selectedFile?.path?.toLowerCase() || ''

                    // CSV files
                    if (filePath.endsWith('.csv')) {
                      return <CsvRenderer content={fileContent} />
                    }

                    // Excel files (binary)
                    if ((filePath.endsWith('.xlsx') || filePath.endsWith('.xls')) && binaryFileData) {
                      return (
                        <Suspense fallback={<FileSurfaceFallback />}>
                          <XlsxRenderer data={binaryFileData} />
                        </Suspense>
                      )
                    }

                    // DOCX files (binary)
                    if (filePath.endsWith('.docx') && binaryFileData) {
                      return (
                        <Suspense fallback={<FileSurfaceFallback />}>
                          <DocxRenderer data={binaryFileData} />
                        </Suspense>
                      )
                    }

                    // PDF files (binary)
                    if (filePath.endsWith('.pdf') && binaryFileData) {
                      return (
                        <div className={`${tallSurfaceClass} w-full`}>
                          <Suspense fallback={<FileSurfaceFallback />}>
                            <PdfRenderer data={binaryFileData} />
                          </Suspense>
                        </div>
                      )
                    }

                    // Video files
                    if (isVideoPath(filePath) && videoObjectUrl) {
                      return (
                        <div className={`${tallSurfaceClass} w-full flex items-center justify-center bg-black rounded-md`}>
                          <video
                            controls
                            className="max-h-full max-w-full"
                            src={videoObjectUrl}
                          />
                        </div>
                      )
                    }

                    // Audio files
                    if (isAudioPath(filePath) && audioObjectUrl) {
                      return (
                        <div className="min-h-[260px] w-full flex items-center justify-center rounded-md border border-border bg-muted p-8">
                          <audio
                            controls
                            className="w-full max-w-3xl"
                            src={audioObjectUrl}
                          />
                        </div>
                      )
                    }

                    // HTML files
                    if (filePath.endsWith('.html') || filePath.endsWith('.htm')) {
                      return (
                        <div className={`${tallSurfaceClass} w-full`}>
                          <HtmlRenderer content={fileContent} />
                        </div>
                      )
                    }

                    // Mermaid diagram files (.mmd, .mermaid)
                    if (filePath.endsWith('.mmd') || filePath.endsWith('.mermaid')) {
                      return (
                        <div className="space-y-2">
                          <div className="flex items-center gap-2 text-sm text-muted-foreground">
                            <span className="font-medium">Mermaid Diagram</span>
                            <span className="text-xs bg-muted text-muted-foreground px-2 py-1 rounded font-mono">
                              {selectedFile?.path?.split('.').pop()}
                            </span>
                          </div>
                          <MermaidDiagram content={fileContent} />
                        </div>
                      )
                    }

                    // Conversation log files (-conversation.json)
                    if (selectedFile?.path && parsedJsonContent !== undefined && isConversationJSON(selectedFile.path, parsedJsonContent)) {
                      return <ConversationRenderer content={fileContent} />
                    }

                    // Check for JSON files
                    if (selectedFile?.path?.toLowerCase().endsWith('.json') || parsedJsonContent !== undefined) {
                      // Check if content looks like formatted JSON (has proper indentation)
                      const isFormattedJson = fileContent.includes('{\n  ') || fileContent.includes('[\n  ')

                      return (
                        <div className="space-y-2">
                          <div className="flex items-center gap-2 text-sm text-muted-foreground">
                            <span className="font-medium">JSON File</span>
                            {isFormattedJson && (
                              <span className="text-xs bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 px-2 py-1 rounded">
                                Formatted
                              </span>
                            )}
                          </div>
                          <div className="bg-muted border border-border rounded-md p-4">
                            <pre className="text-xs font-mono text-foreground overflow-x-auto whitespace-pre-wrap break-words leading-relaxed">
                              {fileContent}
                            </pre>
                          </div>
                        </div>
                      )
                    }

                    if ((selectedFile?.path && isDiffFilePath(selectedFile.path)) || looksLikeDiffContent(fileContent)) {
                      return <DiffRenderer content={fileContent} />
                    }

                    // Default: render as markdown
                    return (
                      <div ref={markdownContentRef} className="max-w-4xl mx-auto">
                        <div className="prose prose-sm max-w-none dark:prose-invert prose-headings:font-semibold prose-headings:text-gray-900 dark:prose-headings:text-gray-100 prose-p:text-gray-700 dark:prose-p:text-gray-300 prose-a:text-blue-600 dark:prose-a:text-blue-400 prose-a:no-underline hover:prose-a:underline prose-strong:text-gray-900 dark:prose-strong:text-gray-100 prose-code:text-blue-600 dark:prose-code:text-blue-400 prose-pre:bg-gray-50 dark:prose-pre:bg-gray-900 prose-blockquote:border-l-blue-500 prose-blockquote:text-gray-700 dark:prose-blockquote:text-gray-300">
                          <MarkdownRenderer
                            content={fileContent}
                            className="max-w-none"
                            showScrollbar={true}
                            basePath={selectedFile?.path}
                          />
                        </div>
                      </div>
                    )
                  })()}
                </div>
              )}
            </>
          )}
        </div>
      </div>

      {/* Push to Gist Dialog */}
      {showPushToGistDialog && (
        <Suspense fallback={<LazyModalFallback label="Loading GitHub sharing..." />}>
          <PushToGistDialog
            isOpen
            onClose={() => setShowPushToGistDialog(false)}
            fileContent={fileContent}
            fileName={selectedFile?.name || selectedFile?.path?.split('/').pop() || 'document.md'}
          />
        </Suspense>
      )}

    </>
  )
}
