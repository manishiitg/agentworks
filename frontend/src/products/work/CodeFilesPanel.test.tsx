// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const transport = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../../services/api', () => ({ default: transport, getApiBaseUrl: () => 'https://example.test' }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (selector: (state: unknown) => unknown) => selector({ chatTabs: { own: { sessionId: 'chat', metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew' } } } }) }))
vi.mock('./codeLocalFiles', () => ({ useLocalFileDevices: () => ({ devices: [{ device_id: 'laptop', settings_managed: true }], refresh: vi.fn(async () => [{ device_id: 'laptop' }]) }), useLatestCliVersion: () => undefined, cliUpdateNeeded: () => false, localFileError: (e: Error) => e.message }))
import { CodeFilesPanel, CodeLocalFilesSettings } from './CodeFilesPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
let container: HTMLDivElement
afterEach(async () => { if (root) await act(async () => root.unmount()); container?.remove(); vi.clearAllMocks() })
it('uses backend folder permissions and changes them without reconnecting the CLI', async () => {
 const settings = { device_id: 'laptop', commands: false, folders: [{ id: 'reports', path: '/home/person/Reports', access: 'read' }] }
 transport.get.mockResolvedValue({ data: settings })
 transport.put.mockImplementation(async (_: string, next: unknown) => ({ data: next }))
 container = document.createElement('div'); document.body.append(container); root = createRoot(container)
 await act(async () => root.render(<CodeLocalFilesSettings sessionId="chat" />))
 const permission = container.querySelector('select[aria-label="Access for /home/person/Reports"]') as HTMLSelectElement
 expect(permission.value).toBe('read')
 await act(async () => { permission.value = 'write'; permission.dispatchEvent(new Event('change', { bubbles: true })) })
 expect(transport.put).toHaveBeenCalledWith('/api/agent-profiles/work/projects/crew/laptop', { ...settings, folders: [{ ...settings.folders[0], access: 'write' }] }, { skipSessionContext: true })
 // Terminal commands are their own switch, separate from the folders' Read or Read & write.
 const commands = container.querySelector('input[type="checkbox"]') as HTMLInputElement
 expect(commands.checked).toBe(false)
 await act(async () => { commands.click() })
 expect(transport.put).toHaveBeenLastCalledWith('/api/agent-profiles/work/projects/crew/laptop', expect.objectContaining({ commands: true }), { skipSessionContext: true })
 expect(container.textContent).toContain('AgentBridge uses the same CLI')
 expect(container.textContent).not.toContain('--workspace')
})
it('searches the folders of the folder picker and hides dot folders until asked', async () => {
 const settings = { device_id: 'laptop', commands: false, folders: [] }
 const directory = { path: '/Users/me', parent: '/Users', folders: ['.hidden', 'Documents', 'notes', 'notes-old'] }
 transport.get.mockImplementation(async (url: string) => ({ data: url.includes('/folders') ? directory : settings }))
 container = document.createElement('div'); document.body.append(container); root = createRoot(container)
 await act(async () => root.render(<CodeLocalFilesSettings sessionId="chat" />))
 const add = [...container.querySelectorAll('button')].find(button => button.textContent === 'Add folder') as HTMLButtonElement
 await act(async () => { add.click() })
 const names = () => [...container.querySelectorAll('button')].map(button => button.textContent).filter(text => ['.hidden', 'Documents', 'notes', 'notes-old'].includes(text ?? ''))
 expect(names()).toEqual(['Documents', 'notes', 'notes-old'])
 const type = async (input: HTMLInputElement, value: string) => act(async () => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
 })
 const search = container.querySelector('input[aria-label="Search folders"]') as HTMLInputElement
 await type(search, 'NOTES')
 expect(names()).toEqual(['notes', 'notes-old'])
 await type(search, '.hi')
 expect(names()).toEqual(['.hidden'])
})
it('keeps ordinary files on the server', async () => {
 container = document.createElement('div'); document.body.append(container); root = createRoot(container)
 await act(async () => root.render(<CodeFilesPanel sessionId="chat" onAsk={async () => {}} serverFiles={<p>Server files</p>} />))
 expect(container.textContent).toBe('Server files')
})
