// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { list, remove, listTriggers, removeTrigger } = vi.hoisted(() => ({ list: vi.fn(), remove: vi.fn(), listTriggers: vi.fn(), removeTrigger: vi.fn() }))
vi.mock('../../api/crewFunctions', () => ({
  crewFunctionsApi: { list: (...args: unknown[]) => list(...args), delete: (...args: unknown[]) => remove(...args) },
}))
vi.mock('../../api/productWebhooks', () => ({
  productWebhooksApi: { list: (...args: unknown[]) => listTriggers(...args), delete: (...args: unknown[]) => removeTrigger(...args) },
}))

import CrewFunctionsView, { schemaFields } from './CrewFunctionsView'

const scope = { profileId: 'work', projectId: 'beta' }
let container: HTMLDivElement
let root: Root

async function renderView(props: Partial<Parameters<typeof CrewFunctionsView>[0]> = {}) {
  await act(async () => { root.render(<CrewFunctionsView scope={scope} {...props} />) })
  await act(async () => { await Promise.resolve() })
}

const byTestId = (id: string) => container.querySelector(`[data-testid="${id}"]`) as HTMLElement | null

describe('CrewFunctionsView', () => {
  beforeEach(() => {
    ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    list.mockReset()
    remove.mockReset()
    listTriggers.mockReset()
    removeTrigger.mockReset()
    listTriggers.mockResolvedValue({ triggers: [
      { id: 'hook-1', name: 'GitHub push', enabled: true, message: 'x', auth_mode: 'github', path: '/api/hooks/product/hook-1', run_destination: 'crew_chat' },
      { id: 'bind-1', name: 'Called by Alpha Bot', enabled: true, message: 'x', auth_mode: '', path: '', run_destination: 'isolated', kind: 'internal', caller: { type: 'crew', id: 'alpha' } },
    ] })
    removeTrigger.mockResolvedValue({})
    list.mockResolvedValue({
      functions: [
        {
          name: 'run_login_flow', description: 'Run the login flow', created_by: 'crew:alpha (Alpha Bot)', updated_at: '2026-09-24T05:00:00Z',
          input_schema: { type: 'object', required: ['build'], properties: { build: { type: 'string' }, env: { type: 'string', enum: ['staging', 'prod'] } } },
          result_schema: { type: 'object', required: ['passed'], properties: { passed: { type: 'boolean' } } },
        },
        { name: 'ask', description: 'Ask anything', implicit: true, input_schema: { type: 'object', required: ['message'], properties: { message: { type: 'string' } } }, result_schema: { type: 'object', properties: { answer: { type: 'string' } } } },
      ],
      calls: [
        { call_id: 'fn-1', function: 'run_login_flow', caller_kind: 'crew', caller_label: 'Alpha Bot', status: 'running', started_at: '2026-09-24T05:01:00Z', latest_progress: { at: '2026-09-24T05:02:00Z', message: 'login page loaded' }, progress: [{ at: '2026-09-24T05:02:00Z', message: 'login page loaded', percent: 40 }] },
        { call_id: 'fn-2', function: 'ask', caller_kind: 'workflow', caller_label: 'Reports', status: 'completed', started_at: '2026-09-24T04:00:00Z', finished_at: '2026-09-24T04:01:00Z', answer: 'three bugs', files: [{ name: 'report.pdf', size: 120, mime_type: 'application/pdf' }], isolated_execution: true },
      ],
    })
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('lists declared functions with inputs, returns and creator, excluding conversational ask', async () => {
    await renderView()
    const fn = byTestId('crew-function-run_login_flow')!
    expect(fn.textContent).toContain('build: string')
    expect(fn.textContent).toContain('env: staging | prod?')
    expect(fn.textContent).toContain('passed: boolean')
    expect(fn.textContent).toContain('By crew:alpha (Alpha Bot)')
    expect(byTestId('crew-function-ask')).toBeNull()
    expect(container.textContent).toContain('fresh isolated chat')
  })

  it('shows recent calls with status and latest progress, expandable to the result', async () => {
    await renderView()
    const running = byTestId('crew-function-call-fn-1')!
    expect(running.textContent).toContain('Running')
    expect(running.textContent).toContain('login page loaded')
    const done = byTestId('crew-function-call-fn-2')!
    expect(done.textContent).toContain('Done')
    expect(done.textContent).not.toContain('three bugs')
    await act(async () => { done.querySelector('button')!.click() })
    expect(done.textContent).toContain('three bugs')
    expect(done.textContent).toContain('report.pdf')
    expect(done.textContent).toContain('Isolated execution')
  })

  it('removes a function and routes edits to the Crew chat', async () => {
    const onEdit = vi.fn()
    remove.mockResolvedValue({})
    await renderView({ onEdit })
    const fn = byTestId('crew-function-run_login_flow')!
    const buttons = Array.from(fn.querySelectorAll('button'))
    await act(async () => { buttons.find(button => button.textContent === 'Edit in chat')!.click() })
    expect(onEdit).toHaveBeenCalledWith(expect.stringContaining('"run_login_flow"'))
    await act(async () => { buttons.find(button => button.textContent === 'Remove')!.click() })
    expect(remove).toHaveBeenCalledWith(scope, 'run_login_flow')
  })

  it('describes schema fields', () => {
    expect(schemaFields({ type: 'object', required: ['a'], properties: { a: { type: 'array', items: { type: 'number' } }, b: { type: 'boolean' } } }))
      .toEqual([{ name: 'a', type: 'number[]', required: true }, { name: 'b', type: 'boolean', required: false }])
  })
  it('lists callers (not webhooks) and disconnects one', async () => {
    await renderView()
    const callers = byTestId('crew-function-callers')!
    expect(callers.textContent).toContain('Called by Alpha Bot')
    expect(callers.textContent).toContain('Crew')
    expect(callers.textContent).not.toContain('GitHub push')
    const disconnect = Array.from(callers.querySelectorAll('button')).find(button => button.textContent === 'Disconnect')!
    await act(async () => { disconnect.click(); await Promise.resolve() })
    expect(removeTrigger).toHaveBeenCalledWith(scope, 'bind-1')
  })
})
