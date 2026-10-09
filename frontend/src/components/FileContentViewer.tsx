import { useFileGit, useFileGitStore } from './workspace/FileGitContext'
import { sharedLink } from '../utils/sharedLinks'
import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ArrowLeft, Download, FileText, GitCommitHorizontal, GitCompare, Github, History, Link, Loader2, MoreHorizontal, Pencil } from 'lucide-react'
import { WorkspaceViewHeader } from './workflow/WorkspaceViewHeader'
import type { FileViewerSource } from './workspace/fileWorkspaceSource'
import { FileBreadcrumbs, FileTabs } from './workspace/FileTabs'
import { repoForPath } from '../stores/useWorkspaceGitStore'
import { useGitLineChanges } from '../hooks/useGitLineChanges'
import { MarkdownRenderer, MermaidDiagram } from './ui/MarkdownRenderer'
import { CsvRenderer } from './ui/CsvRenderer'
import { HtmlRenderer } from './ui/HtmlRenderer'
import { ConversationRenderer, isConversationJSON } from './ui/ConversationRenderer'
import { DiffRenderer } from './ui/DiffRenderer'
import { RenderedContentSearchBar, RenderedContentSearchButton, useRenderedContentSearch } from './ui/RenderedContentSearch'
import LazyModalFallback from './ui/LazyModalFallback'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useChatStore } from '../stores/useChatStore'
import { useAuthStore } from '../stores/useAuthStore'
import { prepareDomForPdfExport } from '../utils/pdfExport'
import { convertToSlackMarkdown } from '../utils/slackMarkdown'
import { isDiffFilePath, looksLikeDiffContent } from '../utils/diff'
import { copyToClipboard } from '../utils/textUtils'
import { agentApi } from '../services/api'
import { openWorkspaceFile, isViewableBinaryFile } from '../utils/openWorkspaceFile'
import { readRawFile, saveEditedFile } from '../utils/editRawFile'
import { isCodeFile } from '../utils/codeFileLanguage'
import {
  AUDIO_MIME_TYPES,
  VIDEO_MIME_TYPES,
  isAudioPath,
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
// A file the editor can hold as text: not an image, document or media file.
const NON_TEXT_EXTENSIONS = /\.(png|jpe?g|gif|webp|bmp|ico|svg|pdf|docx?|xlsx?|pptx?|zip|gz|tar|mp[34]|mov|webm|wav|m4a|ogg|flac)$/i

/**
 * @param editable Offers Edit for the signed-in person's own files (the server still refuses writes they may not make).
 *   Editing loads and saves the file's raw text: the view above unescapes "\\n" and reformats JSON, which would corrupt
 *   code if saved back.
 */
export function FileContentViewerBody({ headerAction, source, editable = false }: { headerAction?: React.ReactNode; source?: FileViewerSource; editable?: boolean }) {
  const rootRef = useRef<HTMLDivElement>(null)
  const workspaceState = useWorkspaceStore(useShallow(state => ({
    selectedFile: state.selectedFile, fileContent: state.fileContent, loadingFileContent: state.loadingFileContent,
    showFileContent: state.showFileContent, setShowFileContent: state.setShowFileContent, binaryFileData: state.binaryFileData,
  })))
  const {
    selectedFile,
    fileContent,
    loadingFileContent,
    showFileContent,
    setShowFileContent,
    binaryFileData,
  } = source ? { ...source, binaryFileData: null } : workspaceState

  const videoObjectUrl = useMediaObjectUrl(
    selectedFile?.path ? mimeForExtension(selectedFile.path, VIDEO_MIME_TYPES) : null,
    binaryFileData,
  )
  const audioObjectUrl = useMediaObjectUrl(
    selectedFile?.path ? mimeForExtension(selectedFile.path, AUDIO_MIME_TYPES) : null,
    binaryFileData,
  )

  const addToast = useChatStore(state => state.addToast)
  const [isExportingPdf, setIsExportingPdf] = useState(false)
  const [showPushToGistDialog, setShowPushToGistDialog] = useState(false)
  const [shareCopied, setShareCopied] = useState(false)
  const [contentCopied, setContentCopied] = useState(false)
  const [slackCopied, setSlackCopied] = useState(false)
  const [failedImagePath, setFailedImagePath] = useState<string | null>(null)
  const [edit, setEdit] = useState<{ path: string; opened: string; draft: string; saving: boolean } | null>(null)
  // Switching to another file ends an edit (an unsaved draft is dropped).
  useEffect(() => { setEdit(null) }, [selectedFile?.path])
  const editingHere = edit !== null && edit.path === selectedFile?.path ? edit : null
  const canEdit = editable && !source && !!selectedFile?.path && !loadingFileContent && !binaryFileData
    && !fileContent.startsWith('data:') && !NON_TEXT_EXTENSIONS.test(selectedFile.path) && !isViewableBinaryFile(selectedFile.name)
  const markdownContentRef = useRef<HTMLDivElement>(null)
  const selectedFilePathLower = selectedFile?.path?.toLowerCase() || ''
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

  const startEdit = useCallback(async () => {
    if (!selectedFile?.path) return
    const path = selectedFile.path
    try {
      const raw = await readRawFile(agentApi, path)
      setEdit({ path, opened: raw, draft: raw, saving: false })
    } catch {
      addToast('Could not open this file for editing.', 'error')
    }
  }, [selectedFile?.path, addToast])

  const saveEdit = useCallback(async () => {
    if (!edit || edit.saving || edit.draft === edit.opened) return
    setEdit({ ...edit, saving: true })
    try {
      if (await saveEditedFile(agentApi, edit.path, edit.opened, edit.draft) === 'changed') {
        addToast('This file changed since you opened it. Close the editor, reopen the file and make your edit again.', 'error')
        setEdit({ ...edit, saving: false })
        return
      }
      setEdit(null)
      await openWorkspaceFile(edit.path)
      addToast('Saved.', 'success')
    } catch (cause) {
      const status = (cause as { response?: { status?: number } })?.response?.status
      addToast(status === 403 ? 'You do not have permission to change this file.' : 'Could not save this file. Your edit is still here.', 'error')
      setEdit(current => current && { ...current, saving: false })
    }
  }, [edit, addToast])

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

  const git = useFileGit()
  const gitWorkspacePath = useFileGitStore(state => state.workspacePath)
  const gitRepos = useFileGitStore(state => state.repos)
  const gitFile = (!source || source.git) && selectedFile?.path && gitWorkspacePath !== null ? repoForPath(gitWorkspacePath, gitRepos, selectedFile.path) : null
  const gitFileChanged = !!gitFile && gitFile.repo.files.some(entry => entry.path === gitFile.file)

  const gitLineChanges = useGitLineChanges(source && !source.git ? undefined : selectedFile?.path, fileContent)

  const paneActions: PaneAction[] = [
    { key: 'copy', label: contentCopied ? 'Copied!' : 'Copy content', icon: <CopyIcon />, onSelect: () => { void copyContent() } },
    { key: 'slack', label: slackCopied ? 'Copied!' : 'Copy as Slack format', icon: <SlackIcon />, onSelect: () => { void copyAsSlack() } },
    ...(!source ? [{ key: 'share', label: shareCopied ? 'Copied!' : 'Copy share link', icon: <Link className="w-4 h-4" />, onSelect: copyShareLink }] : []),
    ...(gitFile ? [
      ...(gitFileChanged ? [{ key: 'git-changes', label: 'View changes (git)', icon: <GitCompare className="w-4 h-4" />, onSelect: () => git.store.getState().openPanel({ kind: 'diff', repo: gitFile.repo.root, file: gitFile.file }) }] : []),
      { key: 'git-blame', label: 'Git blame', icon: <GitCommitHorizontal className="w-4 h-4" />, onSelect: () => git.store.getState().openPanel({ kind: 'blame', repo: gitFile.repo.root, file: gitFile.file }) },
      { key: 'git-history', label: 'File history (git)', icon: <History className="w-4 h-4" />, onSelect: () => git.store.getState().openPanel({ kind: 'history', repo: gitFile.repo.root, file: gitFile.file }) },
    ] : []),
    ...(isMarkdownFile ? [
      { key: 'pdf', label: isExportingPdf ? 'Exporting…' : 'Export as PDF', icon: isExportingPdf ? <Loader2 className="w-4 h-4 animate-spin" /> : <PdfIcon />, onSelect: () => { void handleExportPdf() }, disabled: isExportingPdf },
      ...(!source ? [{ key: 'gist', label: 'Push to GitHub Gist', icon: <Github className="w-4 h-4" />, onSelect: () => setShowPushToGistDialog(true) }] : []),
    ] : []),
  ]

  return (
    <>
      <div
        ref={rootRef}
        className="flex h-full min-h-0 flex-col bg-background"
        data-ui-file-path={selectedFile?.path || undefined}
        data-ui-file-ready={showFileContent && !loadingFileContent ? 'true' : 'false'}
      >
        <FileTabs source={source} />
        <WorkspaceViewHeader
          icon={FileText}
          showWalkthrough={false}
          title={selectedFile?.path ? (
            <span className="inline-flex max-w-full items-center gap-1.5">
              <button
                type="button"
                onClick={() => setShowFileContent(false)}
                aria-label="Back to files"
                title="Back to files"
                className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                <ArrowLeft className="h-4 w-4" />
              </button>
              <span className="truncate">{selectedFile.path.split('/').pop() || selectedFile.path}</span>
            </span>
          ) : 'File'}
          subtitle={selectedFile?.path ? <FileBreadcrumbs path={selectedFile.path} onReveal={source?.revealFolder} /> : undefined}
          actions={<>
            <div className="flex items-center gap-0.5">
              {editingHere ? (
                <>
                  <button onClick={() => setEdit(null)} disabled={editingHere.saving} className={`${ICON_BUTTON_CLASS} px-2 text-xs disabled:opacity-50`}>Cancel</button>
                  <button onClick={() => { void saveEdit() }} disabled={editingHere.saving || editingHere.draft === editingHere.opened} className="rounded-md bg-primary px-2 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50">{editingHere.saving ? 'Saving…' : 'Save'}</button>
                </>
              ) : canEdit && (
                <button onClick={() => { void startEdit() }} className={ICON_BUTTON_CLASS} title="Edit file" aria-label="Edit file">
                  <Pencil className="w-4 h-4" />
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
            {headerAction}
          </>}
        />

        {source?.contentHeader}
        {isRenderedMarkdownSearchAvailable && (
          <RenderedContentSearchBar search={renderedContentSearch} />
        )}

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
              {!source && fileContent.startsWith('data:image/') ? (
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
              ) : editingHere ? (
                <div className="h-full overflow-hidden">
                  <Suspense fallback={<FileSurfaceFallback />}>
                    <FileEditor
                      value={editingHere.draft}
                      filepath={editingHere.path}
                      readOnly={editingHere.saving}
                      onChange={draft => setEdit(current => current && { ...current, draft })}
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
                      lineChanges={gitLineChanges}
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
                            basePath={source ? undefined : selectedFile?.path}
                            untrustedContent={!!source}
                            disablePathLinking={!!source}
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
