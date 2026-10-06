// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { PulseReviewOverview } from './PulseReviewOverview'

it('keeps the current outcome but omits the old expandable check history', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container)
  const audits = Array.from({ length: 13 }, (_, i) => ({
    workspace_path: 'Workflow/example', module: 'technical_review', pulse_run_id: `run-${i}`,
    result: 'done', reason: `Review outcome ${i}`, recorded_at: `2026-09-${String(20 - i).padStart(2, '0')}T09:00:00Z`,
    verification: [`Verified check ${i}`],
  }))
  const render = (moduleFilter: string) => root.render(<PulseReviewOverview moduleStates={[]} coverage={[]} findings={[]}
    audits={audits} moduleFilter={moduleFilter} onSelectModule={() => {}} />)
  try {
    await act(async () => render('technical_review'))
    expect(container.querySelector('[aria-label="Technical content"]')?.textContent).toContain('Review outcome 0')
    expect(container.textContent).not.toContain('Latest check')
    expect(container.textContent).not.toContain('View review history')
    expect(container.querySelector('[aria-label="Review history"]')).toBeNull()
  } finally { await act(async () => root.unmount()); container.remove() }
})

it('makes Strategy primary and groups Architecture with platform stability', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<PulseReviewOverview moduleStates={[]} coverage={[]} findings={[]} audits={[]} moduleFilter="architecture_review" onSelectModule={() => {}} />))
    const navigation = container.querySelector('[aria-label="Pulse work areas"]')!
    expect(navigation.querySelectorAll('[role="switch"]')).toHaveLength(3)
    expect(navigation.textContent).toContain('Goals, metrics & strategy')
    expect(navigation.textContent).toContain('Platform health & stability')
    expect(navigation.textContent).toContain('Architecture')
    expect(navigation.textContent).toContain('Workflow Review')
    expect(navigation.textContent!.indexOf('Strategy')).toBeLessThan(navigation.textContent!.indexOf('Architecture'))
    expect(container.querySelector('[aria-label="Architecture content"]')?.textContent).toContain('Learning quality')
    expect(container.querySelector('[aria-label="Technical content"]')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})

it('lets the user turn an individual reviewer back on while preserving its history', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const container = document.createElement('div'); const root = createRoot(container)
  const onToggleReviewModule = vi.fn()
  const onRunReviewModule = vi.fn()
  try {
    await act(async () => root.render(<PulseReviewOverview moduleStates={[]} coverage={[]} findings={[]} audits={[]}
      moduleFilter="strategic_review" onSelectModule={() => {}} disabledReviewModules={['strategic_review']}
      onToggleReviewModule={onToggleReviewModule} onRunReviewModule={onRunReviewModule} />))
    const strategySwitch = container.querySelector<HTMLButtonElement>('[aria-label="Include Strategy reviewer in Pulse reviews"]')!
    expect(strategySwitch.getAttribute('aria-checked')).toBe('false')
    expect(container.textContent).toContain('Future Pulse runs will skip it; previous findings and coverage remain below.')
    await act(async () => strategySwitch.click())
    expect(onToggleReviewModule).toHaveBeenCalledWith('strategic_review')
    const runButton = container.querySelector<HTMLButtonElement>('[aria-label="Run Strategy review now"]')!
    expect(runButton.disabled).toBe(false)
    await act(async () => runButton.click())
    expect(onRunReviewModule).toHaveBeenCalledWith('strategic_review')
  } finally { await act(async () => root.unmount()) }
})

it('shows the stored date, run, or cooldown boundary for skipped reviewers', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const container = document.createElement('div'); const root = createRoot(container)
  const render = (module: string, boundary: { next_check_at?: string; next_check_after_run_id?: string; cooldown_runs?: number }) => root.render(
    <PulseReviewOverview moduleStates={[{ workspace_path: 'Workflow/example', module, last_gate_decision: 'skipped', last_reason: 'No mature evidence yet.', ...boundary }]}
      coverage={[]} findings={[]} audits={[]} moduleFilter={module} onSelectModule={() => {}} />)
  try {
    await act(async () => render('technical_review', { cooldown_runs: 2 }))
    expect(container.textContent).toContain('Next assessment: After 2 scheduled runs')
    await act(async () => render('architecture_review', { next_check_after_run_id: 'iteration-42' }))
    expect(container.textContent).toContain('Next assessment: After workflow run iteration-42')
    await act(async () => render('strategic_review', { next_check_at: '2026-09-19T00:00:00Z' }))
    expect(container.textContent).toContain('Next assessment:')
    expect(container.textContent).toMatch(/Sep\w* 19|19 Sep\w*/)
    expect(container.textContent).toContain('2026')
  } finally { await act(async () => root.unmount()) }
})

it('requires the Workflow Review before manual reviewers and keeps the drift action available', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  const container = document.createElement('div'); const root = createRoot(container)
  const onRunReviewModule = vi.fn()
  try {
    await act(async () => root.render(<PulseReviewOverview moduleStates={[]} coverage={[]} findings={[]} audits={[]}
      moduleFilter="strategic_review" onSelectModule={() => {}} planDriftDue onRunReviewModule={onRunReviewModule} />))
    expect(container.querySelector<HTMLButtonElement>('[aria-label="Run Strategy review now"]')!.disabled).toBe(true)
    expect(container.querySelector<HTMLButtonElement>('[aria-label="Run Technical review now"]')!.disabled).toBe(true)
    expect(container.querySelector<HTMLButtonElement>('[aria-label="Run Architecture review now"]')!.disabled).toBe(true)
    expect(container.querySelectorAll('[aria-label="Workflow Review due"]')).toHaveLength(1)
    expect([...container.querySelectorAll('button')].filter(button => button.textContent?.startsWith('Workflow Review ·'))).toHaveLength(0)
    expect(container.textContent!.indexOf('Technical')).toBeLessThan(container.textContent!.indexOf('Workflow Review is due'))
    expect(container.textContent!.indexOf('Architecture')).toBeLessThan(container.textContent!.indexOf('Workflow Review is due'))
    const driftButton = container.querySelector<HTMLButtonElement>('[aria-label="Run Workflow Review now"]')!
    expect(driftButton.disabled).toBe(false)
    await act(async () => driftButton.click())
    expect(onRunReviewModule).toHaveBeenCalledWith('plan_drift_review')
  } finally { await act(async () => root.unmount()) }
})
