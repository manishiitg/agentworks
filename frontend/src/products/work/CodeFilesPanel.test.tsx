// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const account = vi.hoisted(() => ({ user: { id: 'alice' } }))
const workspace = vi.hoisted(() => ({ activeWorkspaceId: 'hosted' }))
const transport = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: transport, getApiBaseUrl: () => 'https://code.example.test' }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((selector: (state: typeof account) => unknown) => selector(account), { getState: () => account }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: Object.assign((selector: (state: typeof workspace) => unknown) => selector(workspace), { getState: () => workspace }) }))
import { CodeFilesPanel } from './CodeFilesPanel'
import { codeLocalFilesForChat, readCodeFilesPreference, writeCodeFilesPreference } from './codeLocalFiles'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const project = 'Chats/Code/projects/my-project'
const target = { device_id: 'laptop', resource_id: 'project' }
let root: Root | undefined
beforeEach(() => {
  const storage = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => { storage.set(key, value) }, clear: () => storage.clear() })
  localStorage.clear(); account.user.id = 'alice'; workspace.activeWorkspaceId = 'hosted'
  transport.get.mockReset(); transport.post.mockReset()
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'project', writable: false, guard: {} }] }] } })
  transport.post.mockImplementation(async (_url, request) => ({ data: request.operation === 'list'
    ? { entries: [{ path: 'README.md', type: 'file' }] }
    : { file: { path: 'README.md', exists: true, content: 'Local source', revision: 'rev-1' } } }))
})
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = ''; vi.useRealTimers() })
async function render() {
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  const ask = vi.fn(async () => {})
  await act(async () => root?.render(<CodeFilesPanel workspacePath={project} serverFiles={<p>Server source</p>} onAsk={ask} />))
  return { host, ask }
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

it('defaults to server files and requires a folder selection before local chat', async () => {
  const { host } = await render()
  expect(host.textContent).toContain('Server source'); expect(transport.get).not.toHaveBeenCalled()
  await choose(host, 'Files location', 'computer')
  expect(host.textContent).not.toContain('Server source')
  expect(() => codeLocalFilesForChat(project)).toThrow('Choose a connected computer')
  await choose(host, 'Computer and shared folder', JSON.stringify(['laptop', 'project']))
  expect(host.textContent).toContain('Connected'); expect(host.textContent).toContain('Read only')
  expect(codeLocalFilesForChat(project)).toEqual(target)
  await click(host, 'README.md')
  expect(host.querySelector<HTMLTextAreaElement>('textarea')?.value).toBe('Local source')
  expect(host.querySelector<HTMLTextAreaElement>('textarea')?.readOnly).toBe(true)
  expect(host.textContent).not.toContain('Save to computer')
  await click(host, 'Ask Code')
  expect(transport.post).toHaveBeenCalledWith('/api/devices/laptop/files', expect.objectContaining({ resource_id: 'project', operation: 'read', path: 'README.md' }), expect.anything())
  await click(host, 'Folder'); expect(host.textContent).toContain('README.md')
})

it('preserves offline selections without rendering server files or silently widening chat', async () => {
  writeCodeFilesPreference(project, { location: 'computer', target })
  transport.get.mockResolvedValue({ data: { devices: [] } })
  const { host } = await render()
  expect(host.textContent).toContain('Offline')
  expect(host.textContent).toContain('Code will not switch to server files')
  expect(host.textContent).toContain('--scopes devices:connect')
  expect(host.textContent).not.toContain('Server source')
  expect(codeLocalFilesForChat(project)).toEqual(target)
  expect(transport.post).not.toHaveBeenCalled()
})

it('isolates project choices by account, server workspace and project', () => {
  writeCodeFilesPreference(project, { location: 'computer', target })
  expect(codeLocalFilesForChat(`${project}-other`)).toBeUndefined()
  account.user.id = 'bob'; expect(readCodeFilesPreference(project).location).toBe('server')
  account.user.id = 'alice'; workspace.activeWorkspaceId = 'other-server'
  expect(codeLocalFilesForChat(project)).toBeUndefined()
  workspace.activeWorkspaceId = 'hosted'; expect(codeLocalFilesForChat(project)).toEqual(target)
})

it('reconciles uncertain saves with the identical request ID and revision', async () => {
  vi.useFakeTimers()
  writeCodeFilesPreference(project, { location: 'computer', target })
  transport.get.mockResolvedValue({ data: { devices: [{ device_id: 'laptop', resources: [{ id: 'project', writable: true, guard: {} }] }] } })
  const { host } = await render()
  await click(host, 'README.md')
  const textarea = host.querySelector<HTMLTextAreaElement>('textarea')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, 'Edited locally')
    textarea.dispatchEvent(new Event('input', { bubbles: true }))
  })
  transport.post.mockRejectedValueOnce({ response: { data: { error: { message: 'Device disconnected' } } } })
  await click(host, 'Save to computer')
  expect(host.textContent).toContain('Device disconnected')
  const write = transport.post.mock.calls.at(-1)![1]
  transport.get.mockResolvedValueOnce({ data: { devices: [] } })
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  expect(host.querySelector<HTMLTextAreaElement>('textarea')?.value).toBe('Edited locally')
  const retry = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Retry save')!
  expect(retry.disabled).toBe(true)
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  expect(retry.disabled).toBe(false)
  transport.post.mockResolvedValueOnce({ data: { receipt: { applied: true, revision: 'rev-2', identity: { username: 'alice' } } } })
  await click(host, 'Retry save')
  expect(transport.post.mock.calls.at(-1)![1]).toEqual(write)
  expect(write).toMatchObject({ expected_revision: 'rev-1', content: 'Edited locally', operation: 'write' })
  expect(host.textContent).toContain('Saved by alice')
})
