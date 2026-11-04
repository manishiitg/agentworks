// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ScheduleRunCard } from './ScheduleRunCard'
import type { ScheduledJob, ScheduledJobRun } from '../services/api-types'

vi.mock('../api/workflowWebhooks', () => ({ workflowWebhooksApi: { getPayload: vi.fn() } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const job = { id: 'job-1', name: 'Morning briefing', schedule_type: 'cron', messages: ['Summarize overnight events'], query: '' } as ScheduledJob
const successRun = {
  id: 'run-1', job_id: 'job-1', session_id: 'session-1', status: 'success', duration_ms: 7860000,
  started_at: '2026-09-17T08:31:00Z', completed_at: '2026-09-17T10:42:00Z',
  trigger_source: 'cron', run_folder: 'iteration-84-sched', group_names: ['Default Group'],
} as ScheduledJobRun
const failedRun = {
  ...successRun, id: 'run-2', status: 'failed', duration_ms: 2100000, error: 'workflow turn 10/10 produced no response',
} as ScheduledJobRun
const webhookJob = { id: 'job-w', name: 'PR hook', schedule_type: 'webhook', messages: [] as string[], query: '' } as ScheduledJob
const webhookRun = {
  id: 'run-w', job_id: 'job-w', session_id: 'webhook-session', status: 'success', trigger_source: 'webhook',
  started_at: '2026-09-19T08:00:00Z', final_response: 'Reviewed PR 87.',
  webhook: { trigger_name: 'PR opened', delivery_id: 'delivery-87', event: 'pull_request', received_at: '2026-09-19T08:00:00Z', commit_sha: '0b8b40e69a1234567890abcdef1234567890abcd', component: 'chat', env: 'staging', deployed_at: '2026-09-23T10:32:19Z' },
} as ScheduledJobRun

const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(clean => clean()); vi.clearAllMocks() })

async function mount(props: Partial<React.ComponentProps<typeof ScheduleRunCard>> = {}) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const onOpen = vi.fn()
  const onDelete = vi.fn()
  await act(async () => root.render(
    <ScheduleRunCard job={job} run={successRun} onOpen={onOpen} onDelete={onDelete} deletingRunIds={new Set()} {...props} />,
  ))
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return { host, onOpen, onDelete }
}

async function expand(host: HTMLElement) {
  await act(async () => host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')!.click())
}

it('collapses a schedule run to status, headline, and one summary line', async () => {
  const { host } = await mount()
  expect(host.textContent).toContain('Completed')
  expect(host.textContent).toContain('Completed in 2h 11m')
  expect(host.textContent).toContain('Finished successfully.')
  expect(host.textContent).not.toContain('Started with')
  expect(host.textContent).not.toContain('View chat')
  expect(host.textContent).not.toContain('iteration-84-sched')
})

it('expands to labeled rows, facts, and actions', async () => {
  const { host, onOpen, onDelete } = await mount()
  await expand(host)
  expect(host.textContent).toContain('Started with')
  expect(host.textContent).toContain('Summarize overnight events')
  expect(host.textContent).toContain('Outcome')
  expect(host.textContent).toContain('iteration-84-sched')
  expect(host.textContent).toContain('Default Group')
  const viewChat = [...host.querySelectorAll('button')].find(button => button.textContent === 'View chat')
  expect(viewChat).toBeDefined()
  await act(async () => (viewChat as HTMLButtonElement).click())
  expect(onOpen).toHaveBeenCalledWith(successRun)
  const remove = host.querySelector('button[aria-label="Delete conversation record"]')
  await act(async () => (remove as HTMLButtonElement).click())
  expect(onDelete).toHaveBeenCalledWith(successRun)
})

it('keeps raw errors out of the collapsed line', async () => {
  const { host } = await mount({ run: failedRun })
  expect(host.textContent).toContain('Failed run')
  expect(host.textContent).toContain('Stopped after 35m')
  expect(host.textContent).toContain('This recorded execution failed.')
  expect(host.textContent).not.toContain('produced no response')
  await expand(host)
  expect(host.textContent).toContain('Technical details')
  expect(host.textContent).toContain('produced no response')
})

it('names webhook runs and skips Started with', async () => {
  const { host } = await mount({ job: webhookJob, run: webhookRun, showScheduleName: true })
  expect(host.textContent).toContain('PR opened')
  expect(host.textContent).toContain('Reviewed PR 87.')
  expect(host.textContent).toContain('0b8b40e69a12')
  await expand(host)
  expect(host.textContent).toContain('Final response')
  expect(host.textContent).toContain('Delivery details')
  expect(host.textContent).toContain('0b8b40e69a1234567890abcdef1234567890abcd')
  expect(host.textContent).toContain('Componentchat')
  expect(host.textContent).toContain('Environmentstaging')
  expect(host.textContent).not.toContain('Started with')
})
