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

it('leads with a few plain lines and keeps the exact permissions behind details', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'crews:read', 'crews:run', 'crews:write'], [])
  try {
    const lines = [...view.host.querySelectorAll('main > div > ul > li')].map(li => li.textContent)
    expect(lines).toEqual(['See and run your workflows', 'Use your Crews', 'Make changes: edit your Crews, Relays and workflows, as far as your role allows'])
    expect(view.host.querySelector('details')).not.toBeNull()
    expect(view.host.querySelector('details')!.textContent).toContain('Read workflow files, including test code')
  } finally { await view.cleanup() }
})

it('shows the reason the server gives when a connection is refused', async () => {
  const view = await mount(['workflows:read'], [])
  try {
    api.post.mockReset()
    api.post.mockRejectedValue({ response: { data: { error_description: 'Relay authoring is not available to this account or deployment' } } })
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    await act(async () => allow.click())
    expect(view.host.querySelector('[role=alert]')!.textContent).toContain('not available to this account or deployment')
  } finally { await view.cleanup() }
})

it('asks for no workflow selection: Builder follows the account', async () => {
  const view = await mount(['workflows:read', 'files:read', 'runs:execute', 'builder:chat'], [])
  try {
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
    expect(view.host.textContent).toContain('Manage Vault connections, groups and permissions (administrator)')
    expect(view.host.textContent).toContain('Secret values are not returned')
    expect(view.host.textContent).not.toContain('Use Vault MCP tools you are allowed to use')
  } finally { await view.cleanup() }
})

it('explains direct file write consent and the protected plan boundary', async () => {
  const view = await mount(['workflows:read', 'files:read', 'files:write'], [])
  try {
    expect(view.host.textContent).toContain('Write workflow source and documentation with revision checks')
    expect(view.host.textContent).toContain('Plans, configuration, databases and private files stay protected')
    expect(view.host.querySelector('input[type=checkbox]')).toBeNull()
  } finally { await view.cleanup() }
})

// Owner 2026-10-08: the person approving can hand an app read-only access; the
// server then keeps only the read scopes (mcp_oauth_test.go).
it('sends read_only only when the person turns Read only on', async () => {
  const view = await mount(['workflows:read', 'runs:execute'])
  try {
    const toggle = view.host.querySelector('button[role=switch]') as HTMLButtonElement
    expect(toggle.getAttribute('aria-checked')).toBe('false')
    await act(async () => toggle.click())
    const allow = [...view.host.querySelectorAll('button')].find(button => button.textContent === 'Allow access')!
    await act(async () => allow.click())
    expect(api.post).toHaveBeenCalledWith(expect.any(String), { decision: 'approve', workflow_ids: [], read_only: true })
  } finally { await view.cleanup() }
})
