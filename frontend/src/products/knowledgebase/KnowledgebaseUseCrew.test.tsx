// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, it, expect, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  allowed: ['work'] as string[], list: vi.fn(), project: vi.fn(), save: vi.fn(), open: vi.fn(), create: vi.fn(), copy: vi.fn(async () => true),
}))
vi.mock('../work/workSessions', () => ({ loadWorkSessions: mocks.list }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select: any) => select({ user: { id: 'owner', allowed_products: mocks.allowed } }), { getState: () => ({ user: { id: 'owner', allowed_products: mocks.allowed } }) }) }))
vi.mock('../productSurfaceConfig', () => ({ visibleProductSurfaceIDs: (allowed: string[]) => allowed }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: mocks.open }))
vi.mock('../../utils/quickNavigation', () => ({ quickNavigationItems: () => [], openQuickNavigation: mocks.create }))
vi.mock('../../utils/textUtils', () => ({ copyToClipboard: mocks.copy }))
vi.mock('../../services/knowledgebaseApi', () => ({ knowledgebaseApi: { project: mocks.project, setProjectAccess: mocks.save } }))
import { KnowledgebaseUseCrew } from './KnowledgebaseUseCrew'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.clearAllMocks(); mocks.allowed = ['work'] })
async function render(check: (host: HTMLDivElement) => Promise<void>) {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<KnowledgebaseUseCrew />))
    await check(host)
  } finally { await act(async () => root.unmount()); host.remove() }
}
it('shows organization examples and manual instructions without project setup or previous activity', async () => {
  await render(async host => {
    expect(host.textContent).toContain('Map your knowledge')
    expect(host.textContent).toContain('Find duplicate notes')
    expect(host.textContent).toContain('Group notes by topic')
    expect(host.textContent).toContain('Connections → Add connection → Brain')
    expect(host.textContent).not.toContain('Previous Brain activity')
    expect(host.querySelector('select, input, form')).toBeNull()
    expect(mocks.list).not.toHaveBeenCalled()
    expect(mocks.project).not.toHaveBeenCalled()
    expect(mocks.save).not.toHaveBeenCalled()
    expect(mocks.create).not.toHaveBeenCalled()
  })
})
it('copies a suggested request without running it, creating a Crew or changing access', async () => {
  await render(async host => {
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Copy request: Find duplicate notes"]')!.click())
    expect(mocks.copy).toHaveBeenCalledWith(expect.stringContaining('Ask me before changing or deleting anything'))
    expect(host.querySelector('[role="status"]')?.textContent).toContain('Paste it into your Crew')
    expect(mocks.open).not.toHaveBeenCalled()
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.save).not.toHaveBeenCalled()
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Open Crew')!.click())
    expect(mocks.open).toHaveBeenCalledWith('work')
  })
})
it('keeps guidance available without Crew access and rechecks access before opening Crew', async () => {
  mocks.allowed = []
  await render(async host => {
    expect(host.textContent).toContain('Ask an administrator for Crew access')
    expect([...host.querySelectorAll('button')].some(button => button.textContent === 'Open Crew')).toBe(false)
  })
  mocks.allowed = ['work']
  await render(async host => {
    mocks.allowed = []
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Open Crew')!.click())
    expect(mocks.open).not.toHaveBeenCalled()
  })
})
