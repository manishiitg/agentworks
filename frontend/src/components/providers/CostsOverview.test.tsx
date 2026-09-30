// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { agentApi } from '../../services/api'
import type { CostAggregate, CostOverview } from '../../services/api-types'
import CostsOverview from './CostsOverview'

vi.mock('../../services/api', () => ({ agentApi: { getCostOverview: vi.fn() } }))
vi.mock('../../services/llm-config-api', () => ({ llmConfigService: {} }))
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
      { id: '_users/alice/Chats/Video Studio/projects/launch', kind: 'product', name: 'Video Studio · launch', owner_email: 'owner@example.com', ...usage({ total_cost_usd: 0.5, call_count: 1 }) },
      { id: 'other', kind: 'other', name: 'Unattributed activity', ...usage({ total_cost_usd: 0.25, call_count: 1 }) },
    ],
    by_user: [{ id: 'alice', name: 'Alice', ...usage({ total_cost_usd: 3, call_count: 2 }), by_scope: { chat: usage({ total_cost_usd: 1, call_count: 1 }) }, by_model: { gpt: usage({ provider: 'codex-cli', total_cost_usd: 3, call_count: 2 }) }, by_work: [{ id: 'Workflow/shared', kind: 'workflow', name: 'shared', ...usage({ total_cost_usd: 2, call_count: 1 }) }] }],
    by_bot: [{ id: 'slack:bot:Workflow/shared', name: 'Slack · shared', workflow: 'Workflow/shared', platform: 'slack', user_id: 'bot', ...usage({ total_cost_usd: 2, call_count: 1 }) }],
    by_mcp: [{ server: 'github', calls: 3, unpriced_calls: 3, recorded_cost_usd: 0, by_user: [{ id: 'alice', name: 'Alice', email: 'alice@example.com', calls: 3, unpriced_calls: 3, recorded_cost_usd: 0 }] }], includes_other: true,
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
  expect(container.textContent).toContain('Owner: owner@example.com')
  const search = container.querySelector<HTMLInputElement>('[aria-label="Find projects"]')
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    setter.call(search, 'owner@example.com')
    search?.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(button(container, 'Video Studio · launch')).toBeTruthy()
  await click(button(container, 'Bots'))
  expect(container.textContent).toContain('External channel delivery fees are not included')
  await click(button(container, 'MCP'))
  expect(container.textContent).toContain('Known service chargeNone recorded')
  expect(container.textContent).toContain('Users who accessed this MCP')
  expect(container.textContent).toContain('alice@example.com')
  expect(container.textContent).toContain('3 tool calls')
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
  expect(container.textContent).toContain('Tracked costNot priced')
  expect(container.textContent).toContain('472 calls have tokens but no price')
  expect(button(container, 'rts')?.textContent).toContain('Not priced')
})

it('shows cost by account with the split by work and person', async () => {
  vi.mocked(agentApi.getCostOverview).mockResolvedValue({
    total: usage({ total_cost_usd: 5, call_count: 2 }), by_provider: {}, by_model: {}, items: [], by_user: [], includes_other: false,
    by_account: [{ provider: 'claude-code', total: usage({ total_cost_usd: 5, call_count: 2 }), accounts: [
      { account_id: 'acct-1', name: 'Alice Max', kind: 'user', owner_name: 'Alice', full_split: false, total: usage({ total_cost_usd: 5, call_count: 2, prompt_tokens: 1000 }),
        split: [{ work_id: 'wf-1', work_kind: 'workflow', work_name: 'Research', user_id: 'bob', user_name: 'Bob', ...usage({ total_cost_usd: 5, call_count: 2 }) }] },
    ] }],
  } as CostOverview)
  const container = await render()
  const byAccount = [...container.querySelectorAll('details')].find(details => details.textContent?.includes('By account'))
  expect(byAccount).toBeTruthy()
  expect(byAccount?.textContent).toContain('Claude Code')
  expect(byAccount?.textContent).toContain('Alice Max')
  expect(byAccount?.textContent).toContain('Owner: Alice')
  expect(byAccount?.textContent).toContain('Your share only')
  await click(byAccount?.querySelector<HTMLButtonElement>('[aria-label="Show where Alice Max was used"]') ?? undefined)
  expect(byAccount?.textContent).toContain('Research')
  expect(byAccount?.textContent).toContain('Bob')
})

 it('shows canonical input, output and cached input without adding cache again', async () => {
  const muse = usage({ provider: 'muse-cli', input_tokens: 1000, prompt_tokens: 1000, completion_tokens: 25, cache_read_tokens: 800, total_cost_usd: 0.1, call_count: 3 })
  vi.mocked(agentApi.getCostOverview).mockResolvedValue({
    total: muse, by_provider: { 'muse-cli': muse }, by_model: {}, items: [], includes_other: false,
    by_user: [{ id: 'alice', name: 'Alice', ...muse }],
  } as CostOverview)
  const container = await render()
  expect(container.textContent).toContain('Input tokens1.0K')
  expect(container.textContent).toContain('Output tokens25')
  expect(container.textContent).toContain('Cached input800')
  expect(container.textContent).toContain('1.0K input · 25 output')
  expect(container.textContent).not.toContain('LLM calls')
  expect(container.textContent).not.toContain('1.8K')
 })

it('separates Code workspaces from other product projects and searches owner emails', async () => {
  const code = { id: '_users/alice/Chats/Code/projects/code', kind: 'product' as const, name: 'Code · code', owner_email: 'alice@example.com', ...usage({ call_count: 1 }) }
  vi.mocked(agentApi.getCostOverview).mockResolvedValue({ total: usage(), by_provider: {}, by_model: {}, items: [code], by_user: [], includes_other: false } as CostOverview)
  const container = await render()
  expect(button(container, 'Projects')).toBeUndefined()
  await click(button(container, 'Code'))
  expect(button(container, 'Code · code')).toBeTruthy()
  expect(container.textContent).toContain('Owner: alice@example.com')
  const search = container.querySelector<HTMLInputElement>('[aria-label="Find code"]')
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'alice@example.com')
    search?.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(button(container, 'Code · code')).toBeTruthy()
})

it('explains absent usage without claiming the model has no rate', async () => {
  const missing = usage({ call_count: 6, unpriced_call_count: 6, missing_usage_call_count: 6 })
  vi.mocked(agentApi.getCostOverview).mockResolvedValue({ total: missing, by_provider: {}, by_model: {}, items: [], by_user: [{ id: 'alice', name: 'Alice', ...missing }], includes_other: false } as CostOverview)
  const container = await render()
  expect(container.textContent).toContain('6 calls did not report tokens or cost')
  expect(container.textContent).not.toContain('no model rate')
})
