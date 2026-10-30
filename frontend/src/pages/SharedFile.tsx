import { sharedLink } from '../utils/sharedLinks'
import { useEffect, useRef, useState } from 'react'
import { Loader2, FileText, ArrowLeft, LogIn, Download } from 'lucide-react'
import { MarkdownRenderer } from '../components/ui/MarkdownRenderer'
import { RenderedContentSearchBar, RenderedContentSearchButton, useRenderedContentSearch } from '../components/ui/RenderedContentSearch'
import { CsvRenderer } from '../components/ui/CsvRenderer'
import { DiffRenderer } from '../components/ui/DiffRenderer'
import { getApiBaseUrl, getAuthToken } from '../services/api'
import { isDiffFilePath, looksLikeDiffContent } from '../utils/diff'
import { isValidJSON } from '../utils/event-helpers'

interface SharedFileProps {
  encodedPath: string
  uid?: string
  onBack?: () => void
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

export function SharedFile({ encodedPath, uid, onBack }: SharedFileProps) {
  const [content, setContent] = useState<string | null>(null)
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
        setContent(null)
        const base = getApiBaseUrl() || ''
        const headers: Record<string, string> = {}
        const token = getAuthToken()
        if (token) headers['Authorization'] = `Bearer ${token}`
        const uidParam = uid ? `&uid=${encodeURIComponent(uid)}` : ''
        const resp = await fetch(`${base}/api/public/file?path=${encodeURIComponent(encodedPath)}${uidParam}`, { headers, signal: controller.signal })
        if (controller.signal.aborted) return
        if (resp.status === 401) {
          setNeedsAuth(true)
          return
        }
        if (!resp.ok) {
          throw new Error(resp.status === 403 ? 'You do not have access to this file.' : resp.status === 404 ? 'File not found' : `Failed to load file (${resp.status})`)
        }

        if (isVideoFile || isAudioFile || isImageFile || isPDF || isOfficeOrBinary || (!resp.headers.get("Content-Type")?.startsWith("text/") && !/\.(md|markdown|json|jsonl|csv|xml|ya?ml|py|go|js|ts|tsx|jsx|sh|css|html?)$/.test(lowerPath))) {
          const blob = await resp.blob()
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
  }, [encodedPath, filePath, uid, isVideoFile, isAudioFile, isImageFile, isPDF, isOfficeOrBinary, lowerPath])

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-100 dark:bg-gray-900">
        <div className="text-center">
          <Loader2 className="h-8 w-8 animate-spin mx-auto text-blue-500" />
          <p className="mt-4 text-gray-600 dark:text-gray-400">Loading file...</p>
        </div>
      </div>
    )
  }

  if (needsAuth) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-100 dark:bg-gray-900">
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
      <div className="min-h-screen flex items-center justify-center bg-gray-100 dark:bg-gray-900">
        <div className="max-w-md w-full p-8 bg-white dark:bg-gray-800 rounded-lg shadow-lg text-center">
          <div className="text-red-500 dark:text-red-400 text-6xl mb-4">!</div>
          <h2 className="text-xl font-bold text-gray-900 dark:text-white mb-2">Unable to open file</h2>
          <p className="text-gray-600 dark:text-gray-400 mb-4">{error}</p>
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

    if (lowerPath.endsWith('.csv')) {
      return <CsvRenderer content={content} />
    }

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
          <MarkdownRenderer basePath={filePath || undefined} workspaceLinkHref={path => sharedLink(window.location.origin, 'file', path, uid)} content={codeBlockContent} className="max-w-none" showScrollbar={true} />
        </div>
      )
    }

    // Default: render as markdown
    return (
      <div ref={renderedContentRef} className="prose prose-sm max-w-none dark:prose-invert prose-headings:font-semibold prose-headings:text-gray-900 dark:prose-headings:text-gray-100 prose-p:text-gray-700 dark:prose-p:text-gray-300 prose-a:text-blue-600 dark:prose-a:text-blue-400 prose-a:no-underline hover:prose-a:underline prose-code:text-blue-600 dark:prose-code:text-blue-400 prose-pre:bg-gray-50 dark:prose-pre:bg-gray-900">
        <MarkdownRenderer basePath={filePath || undefined} workspaceLinkHref={path => sharedLink(window.location.origin, 'file', path, uid)} content={content} className="max-w-none" showScrollbar={true} />
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-gray-100 dark:bg-gray-900">
      {/* Header */}
      <div className="bg-white dark:bg-gray-800 shadow sticky top-0 z-10">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center space-x-3">
              {onBack && (
                <button
                  onClick={onBack}
                  className="p-1.5 rounded-md text-gray-500 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                  title="Back to folder"
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
              <button onClick={() => {
                const href = binaryUrl || URL.createObjectURL(new Blob([content || ''], { type: 'text/plain' }))
                const link = document.createElement('a'); link.href = href; link.download = fileName; link.click()
                if (!binaryUrl) setTimeout(() => URL.revokeObjectURL(href), 1000)
              }} className="inline-flex items-center gap-2 text-sm px-3 py-1.5 rounded border border-gray-300 dark:border-gray-600">
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
