import { randomUUID } from '../../utils/randomUUID'
import { workspaceApi } from '../../services/api'
import { PREVIEW_PAGE_BYTES } from './readPreviewPage'

export type BinaryPage = { bytes: Uint8Array<ArrayBuffer>; total: number }
export const DOCUMENT_PREVIEW_MAX_BYTES = 32 * 1024 * 1024

export function binaryPreviewPage(status: number, contentRange: string, data: ArrayBuffer, offset: number): BinaryPage {
  const match = /^bytes (\d+)-(\d+)\/(\d+)$/.exec(contentRange)
  if (status !== 206 || !match || Number(match[1]) !== offset || Number(match[2]) + 1 !== offset + data.byteLength || data.byteLength > PREVIEW_PAGE_BYTES || !data.byteLength || Number(match[3]) < offset + data.byteLength) throw new Error('The server could not return a bounded document preview.')
  return { bytes: new Uint8Array(data), total: Number(match[3]) }
}

/** Office ZIPs and notebook JSON need a complete bounded document, not an unbounded download. */
export async function readBoundedDocument(read: (offset: number) => Promise<BinaryPage>, maximum = DOCUMENT_PREVIEW_MAX_BYTES): Promise<ArrayBuffer> {
  const first = await read(0)
  if (first.total > maximum) throw new Error(`This document is too large for an inline preview (limit ${maximum / 1024 / 1024} MB). Download it to open it.`)
  const bytes = new Uint8Array(first.total)
  bytes.set(first.bytes)
  let offset = first.bytes.length
  while (offset < bytes.length) {
    const page = await read(offset)
    if (page.total !== bytes.length || !page.bytes.length || offset + page.bytes.length > bytes.length) throw new Error('The file changed while its preview was loading. Open it again.')
    bytes.set(page.bytes, offset)
    offset += page.bytes.length
  }
  return bytes.buffer
}

export function readWorkspaceDocument(path: string, signal?: AbortSignal) {
  return readBoundedDocument(async offset => {
    const response = await workspaceApi.get<ArrayBuffer>(`/api/documents/${encodeURIComponent(path)}/raw`, {
      responseType: 'arraybuffer', signal, headers: { Range: `bytes=${offset}-${offset + PREVIEW_PAGE_BYTES - 1}` },
    })
    console.info(`[FILE_PREVIEW] id=${randomUUID()} source=workspace kind=document offset=${offset} bytes=${response.data.byteLength}`)
    return binaryPreviewPage(response.status, String(response.headers['content-range'] || ''), response.data, offset)
  })
}
