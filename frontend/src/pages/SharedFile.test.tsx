// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
vi.mock('../services/api', () => ({ getApiBaseUrl: () => 'http://localhost:18743', apiUrlForPath: (route: string) => `http://localhost:18743${route}`, getAuthToken: () => 'app-session' }))
vi.mock('../components/ui/MarkdownRenderer', () => ({ MarkdownRenderer: () => <div>Markdown</div> }))
vi.mock('../components/ui/RenderedContentSearch', () => ({ useRenderedContentSearch: () => ({}), RenderedContentSearchButton: () => null, RenderedContentSearchBar: () => null }))
vi.mock('../components/ui/CsvRenderer', () => ({ CsvRenderer: () => null }))
vi.mock('../components/ui/DiffRenderer', () => ({ DiffRenderer: () => null }))
vi.mock('../components/files/DocumentPreview', async importOriginal => ({ ...await importOriginal<typeof import('../components/files/DocumentPreview')>(), DocumentPreview: ({ name, data }: { name: string; data?: ArrayBuffer | null }) => <div data-document-preview={name}>{data ? new TextDecoder().decode(data) : ''}</div> }))
import { SharedFile } from './SharedFile'
import { WorkspaceImage } from '../components/ui/WorkspaceImage'
import { sharedLink } from '../utils/sharedLinks'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.restoreAllMocks())

async function mount(element: React.ReactNode) {
  const host = document.createElement('div')
  const root = createRoot(host)
  await act(async () => root.render(element))
  return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

describe('Shared file content', () => {
  // PLAT-746-1029: document previews read bounded byte ranges and render through the shared DocumentPreview.
  it.each(['pdf', 'docx'])('fetches %s with authentication and cleans up its binary preview', async ext => {
    const fetchFile = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('binary', { status: 206, headers: { 'Content-Type': 'application/octet-stream', 'Content-Range': 'bytes 0-5/6' } }))
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:asset')
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    const path = `Workflow/example/docs/日本 report.${ext}`
    const encoded = new URL(sharedLink('http://localhost', 'file', path)).searchParams.get('path')!
    const view = await mount(<SharedFile encodedPath={encoded} />)
    try {
      const [requestURL, options] = fetchFile.mock.calls[0]
      expect(new URL(String(requestURL)).searchParams.get('path')).toBe(encoded)
      expect(options?.headers).toEqual({ Authorization: 'Bearer app-session', Range: 'bytes=0-262143' })
      expect(String(requestURL)).not.toContain('app-session')
      expect(view.host.textContent).toContain('Download')
      expect(view.host.querySelector(`[data-document-preview="日本 report.${ext}"]`)?.textContent).toBe('binary')
    } finally { await view.cleanup() }
    expect(revoke).toHaveBeenCalledWith('blob:asset')
  })

  it('isolates HTML scripts in a sandbox', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('<h1>Report</h1><script>parent.alert(1)</script>', { headers: { 'Content-Type': 'text/html' } }))
    const view = await mount(<SharedFile encodedPath={btoa('Workflow/example/report.html')} />)
    try {
      const frame = view.host.querySelector('iframe')!
      expect(frame.getAttribute('sandbox')).toBe('')
      expect(frame.srcdoc).toContain('<h1>Report</h1>')
    } finally { await view.cleanup() }
  })

  it('shows sign-in when the app session expires', async () => {
    for (const name of ['report.md', 'report.pdf', 'report.bin']) {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('', { status: 401 }))
      const view = await mount(<SharedFile encodedPath={btoa(`Workflow/example/${name}`)} />)
      try { expect(view.host.textContent).toContain('Login Required') } finally { await view.cleanup() }
    }
  })

  it('loads Markdown workspace images with headers instead of exposing a token in the URL', async () => {
    const fetchImage = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('image', { headers: { 'Content-Type': 'image/png' } }))
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:image')
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    const view = await mount(<WorkspaceImage path="Workflow/example/docs/chart.png" alt="Chart" />)
    try {
      expect(fetchImage.mock.calls[0][1]?.headers).toEqual({ Authorization: 'Bearer app-session' })
      expect(view.host.querySelector('img')?.src).toBe('blob:image')
    } finally { await view.cleanup() }
    expect(revoke).toHaveBeenCalledWith('blob:image')
  })
})
