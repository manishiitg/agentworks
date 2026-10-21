import { lazy, Suspense, useEffect, useState } from 'react';
import { File, FileJson, FileText, Image, Music, Video } from 'lucide-react';
import type { PlannerFile, ReportFileRenderFormat, ReportWidget } from '../../../services/api-types';
import { agentApi, workspaceApi } from '../../../services/api'
import { WidgetError, WidgetHeader } from './shared'
import { useReportFilePreviewStore } from '../../../stores/useReportFilePreviewStore'
import { HtmlReportFrame } from './HtmlWidgetFrame'

const DocxRenderer = lazy(() => import('../../ui/DocxRenderer').then(module => ({ default: module.DocxRenderer })))

function DocxPreview({ data }: { data: ArrayBuffer }) {
  return (
    <Suspense fallback={<div className="p-4 text-sm text-muted-foreground">Loading document preview…</div>}>
      <DocxRenderer data={data} />
    </Suspense>
  )
}

// previewReportFile opens a file-list entry in the in-report preview modal.
// file.filepath is the absolute workspace path the planner-files API returns.
function previewReportFile(file: PlannerFile) {
  useReportFilePreviewStore.getState().show({ path: file.filepath, name: basename(file.filepath) })
}

type ArtifactKind = 'html' | 'text' | 'code' | 'json' | 'image' | 'video' | 'audio' | 'pdf' | 'docx' | 'other'

type FileContentState =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; content?: string; objectUrl?: string; mimeType?: string; binaryData?: ArrayBuffer }

const TEXT_EXTENSIONS = new Set(['txt', 'log', 'csv', 'tsv', 'yaml', 'yml', 'xml', 'diff', 'patch'])
const CODE_EXTENSIONS = new Set(['js', 'jsx', 'ts', 'tsx', 'py', 'go', 'java', 'c', 'cpp', 'cs', 'php', 'rb', 'rs', 'sql', 'sh', 'bash', 'zsh', 'css'])
const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico'])
const VIDEO_EXTENSIONS = new Set(['webm', 'mp4', 'mov', 'm4v'])
const AUDIO_EXTENSIONS = new Set(['mp3', 'wav', 'm4a', 'aac', 'ogg', 'oga', 'flac', 'opus'])

function normalizeSource(source: string): string {
  return source.replace(/\\/g, '/').replace(/^\/+/, '').replace(/\/+/g, '/')
}

function isAllowedArtifactSource(source: string): boolean {
  const normalized = normalizeSource(source)
  if (!normalized || normalized.split('/').some(part => part === '..')) return false
  return normalized.startsWith('db/') || normalized.startsWith('knowledgebase/') || normalized.startsWith('docs/')
}

function workspaceFilePath(workspacePath: string, source: string): string {
  return `${workspacePath.replace(/\/+$/, '')}/${normalizeSource(source)}`
}

function extensionFor(path: string): string {
  const leaf = path.split(/[?#]/, 1)[0].split('/').pop() || ''
  const idx = leaf.lastIndexOf('.')
  return idx >= 0 ? leaf.slice(idx + 1).toLowerCase() : ''
}

function artifactKind(path: string): ArtifactKind {
  const ext = extensionFor(path)
  if (ext === 'html' || ext === 'htm') return 'html'
  if (ext === 'md' || ext === 'markdown') return 'text'
  if (ext === 'json' || ext === 'jsonl') return 'json'
  if (ext === 'pdf') return 'pdf'
  if (ext === 'docx') return 'docx'
  if (IMAGE_EXTENSIONS.has(ext)) return 'image'
  if (VIDEO_EXTENSIONS.has(ext)) return 'video'
  if (AUDIO_EXTENSIONS.has(ext)) return 'audio'
  if (CODE_EXTENSIONS.has(ext)) return 'code'
  if (TEXT_EXTENSIONS.has(ext)) return 'text'
  return 'other'
}

function effectiveRenderFormat(widget: ReportWidget): ArtifactKind | 'link' {
  const requested = (widget.renderFormat || 'auto') as ReportFileRenderFormat
  if (requested && requested !== 'auto') return requested === 'link' ? 'link' : requested
  return artifactKind((widget.source ?? ''))
}

function mimeTypeFor(path: string): string {
  const ext = extensionFor(path)
  const mimeTypes: Record<string, string> = {
    pdf: 'application/pdf',
    html: 'text/html',
    htm: 'text/html',
    mp4: 'video/mp4',
    webm: 'video/webm',
    mov: 'video/quicktime',
    mp3: 'audio/mpeg',
    wav: 'audio/wav',
    m4a: 'audio/mp4',
    aac: 'audio/aac',
    ogg: 'audio/ogg',
    oga: 'audio/ogg',
    flac: 'audio/flac',
    opus: 'audio/opus',
  }
  return mimeTypes[ext] || 'application/octet-stream'
}

function basename(path: string): string {
  return path.split('/').filter(Boolean).pop() || path
}

function collectFiles(items: PlannerFile[], out: PlannerFile[] = []): PlannerFile[] {
  for (const item of items) {
    const children = Array.isArray(item.children) ? item.children : []
    const isFolder = item.type === 'folder' || children.length > 0
    if (!isFolder && item.filepath) out.push(item)
    if (children.length > 0) collectFiles(children, out)
  }
  return out
}

async function loadDocxData(path: string): Promise<ArrayBuffer> {
  const response = await workspaceApi.get<ArrayBuffer>(`/api/documents/${encodeURIComponent(path)}`, {
    params: { download: 'true' },
    responseType: 'arraybuffer',
    headers: { Accept: 'application/octet-stream' },
    transformResponse: [(data) => data],
  })
  return response.data
}

async function loadBinaryObjectUrl(path: string, mimeType = mimeTypeFor(path)): Promise<string> {
  const response = await workspaceApi.get(`/api/documents/${encodeURIComponent(path)}`, {
    params: { download: 'true' },
    responseType: 'blob',
    headers: { Accept: 'application/octet-stream' },
    transformResponse: [(data) => data],
  })
  const blob = response.data instanceof Blob
    ? response.data
    : new Blob([response.data], { type: mimeType })
  return URL.createObjectURL(blob.type ? blob : blob.slice(0, blob.size, mimeType))
}

function ArtifactIcon({ kind }: { kind: ArtifactKind }) {
  const className = "h-4 w-4"
  if (kind === 'image') return <Image className={className} />
  if (kind === 'video') return <Video className={className} />
  if (kind === 'audio') return <Music className={className} />
  if (kind === 'json') return <FileJson className={className} />
  if (kind === 'text' || kind === 'code' || kind === 'html' || kind === 'pdf' || kind === 'docx') return <FileText className={className} />
  return <File className={className} />
}

function useFileContent(widget: ReportWidget, workspacePath: string): FileContentState {
  const [state, setState] = useState<FileContentState>({ status: 'loading' })
  const source = widget.source ?? ''
  const path = workspaceFilePath(workspacePath, source)
  const format = effectiveRenderFormat(widget)

  useEffect(() => {
    let cancelled = false
    let objectUrl: string | undefined
    setState({ status: 'loading' })

    const load = async () => {
      try {
        if (!isAllowedArtifactSource(source)) {
          throw new Error('File widgets can only read db/, knowledgebase/, or docs/ paths.')
        }
        if (format === 'link') {
          if (!cancelled) setState({ status: 'ready' })
          return
        }
        if (format === 'docx') {
          const binaryData = await loadDocxData(path)
          if (!cancelled) setState({ status: 'ready', binaryData })
          return
        }
        if (format === 'image') {
          const response = await agentApi.getPlannerFileContent(path)
          const content = typeof response?.data?.content === 'string' ? response.data.content : ''
          if (content.startsWith('data:image/')) {
            if (!cancelled) setState({ status: 'ready', content })
            return
          }
          objectUrl = await loadBinaryObjectUrl(path)
          if (!cancelled) setState({ status: 'ready', objectUrl })
          return
        }
        if (format === 'pdf' || format === 'video' || format === 'audio') {
          objectUrl = await loadBinaryObjectUrl(path)
          if (!cancelled) setState({ status: 'ready', objectUrl, mimeType: mimeTypeFor(path) })
          return
        }
        const response = await agentApi.getPlannerFileContent(path)
        const content = typeof response?.data?.content === 'string' ? response.data.content : ''
        if (!cancelled) setState({ status: 'ready', content })
      } catch (error) {
        if (!cancelled) {
          setState({ status: 'error', message: error instanceof Error ? error.message : String(error) })
        }
      }
    }

    void load()
    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [format, path, source])

  return state
}

export function FileWidget({ widget, workspacePath }: { widget: ReportWidget; workspacePath: string }) {
  const state = useFileContent(widget, workspacePath)
  const format = effectiveRenderFormat(widget)
  const name = basename((widget.source ?? ''))

  if (!isAllowedArtifactSource((widget.source ?? ''))) {
    return <WidgetError widget={widget} message="Unsupported file source." hint="Use db/, knowledgebase/, or docs/." />
  }

  if (state.status === 'loading') {
    return <div className="py-3 text-sm text-muted-foreground">Loading {name}...</div>
  }
  if (state.status === 'error') {
    return <WidgetError widget={widget} message={`Could not load ${(widget.source ?? '')}.`} hint={state.message} />
  }

  // HTML documents carry their own heading, so suppress the widget
  // title to avoid a redundant double-heading (e.g. a "Full Report" label above
  // an HTML report that already starts with its own <h1>).
  const suppressHeader = format === 'html'

  return (
    <div className="flex flex-col gap-2">
      {!suppressHeader && <WidgetHeader widget={widget} />}
      {format === 'html' && (
        <HtmlReportFrame
          html={state.content || ''}
          title={widget.title || name}
          autoHeight
          className="block w-full border-0"
        />
      )}
      {(format === 'text' || format === 'code' || format === 'json') && (
        <pre className="max-h-[640px] overflow-auto rounded-lg bg-muted/25 px-2.5 py-2 text-xs leading-6 text-foreground">
          {format === 'json' ? formatJSONText(state.content || '') : state.content}
        </pre>
      )}
      {format === 'image' && (
        <img src={state.content || state.objectUrl} alt={widget.title || name} className="max-h-[720px] w-full rounded-lg border border-border object-contain bg-background" />
      )}
      {format === 'video' && state.objectUrl && (
        <video src={state.objectUrl} controls className="max-h-[720px] w-full rounded-lg border border-border bg-black" />
      )}
      {format === 'audio' && state.objectUrl && (
        <audio src={state.objectUrl} controls className="w-full" />
      )}
      {format === 'pdf' && state.objectUrl && (
        <iframe title={widget.title || name} src={state.objectUrl} className="h-[min(820px,75vh)] w-full rounded-lg border border-border bg-background" />
      )}
      {format === 'docx' && state.binaryData && (
        <div className="max-h-[720px] overflow-auto p-3">
          <DocxPreview data={state.binaryData} />
        </div>
      )}
      {(format === 'link' || format === 'other') && (
        <div className="flex items-center gap-2 rounded-lg border border-border bg-muted/20 px-2.5 py-2 text-sm text-foreground">
          <ArtifactIcon kind={artifactKind((widget.source ?? ''))} />
          <span className="min-w-0 truncate">{(widget.source ?? '')}</span>
        </div>
      )}
    </div>
  )
}

function formatJSONText(content: string): string {
  try {
    return JSON.stringify(JSON.parse(content), null, 2)
  } catch {
    return content
  }
}

// groupFilesByFolder buckets files by the first path segment under the source
// folder (e.g. the PAN folder under db/reports). Files directly in the source
// root land in the "" bucket. Order of first appearance is preserved.
function groupFilesByFolder(files: PlannerFile[], sourceFolderAbs: string): { group: string; files: PlannerFile[] }[] {
  const prefix = sourceFolderAbs.replace(/\/+$/, '') + '/'
  const order: string[] = []
  const buckets = new Map<string, PlannerFile[]>()
  for (const file of files) {
    const fp = file.filepath.replace(/\\/g, '/')
    const rel = fp.startsWith(prefix) ? fp.slice(prefix.length) : basename(fp)
    const slash = rel.indexOf('/')
    const group = slash > 0 ? rel.slice(0, slash) : ''
    if (!buckets.has(group)) {
      buckets.set(group, [])
      order.push(group)
    }
    buckets.get(group)!.push(file)
  }
  return order.map(group => ({ group, files: buckets.get(group)! }))
}

// useAbsoluteFileContent loads a workspace file by its ABSOLUTE path (the form
// the planner-files API returns for report file-lists), mirroring useFileContent
// but without needing a ReportWidget/source. Used by the in-report preview modal.
function useAbsoluteFileContent(path: string, kind: ArtifactKind): FileContentState {
  const [state, setState] = useState<FileContentState>({ status: 'loading' })

  useEffect(() => {
    let cancelled = false
    let objectUrl: string | undefined
    setState({ status: 'loading' })

    const load = async () => {
      try {
        if (kind === 'docx') {
          const binaryData = await loadDocxData(path)
          if (!cancelled) setState({ status: 'ready', binaryData })
          return
        }
        if (kind === 'image') {
          const response = await agentApi.getPlannerFileContent(path)
          const content = typeof response?.data?.content === 'string' ? response.data.content : ''
          if (content.startsWith('data:image/')) {
            if (!cancelled) setState({ status: 'ready', content })
            return
          }
          objectUrl = await loadBinaryObjectUrl(path)
          if (!cancelled) setState({ status: 'ready', objectUrl })
          return
        }
        if (kind === 'pdf' || kind === 'video' || kind === 'audio') {
          objectUrl = await loadBinaryObjectUrl(path)
          if (!cancelled) setState({ status: 'ready', objectUrl, mimeType: mimeTypeFor(path) })
          return
        }
        const response = await agentApi.getPlannerFileContent(path)
        const content = typeof response?.data?.content === 'string' ? response.data.content : ''
        if (!cancelled) setState({ status: 'ready', content })
      } catch (error) {
        if (!cancelled) setState({ status: 'error', message: error instanceof Error ? error.message : String(error) })
      }
    }

    void load()
    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [path, kind])

  return state
}

// FilePreviewByPath renders a single workspace file inline given its absolute
// path — the body of the in-report preview modal. Handles the same formats as
// the single-file FileWidget (pdf/docx/image/video/audio/html/text/code/json).
export function FilePreviewByPath({ path, name }: { path: string; name?: string }) {
  const kind = artifactKind(path)
  const state = useAbsoluteFileContent(path, kind)
  const label = name || basename(path)

  if (state.status === 'loading') {
    return <div className="flex h-full items-center justify-center text-sm text-muted-foreground">Loading {label}…</div>
  }
  if (state.status === 'error') {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-center text-sm">
        <div className="text-destructive">Could not load {label}.</div>
        <div className="text-xs text-muted-foreground">{state.message}</div>
      </div>
    )
  }

  if (kind === 'html') {
    return <HtmlReportFrame html={state.content || ''} title={label} className="h-full w-full bg-background" />
  }
  if (kind === 'pdf' && state.objectUrl) {
    return <iframe title={label} src={state.objectUrl} className="h-full w-full bg-background" />
  }
  if (kind === 'docx' && state.binaryData) {
    return <div className="h-full overflow-auto p-3"><DocxPreview data={state.binaryData} /></div>
  }
  if (kind === 'image') {
    return (
      <div className="flex h-full items-center justify-center bg-background p-3">
        <img src={state.content || state.objectUrl} alt={label} className="max-h-full max-w-full object-contain" />
      </div>
    )
  }
  if (kind === 'video' && state.objectUrl) {
    return (
      <div className="flex h-full items-center justify-center bg-black p-3">
        <video src={state.objectUrl} controls className="max-h-full max-w-full" />
      </div>
    )
  }
  if (kind === 'audio' && state.objectUrl) {
    return (
      <div className="flex h-full items-center justify-center p-4">
        <audio src={state.objectUrl} controls className="w-full max-w-xl" />
      </div>
    )
  }
  if (kind === 'text' || kind === 'code' || kind === 'json') {
    return (
      <pre className="h-full overflow-auto bg-muted/20 px-4 py-3 text-xs leading-6 text-foreground">
        {kind === 'json' ? formatJSONText(state.content || '') : state.content}
      </pre>
    )
  }
  // 'other' — no inline renderer; offer a hint.
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 text-center text-sm text-muted-foreground">
      <ArtifactIcon kind={kind} />
      <div>No inline preview for this file type.</div>
      <div className="text-xs">{label}</div>
    </div>
  )
}
