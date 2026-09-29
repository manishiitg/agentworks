import { agentApi, workspaceApi } from '../services/api'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { isValidJSON } from './event-helpers'

const VIEWABLE_BINARY_EXTENSIONS = new Set(['xls', 'xlsx', 'docx', 'pdf', 'webm', 'mp4', 'mov', 'mp3', 'wav', 'm4a', 'aac', 'ogg', 'oga', 'flac', 'opus'])

export function isViewableBinaryFile(fileName: string): boolean {
  return VIEWABLE_BINARY_EXTENSIONS.has(fileName.split('.').pop()?.toLowerCase() || '')
}

/**
 * Load a workspace file (full workspace path) into the shared file viewer.
 * The one loader behind a tree click and a tab click.
 */
export async function openWorkspaceFile(fullFilePath: string): Promise<void> {
  const store = useWorkspaceStore.getState()
  const fileName = fullFilePath.split('/').pop() || fullFilePath
  try {
    store.setLoadingFileContent(true)
    store.setSelectedFile({ name: fileName, path: fullFilePath })

    // Viewable binary files load as raw bytes so the viewer can render them.
    if (isViewableBinaryFile(fileName)) {
      const response = await workspaceApi.get(
        `/api/documents/${encodeURIComponent(fullFilePath)}`,
        { params: { download: 'true' }, responseType: 'arraybuffer' },
      )
      store.setBinaryFileData(response.data as ArrayBuffer)
      store.setFileContent('')
      store.setShowFileContent(true)
      return
    }
    store.setBinaryFileData(null)

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
    if (!(response.data.is_image && content.startsWith('data:image/')) && content) {
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
