// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({
  canWrite: true,
  native: false,
  sourceError: false,
  inspectError: false,
  get: vi.fn(async () => ({ manifest: { id: 'id', relay_durability: state.native ? 'dbos' : '' } })),
  update: vi.fn(async (_request: unknown) => ({})),
  refresh: vi.fn(async () => {}),
}))
vi.mock('../../services/api', () => ({ agentApi: { getPlannerFileContent: async () => {
  if (state.sourceError) throw new Error('source unavailable')
  return { data: { content: state.native ? 'from dbos import DBOS\n@DBOS.workflow()\nasync def run(INPUT): return INPUT' : 'async def run(INPUT, ctx): return INPUT' } }
}, getWorkflowManifest: state.get, updateWorkflowManifest: state.update } }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => state.canWrite }))
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: { getState: () => ({ refreshWorkflows: state.refresh }) } }))
vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { relayGraph: async () => {
  if (state.inspectError) throw new Error('inspection unavailable')
  return { native: state.native, nodes: [], edges: [], errors: [] }
} } }))
import { RelayDurabilitySetting } from './RelayDurabilitySetting'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div')
const root = createRoot(host)
afterEach(async () => { await act(async () => root.render(null)); vi.clearAllMocks(); state.canWrite = true; state.native = false; state.sourceError = false; state.inspectError = false })
async function renderSetting() {
  await act(async () => root.render(<RelayDurabilitySetting workspacePath="Workflow/orders" />))
  return host.querySelector('input') as HTMLInputElement
}
it('saves the explicit recovery setting for this draft and refreshes its manifest', async () => {
  const checkbox = await renderSetting()
  await act(async () => checkbox.click())
  expect(state.update).toHaveBeenCalledWith({ workspace_path: 'Workflow/orders', relay_durability: 'dbos' })
  expect(checkbox.checked).toBe(true)
  expect(state.refresh).toHaveBeenCalledOnce()
  expect(host.textContent).toContain('Publish a new version')
})
it('keeps recovery read-only for a reader', async () => {
  state.canWrite = false
  const checkbox = await renderSetting()
  expect(checkbox.disabled).toBe(true)
  await act(async () => checkbox.click())
  expect(state.update).not.toHaveBeenCalled()
})
it('does not display an unsaved setting after a rejected update', async () => {
  state.update.mockRejectedValueOnce(new Error('denied'))
  const checkbox = await renderSetting()
  await act(async () => checkbox.click())
  expect(checkbox.checked).toBe(false)
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not save')
})

it('keeps recovery enabled for authored native DBOS programs', async () => {
  state.native = true
  const checkbox = await renderSetting()
  expect(checkbox.checked).toBe(true)
  expect(checkbox.disabled).toBe(true)
  expect(host.textContent).toContain('Native DBOS programs require recovery to stay enabled')
})

it('refuses recovery changes when source loading or inspection fails', async () => {
  state.native = true
  for (const failure of ['sourceError', 'inspectError'] as const) {
    state[failure] = true
    const checkbox = await renderSetting()
    expect(checkbox.disabled).toBe(true)
    await act(async () => checkbox.click())
    expect(state.update).not.toHaveBeenCalled()
    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    await act(async () => root.render(null))
    state[failure] = false
  }
})
