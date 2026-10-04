// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ list: vi.fn(), create: vi.fn(), revoke: vi.fn() }))
vi.mock('../../services/api', () => ({ default: {}, getApiBaseUrl: () => 'https://knowledge.example/', authApi: { listAccessTokens: mocks.list, createAccessToken: mocks.create, revokeAccessToken: mocks.revoke } }))
import { KnowledgebaseConnectPanel, knowledgebaseMcpURL, knowledgebaseTokenCaps } from './KnowledgebaseConnectPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.clearAllMocks(); document.body.innerHTML = '' })
describe('Knowledge Base connections', () => {
  it('distinguishes restricted, unrestricted live-grant, and zero-access caps', () => {
    expect(knowledgebaseTokenCaps('folder', 'Engineering/Payments', false)).toEqual([{ folder_path: 'Engineering/Payments', role: 'reader' }])
    expect(knowledgebaseTokenCaps('folder', '', true)).toEqual([{ folder_path: '', role: 'editor' }])
    expect(knowledgebaseTokenCaps('grants', '', true)).toBeNull()
    expect(knowledgebaseTokenCaps('none', '', true)).toEqual([])
    expect(knowledgebaseMcpURL('https://knowledge.example/')).toBe('https://knowledge.example/api/external/v1/mcp')
  })
  it('offers enabled service identities to admins and keeps secrets out of connection examples', async () => {
    mocks.list.mockResolvedValue({ tokens: [] })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    cleanups.push(() => act(() => root.unmount()))
    await act(async () => { root.render(<KnowledgebaseConnectPanel folder="Payments" isAdmin identities={[{ id: 'service_checkout', name: 'Checkout agent', type: 'service' }, { id: 'disabled', name: 'Disabled agent', type: 'service', disabled: true }]} onAsk={() => {}} />) })
    expect(host.textContent).toContain('Checkout agent')
    expect(host.textContent).not.toContain('Disabled agent')
    expect(host.querySelector('pre')?.textContent).toContain('Bearer YOUR_TOKEN')
    expect(host.querySelector('pre')?.textContent).toContain('/api/external/v1/mcp')
    expect(host.textContent).toContain('get_api_spec(names=["read_knowledgebase", "update_knowledgebase"])')
    expect((host.querySelector('input[type="number"]') as HTMLInputElement).max).toBe('90')
    await act(async () => { root.render(<KnowledgebaseConnectPanel folder="Payments" isAdmin={false} identities={[{ id: 'service_checkout', name: 'Checkout agent', type: 'service' }]} onAsk={() => {}} />) })
    expect(host.textContent).not.toContain('Act as')
    expect(host.textContent).not.toContain('Checkout agent')
  })
  it('creates a scoped service token with both required capabilities and a one-time secret', async () => {
    mocks.list.mockResolvedValue({ tokens: [] })
    mocks.create.mockResolvedValue({ token: 'secret-for-agent', access_token: { id: 'token1' } })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    cleanups.push(() => act(() => root.unmount()))
    await act(async () => { root.render(<KnowledgebaseConnectPanel folder="Payments" isAdmin identities={[{ id: 'service_checkout', name: 'Checkout agent', type: 'service' }]} onAsk={() => {}} />) })
    const name = host.querySelector('input[placeholder="Claude Code · Payments"]') as HTMLInputElement
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'Checkout connection'); name.dispatchEvent(new Event('input', { bubbles: true })); name.dispatchEvent(new Event('change', { bubbles: true })) })
    const selects = host.querySelectorAll('select')
    await act(async () => { selects[0].value = 'write'; selects[0].dispatchEvent(new Event('change', { bubbles: true })); selects[2].value = 'service_checkout'; selects[2].dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => { host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    expect(mocks.create).toHaveBeenCalledWith({ name: 'Checkout connection', scopes: ['knowledgebase:read', 'knowledgebase:write'], workflow_ids: [], all_workflows: false, expires_in_days: 30, knowledgebase_folders: [{ folder_path: 'Payments', role: 'editor' }], knowledgebase_identity_id: 'service_checkout' })
    expect((host.querySelector('input[type="password"]') as HTMLInputElement).value).toBe('secret-for-agent')
    expect(host.querySelector('pre')?.textContent).not.toContain('secret-for-agent')
  })
})
