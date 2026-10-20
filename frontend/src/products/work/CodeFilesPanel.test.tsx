// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const chats = vi.hoisted(() => ({ chatTabs: {} as Record<string, {sessionId:string; isStreaming:boolean; hasRunningBgAgents?:boolean; metadata?:{agentProfileProjectId?:string}}> }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: Object.assign((selector: (state: typeof chats) => unknown) => selector(chats), { getState: () => chats }) }))
const account = vi.hoisted(() => ({ user: { id: 'alice' } }))
const workspace = vi.hoisted(() => ({ activeWorkspaceId: 'hosted' }))
const transport = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: transport, getApiBaseUrl: () => 'https://code.example.test' }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((selector: (state: typeof account) => unknown) => selector(account), { getState: () => account }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: Object.assign((selector: (state: typeof workspace) => unknown) => selector(workspace), { getState: () => workspace }) }))
import { CodeChatConnectionStatus } from './CodeChatConnectionStatus'
import { CodeFilesPanel, CodeLocalFilesSettings } from './CodeFilesPanel'
import { codeLocalFilesForChat, readCodeFilesPreference, registerLocalFilesPersister, resetLatestCliVersionForTests, setPendingLocalLink, setProjectLocalFiles, setProjectMode, takePendingLocalLink, writeCodeFilesPreference } from './codeLocalFiles'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const session = 'code-session-1'
const target = { device_id: 'laptop', resource_id: 'project' }
let root: Root | undefined
beforeEach(() => {
  const storage = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => { storage.set(key, value) }, removeItem: (key: string) => { storage.delete(key) }, clear: () => storage.clear() })
  localStorage.clear(); chats.chatTabs = {}; account.user.id = 'alice'; workspace.activeWorkspaceId = 'hosted'
  transport.get.mockReset(); transport.post.mockReset()
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'project', writable: false, guard: {} }] }] } })
  transport.post.mockImplementation(async (_url, request) => ({ data: request.operation === 'list'
    ? { entries: [{ path: 'README.md', type: 'file' }] }
    : { file: { path: 'README.md', exists: true, content: 'Local source', revision: 'rev-1' } } }))
})
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = ''; vi.useRealTimers() })
async function render(settings = false, status = false) {
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  const ask = vi.fn(async () => {})
  const files = <CodeFilesPanel sessionId={session} serverFiles={<p>Server source</p>} onAsk={ask} />
  await act(async () => root?.render(<>{status && <CodeChatConnectionStatus sessionId={session} />}{settings ? <CodeLocalFilesSettings sessionId={session} /> : files}</>))
  const showFiles = async () => { await act(async () => root?.render(files)) }
  return { host, ask, showFiles }
}

it('keeps server files visible with a laptop connection', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  const { host } = await render()
  expect(host.textContent).toContain('Server source')
})
it('shows optional laptop setup without replacing server work or offering Publish', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  const { host } = await render(true)
  expect(host.textContent).toContain('Your Crew runs on the server')
  expect(host.textContent).toContain('Disconnect laptop')
  expect(host.textContent).not.toContain('Publish')
})
