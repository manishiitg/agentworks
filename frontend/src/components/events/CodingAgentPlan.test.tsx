import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { CodingAgentPlan } from './CodingAgentPlan'
import type { PollingEvent } from '../../services/api-types'

it('shows native completed/running/pending states as text, safely (PLAT-746)', () => {
  const event = { type: 'coding_agent_plan', data: { plan_id: 'p', items: [
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
