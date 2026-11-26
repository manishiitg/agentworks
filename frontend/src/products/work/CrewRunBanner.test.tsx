// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { CrewRunBanner } from './CrewRunBanner'
import type { RunFolderInfo, RunPythonStep } from '../../services/api-types'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const step = (id: string, name: string, status: string, detail?: string): RunPythonStep => ({ execution_id: 'e', id, name, started_at: `2026-01-01T00:00:0${id}Z`, status, detail })
const run = (status: string, duration_ms = 4200) => ({ name: 'run-1', metadata: { status, duration_ms } }) as unknown as RunFolderInfo

async function render(props: React.ComponentProps<typeof CrewRunBanner>) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewRunBanner {...props} />))
  return { host, banner: host.querySelector('[data-testid="run-banner"]')!, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('says it is starting before the run has written anything', async () => {
  const view = await render({ state: 'starting', steps: [] })
  try { expect(view.banner.getAttribute('data-state')).toBe('starting'); expect(view.banner.textContent).toContain('Starting the run') } finally { await view.cleanup() }
})

it('says which task and step is running, and how many are done', async () => {
  const view = await render({ state: 'running', task: 'github-issues-tracker', steps: [step('1', 'Fetch all issues', 'completed'), step('2', 'Compare with last snapshot', 'running')] })
  try {
    expect(view.banner.textContent).toContain('Running now')
    expect(view.banner.textContent).toContain('task: github-issues-tracker')
    expect(view.banner.textContent).toContain('step 2: Compare with last snapshot')
    expect(view.banner.textContent).toContain('1 done')
  } finally { await view.cleanup() }
})

it('keeps how a run ended: finished in green, failed in red with the step that stopped it', async () => {
  const done = await render({ state: 'ended', run: run('completed'), task: 'slack-message-notifier', steps: [], onDismiss: () => undefined })
  try { expect(done.banner.getAttribute('data-status')).toBe('completed'); expect(done.banner.textContent).toContain('Run finished'); expect(done.banner.textContent).toContain('took 4s'); expect(done.banner.className).toContain('emerald') } finally { await done.cleanup() }
  const bad = await render({ state: 'ended', run: run('failed'), steps: [step('1', 'Fetch all issues', 'failed', 'GitHub rate limit reached')] })
  try { expect(bad.banner.textContent).toContain('Run failed'); expect(bad.banner.textContent).toContain('Stopped at step: Fetch all issues. GitHub rate limit reached'); expect(bad.banner.className).toContain('red') } finally { await bad.cleanup() }
})
