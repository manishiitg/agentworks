// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { BookOpen, Files, LayoutDashboard } from 'lucide-react'
const mocks = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), endpoint: 'http://server-one' }))
vi.mock('../../services/contextShortcuts', () => ({ contextShortcutsApi: { get: mocks.get, save: mocks.save } }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => mocks.endpoint }))
vi.mock('../../stores/useAuthStore', async () => {
  const { create } = await import('zustand')
  return { useAuthStore: create(() => ({ user: { id: 'alice' } })) }
})
vi.mock('../../stores/useWorkspaceConnectionStore', async () => {
  const { create } = await import('zustand')
  return { useWorkspaceConnectionStore: create(() => ({ activeWorkspaceId: 'local' })) }
})
import { WorkspaceContextCard, WorkspaceContextShortcut as Shortcut } from './WorkspaceContextCard'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useContextShortcutStore } from '../../hooks/useContextShortcuts'
import { normalizeConnectionShortcuts } from './WorkspaceShortcutContents'
import type { ShortcutProduct } from '../../services/contextShortcuts'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined, host: HTMLDivElement
beforeEach(() => {
  mocks.endpoint = 'http://server-one'
  mocks.get.mockReset().mockResolvedValue({ version: 1, revision: 2, shortcuts: null })
  mocks.save.mockReset().mockImplementation(async (_product, value) => ({ ...value, revision: value.revision + 1 }))
  useAuthStore.setState({ user: { id: 'alice' } as never })
  useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'local' })
  useContextShortcutStore.setState({ entries: {} })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root?.unmount()); root = undefined; host.remove() })
async function render(product: ShortcutProduct = 'work', restricted = false, legacy?: ShortcutProduct) {
  await act(async () => root!.render(<WorkspaceContextCard product={product} summary="Overview subtitle" legacyProduct={legacy} availableShortcuts={restricted ? [] : [{ id: 'files', label: 'Files', icon: Files, onClick: () => {} }]}>
    <Shortcut id="dashboard" label="Dashboard" icon={LayoutDashboard}><button>Dashboard</button><p>Live results</p></Shortcut>
    {!restricted && <Shortcut id="memory" label="Memory & skills" icon={BookOpen}><button>Memory &amp; skills</button><p>2 waiting</p></Shortcut>}
  </WorkspaceContextCard>))
}
async function click(label: string) {
  await act(async () => {
    const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim() === label)
    expect(button, `button ${label}`).toBeDefined(); button!.click()
  })
}
const checkbox = (label: string) => [...host.querySelectorAll('label')].find(item => item.textContent === label)?.querySelector('input')

it.each(['agentworks', 'relays', 'work', 'code'] as ShortcutProduct[])('loads and saves preferences through the %s product key', async product => {
  await render(product)
  expect(mocks.get).toHaveBeenCalledWith(product, undefined, expect.any(AbortSignal))
  expect(host.textContent).not.toContain('Project overview')
  expect(host.textContent).not.toContain('Overview subtitle')
  const search = host.querySelector('[aria-label="All views"]')!
  expect(search.closest('.overflow-y-auto')).toBeNull()
  expect([...host.querySelectorAll('.overflow-y-auto')].some(region => region.textContent?.includes('Live results'))).toBe(true)
  await click('Edit shortcuts')
  expect(host.querySelector('h2')?.textContent).toBe('Edit shortcuts')
  expect(checkbox('Dashboard')).toBeDefined()
  await click('Done')
  expect(mocks.save).toHaveBeenCalledWith(product, expect.objectContaining({ version: 1, revision: 2, shortcuts: null }))
  expect(host.querySelector('[aria-label="Shortcut editor"]')).toBeNull()
})

it('adds/removes shortcuts, reorders with the keyboard, and retains the existing live content', async () => {
  await render()
  await click('Edit shortcuts')
  await act(async () => checkbox('Files')!.click())
  const handle = host.querySelector<HTMLButtonElement>('[aria-label="Reorder Memory & skills"]')!
  await act(async () => handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true })))
  await click('Done')
  const saved = mocks.save.mock.calls[0][1].shortcuts as string[]
  expect(saved.indexOf('memory')).toBeLessThan(saved.indexOf('dashboard'))
  expect(saved).toContain('files')
  expect(host.textContent).toContain('Live results')
  expect(host.textContent).toContain('2 waiting')
  await click('Edit shortcuts')
  await act(async () => checkbox('Memory & skills')!.click())
  await click('Done')
  expect(host.textContent).not.toContain('2 waiting')
  expect(host.textContent).toContain('Live results')
})

it('filters saved shortcuts through current workspace permissions and preserves unavailable choices on edit', async () => {
  mocks.get.mockResolvedValue({ version: 1, revision: 3, shortcuts: ['shell', 'files', 'dashboard'] })
  await render('code', true)
  expect(host.textContent).toContain('Live results')
  expect(host.textContent).not.toContain('Files')
  expect(host.textContent).not.toContain('Terminal')
  await click('Edit shortcuts')
  expect(checkbox('Files')).toBeUndefined()
  expect(checkbox('Dashboard')).toBeDefined()
  await click('Done')
  expect(mocks.save.mock.calls[0][1].shortcuts).toEqual(['shell', 'files', 'dashboard'])
})

it('consolidates old connection shortcuts only where Connections is available and keeps settings in their own menu', async () => {
  mocks.get.mockResolvedValue({ version: 1, revision: 3, shortcuts: ['mcp:secrets', 'mcp:gmail', 'identity', 'files'] })
  await act(async () => root!.render(<WorkspaceContextCard product="work" consolidateConnections>
    <Shortcut id="mcp" label="Connections" icon={Files}><button>Connections</button></Shortcut>
  </WorkspaceContextCard>))
  expect([...host.querySelectorAll('button')].filter(button => button.textContent === 'Connections')).toHaveLength(1)
  expect(mocks.save).not.toHaveBeenCalled()
  await click('Edit shortcuts')
  expect(checkbox('Connections')?.checked).toBe(true)
  expect(checkbox('Secrets')).toBeUndefined()
  await click('Done')
  expect(mocks.save.mock.calls[0][1].shortcuts).toEqual(['mcp', 'identity', 'files'])
  expect(normalizeConnectionShortcuts(['mcp:secrets', 'mcp:gmail'], false)).toEqual(['mcp:secrets', 'mcp:gmail'])
  expect(normalizeConnectionShortcuts(['mcp:future'], true)).toEqual(['mcp:future'])
})

it('handles an intentionally empty layout and persists Reset to default as null', async () => {
  mocks.get.mockResolvedValue({ version: 1, revision: 4, shortcuts: [] })
  await render()
  expect(host.textContent).not.toContain('Live results')
  expect(host.textContent).toContain('Add the shortcuts')
  await click('Edit shortcuts')
  await click('Reset to default')
  await click('Done')
  expect(mocks.save.mock.calls[0][1].shortcuts).toBeNull()
  expect(host.textContent).toContain('Live results')
})

it('reports save errors and keeps the prior preference until saving succeeds', async () => {
  mocks.save.mockRejectedValueOnce({ response: { status: 503 } })
  await render()
  await click('Edit shortcuts')
  await act(async () => checkbox('Dashboard')!.click())
  await click('Done')
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not save')
  expect(host.querySelector('[aria-label="Shortcut editor"]')).not.toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Cancel shortcut changes"]')!.click())
  expect(host.textContent).toContain('Live results')
})

it('offers reload after a revision conflict rather than overwriting another tab', async () => {
  mocks.save.mockRejectedValueOnce({ response: { status: 409 } })
  await render()
  await click('Edit shortcuts')
  await click('Done')
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('another tab')
  mocks.get.mockResolvedValue({ version: 1, revision: 9, shortcuts: ['files'] })
  await click('Reload saved shortcuts')
  await click('Done')
  expect(mocks.save.mock.calls[1][1]).toMatchObject({ revision: 9, shortcuts: ['files'] })
})

it('reports a load failure and does not save a guessed default over an unread preference', async () => {
  mocks.get.mockRejectedValue(new Error('offline'))
  await render()
  expect(host.querySelector('[role="status"]')?.textContent).toContain('Could not load saved shortcuts')
  await click('Edit shortcuts')
  expect(host.textContent).toContain('Could not load your shortcuts')
  const done = [...host.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === 'Done')!
  expect(done.disabled).toBe(true)
  expect(mocks.save).not.toHaveBeenCalled()
})

it('discards a late save result after switching accounts', async () => {
  let finish!: (value: unknown) => void
  mocks.save.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await render()
  await click('Edit shortcuts')
  await act(async () => checkbox('Dashboard')!.click())
  await click('Done')
  mocks.get.mockResolvedValue({ version: 1, revision: 0, shortcuts: ['dashboard'] })
  await act(async () => useAuthStore.setState({ user: { id: 'bob' } as never }))
  expect(host.textContent).toContain('Live results')
  await act(async () => finish({ version: 1, revision: 3, shortcuts: [] }))
  expect(host.textContent).toContain('Live results')
  expect(host.textContent).not.toContain('Add the shortcuts')
})

it('does not inherit a late preference response from another user or server', async () => {
  let finish!: (value: unknown) => void
  mocks.get.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await render()
  mocks.get.mockResolvedValue({ version: 1, revision: 0, shortcuts: [] })
  await act(async () => useAuthStore.setState({ user: { id: 'bob' } as never }))
  expect(host.textContent).not.toContain('Live results')
  await act(async () => finish({ version: 1, revision: 9, shortcuts: ['dashboard'] }))
  expect(host.textContent).not.toContain('Live results')
  mocks.endpoint = 'http://server-two'
  mocks.get.mockResolvedValue({ version: 1, revision: 0, shortcuts: ['memory'] })
  await render()
  expect(host.textContent).toContain('2 waiting')
  expect(host.textContent).not.toContain('Live results')
})

it('uses the explicit canonical/legacy product policy for a consolidated project', async () => {
  mocks.get.mockResolvedValue({ version: 1, revision: 0, shortcuts: ['memory'], inherited_from: 'work' })
  await render('code', false, 'work')
  expect(mocks.get).toHaveBeenCalledWith('code', 'work', expect.any(AbortSignal))
  expect(host.textContent).toContain('2 waiting')
  expect(host.textContent).not.toContain('Live results')
  await click('Edit shortcuts')
  await click('Done')
  expect(mocks.save).toHaveBeenCalledWith('code', expect.objectContaining({ shortcuts: ['memory'] }))
})
