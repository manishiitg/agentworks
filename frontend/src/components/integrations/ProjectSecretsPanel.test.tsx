// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ listWorkflowSecrets: vi.fn(), getGlobalSecrets: vi.fn(), getVaultShareGroups: vi.fn() }))
vi.mock('../../api/secrets', () => ({ secretsApi: api }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))
vi.mock('./OpenVaultButton', () => ({ OpenVaultButton: () => null }))
import { ProjectSecretsPanel } from './ProjectSecretsPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let cleanup = () => {}
beforeEach(() => {
  vi.clearAllMocks()
  api.listWorkflowSecrets.mockResolvedValue([{ name: 'SAME_KEY', encrypted_value: 'encrypted-project-value' }])
  api.getGlobalSecrets.mockResolvedValue([{ name: 'SAME_KEY', managed: true }])
  api.getVaultShareGroups.mockResolvedValue({ groups: [] })
})
afterEach(() => { cleanup(); vi.unstubAllEnvs() })
async function mount() {
  const local = vi.fn(), global = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<ProjectSecretsPanel workspacePath="Workflow/demo" placeNoun="Workflow"
    selectedSecrets={['SAME_KEY']} selectedGlobalSecrets={[]} onSecretChange={local} onGlobalSecretChange={global} />))
  return { host, local, global }
}
it('shows project secrets first and permitted Vault secrets below without another tab row', async () => {
  const { host, local, global } = await mount()
  const project = host.querySelector('section[aria-label="Workflow secrets"]')!
  const vault = host.querySelector('section[aria-label="Vault secrets"]')!
  expect(project.compareDocumentPosition(vault) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(host.querySelector('[role="tablist"]')).toBeNull()
  expect(api.listWorkflowSecrets).toHaveBeenCalledTimes(1)
  expect(api.getGlobalSecrets).toHaveBeenCalledWith(false)
  expect(vault.querySelector('[aria-label="Reveal SAME_KEY"]')).toBeNull()
  expect(vault.querySelector('[aria-label="Copy SAME_KEY"]')).toBeNull()
  expect(vault.querySelector('[aria-label="Rotate SAME_KEY"]')).toBeNull()
  expect(vault.textContent).not.toContain('encrypted-project-value')
  await act(async () => { vault.querySelector<HTMLButtonElement>('[aria-label="Use SAME_KEY"]')!.click() })
  expect(global).toHaveBeenCalledWith(['SAME_KEY'])
  expect(local).not.toHaveBeenCalled()
  await act(async () => { project.querySelector<HTMLButtonElement>('[aria-label="Use SAME_KEY"]')!.click() })
  expect(local).toHaveBeenCalledWith([])
})
it('shows Vault access even if the project secrets cannot load', async () => {
  api.listWorkflowSecrets.mockRejectedValue(new Error('Project secrets unavailable'))
  const { host } = await mount()
  expect(host.querySelector('section[aria-label="Workflow secrets"] [role="alert"]')?.textContent).toContain('Project secrets unavailable')
  expect(host.querySelector('section[aria-label="Vault secrets"]')?.textContent).toContain('SAME_KEY')
  expect(host.querySelector('section[aria-label="Vault secrets"] [role="alert"]')).toBeNull()
})
it('loads local project secrets without Vault lookups or sharing controls', async () => {
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'local')
  const { host } = await mount()
  expect(host.querySelector('section[aria-label="Workflow secrets"]')?.textContent).toContain('SAME_KEY')
  expect(host.querySelector('section[aria-label="Vault secrets"]')).toBeNull()
  expect(api.listWorkflowSecrets).toHaveBeenCalledOnce()
  expect(api.getGlobalSecrets).not.toHaveBeenCalled()
  expect(api.getVaultShareGroups).not.toHaveBeenCalled()
})
