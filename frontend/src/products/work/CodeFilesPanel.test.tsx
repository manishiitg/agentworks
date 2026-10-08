// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const chats = vi.hoisted(() => ({ chatTabs: {} as Record<string, {sessionId:string; isStreaming:boolean; hasRunningBgAgents?:boolean}> }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (selector: (state: typeof chats) => unknown) => selector(chats) }))
const account = vi.hoisted(() => ({ user: { id: 'alice' } }))
const workspace = vi.hoisted(() => ({ activeWorkspaceId: 'hosted' }))
const transport = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: transport, getApiBaseUrl: () => 'https://code.example.test' }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((selector: (state: typeof account) => unknown) => selector(account), { getState: () => account }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: Object.assign((selector: (state: typeof workspace) => unknown) => selector(workspace), { getState: () => workspace }) }))
import { CodeChatConnectionStatus } from './CodeChatConnectionStatus'
import { CodeFilesPanel, CodeLocalFilesSettings } from './CodeFilesPanel'
import { codeChatModeForChat, codeLocalFilesForChat, readCodeFilesPreference, writeCodeFilesPreference } from './codeLocalFiles'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const session = 'code-session-1'
const target = { device_id: 'laptop', resource_id: 'project' }
let root: Root | undefined
beforeEach(() => {
  const storage = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => { storage.set(key, value) }, clear: () => storage.clear() })
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
  const manage = vi.fn()
  const files = <CodeFilesPanel sessionId={session} serverFiles={<p>Server source</p>} onAsk={ask} onManageConnection={manage} />
  await act(async () => root?.render(<>{status && <CodeChatConnectionStatus sessionId={session} />}{settings ? <CodeLocalFilesSettings sessionId={session} /> : files}</>))
  const showFiles = async () => { await act(async () => root?.render(files)) }
  return { host, ask, manage, showFiles }
}
async function choose(host: HTMLElement, label: string, value: string) {
  await act(async () => {
    const select = host.querySelector<HTMLSelectElement>(`select[aria-label="${label}"]`)!
    select.value = value; select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
async function click(host: HTMLElement, text: string) {
  const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === text)
  expect(button, `button ${text}`).toBeDefined()
  await act(async () => button!.click())
}

it('keeps connection controls in Settings and links there from Files', async () => {
  const { host, manage } = await render()
  expect(host.textContent).toContain('Server source'); expect(transport.get).not.toHaveBeenCalled()
  expect(host.querySelector('select')).toBeNull()
  await click(host, 'Manage file connection')
  expect(manage).toHaveBeenCalledOnce()
})

it('connects the current session and shows only CLI connection controls', async () => {
  const { host, showFiles } = await render(true)
  await click(host, 'Connect local files')
  expect(host.textContent).toContain('--scopes devices:connect')
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  await choose(host, 'Computer and shared folder', JSON.stringify(['laptop', 'project']))
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  await click(host, 'Use this folder')
  expect(host.textContent).toContain('Connected')
  expect(codeLocalFilesForChat(session)).toEqual(target)
  expect(codeLocalFilesForChat('another-chat-in-the-same-project')).toBeUndefined()
  expect(transport.post).not.toHaveBeenCalled()
  await showFiles()
  expect(host.textContent).toContain('Local CLI connection')
  expect(host.querySelector('textarea')).toBeNull()
  expect(host.textContent).not.toContain('Server source')
  expect(host.textContent).not.toContain('README.md')
  expect(transport.post).not.toHaveBeenCalled()

})

it('disconnects only this session in Settings without a server runtime mutation', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  const { host, showFiles } = await render(true)
  await click(host, 'Disconnect local files')
  expect(codeLocalFilesForChat(session)).toEqual(target)
  expect(host.textContent).toContain('normal Code features')
  await click(host, 'Switch to server files')
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  expect(transport.post).not.toHaveBeenCalled()
  await showFiles()
  expect(host.textContent).toContain('Server source')
})

it('preserves offline selections without rendering server files or silently widening chat', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  transport.get.mockResolvedValue({ data: { devices: [] } })
  const { host } = await render()
  expect(host.textContent).toContain('Offline')
  expect(host.textContent).toContain('--scopes devices:connect')
  expect(host.textContent).toContain('Costs and Models')
  expect(host.textContent).not.toContain('Server source')
  expect(codeLocalFilesForChat(session)).toEqual(target)
  expect(transport.post).not.toHaveBeenCalled()
})

it('isolates file connections by account, server workspace and session', () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  expect(codeLocalFilesForChat(`${session}-other`)).toBeUndefined()
  account.user.id = 'bob'; expect(readCodeFilesPreference(session).location).toBe('server')
  account.user.id = 'alice'; workspace.activeWorkspaceId = 'other-server'
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  workspace.activeWorkspaceId = 'hosted'; expect(codeLocalFilesForChat(session)).toEqual(target)
})

it('only applies mode changes from the connection panel after showing consequences', async () => {
  const { host } = await render(true, true)
  expect(codeChatModeForChat(session)).toBe('server')
  expect(host.querySelector('[aria-label="Code chat mode"]')).toBeNull()
  await click(host, 'Connect local files')
  expect(codeChatModeForChat(session)).toBe('server')
  await choose(host, 'Computer and shared folder', JSON.stringify(['laptop', 'project']))
  expect(codeChatModeForChat(session)).toBe('server')
  expect(host.textContent).toContain('sent to the server and model provider')
  expect(host.textContent).toContain('unavailable in Local mode')
  expect(host.textContent).toContain('This folder is read only')
  await click(host, 'Use this folder')
  expect(codeChatModeForChat(session)).toBe('local')
  expect(codeLocalFilesForChat(session)).toEqual(target)
  expect(codeChatModeForChat('other-chat')).toBe('server')
  const connection = host.querySelector('[aria-label="File connection: Local files · project"]')!
  expect(connection.tagName).toBe('SPAN')
  expect(connection.querySelector('button,select')).toBeNull()
  await click(host, 'Disconnect local files')
  await click(host, 'Keep local connection')
  expect(codeChatModeForChat(session)).toBe('local')
  expect(transport.post).not.toHaveBeenCalled()
})

it('prevents changing connections during an active turn', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  chats.chatTabs = { running: { sessionId: session, isStreaming: true } }
  const { host } = await render(true)
  expect(host.querySelector<HTMLSelectElement>('select')!.disabled).toBe(true)
  const disconnect = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Disconnect local files')!
  expect(disconnect.disabled).toBe(true)
  expect(host.textContent).toContain('Wait for the current turn')
  expect(codeLocalFilesForChat(session)).toEqual(target)
})

it('shows shell capability and a CLI command that enables local builds and tests by default', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'project', writable: true, shell: true, guard: {} }] }] } })
  const { host } = await render(true)
  expect(host.textContent).toContain('--write-folder project=/absolute/path/to/project')
  expect(host.textContent).not.toContain('--shell')
  expect(host.textContent).toContain('Shell commands are enabled automatically')
  expect(host.textContent).toContain('Files and commands')
  expect(host.textContent).toContain('Shell commands enabled on this computer')
  expect(transport.post).not.toHaveBeenCalled()
})
