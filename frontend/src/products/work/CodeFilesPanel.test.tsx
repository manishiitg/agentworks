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
import { codeChatModeForChat, codeLocalFilesForChat, readCodeFilesPreference, registerLocalFilesPersister, resetLatestCliVersionForTests, setPendingLocalLink, setProjectLocalFiles, setProjectMode, takePendingLocalLink, writeCodeFilesPreference } from './codeLocalFiles'

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

it('shows server files with no header or label, and keeps connection controls in Settings', async () => {
  const { host } = await render(false, true)
  expect(host.textContent).toContain('Server source'); expect(transport.get).not.toHaveBeenCalled()
  expect(host.textContent).not.toContain('Server files')
  expect(host.textContent).not.toContain('Manage file connection')
  expect(host.querySelector('[aria-label^="File connection"]')).toBeNull()
  expect(host.querySelector('select')).toBeNull()
})

it('connects the current session and shows only CLI connection controls', async () => {
  const { host, showFiles } = await render(true)
  await click(host, 'Connect local files')
  expect(host.textContent).toContain('agentworks start --server')
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  await choose(host, 'Computer and shared folder', JSON.stringify(['laptop', 'project']))
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  expect(host.textContent).toContain('services on your computer (localhost), and your local network')
  expect(host.textContent).toContain('they do not limit network destinations')
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
  expect(host.textContent).not.toContain('Disconnect local files')
  await click(host, 'Details')
  await click(host, 'Disconnect local files')
  expect(codeLocalFilesForChat(session)).toEqual(target)
  expect(host.textContent).toContain('not a quick toggle')
  await click(host, 'Switch to server files')
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  expect(transport.post).not.toHaveBeenCalled()
  await showFiles()
  expect(host.textContent).toContain('Server source')
})

it('preserves offline selections without rendering server files or silently widening chat', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  transport.get.mockResolvedValue({ data: { devices: [] } })
  vi.useFakeTimers()
  const { host } = await render()
  expect(host.textContent).toContain('Offline')
  // A short gap (the CLI renewing its sign-in) does not ask; a folder that stays missing does.
  expect(document.body.textContent).not.toContain('Your computer is not connected')
  await act(async () => { await vi.advanceTimersByTimeAsync(21_000) })
  vi.useRealTimers()
  expect(document.body.textContent).toContain('Your computer is not connected')
  expect(document.body.textContent).toContain('agentworks start')
  expect(host.textContent).toContain('agentworks start --server')
  expect(host.textContent).toContain('Costs and Models')
  expect(host.textContent).not.toContain('Server source')
  expect(codeLocalFilesForChat(session)).toEqual(target)
  expect(transport.post).not.toHaveBeenCalled()
})

it('when the saved folder is not connected but another one is, it offers that folder instead of saying not connected', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'other-folder', writable: true, shell: true, guard: {} }] }] } })
  await render(true)
  expect(document.body.textContent).toContain('A different folder is connected')
  expect(document.body.textContent).not.toContain('Your computer is not connected')
  await act(async () => [...document.body.querySelectorAll('button')].find(b => b.textContent === 'Use other-folder')!.click())
  expect(codeLocalFilesForChat(session)).toEqual({ device_id: 'laptop', resource_id: 'other-folder' })
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
  await click(host, 'Details')
  await click(host, 'Disconnect local files')
  await click(host, 'Keep local connection')
  expect(codeChatModeForChat(session)).toBe('local')
  expect(transport.post).not.toHaveBeenCalled()
})

it('prevents changing connections during an active turn', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  chats.chatTabs = { running: { sessionId: session, isStreaming: true } }
  const { host } = await render(true)
  await click(host, 'Details')
  expect(host.querySelector<HTMLSelectElement>('[aria-label="Computer and shared folder"]')!.disabled).toBe(true)
  const disconnect = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Disconnect local files')!
  expect(disconnect.disabled).toBe(true)
  expect(host.textContent).toContain('Wait for the current turn')
  expect(codeLocalFilesForChat(session)).toEqual(target)
})

it('shows shell capability and a CLI command that enables local builds and tests by default', async () => {
  writeCodeFilesPreference(session, { location: 'computer', target })
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'project', writable: true, shell: true, guard: {} }] }] } })
  const { host } = await render(true)
  expect(host.textContent).toContain('Shell commands enabled on this computer')
  await click(host, 'Details')
  expect(host.textContent).toContain('agentworks start --server')
  expect(host.textContent).not.toContain('--write-folder')
  expect(host.textContent).not.toContain('Read only')
  expect(host.textContent).toContain('Files and commands')
  expect(host.textContent).toContain('Shell commands enabled on this computer')
  expect(transport.post).not.toHaveBeenCalled()
})

it('shows one start command with no folder form, and Verify connection says what it found', async () => {
  transport.get.mockResolvedValue({ data: { devices: [] } })
  const { host } = await render(true)
  await click(host, 'Connect local files')
  expect(host.textContent).toContain('agentworks start --server')
  expect(host.textContent).toContain('--workspace')
  expect(host.textContent).toContain('agentworks stop')
  expect(host.textContent).toContain('agentworks start --debug')
  expect(host.querySelector('#local-project-folder')).toBeNull()
  expect(host.textContent).not.toContain('Nothing is connected yet')
  await click(host, 'Verify connection')
  expect(host.textContent).toContain('Nothing is connected yet')
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'project', writable: true, shell: true, guard: {} }] }] } })
  await click(host, 'Verify connection')
  expect(host.textContent).toContain('Connected: laptop / project.')
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  expect(transport.post).not.toHaveBeenCalled()
})

it('the workspace decides the mode (saved in its product.json), so a chat with a new session ID after a refresh is still Local', async () => {
  const tab = (sessionId: string, project: string) => ({ sessionId, isStreaming: false, metadata: { agentProfileProjectId: project } })
  chats.chatTabs = { a: tab('chat-before-refresh', 'code-1'), b: tab('other-project-chat', 'code-2') }
  setProjectLocalFiles('code-1', target)
  setProjectLocalFiles('code-2', null)
  expect(codeChatModeForChat('chat-before-refresh')).toBe('local')
  chats.chatTabs = { a: tab('chat-after-refresh', 'code-1'), b: chats.chatTabs.b }
  expect(codeLocalFilesForChat('chat-after-refresh')).toEqual(target)
  expect(codeChatModeForChat('other-project-chat')).toBe('server')
  // Switching saves through the workspace's product.json, and only then is the workspace on server files.
  const saved = vi.fn(async () => {})
  registerLocalFilesPersister(saved)
  writeCodeFilesPreference('chat-after-refresh', { location: 'server' })
  expect(saved).toHaveBeenCalledWith('code-1', null)
  expect(codeChatModeForChat('chat-after-refresh')).toBe('server')
  // A save that fails puts the previous choice back.
  setProjectLocalFiles('code-1', target)
  registerLocalFilesPersister(async () => { throw new Error('offline') })
  writeCodeFilesPreference('chat-after-refresh', { location: 'server' })
  await act(async () => {})
  expect(codeChatModeForChat('chat-after-refresh')).toBe('local')
  registerLocalFilesPersister(undefined)
})

it('a link from `agentworks start` names its workspace, is handed out once, and a bad one is ignored', () => {
  setPendingLocalLink({ target: { device_id: '../bad', resource_id: 'project' }, workspace: 'App' })
  setPendingLocalLink({ target, workspace: '  ' })
  expect(takePendingLocalLink()).toBeUndefined()
  setPendingLocalLink({ target, workspace: 'App' })
  expect(takePendingLocalLink()).toEqual({ target, workspace: 'App' })
  expect(takePendingLocalLink()).toBeUndefined()
  // It never configures a chat or workspace by itself.
  setPendingLocalLink({ target, workspace: 'App' })
  expect(codeChatModeForChat('fresh-chat')).toBe('server')
})

it('the setup page also gives Windows commands, with the no-sandbox note', async () => {
  transport.get.mockResolvedValue({ data: { devices: [] } })
  const { host } = await render(true)
  await click(host, 'Connect local files')
  expect(host.textContent).not.toContain('install-agentworks.ps1')
  await click(host, 'Windows')
  expect(host.textContent).toContain('install-agentworks.ps1')
  expect(host.textContent).toContain('agentworks start --server')
  expect(host.textContent).not.toContain('$HOME') // the folder is a separate cd step; the command is just agentworks start
  expect(host.textContent).toContain('commands the agent runs are not sandboxed')
  await click(host, 'macOS / Linux')
  expect(host.textContent).not.toContain('install-agentworks.ps1')
})

it('a project made in Local mode is Local before any folder is linked, and Dev or Cowork projects are not', () => {
  const tab = (sessionId: string, project: string) => ({ sessionId, isStreaming: false, metadata: { agentProfileProjectId: project } })
  chats.chatTabs = { a: tab('new-local-chat', 'proj-local'), b: tab('cowork-chat', 'proj-cowork') }
  setProjectLocalFiles('proj-local', null); setProjectMode('proj-local', 'local')
  setProjectLocalFiles('proj-cowork', null); setProjectMode('proj-cowork', 'cowork')
  expect(codeChatModeForChat('new-local-chat')).toBe('local')
  expect(codeLocalFilesForChat('new-local-chat')).toBeUndefined()
  expect(codeChatModeForChat('cowork-chat')).toBe('server')
  setProjectLocalFiles('proj-local', target)
  expect(codeLocalFilesForChat('new-local-chat')).toEqual(target)
})

it('asks the person to update a CLI that is not the build this server offers, and says nothing when it is current', async () => {
  const devices = (cli_version?: string, cli_build?: string) => ({ data: { devices: [{ device_id: 'laptop', ...(cli_version ? { cli_version } : {}), ...(cli_build ? { cli_build } : {}), resources: [{ id: 'project', writable: true, shell: true, guard: {} }] }] } })
  const serve = (cli_version: string | undefined, latest: string, mineBuild?: string, latestBuild?: string) => transport.get.mockImplementation(async (url: string) => String(url).includes('version.json') ? { data: { version: latest, ...(latestBuild ? { cli_build: latestBuild } : {}) } } : devices(cli_version, mineBuild))
  // [my revision, server revision, my build, server build, notice?]: the build decides when both have one, so a server-only deploy is not "older".
  for (const [mine, latest, mineBuild, latestBuild, notice] of [['aaaaaaa111', 'bbbbbbb222', undefined, undefined, true], [undefined, 'bbbbbbb222', undefined, undefined, true], ['bbbbbbb222', 'bbbbbbb222', undefined, undefined, false], ['dev', 'bbbbbbb222', undefined, undefined, false], ['aaaaaaa111', 'bbbbbbb222', 'build1', 'build1', false], ['aaaaaaa111', 'bbbbbbb222', 'build1', 'build2', true], ['aaaaaaa111', 'bbbbbbb222', undefined, 'build2', true]] as const) {
    resetLatestCliVersionForTests()
    serve(mine, latest, mineBuild, latestBuild)
    writeCodeFilesPreference(session, { location: 'computer', target })
    const { host } = await render(true)
    await act(async () => { await Promise.resolve() })
    expect(host.textContent?.includes('Update your CLI'), `${mine}/${mineBuild} vs ${latest}/${latestBuild}`).toBe(notice)
    if (notice) expect(host.textContent).toContain('agentworks update')
    act(() => root?.unmount()); root = undefined; document.body.innerHTML = ''
  }
})

it('shows Downloads permission before switching and on the connected summary', async () => {
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'project', writable: false, shell: true, downloads: true, guard: {} }] }] } })
  const { host } = await render(true)
  await click(host, 'Connect local files')
  await choose(host, 'Computer and shared folder', JSON.stringify(['laptop', 'project']))
  expect(host.textContent).toContain('Downloads is also shared with read and write access')
  expect(codeLocalFilesForChat(session)).toBeUndefined()
  await click(host, 'Use this folder')
  expect(host.textContent).toContain('Project: read only')
  expect(host.textContent).toContain('Downloads: read and write')
  const calls = transport.get.mock.calls.length
  await click(host, 'Verify connection')
  expect(transport.get.mock.calls.length).toBe(calls + 1)
  expect(codeLocalFilesForChat(session)).toEqual(target)
})
