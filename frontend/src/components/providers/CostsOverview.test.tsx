// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { agentApi } from '../../services/api'
import type { CostAggregate, CostOverview } from '../../services/api-types'
import CostsOverview from './CostsOverview'

vi.mock('../../services/api', () => ({ agentApi: { getCostOverview: vi.fn() } }))
vi.mock('../../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: vi.fn().mockResolvedValue([]) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
const usage = (fields: Partial<CostAggregate> = {}): CostAggregate => ({ prompt_tokens: 0, completion_tokens: 0, reasoning_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, total_cost_usd: 0, call_count: 0, ...fields })
const render = async () => {
  const container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  await act(async () => { root?.render(<CostsOverview />) })
  return container
}
const button = (container: HTMLElement, label: string) => [...container.querySelectorAll('button')].find(value => value.textContent?.trim().startsWith(label))
const click = async (element?: HTMLButtonElement) => { expect(element).toBeTruthy(); await act(async () => { element?.click() }) }
afterEach(() => { act(() => { root?.unmount() }); root = undefined; document.body.innerHTML = ''; vi.clearAllMocks() })

it('offers separate user, workflow, crew and project summaries with cross navigation', async () => {
  vi.mocked(agentApi.getCostOverview).mockResolvedValue({
    total: { ...usage({ total_cost_usd: 4, call_count: 3 }), subscription_shadow_cost_usd: 4 },
    by_provider: { 'cursor-cli': usage({ call_count: 5, unpriced_call_count: 5 }) }, by_model: {},
    items: [
      { id: 'Workflow/shared', kind: 'workflow', name: 'shared', ...usage({ total_cost_usd: 3, call_count: 2 }), by_scope: { workflow_execution: usage({ total_cost_usd: 3, call_count: 2 }) }, by_user: [{ id: 'alice', name: 'Alice', ...usage({ total_cost_usd: 2, call_count: 1 }) }] },
      { id: '_users/alice/Chats/Work/projects/crew', kind: 'crew', name: 'crew', ...usage({ total_cost_usd: 1, call_count: 1 }) },
      { id: '_users/alice/Chats/Video Studio/projects/launch', kind: 'product', name: 'Video Studio · launch', ...usage({ total_cost_usd: 0.5, call_count: 1 }) },
      { id: 'other', kind: 'other', name: 'Unattributed activity', ...usage({ total_cost_usd: 0.25, call_count: 1 }) },
    ],
    by_user: [{ id: 'alice', name: 'Alice', ...usage({ total_cost_usd: 3, call_count: 2 }), by_scope: { chat: usage({ total_cost_usd: 1, call_count: 1 }) }, by_model: { gpt: usage({ provider: 'codex-cli', total_cost_usd: 3, call_count: 2 }) }, by_work: [{ id: 'Workflow/shared', kind: 'workflow', name: 'shared', ...usage({ total_cost_usd: 2, call_count: 1 }) }] }],
    by_bot: [{ id: 'slack:bot:Workflow/shared', name: 'Slack · shared', workflow: 'Workflow/shared', platform: 'slack', user_id: 'bot', ...usage({ total_cost_usd: 2, call_count: 1 }) }],
    by_mcp: [{ server: 'github', calls: 3, unpriced_calls: 3, recorded_cost_usd: 0 }], includes_other: true,
  } as CostOverview)
  const container = await render()
  expect(button(container, 'Users')?.getAttribute('aria-pressed')).toBe('true')
  expect(container.textContent).toContain('Where this user worked')
  expect(container.textContent).toContain('Models')
  expect(container.querySelector('details')?.open).toBe(false)
  await click(button(container, 'shared'))
  expect(button(container, 'Workflows')?.getAttribute('aria-pressed')).toBe('true')
  expect(container.textContent).toContain('Users in this workflow')
  await click(button(container, 'Crews'))
  expect(container.textContent).toContain('Detailed summary')
  await click(button(container, 'Projects'))
  expect(container.textContent).toContain('Video Studio · launch')
  await click(button(container, 'Bots'))
  expect(container.textContent).toContain('External channel delivery fees are not included')
  await click(button(container, 'MCP'))
  expect(container.textContent).toContain('Known service chargeUnknown')
  await click(button(container, 'Other'))
  expect(container.textContent).toContain('Unattributed activity')
})

it('labels unpriced workflow cost as unknown instead of zero', async () => {
  vi.mocked(agentApi.getCostOverview).mockResolvedValue({
    total: { ...usage({ call_count: 472, unpriced_call_count: 472 }) }, by_provider: {}, by_model: {},
    items: [{ id: 'Workflow/rts', kind: 'workflow', name: 'rts', ...usage({ call_count: 472, unpriced_call_count: 472 }) }],
    by_user: [], includes_other: false,
  } as CostOverview)
  const container = await render()
  await click(button(container, 'Workflows'))
  expect(container.textContent).toContain('Tracked costUnknown')
  expect(container.textContent).toContain('472 LLM calls have unknown cost')
  expect(button(container, 'rts')?.textContent).toContain('Unknown')
})
