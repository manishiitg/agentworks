import { describe, it, expect } from 'vitest'
import { withReportBootstrap } from './reportHostRuntime'

describe('withReportBootstrap', () => {
  it('rejects chat before initialization instead of replaying an action on render', async () => {
    const win: Record<string, unknown> = {}
    const stub = withReportBootstrap('<div></div>').match(/<script>([\s\S]*?)<\/script>/)![1]
    new Function('window', stub)(win)
    const api = win.report as { sendChatMessage: (message: string) => Promise<unknown>; chat: { available: boolean; open: () => Promise<void> } }
    await expect(api.sendChatMessage('Apply item 42')).rejects.toThrow('not ready')
    expect(api.chat.available).toBe(false)
    await expect(api.chat.open()).rejects.toThrow('not ready')
    expect(win.__reportPendingCalls).toEqual([])
  })
  it('injects the stub immediately after <head> so it precedes any author script', () => {
    const html = '<html><head><title>t</title></head><body><script>go()</script></body></html>'
    const out = withReportBootstrap(html)
    expect(out.indexOf('__reportQueuedCallbacks')).toBeLessThan(out.indexOf('<title>'))
  })

  it('falls back to after <html> when there is no <head>', () => {
    const out = withReportBootstrap('<html><body>x</body></html>')
    expect(out.indexOf('__reportQueuedCallbacks')).toBeLessThan(out.indexOf('<body>'))
  })

  it('handles a bare fragment with no document shell', () => {
    expect(withReportBootstrap('<div>x</div>')).toContain('__reportQueuedCallbacks')
  })

  it('is idempotent — re-wrapping does not inject a second stub', () => {
    const once = withReportBootstrap('<html><head></head><body></body></html>')
    const twice = withReportBootstrap(once)
    expect(twice).toBe(once)
  })

  it('queues callbacks registered before the API exists, and runs them on flush', () => {
    const win: Record<string, unknown> = {}
    // Execute the real stub source against a bare window, as the iframe would.
    const stub = withReportBootstrap('<div></div>').match(/<script>([\s\S]*?)<\/script>/)![1]
    new Function('window', stub)(win)

    const calls: string[] = []
    ;(win.report as { ready: (f: () => void) => void }).ready(() => calls.push('render'))

    // Nothing runs while the API is not yet live.
    expect(calls).toEqual([])
    expect(win.__reportQueuedCallbacks).toHaveLength(1)

    // Host flush (what inject() does).
    ;(win.__reportQueuedCallbacks as Array<() => void>).forEach((fn) => fn())
    expect(calls).toEqual(['render'])
  })

  it('queues a query/get/getText/getHtml/fileUrl call made before injection instead of throwing', async () => {
    const win: Record<string, unknown> = {}
    const stub = withReportBootstrap('<div></div>').match(/<script>([\s\S]*?)<\/script>/)![1]
    new Function('window', stub)(win)

    // This is the naive pattern a report can reach for instinctively —
    // DOMContentLoaded/window.onload/a bare top-level call — running before
    // the host has injected the real API. Before this fix, window.report.query
    // was undefined at this point and calling it threw a TypeError.
    const report = win.report as {
      query: (sql: string) => Promise<unknown>
      mediaUrl: (path: string) => Promise<unknown>
      openFile: (path: string) => void
    }
    const resultPromise = report.query('select 1')
    expect(resultPromise).toBeInstanceOf(Promise)
    report.openFile('db/a.png')
    const mediaPromise = report.mediaUrl('db/assets/clip.webm')

    const pending = win.__reportPendingCalls as Array<{
      name: string
      args: unknown[]
      resolve: (v: unknown) => void
      reject: (e: unknown) => void
    }>
    expect(pending).toHaveLength(3)
    expect(pending[0]).toMatchObject({ name: 'query', args: ['select 1'] })
    expect(pending[1]).toMatchObject({ name: 'openFile', args: ['db/a.png'] })
    expect(pending[2]).toMatchObject({ name: 'mediaUrl', args: ['db/assets/clip.webm'] })

    // Host replay (what inject() does once the real API exists).
    pending[0].resolve([{ ok: 1 }])
    await expect(resultPromise).resolves.toEqual([{ ok: 1 }])
    pending[2].resolve('/api/workflow/report-media?token=file-only')
    await expect(mediaPromise).resolves.toBe('/api/workflow/report-media?token=file-only')
  })
})
