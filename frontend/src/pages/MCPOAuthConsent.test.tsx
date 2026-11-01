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

it('explains explicit Flow authoring without requiring an existing workflow selection', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'relays:write'], [])
  try {
    expect(view.host.textContent).toContain('Create, edit files and dashboards, test and publish Flows within your current role')
    expect(view.host.textContent).not.toContain('files:write')
    expect(view.host.textContent).not.toContain('dashboards:write')
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

it('says in one plain line that the app can do what the person can do on the website, with the exact permissions behind details', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'crews:read', 'crews:run', 'crews:write'], [])
  try {
    expect(view.host.querySelector('main > div > ul')).toBeNull()
    expect(view.host.textContent).toContain('This app can do what you can do on the website, and nothing more.')
    expect(view.host.querySelector('details')).not.toBeNull()
    expect(view.host.querySelector('details')!.textContent).toContain('Read workflow files, including test code')
  } finally { await view.cleanup() }
})

it('shows the reason the server gives when a connection is refused', async () => {
  const view = await mount(['workflows:read'], [])
  try {
    api.post.mockReset()
    api.post.mockRejectedValue({ response: { data: { error_description: 'Flow authoring is not available to this account or deployment' } } })
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    await act(async () => allow.click())
    expect(view.host.querySelector('[role=alert]')!.textContent).toContain('not available to this account or deployment')
  } finally { await view.cleanup() }
})

it('asks for no workflow selection: Builder follows the account', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'builder:chat'], [])
  try {
    expect(view.host.textContent).toContain('Edit workflow plans directly, without a model turn')
    expect(view.host.textContent).toContain('native validation, revisions and the plan changelog')
    expect(view.host.querySelector('input[type=checkbox]')).toBeNull()
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    expect(allow.disabled).toBe(false)
    await act(async () => allow.click())
    expect(api.post).toHaveBeenCalledWith(expect.any(String), { decision: 'approve', workflow_ids: [] })
  } finally { await view.cleanup() }
})

it('explains Vault management separately from runtime tool access', async () => {
  const view = await mount(['vault:manage'], [])
  try {
    expect(view.host.textContent).toContain('This app can do what you can do on the website')
    expect(view.host.textContent).toContain('Secret values are not returned')
    expect(view.host.textContent).not.toContain('Use Vault MCP tools you are allowed to use')
  } finally { await view.cleanup() }
})


it('shows a single project limit and its saved capabilities on consent', async () => {
  window.history.replaceState({}, '', `/oauth/consent?request=mcp_req_${'a'.repeat(64)}`)
  api.get.mockResolvedValue({ data: { client_name: 'Project app', redirect_uri: 'https://client.example/callback', scopes: ['dashboards:read', 'crews:read', 'crews:run'], project: { id: 'p1', kind: 'work', title: 'Reporting', access: 'view' } } })
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<MCPOAuthConsent />))
    expect(host.textContent).toContain('Reporting only')
    expect(host.textContent).toContain('Other projects and project settings are excluded')
    expect(host.textContent).not.toContain('This app can do what you can do on the website')
    expect(host.textContent).not.toContain('View and use this project')
  } finally { await act(async () => root.unmount()) }
})
