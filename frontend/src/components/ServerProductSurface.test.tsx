// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ServerProductSurface } from './ServerProductSurface'

vi.mock('../services/api', () => ({ getAuthToken: () => 'app-session-jwt' }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: (state: { user: { id: string } }) => unknown) => select({ user: { id: 'alice' } }) }))
vi.mock('./ModePresetBar', () => ({ ModePresetBar: () => null }))
let container: HTMLDivElement
let root: Root
beforeEach(() => {
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
 container = document.createElement('div')
 document.body.appendChild(container)
 root = createRoot(container)
})
afterEach(async () => {
 await act(async () => root.unmount())
 container.remove()
 vi.unstubAllGlobals()
})
it('loads only after access succeeds and declares the trusted UI restrictions', async () => {
 const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ user_id: 'alice' }) })
 vi.stubGlobal('fetch', fetcher)
 await act(async () => { root.render(<ServerProductSurface product="llm-gateway" label="LLM Gateway" />) })
 expect(fetcher).toHaveBeenCalledWith('/api/server-products/llm-gateway/session', expect.objectContaining({ method: 'POST', credentials: 'same-origin', cache: 'no-store', headers: { Authorization: 'Bearer app-session-jwt', 'X-AgentWorks-Product-Session': '1' } }))
 const frame = container.querySelector('iframe')
 expect(frame?.getAttribute('sandbox')).toBe('allow-scripts allow-same-origin')
 expect(frame?.getAttribute('referrerpolicy')).toBe('no-referrer')
 expect(frame?.getAttribute('src')).toBe('/api/server-products/llm-gateway/ui/')
})
it('does not load the UI when access is rejected', async () => {
 vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
 await act(async () => { root.render(<ServerProductSurface product="llm-gateway" label="LLM Gateway" />) })
 expect(container.querySelector('iframe')).toBeNull()
 expect(container.querySelector('[role="alert"]')?.textContent).toContain('unavailable for this account')
})

it('does not mount an iframe after an aborted bootstrap resolves', async () => {
 let resolve!: (response: { ok: boolean }) => void
 vi.stubGlobal('fetch', vi.fn(() => new Promise(done => { resolve = done })))
 await act(async () => { root.render(<ServerProductSurface product="llm-gateway" label="LLM Gateway" />) })
 await act(async () => { root.render(null); resolve({ ok: true }) })
 expect(container.querySelector('iframe')).toBeNull()
})
