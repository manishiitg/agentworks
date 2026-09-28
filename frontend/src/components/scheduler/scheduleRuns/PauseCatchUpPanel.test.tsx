// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PauseCatchUpPanel } from './PauseCatchUpPanel'

afterEach(() => { document.body.innerHTML = '' })

const items = [
  { workspace_path: 'Workflow/upwork', workflow_label: 'Upwork', schedule_id: 'bid', schedule_name: 'Daily Toptal Find + Bid', count: 2, latest_scheduled_for: new Date().toISOString() },
  { workspace_path: 'Workflow/social', workflow_label: 'Social', schedule_id: 'post', schedule_name: 'Build-in-Public Post', count: 1, latest_scheduled_for: new Date().toISOString() },
]

describe('PauseCatchUpPanel', () => {
  it('lists what the pause skipped and runs only what the owner picks', async () => {
    const onRun = vi.fn()
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<PauseCatchUpPanel items={items} running={false} canRun onRun={onRun} onDismiss={() => {}} />) })
    expect(container.textContent).toContain('3 scheduled runs were skipped while schedules were paused')
    const run = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Run selected now')) as HTMLButtonElement
    expect(run.disabled).toBe(true)
    await act(async () => { (container.querySelector('input[aria-label="Run Daily Toptal Find + Bid now"]') as HTMLInputElement).click() })
    expect(run.disabled).toBe(false)
    await act(async () => { run.click() })
    expect(onRun).toHaveBeenCalledWith(['bid'])
    await act(async () => root.unmount())
  })

  it('renders nothing when nothing was skipped, and no run button for read-only users', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<PauseCatchUpPanel items={[]} running={false} canRun onRun={() => {}} onDismiss={() => {}} />) })
    expect(container.textContent).toBe('')
    await act(async () => { root.render(<PauseCatchUpPanel items={items} running={false} canRun={false} onRun={() => {}} onDismiss={() => {}} />) })
    expect(container.textContent).not.toContain('Run selected now')
    await act(async () => root.unmount())
  })
})
