// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
vi.mock('../../services/api', () => ({ authApi: { listAccessTokens: vi.fn(), createAccessToken: vi.fn(), revokeAccessToken: vi.fn() } }))
import { authApi } from '../../services/api'
import { LocalMcpTokenPanel } from './LocalMcpTokenPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const token = { id: 'test-token', name: 'agentworks-local', scopes: ['knowledgebase:read', 'knowledgebase:write'], non_expiring: true, expires_at: '9999-12-31T23:59:59Z', revoked_at: null }
const cleanups: (() => void)[] = []
beforeEach(() => { vi.mocked(authApi.listAccessTokens).mockResolvedValue({ tokens: [] }); vi.mocked(authApi.createAccessToken).mockResolvedValue({ token: 'test-only-secret', access_token: token as any }); vi.mocked(authApi.revokeAccessToken).mockResolvedValue(undefined) })
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.resetAllMocks(); document.body.innerHTML = '' })
async function mount() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanups.push(() => act(() => root.unmount()))
  await act(async () => root.render(<LocalMcpTokenPanel endpoint="http://127.0.0.1:19743/api/external/v1/mcp" />))
  return host
}
function button(host: HTMLElement, label: string) { return [...host.querySelectorAll('button')].find(button => button.textContent === label)! }
describe('local MCP access tokens', () => {
  it('issues full local account access with access and expiry presets and revokes the token', async () => {
    const host = await mount()
    expect(host.querySelector('input[type=checkbox]')).toBeNull()
    expect(host.querySelectorAll('select')).toHaveLength(2)
    expect(host.querySelector('[aria-label="Access token name"]')).toBeNull()
    vi.mocked(authApi.listAccessTokens).mockResolvedValue({ tokens: [token as any] })
    await act(async () => button(host, 'Create access token').click())
    expect(authApi.createAccessToken).toHaveBeenCalledWith(expect.objectContaining({ name: 'agentworks-local', local_full_access: true, scopes: [] }))
    expect(host.querySelector<HTMLInputElement>('[aria-label="New access token"]')?.value).toBe('test-only-secret')
    expect(host.querySelector('[aria-label="Local MCP client config"]')?.textContent).not.toContain('test-only-secret')
    await act(async () => button(host, 'Revoke').click())
    expect(authApi.revokeAccessToken).toHaveBeenCalledWith('test-token')
    expect(host.querySelector('[aria-label="New access token"]')).toBeNull()
  })
  it('requests read-only access with an expiry', async () => {
    const host = await mount()
    const access = host.querySelector<HTMLSelectElement>('[aria-label="Local token access"]')!
    const expiry = host.querySelector<HTMLSelectElement>('[aria-label="Local token expiry"]')!
    await act(async () => { access.value = 'read'; access.dispatchEvent(new Event('change', { bubbles: true })); expiry.value = '7'; expiry.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => button(host, 'Create access token').click())
    expect(authApi.createAccessToken).toHaveBeenCalledWith(expect.objectContaining({ local_full_access: false, local_read_only: true, expires_in_days: 7 }))
  })
  it('handles failed issuance', async () => {
    const host = await mount()
    vi.mocked(authApi.createAccessToken).mockRejectedValue(new Error('offline'))
    await act(async () => button(host, 'Create access token').click())
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not create')
    expect(host.querySelector('[aria-label="New access token"]')).toBeNull()
    expect(button(host, 'Create access token').disabled).toBe(false)
  })
})
