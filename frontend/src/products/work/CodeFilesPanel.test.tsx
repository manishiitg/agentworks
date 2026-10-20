// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const transport = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../../services/api', () => ({ default: transport, getApiBaseUrl: () => 'https://example.test' }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (selector: (state: unknown) => unknown) => selector({ chatTabs: { own: { sessionId: 'chat', metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew' } } } }) }))
vi.mock('./codeLocalFiles', () => ({ useLocalFileDevices: () => ({ devices: [{ device_id: 'laptop', settings_managed: true }], refresh: vi.fn() }), useLatestCliVersion: () => undefined, cliUpdateNeeded: () => false, localFileError: (e: Error) => e.message }))
import { CodeFilesPanel, CodeLocalFilesSettings } from './CodeFilesPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
let container: HTMLDivElement
afterEach(async () => { if (root) await act(async () => root.unmount()); container?.remove(); vi.clearAllMocks() })
it('uses backend folder permissions and changes them without reconnecting the CLI', async () => {
 const settings = { device_id: 'laptop', folders: [{ id: 'reports', path: '/home/person/Reports', access: 'read' }] }
 transport.get.mockResolvedValue({ data: settings })
 transport.put.mockImplementation(async (_: string, next: unknown) => ({ data: next }))
 container = document.createElement('div'); document.body.append(container); root = createRoot(container)
 await act(async () => root.render(<CodeLocalFilesSettings sessionId="chat" />))
 const permission = container.querySelector('select[aria-label="Access for /home/person/Reports"]') as HTMLSelectElement
 expect(permission.value).toBe('read')
 await act(async () => { permission.value = 'write'; permission.dispatchEvent(new Event('change', { bubbles: true })) })
 expect(transport.put).toHaveBeenCalledWith('/api/agent-profiles/work/projects/crew/laptop', { device_id: 'laptop', folders: [{ ...settings.folders[0], access: 'write' }] }, { skipSessionContext: true })
 expect(container.textContent).toContain('AgentBridge uses the same CLI')
 expect(container.textContent).not.toContain('--workspace')
})
it('keeps ordinary files on the server', async () => {
 container = document.createElement('div'); document.body.append(container); root = createRoot(container)
 await act(async () => root.render(<CodeFilesPanel sessionId="chat" onAsk={async () => {}} serverFiles={<p>Server files</p>} />))
 expect(container.textContent).toBe('Server files')
})
