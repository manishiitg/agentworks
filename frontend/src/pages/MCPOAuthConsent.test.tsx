// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../services/api', () => ({ default: api }))
import { MCPOAuthConsent } from './MCPOAuthConsent'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.resetAllMocks())

async function mount(scopes: string[], workflows = [{ id: 'invoices', label: 'Invoices' }], path = '/oauth/consent') {
  window.history.replaceState({}, '', `${path}?request=mcp_req_${'a'.repeat(64)}`)
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


it('explains explicit Relay authoring without requiring an existing workflow selection', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'relays:write'], [])
  try {
    expect(view.host.textContent).toContain('Create, edit, test and publish Relays')
    expect(view.host.querySelector('input[type="checkbox"]')).toBeNull()
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    expect(allow.disabled).toBe(false)
  } finally { await view.cleanup() }
})

it('uses the Vault consent API with the shared platform login page', async () => {
  const view = await mount(['vault:mcp'], [], '/oauth/vault')
  try {
    expect(api.get).toHaveBeenCalledWith('/api/oauth/vault/consent', expect.any(Object))
    expect(view.host.textContent).toContain('Connect to Vault')
    expect(view.host.textContent).toContain('your current Vault groups')
    expect(view.host.querySelector('input[type=checkbox]')).toBeNull()
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    await act(async () => allow.click())
    expect(api.post).toHaveBeenCalledWith(expect.stringContaining('/api/oauth/vault/consent?request='), { decision: 'approve', workflow_ids: [] })
  } finally { await view.cleanup() }
})
