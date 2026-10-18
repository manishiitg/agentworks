// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { GlobalActivityButton } from './GlobalActivityButton'

const state = vi.hoisted(() => ({ user: { id: 'alice' }, activeWorkspaceId: 'local', load: vi.fn() }))
vi.mock('../../services/api', () => ({ agentApi: { listNeedsYouDecisionsAggregate: state.load } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: typeof state) => unknown) => select(state) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (s: typeof state) => unknown) => select(state) }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../ui/tooltip', () => ({ Tooltip: ({ children }: any) => <>{children}</>, TooltipTrigger: ({ children }: any) => children, TooltipContent: ({ children }: any) => <span>{children}</span> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { state.user.id = 'alice'; state.activeWorkspaceId = 'local'; state.load.mockReset() })
async function mounted(check: (host: HTMLDivElement, update: () => Promise<void>, open: ReturnType<typeof vi.fn>) => Promise<void>) {
  const host = document.createElement('div'); const root = createRoot(host); const open = vi.fn()
  const update = async () => { await act(async () => root.render(<GlobalActivityButton workspacePaths={['Workflow/mine']} active={false} onOpen={open} />)) }
  try { await update(); await check(host, update, open) } finally { await act(async () => root.unmount()) }
}
it('opens existing Goal decisions and counts only currently admitted workspaces', async () => {
  state.load.mockResolvedValue({ success: true, inputs: [{ workspace_path: 'Workflow/mine' }, { workspace_path: 'Workflow/foreign' }] })
  await mounted(async (host, _, open) => {
    const button = host.querySelector<HTMLButtonElement>('[aria-label="Needs you: 1 decision"]')!
    expect(button).not.toBeNull()
    await act(async () => button.click()); expect(open).toHaveBeenCalledOnce()
    expect(host.textContent).not.toContain('Activity')
  })
})
it.each([[], undefined])('does not turn empty or failed requests into attention', async inputs => {
  state.load.mockResolvedValue({ success: !!inputs, inputs })
  await mounted(async host => { expect(host.querySelector('button')).toBeNull() })
})
it('clears prior account decisions and discards a late previous account response', async () => {
  let finish!: (result: unknown) => void
  state.load.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue({ success: true, inputs: [] })
  await mounted(async (host, update) => {
    state.user.id = 'bob'; await update()
    await act(async () => finish({ success: true, inputs: [{ workspace_path: 'Workflow/mine' }] }))
    expect(host.querySelector('button')).toBeNull()
  })
})
