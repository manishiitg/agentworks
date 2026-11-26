import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { CodingAgentPlan } from './CodingAgentPlan'
import type { PollingEvent } from '../../services/api-types'

it('shows native completed/running/pending states as text, safely (PLAT-746)', () => {
  const event = { id: 'plan-event', timestamp: '2026-11-14T00:00:00Z', type: 'coding_agent_plan', data: { plan_id: 'p', items: [
    { id: '1', text: '<script>unsafe</script>', status: 'completed' },
    { id: '2', text: 'Review', status: 'running' },
    { id: '3', text: 'Report', status: 'pending' },
  ] } } as PollingEvent
  const html = renderToStaticMarkup(<CodingAgentPlan event={event} />)
  expect(html).toContain('Done: ')
  expect(html).toContain('In progress: ')
  expect(html).toContain('Pending: ')
  expect(html).toContain('&lt;script&gt;unsafe&lt;/script&gt;')
  expect(html).not.toContain('<script>')
})

it('calls the native checklist a to-do list, not a plan (#3128)', () => {
  const event = { id: 'todo-event', timestamp: '2026-11-18T00:00:00Z', type: 'coding_agent_plan', data: { plan_id: 'p', items: [
    { id: '1', text: 'First', status: 'completed' },
    { id: '2', text: 'Second', status: 'pending' },
  ] } } as PollingEvent
  const html = renderToStaticMarkup(<CodingAgentPlan event={event} />)
  expect(html).toContain('aria-label="Agent to-do list"')
  expect(html).toContain('>To-do</span>')
  expect(html).toContain('1 of 2 done')
  expect(html).not.toContain('>Plan</span>')
})
