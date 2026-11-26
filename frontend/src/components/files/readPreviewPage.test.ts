import { describe, expect, it, vi } from 'vitest'
vi.mock('../../services/api', () => ({ workspaceApi: {} }))
import { decodePreviewPage } from './readPreviewPage'

describe('bounded text preview', () => {
  it('keeps UTF-8 characters crossing page boundaries and refuses unbounded or mismatched responses', () => {
    const bytes = new TextEncoder().encode('a€z')
    const first = decodePreviewPage(206, 'bytes 0-2/5', bytes.slice(0, 3).buffer, 0)
    expect(first).toEqual({ text: 'a', offset: 0, next: 1, total: 5 })
    const second = decodePreviewPage(206, 'bytes 1-4/5', bytes.slice(1).buffer, 1)
    expect(first.text + second.text).toBe('a€z')
    expect(decodePreviewPage(416, 'bytes */0', new ArrayBuffer(0), 0).text).toBe('')
    expect(() => decodePreviewPage(200, '', bytes.buffer, 0)).toThrow('bounded')
    expect(() => decodePreviewPage(206, 'bytes 1-5/6', bytes.buffer, 0)).toThrow('bounded')
  })
})

it('rejects oversized office files before allocating or reading subsequent pages', async () => {
  const { readBoundedDocument } = await import('./readBoundedDocument')
  const read = vi.fn(async () => ({ bytes: new Uint8Array([1, 2]), total: 33 * 1024 * 1024 }))
  await expect(readBoundedDocument(read)).rejects.toThrow('too large')
  expect(read).toHaveBeenCalledTimes(1)
})
