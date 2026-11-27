import { randomUUID } from '../utils/randomUUID'
import { binaryPreviewPage, readBoundedDocument } from '../components/files/readBoundedDocument'
import { PagedTextPreview } from '../components/files/PagedTextPreview'
import { decodePreviewPage, isPagedTextFile, PREVIEW_PAGE_BYTES, type PreviewPage } from '../components/files/readPreviewPage'
import { sharedLink } from '../utils/sharedLinks'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Loader2, FileText, ArrowLeft, LogIn, Download } from 'lucide-react'
import { MarkdownRenderer } from '../components/ui/MarkdownRenderer'
import { RenderedContentSearchBar, RenderedContentSearchButton, useRenderedContentSearch } from '../components/ui/RenderedContentSearch'
import { DocumentPreview, hasDocumentPreview } from '../components/files/DocumentPreview'
import { DiffRenderer } from '../components/ui/DiffRenderer'
import { apiUrlForPath, getAuthToken } from '../services/api'
import { admissionWindowHeaders } from '../utils/chatDeliveryTelemetry'
import { isDiffFilePath, looksLikeDiffContent } from '../utils/diff'
import { isValidJSON } from '../utils/event-helpers'

class SharedFileAuthError extends Error { constructor() { super('Sign in to open this file.') } }

interface SharedFileProps {
  encodedPath: string
  uid?: string
  onBack?: () => void
  /** When embedded in a Crew, send reads to that Crew's existing connection. */
  previewWorkspace?: string
}

const getCodeFileLanguage = (filepath: string): string | null => {
  const ext = filepath.toLowerCase().split('.').pop() || ''
  const codeExtensions: Record<string, string> = {
    'go': 'go', 'py': 'python', 'ts': 'typescript', 'tsx': 'typescript',
    'js': 'javascript', 'jsx': 'javascript', 'java': 'java', 'c': 'c',
    'cpp': 'cpp', 'cs': 'csharp', 'php': 'php', 'rb': 'ruby', 'sql': 'sql',
    'html': 'html', 'css': 'css', 'scss': 'scss', 'sh': 'shell',
    'bash': 'shell', 'yaml': 'yaml', 'yml': 'yaml', 'xml': 'xml',
    'rs': 'rust', 'swift': 'swift', 'kt': 'kotlin', 'dockerfile': 'dockerfile',
  }
  return codeExtensions[ext] || null
}

export function SharedFile({ encodedPath, uid, onBack, previewWorkspace }: SharedFileProps) {
  const [firstPage, setFirstPage] = useState<PreviewPage | null>(null)
  const [content, setContent] = useState<string | null>(null)
  const [binaryData, setBinaryData] = useState<ArrayBuffer | null>(null)
  const [binaryUrl, setBinaryUrl] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [needsAuth, setNeedsAuth] = useState(false)
  const renderedContentRef = useRef<HTMLDivElement>(null)

  // Decode the base64 path
  const filePath = (() => {
    try {
      return decodeURIComponent(escape(atob(encodedPath)))
    } catch {
      return null
    }
  })()

  const fileName = filePath?.split('/').pop() || 'Unknown file'
  const lowerPath = (filePath || '').toLowerCase()
  const isPDF = lowerPath.endsWith('.pdf')
  const isHTML = /\.html?$/.test(lowerPath)
  const isOfficeOrBinary = /\.(docx?|xlsx?|pptx?|zip|gz|bin|dat)$/.test(lowerPath)
  const isVideoFile = lowerPath.endsWith('.mp4') || lowerPath.endsWith('.webm') || lowerPath.endsWith('.mov')
  const isAudioFile = ['.mp3', '.wav', '.m4a', '.aac', '.ogg', '.oga', '.flac', '.opus'].some(ext => lowerPath.endsWith(ext))
  const isImageFile = ['.png', '.jpg', '.jpeg', '.gif', '.webp', '.svg', '.bmp', '.ico'].some(ext => lowerPath.endsWith(ext))
  const language = getCodeFileLanguage(filePath || '')
  const isRenderedSearchAvailable = (
    !loading &&
    !error &&
    !needsAuth &&
    content !== null &&
    !lowerPath.endsWith('.csv') &&
    !lowerPath.endsWith('.json') &&
    !isValidJSON(content) &&
    !isDiffFilePath(filePath || '') &&
    !looksLikeDiffContent(content)
  )
  const renderedContentSearch = useRenderedContentSearch({
    targetRef: renderedContentRef,
    contentKey: `${filePath || ''}:${content?.length || 0}`,
    enabled: isRenderedSearchAvailable,
  })

  const requestFile = useCallback((headers: Record<string, string> = {}, signal?: AbortSignal) => {
    const token = getAuthToken()
    const query = new URLSearchParams({ path: encodedPath })
    if (uid) query.set('uid', uid)
    return fetch(apiUrlForPath(`/api/public/file?${query}`, previewWorkspace), {
      headers: { ...(previewWorkspace ? admissionWindowHeaders() : {}), ...(token ? { Authorization: `Bearer ${token}` } : {}), ...headers },
      signal,
    })
  }, [encodedPath, uid, previewWorkspace])
  const fetchSharedRange = useCallback(async (offset: number, signal?: AbortSignal) => {
    const resp = await requestFile({ Range: `bytes=${offset}-${offset + PREVIEW_PAGE_BYTES - 1}` }, signal)
    if (resp.status === 401) throw new SharedFileAuthError()
    if (resp.status === 403) throw new Error('You do not have access to this file.')
    if (resp.status !== 206 && resp.status !== 416) { await resp.body?.cancel(); throw new Error('Could not read this file preview.') }
    console.info(`[FILE_PREVIEW] id=${randomUUID()} source=shared offset=${offset}`)
    return { status: resp.status, range: resp.headers.get('Content-Range') || '', data: await resp.arrayBuffer() }
  }, [requestFile])
  const downloadOriginal = async () => {
    try {
      const response = await requestFile()
      if (!response.ok) throw new Error('Could not download this file.')
      const href = URL.createObjectURL(await response.blob())
      const link = document.createElement('a'); link.href = href; link.download = fileName; link.click()
      setTimeout(() => URL.revokeObjectURL(href), 60_000)
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not download this file.') }
  }
  const readSharedPage = useCallback(async (_path: string, offset = 0, signal?: AbortSignal) => {
    const response = await fetchSharedRange(offset, signal)
    return decodePreviewPage(response.status, response.range, response.data, offset)
  }, [fetchSharedRange])

  useEffect(() => {
    if (!filePath) {
      setError('Invalid file path encoding')
      setLoading(false)
      return
    }

    const controller = new AbortController()
    let objectUrl: string | undefined
    const loadFile = async () => {
      try {
        setLoading(true)
        setError(null)
        setNeedsAuth(false)
        setBinaryUrl(null)
        setBinaryData(null)
        setContent(null)
        setFirstPage(null)
        if (isPagedTextFile(fileName)) {
          const first = await readSharedPage(filePath, 0, controller.signal)
          if (controller.signal.aborted) return
          if (first.next < first.total) setFirstPage(first)
          setContent(first.next < first.total ? '' : first.text)
          return
        }
        if (hasDocumentPreview(fileName) && !fileName.toLowerCase().endsWith('.csv')) {
          const data = await readBoundedDocument(async offset => {
            const response = await fetchSharedRange(offset, controller.signal)
            return binaryPreviewPage(response.status, response.range, response.data, offset)
          })
          if (controller.signal.aborted) return
          if (/\.ipynb$/i.test(fileName)) setContent(new TextDecoder().decode(data))
          else {
            setBinaryData(data)
            objectUrl = URL.createObjectURL(new Blob([data]))
            setBinaryUrl(objectUrl)
          }
          return
        }
        const resp = await requestFile({}, controller.signal)
        if (controller.signal.aborted) return
        if (resp.status === 401) {
          setNeedsAuth(true)
          return
        }
        if (!resp.ok) {
          throw new Error(resp.status === 403 ? 'You do not have access to this file.' : resp.status === 404 ? 'File not found' : `Failed to load file (${resp.status})`)
        }

        if (isVideoFile || isAudioFile || isImageFile || isPDF || isOfficeOrBinary || (!resp.headers.get("Content-Type")?.startsWith("text/") && !/\.(md|markdown|json|jsonl|csv|xml|ya?ml|py|go|js|ts|tsx|jsx|sh|css|html?|ipynb)$/.test(lowerPath))) {
          const blob = await resp.blob()
          if (controller.signal.aborted) return
          if (hasDocumentPreview(fileName)) setBinaryData(await blob.arrayBuffer())
          if (controller.signal.aborted) return
          objectUrl = URL.createObjectURL(blob)
          setBinaryUrl(objectUrl)
          setContent(null)
        } else {
          const text = await resp.text()
          if (!controller.signal.aborted) setContent(text)
        }
      } catch (err) {
        if (controller.signal.aborted) return
        // An expired session on a paged preview shows the same sign-in screen as a whole-file read.
        if (err instanceof SharedFileAuthError) { setNeedsAuth(true); return }
        console.error('Failed to load shared file:', err)
        setError(err instanceof Error ? err.message : 'Failed to load file')
      } finally {
        if (!controller.signal.aborted) setLoading(false)
      }
    }

    loadFile()
    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [encodedPath, filePath, uid, isVideoFile, isAudioFile, isImageFile, isPDF, isOfficeOrBinary, lowerPath, fileName, readSharedPage, fetchSharedRange, requestFile])

  const heightClass = previewWorkspace ? 'min-h-full' : 'min-h-screen'
  if (loading) {
    return (
      <div className={`${heightClass} flex items-center justify-center bg-background`}>
        <div className="text-center">
          <Loader2 className="h-8 w-8 animate-spin mx-auto text-blue-500" />
          <p className="mt-4 text-gray-600 dark:text-gray-400">Loading file...</p>
        </div>
      </div>
    )
  }

  if (needsAuth) {
    return (
      <div className={`${heightClass} flex items-center justify-center bg-background`}>
        <div className="max-w-md w-full p-8 bg-white dark:bg-gray-800 rounded-lg shadow-lg text-center">
          <LogIn className="h-12 w-12 mx-auto mb-4 text-blue-500" />
          <h2 className="text-xl font-bold text-gray-900 dark:text-white mb-2">Login Required</h2>
          <p className="text-gray-600 dark:text-gray-400 mb-6">
            You need to be logged in to view this shared file.
          </p>
          <button
            onClick={() => { window.location.href = '/login?next=' + encodeURIComponent(window.location.pathname + window.location.search) }}
            className="px-6 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors"
          >
            Go to Login
          </button>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className={`${heightClass} flex items-center justify-center bg-background`}>
        <div className="max-w-md w-full p-8 bg-white dark:bg-gray-800 rounded-lg shadow-lg text-center">
          <div className="text-red-500 dark:text-red-400 text-6xl mb-4">!</div>
          <h2 className="text-xl font-bold text-gray-900 dark:text-white mb-2">Unable to open file</h2>
          <p className="text-gray-600 dark:text-gray-400 mb-4">{error}</p>
          <button className="mr-3 text-primary underline" onClick={() => void downloadOriginal()}>Download original</button>
          {onBack && (
            <button onClick={onBack} className="text-blue-600 dark:text-blue-400 hover:underline">
              Go back
            </button>
          )}
        </div>
      </div>
    )
  }

  if (content === null && binaryUrl === null) return null

  const renderContent = () => {
    if (firstPage && filePath) return <PagedTextPreview key={filePath} path={filePath} first={firstPage} readPage={readSharedPage} />
    if (hasDocumentPreview(fileName)) return <DocumentPreview key={fileName} name={fileName} data={binaryData} text={content || ''} />
    if (isPDF && binaryUrl) return <iframe title={fileName} src={binaryUrl} className="w-full h-[75vh] border-0" />
    if (isHTML && content !== null) return <iframe title={fileName} sandbox="" srcDoc={content} className="w-full h-[75vh] border-0 bg-white" />
    if (binaryUrl && !isVideoFile && !isAudioFile && !isImageFile) return <p className="text-sm p-6">Download this file to open it in its associated application.</p>
    if (isVideoFile && binaryUrl) {
      return (
        <div className="h-[calc(100vh-180px)] w-full flex items-center justify-center bg-black rounded-lg">
          <video controls autoPlay className="max-h-full max-w-full" src={binaryUrl} />
        </div>
      )
    }

    if (isAudioFile && binaryUrl) {
      return (
        <div className="min-h-[240px] w-full flex items-center justify-center rounded-lg border border-gray-200 bg-gray-50 p-8 dark:border-gray-700 dark:bg-gray-900">
          <audio controls autoPlay className="w-full max-w-3xl" src={binaryUrl} />
        </div>
      )
    }

    if (isImageFile && binaryUrl) {
      return (
        <div className="flex items-center justify-center">
          <img src={binaryUrl} alt={fileName} className="max-w-full max-h-[calc(100vh-180px)] object-contain rounded-lg shadow-lg" />
        </div>
      )
    }

    if (content === null) return null

    if (lowerPath.endsWith('.json') || isValidJSON(content)) {
      return (
        <div className="bg-gray-50 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-lg p-4">
          <pre className="text-sm font-mono text-gray-800 dark:text-gray-200 overflow-x-auto whitespace-pre-wrap break-words leading-relaxed">
            {content}
          </pre>
        </div>
      )
    }

    if (isDiffFilePath(filePath || '') || looksLikeDiffContent(content)) {
      return <DiffRenderer content={content} />
    }

    if (language) {
      const codeBlockContent = `\`\`\`${language}\n${content}\n\`\`\``
      return (
        <div ref={renderedContentRef} className="prose prose-sm max-w-none dark:prose-invert">
          <MarkdownRenderer untrustedContent={!!previewWorkspace} disablePathLinking={!!previewWorkspace} basePath={filePath || undefined} workspaceLinkHref={path => sharedLink(window.location.origin, 'file', path, uid)} content={codeBlockContent} className="max-w-none" showScrollbar={true} />
        </div>
      )
    }

    // Default: render as markdown
    return (
      <div ref={renderedContentRef} className="prose prose-sm max-w-none dark:prose-invert prose-headings:font-semibold prose-headings:text-gray-900 dark:prose-headings:text-gray-100 prose-p:text-gray-700 dark:prose-p:text-gray-300 prose-a:text-blue-600 dark:prose-a:text-blue-400 prose-a:no-underline hover:prose-a:underline prose-code:text-blue-600 dark:prose-code:text-blue-400 prose-pre:bg-gray-50 dark:prose-pre:bg-gray-900">
        <MarkdownRenderer untrustedContent={!!previewWorkspace} disablePathLinking={!!previewWorkspace} basePath={filePath || undefined} workspaceLinkHref={path => sharedLink(window.location.origin, 'file', path, uid)} content={content} className="max-w-none" showScrollbar={true} />
      </div>
    )
  }

  return (
    <div className={`${heightClass} bg-background`}>
      {/* Header */}
      <div className="bg-white dark:bg-gray-800 shadow sticky top-0 z-10">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center space-x-3">
              {onBack && (
                <button
                  onClick={onBack}
                  className="p-1.5 rounded-md text-gray-500 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                  title={previewWorkspace ? 'Back to files' : 'Back to folder'}
                >
                  <ArrowLeft className="h-5 w-5" />
                </button>
              )}
              <FileText className="h-5 w-5 text-blue-500 flex-shrink-0" />
              <div className="min-w-0">
                <h1 className="text-lg font-semibold text-gray-900 dark:text-white truncate">{fileName}</h1>
                <p className="text-xs text-gray-500 dark:text-gray-400 truncate">{filePath}</p>
              </div>
            </div>
            <div className="flex items-center gap-2 flex-shrink-0">
              {isRenderedSearchAvailable && (
                <RenderedContentSearchButton search={renderedContentSearch} />
              )}
              <button onClick={() => void downloadOriginal()} className="inline-flex items-center gap-2 text-sm px-3 py-1.5 rounded border border-gray-300 dark:border-gray-600">
                <Download className="w-4 h-4" />Download
              </button>
            </div>
          </div>
        </div>
        {isRenderedSearchAvailable && (
          <RenderedContentSearchBar search={renderedContentSearch} />
        )}
      </div>

      {/* Content */}
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6 pb-16">
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden p-6">
          {renderContent()}
        </div>
      </div>
    </div>
  )
}
