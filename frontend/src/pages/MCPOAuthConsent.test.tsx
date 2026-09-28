// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../services/api', () => ({ default: api }))
import { MCPOAuthConsent } from './MCPOAuthConsent'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.resetAllMocks())

async function mount(scopes: string[], workflows = [{ id: 'invoices', label: 'Invoices' }]) {
  window.history.replaceState({}, '', `/oauth/consent?request=mcp_req_${'a'.repeat(64)}`)
  api.get.mockResolvedValue({ data: { client_name: 'Test app', redirect_uri: 'https://client.example/callback', scopes, editable_workflows: workflows } })
  // Keep the response pending: this test checks authorization payload, not navigation.
  api.post.mockImplementation(() => new Promise(() => {}))
  const host = document.createElement('div')
  const root = createRoot(host)
  await act(async () => root.render(<MCPOAuthConsent />))
  return { host, cleanup: async () => { await act(async () => root.unmount()) } }
}

it('requires an explicit workflow selection before approving Builder', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'builder:chat'])
  try {
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    expect(allow.disabled).toBe(true)
    expect(view.host.textContent).toContain('Reading and running are also limited to this selection')
    await act(async () => (view.host.querySelector('input[type=checkbox]') as HTMLInputElement).click())
    expect(allow.disabled).toBe(false)
    await act(async () => allow.click())
    expect(api.post).toHaveBeenCalledWith(expect.any(String), { decision: 'approve', workflow_ids: ['invoices'] })
  } finally { await view.cleanup() }
})

it('keeps ordinary read/run consent unchanged without authoring selection', async () => {
  const view = await mount(['workflows:read', 'runs:execute'])
  try {
    expect(view.host.querySelector('input[type=checkbox]')).toBeNull()
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    expect(allow.disabled).toBe(false)
    await act(async () => allow.click())
    expect(api.post).toHaveBeenCalledWith(expect.any(String), { decision: 'approve', workflow_ids: [] })
  } finally { await view.cleanup() }
})

it('cannot approve Builder when no workflows are editable', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'builder:chat'], [])
  try {
    expect(view.host.textContent).toContain('You have no editable workflows')
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    expect(allow.disabled).toBe(true)
    expect([...view.host.querySelectorAll('button')].find(button => button.textContent === 'Deny')!.disabled).toBe(false)
  } finally { await view.cleanup() }
})
