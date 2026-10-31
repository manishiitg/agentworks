// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const draft = vi.hoisted(() => vi.fn(async () => ({ tabId: 'own-tab' })))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: draft }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
import { WorkspaceDashboardMenu } from './WorkspaceDashboardMenu'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('groups by the saved label and opens a motion draft in the selected own chat', async () => {
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host); const open = vi.fn()
 try {
  await act(async () => root.render(<WorkspaceDashboardMenu documents={[
   { id:'overview', title:'Motion budget', path:'db/reports/a.html', order:0 },
   { id:'playback', title:'Spending history', path:'db/reports/b.html', order:1, motion:true },
  ]} createTarget={{ tabId:'own-tab' }} onSelect={open} />))
  expect(host.querySelector('[aria-label="Motion dashboards"]')).toBeNull()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Dashboards"]')!.click())
  const motion = host.querySelector('[aria-label="Motion dashboards"]')!
  expect(motion.textContent).toContain('Spending history')
  expect(motion.textContent).not.toContain('Motion budget')
  expect(host.querySelector('[aria-label="Project dashboards"]')?.textContent).toContain('Motion budget')
  expect(host.querySelector('[aria-label="Artifacts"]')).toBeNull()
  await act(async () => [...motion.querySelectorAll('button')].find(b=>b.textContent==='Spending history')!.click())
  expect(open).toHaveBeenCalledWith('db/reports/b.html')
  await act(async () => [...host.querySelectorAll('button')].find(b=>b.textContent==='Create motion dashboard')!.click())
  expect(draft).toHaveBeenCalledWith(expect.objectContaining({ tabId:'own-tab', message:expect.stringContaining('motion dashboard') }))
 } finally { await act(async () => root.unmount()); host.remove() }
})
