// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
const folderApi = vi.hoisted(() => ({ listWorkFolders: vi.fn(), addWorkFolder: vi.fn(), deleteWorkFolder: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: vi.fn(async () => ({ data: { available: false } })) }, workFolderApi: folderApi, getApiBaseUrl: () => 'https://example.test' }))
vi.mock('./workSessions', () => ({ loadWorkSessions: vi.fn(async () => []) }))
vi.mock('./projectProduct', () => ({ useProjectProduct: () => ({}) }))
vi.mock('./WorkModelsPanel', () => ({ WorkModelsPanel: () => null }))
vi.mock('../../components/folders/WorkflowReferenceAccess', () => ({ WorkflowReferenceAccess: () => null }))
import { WorkFoldersSection } from './WorkIdentityPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root
let host: HTMLDivElement
const originalElectron = window.electronAPI
beforeEach(() => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local')
  folderApi.listWorkFolders.mockResolvedValue({ folders: [], roots: [] })
})
afterEach(async () => {
  if (root) await act(async () => root.unmount())
  host?.remove(); window.electronAPI = originalElectron
  vi.clearAllMocks(); vi.unstubAllEnvs()
})
async function mount() {
  const openComputer = vi.fn()
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root.render(<WorkFoldersSection workspacePath="Crew/example" workflowContextPaths={[]} onWorkflowContextPathsChange={vi.fn()} onOpenComputer={openComputer} />))
  return openComputer
}
// PLAT-746-305: the native picker sent /Users paths to a connected Linux server.
it('does not offer the Mac picker in a hosted workspace using local desktop assets', async () => {
  const pick = vi.fn(async () => '/Users/example/Documents')
  window.electronAPI = { ...originalElectron, getDesktopConnection: () => 'company-server', pickWorkflowFolder: pick } as typeof window.electronAPI
  const openComputer = await mount()
  expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Add folder')).toBe(false)
  const open = [...host.querySelectorAll('button')].find(button => button.textContent === 'Open Your computer')!
  await act(async () => open.click())
  expect(openComputer).toHaveBeenCalledOnce()
  expect(pick).not.toHaveBeenCalled(); expect(folderApi.addWorkFolder).not.toHaveBeenCalled()
})
it('keeps local folder selection and displays the actual plain-text server rejection', async () => {
  const pick = vi.fn(async () => '/Users/example/Documents')
  window.electronAPI = { ...originalElectron, getDesktopConnection: () => 'local', pickWorkflowFolder: pick } as typeof window.electronAPI
  const message = 'folder path is unavailable: lstat /Users: no such file or directory'
  folderApi.addWorkFolder.mockRejectedValue({ response: { data: message + '\n' }, message: 'HTTP 400' })
  await mount()
  await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Add folder')!.click())
  expect(pick).toHaveBeenCalledOnce()
  expect(folderApi.addWorkFolder).toHaveBeenCalledWith({ path: '/Users/example/Documents', alias: 'Documents', access: 'read_only' })
  expect(host.textContent).toContain(message)
  expect(host.textContent).not.toContain('HTTP 400')
})
