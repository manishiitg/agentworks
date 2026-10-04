// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../components/integrations/ProjectMcpPanel', () => ({ ProjectMcpPanel: ({ canEdit, workspacePath, selectedServers, onSelectedServersChange }: { canEdit: boolean; workspacePath: string; selectedServers: string[]; onSelectedServersChange: (next: string[]) => Promise<void> }) => <section aria-label="MCP browser" data-editable={canEdit} data-path={workspacePath}><button disabled={!canEdit} onClick={() => { void onSelectedServersChange([...selectedServers, 'vault_shared']).catch(() => {}) }}>Select shared MCP</button></section> }))
vi.mock('../../stores/useChatStore', async () => {
  const { create } = await import('zustand')
  return { useChatStore: create(() => ({ chatTabs: {}, setTabConfig: vi.fn(), setTabMetadata: vi.fn(), addToast: vi.fn() })) }
})
import { WorkMCPTabBody } from './WorkIntegrationsPanel'
import { useChatStore } from '../../stores/useChatStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: Array<() => void> = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks() })
async function mount(path: string, save = vi.fn(async () => undefined)) {
  const host = document.createElement('div'); document.body.appendChild(host); const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  useChatStore.setState({ chatTabs: {
    t1: { tabId: 't1', config: { selectedServers: ['private_linear'] }, metadata: { agentProfileId: 'work', agentProfileProjectId: 'p1' } },
    t2: { tabId: 't2', config: { selectedServers: ['private_linear'] }, metadata: { agentProfileId: 'work', agentProfileProjectId: 'p1' } },
    other: { tabId: 'other', config: { selectedServers: [] }, metadata: { agentProfileId: 'work', agentProfileProjectId: 'p2' } },
  } } as never)
  await act(async () => root.render(<WorkMCPTabBody tabId="t1" projectId="p1" workspacePath={path} onAsk={vi.fn()} onSelectedServersChange={save} />))
  return { host, save, store: useChatStore.getState() }
}
it('persists Vault selection and refreshes only chats for the same project', async () => {
  const { host, save, store } = await mount('Chats/Work/projects/p1')
  expect(host.querySelector('[aria-label="MCP browser"]')).not.toBeNull()
  expect(host.querySelector('[aria-label="MCP browser"]')).not.toBeNull()
  await act(async () => host.querySelector('button')!.click())
  expect(save).toHaveBeenCalledWith(['private_linear', 'vault_shared'])
  expect(store.setTabConfig).toHaveBeenCalledWith('t1', { selectedServers: ['private_linear', 'vault_shared'] })
  expect(store.setTabConfig).toHaveBeenCalledWith('t2', { selectedServers: ['private_linear', 'vault_shared'] })
  expect(store.setTabConfig).not.toHaveBeenCalledWith('other', expect.anything())
  expect(store.setTabMetadata).toHaveBeenCalledWith('t1', expect.objectContaining({ agentProfileRuntimeDirty: true }))
})
it('does not change chat scope when saving project selection fails', async () => {
  const { host, store } = await mount('Chats/Work/projects/p1', vi.fn().mockRejectedValue(new Error('permission denied')))
  await act(async () => host.querySelector('button')!.click())
  expect(store.setTabConfig).not.toHaveBeenCalled()
  expect(store.addToast).toHaveBeenCalledWith('permission denied', 'error')
})
it('shows both sections read-only when viewing another owner\'s Crew', async () => {
  const { host } = await mount('_users/owner/Chats/Work/projects/p1')
  expect(host.querySelector('[aria-label="MCP browser"]')?.getAttribute('data-editable')).toBe('false')
  expect(host.querySelector('button')!.disabled).toBe(true)
})
