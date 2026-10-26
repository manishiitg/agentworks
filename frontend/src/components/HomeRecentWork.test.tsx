// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, String(value)), removeItem: (key: string) => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  Object.defineProperty(window, 'localStorage', { configurable: true, value: storage })
})
import { HomeRecentWork } from './HomeRecentWork'
import { agentApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let cleanup = () => {}
afterEach(() => { cleanup(); vi.restoreAllMocks(); document.body.innerHTML = ''; delete window.__APP_RUNTIME_CONFIG__ })
const project = { workspace_id: 'work:notes', id: 'notes', profile_id: 'work' as const, name: 'Research notes', workspace_path: 'Crew/notes' }
const history = [{ session_id: 'note-chat', workspace_path: 'Crew/notes', updated_at: '2026-10-26T10:00:00Z' }, { session_id: 'goal-chat', workspace_path: 'Goals/reading', updated_at: '2026-10-26T11:00:00Z' }]
async function mount() {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'agentworks', 'relays'] } as never
  useAuthStore.setState({ user: { id: 'alice', allowed_workflow_ids: ['reading'] } as never })
  useChatStore.setState({ chatTabs: {}, activeSessionsCache: [] })
  useGlobalPresetStore.setState({ workflowPresets: [{ id: 'reading', label: 'Reading target', createdAt: 1, selectedFolder: { filepath: 'Goals/reading' } }, { id: 'hidden', label: 'Not admitted', createdAt: 1, selectedFolder: { filepath: 'Goals/hidden' } }] as never })
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  await act(async () => root.render(<HomeRecentWork />))
  return host
}
it('groups admitted history by stable project identity and links to the actual project', async () => {
  vi.spyOn(agentApi, 'listChatHistorySessions').mockResolvedValue({ sessions: history })
  vi.spyOn(agentApi, 'listWorkspaceNavigation').mockResolvedValue({ workspaces: [project] })
  const host = await mount()
  expect(host.textContent).toContain('Reading target')
  expect(host.textContent).not.toContain('Not admitted')
  expect(host.querySelector('a[href="/crew/projects/notes"]')).not.toBeNull()
  expect(host.querySelector('a[href="/workflows/reading"]')).not.toBeNull()
  expect(host.querySelectorAll('li')).toHaveLength(2)
})
it('does not display a previous account’s late inventory response', async () => {
  let resolve: (value: { workspaces: typeof project[] }) => void = () => {}
  vi.spyOn(agentApi, 'listChatHistorySessions').mockResolvedValue({ sessions: [] })
  vi.spyOn(agentApi, 'listWorkspaceNavigation').mockReturnValueOnce(new Promise(done => { resolve = done })).mockResolvedValue({ workspaces: [] })
  const host = await mount()
  await act(async () => useAuthStore.setState({ user: { id: 'bob', allowed_workflow_ids: [] } as never }))
  await act(async () => resolve({ workspaces: [project] }))
  expect(host.textContent).not.toContain('Research notes')
})

it('shows search above twenty projects and removes its filter when the list shrinks', async () => {
  let count = 20
  vi.spyOn(agentApi, 'listChatHistorySessions').mockResolvedValue({ sessions: [] })
  vi.spyOn(agentApi, 'listWorkspaceNavigation').mockImplementation(async () => ({ workspaces: Array.from({ length: count }, (_, index) => ({ ...project, id: `project-${index + 1}`, workspace_id: `work:project-${index + 1}`, workspace_path: `Crew/project-${index + 1}`, name: `Project ${index + 1}` })) }))
  const host = await mount()
  const search = () => host.querySelector<HTMLInputElement>('[aria-label="Search recent work"]')
  const refresh = () => host.querySelector<HTMLButtonElement>('[aria-label="Refresh recent work"]')!.click()
  expect(search()).toBeNull()
  expect(host.querySelector('[aria-label="Refresh recent work"]')).not.toBeNull()
  count = 21
  await act(async () => refresh())
  expect(search()).not.toBeNull()
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search()!, 'Project 21')
    search()!.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(host.querySelectorAll('li')).toHaveLength(1)
  expect(host.querySelector('li')!.textContent).toContain('Project 21')
  count = 20
  await act(async () => refresh())
  expect(search()).toBeNull()
  expect(host.querySelectorAll('li')).toHaveLength(8)
})
