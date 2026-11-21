import { randomUUID } from '../../utils/randomUUID'
import { workspaceApi } from '../../services/api'

export const PREVIEW_PAGE_BYTES = 256 * 1024
/** startLine: the page was opened at this line (it starts there). */
export type PreviewPage = { text: string; offset: number; next: number; total: number; startLine?: number }

/** Range reads use the existing raw-document endpoint and its normal caller grants. */
export async function readPreviewPage(path: string, offset = 0, signal?: AbortSignal): Promise<PreviewPage> {
  const response = await workspaceApi.get<ArrayBuffer>(`/api/documents/${encodeURIComponent(path)}/raw`, {
    responseType: 'arraybuffer', signal, validateStatus: status => status === 206 || status === 416,
    headers: { Range: `bytes=${offset}-${offset + PREVIEW_PAGE_BYTES - 1}` },
  })
  console.info(`[FILE_PREVIEW] id=${randomUUID()} source=workspace offset=${offset} bytes=${response.data.byteLength}`)
  return decodePreviewPage(response.status, String(response.headers['content-range'] || ''), response.data, offset)
}

/** Where a line starts in a large file; the server counts, so nothing before it is transferred. */
export async function readLineOffset(path: string, line: number, signal?: AbortSignal): Promise<number> {
  const response = await workspaceApi.get<{ offset?: number; found?: boolean }>(`/api/documents/${encodeURIComponent(path)}/raw`, { params: { line_offset: line }, signal })
  return response.data?.found ? Number(response.data.offset ?? 0) : 0
}

export function isPagedTextFile(name: string) {
  return !/-conversation\.json$/i.test(name) && /\.(txt|md|markdown|log|csv|tsv|json|jsonl|ya?ml|xml|diff|patch|js|jsx|ts|tsx|py|go|java|c|cpp|cs|php|rb|rs|sql|sh|bash|zsh|css)$/i.test(name)
}

export function decodePreviewPage(status: number, contentRange: string, data: ArrayBuffer, offset: number): PreviewPage {
  if (status === 416 && contentRange === 'bytes */0' && offset === 0) return { text: '', offset: 0, next: 0, total: 0 }
  const bytes = new Uint8Array(data)
  const range = /^bytes (\d+)-(\d+)\/(\d+)$/.exec(contentRange)
  if (status !== 206 || !range || Number(range[1]) !== offset || Number(range[2]) + 1 !== offset + bytes.length || Number(range[3]) < offset + bytes.length || bytes.length === 0 || bytes.length > PREVIEW_PAGE_BYTES) {
    throw new Error('This server did not return a bounded file preview. Download the file instead.')
  }
  let length = bytes.length
  const total = Number(range[3])
  // Preserve a multi-byte UTF-8 character crossing the next range boundary.
  if (offset + length < total) {
    let start = length - 1
    while (start >= 0 && (bytes[start] & 0xc0) === 0x80) start--
    const first = bytes[start]
    const width = first >= 0xf0 ? 4 : first >= 0xe0 ? 3 : first >= 0xc0 ? 2 : 1
    if (start >= 0 && length - start < width) length = start
  }
  return { text: new TextDecoder().decode(bytes.subarray(0, length)), offset, next: offset + length, total }
}
