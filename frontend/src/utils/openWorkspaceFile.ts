import { readWorkspaceDocument } from '../components/files/readBoundedDocument'
import { isPagedTextFile, readLineOffset, readPreviewPage } from '../components/files/readPreviewPage'
import { agentApi, workspaceApi } from '../services/api'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { isValidJSON } from './event-helpers'
import { isCodeFile } from './codeFileLanguage'

const CODE_FILE_FULL_BYTES = 2 * 1024 * 1024

const VIEWABLE_BINARY_EXTENSIONS = new Set(['xls', 'xlsx', 'docx', 'pdf', 'webm', 'mp4', 'mov', 'mp3', 'wav', 'm4a', 'aac', 'ogg', 'oga', 'flac', 'opus'])

export function isViewableBinaryFile(fileName: string): boolean {
  return VIEWABLE_BINARY_EXTENSIONS.has(fileName.split('.').pop()?.toLowerCase() || '')
}

/**
 * Load a workspace file (full workspace path) into the shared file viewer.
 * The one loader behind a tree click and a tab click. A line (from a
 * "Search in files" result) shows the file as saved, scrolled to that line.
 */
export async function openWorkspaceFile(fullFilePath: string, line?: number): Promise<void> {
  const store = useWorkspaceStore.getState()
  const fileName = fullFilePath.split('/').pop() || fullFilePath
  try {
    store.setLoadingFileContent(true)
    store.setSelectedFile({ name: fileName, path: fullFilePath, ...(line ? { line } : {}) })

    // Viewable binary files load as raw bytes so the viewer can render them.
    if (isViewableBinaryFile(fileName)) {
      const data = await readWorkspaceDocument(fullFilePath)
      if (useWorkspaceStore.getState().selectedFile?.path !== fullFilePath) return
      store.setBinaryFileData(data)
      store.setFileContent('')
      store.setShowFileContent(true)
      return
    }
    store.setBinaryFileData(null)

    if (/\.ipynb$/i.test(fileName)) {
      const data = await readWorkspaceDocument(fullFilePath)
      if (useWorkspaceStore.getState().selectedFile?.path !== fullFilePath) return
      store.setFileContent(new TextDecoder().decode(data))
      store.setShowFileContent(true)
      return
    }

    // Source/log/data files are fetched in pieces before any full-content API.
    // Conversation JSON has a semantic loader, so keep that separate.
    // Up to CODE_FILE_FULL_BYTES, source code and files opened at a line (a search result or a chat code link)
    // load whole, so they open highlighted at the line. Anything bigger pages; at a line it pages from there.
    if (isPagedTextFile(fileName)) {
      const first = await readPreviewPage(fullFilePath)
      if (useWorkspaceStore.getState().selectedFile?.path !== fullFilePath) return
      if (first.next >= first.total) {
        // Small files still use the normal text/CSV rendering and saved line offsets.
        store.setFileContent(first.text)
        store.setShowFileContent(true)
        return
      }
      if (first.total > CODE_FILE_FULL_BYTES || !(line || isCodeFile(fullFilePath))) {
        let page = first
        if (line && line > 1) {
          const offset = await readLineOffset(fullFilePath, line)
          if (useWorkspaceStore.getState().selectedFile?.path !== fullFilePath) return
          if (offset > 0) page = { ...await readPreviewPage(fullFilePath, offset), startLine: line }
          if (useWorkspaceStore.getState().selectedFile?.path !== fullFilePath) return
        }
        store.setSelectedFile({ name: fileName, path: fullFilePath, previewPage: page })
        store.setFileContent('')
        store.setShowFileContent(true)
        return
      }
      // A larger source file or a line within CODE_FILE_FULL_BYTES: the whole-file read below.
    }

    const response = await agentApi.getPlannerFileContent(fullFilePath)
    if (!response.success || !response.data) {
      store.setError(response.message || 'Failed to load file content')
      return
    }
    if (response.data.is_binary && !response.data.is_image) {
      const size = typeof response.data.size === 'number' ? ` (${response.data.size.toLocaleString()} bytes)` : ''
      store.setError(`File "${fileName}" is a binary file${size} and cannot be viewed in the editor.`)
      store.setShowFileContent(false)
      return
    }

    const raw = response.data.content ?? ''
    let content = typeof raw === 'string' ? raw : String(raw)
    // Images arrive as data URLs; everything else may carry escaped newlines.
    // Keep the saved text when opening at a line, so the line numbers match the search.
    if (!line && !(response.data.is_image && content.startsWith('data:image/')) && content && !/\.py$/i.test(fullFilePath)) {
      // Detect JSON before unescaping: the \\n replacement corrupts JSON strings.
      const isJson = fullFilePath.toLowerCase().endsWith('.json') || isValidJSON(content)
      if (isJson) {
        try {
          content = JSON.stringify(JSON.parse(content), null, 2)
        } catch (parseError) {
          console.warn('Failed to parse JSON file:', parseError)
        }
      } else {
        content = content.replace(/\\n/g, '\n').replace(/\\t/g, '\t').replace(/\\r/g, '\r')
      }
    }
    store.setFileContent(content)
    store.setShowFileContent(true)
  } catch (err) {
    console.error('Failed to fetch file content:', err)
    store.setError(err instanceof Error ? err.message : 'Failed to fetch file content')
  } finally {
    useWorkspaceStore.getState().setLoadingFileContent(false)
  }
}

/** Close a tab; closing the active one opens its neighbour (or leaves the viewer). */
export function closeOpenTab(path: string, activePath: string) {
  const store = useWorkspaceStore.getState()
  const index = store.openTabs.findIndex(tab => tab.path === path)
  store.closeTab(path)
  if (path !== activePath) return
  const remaining = useWorkspaceStore.getState().openTabs
  const next = remaining[Math.min(index, remaining.length - 1)]
  if (next) void openWorkspaceFile(next.path)
  else store.setShowFileContent(false)
}
