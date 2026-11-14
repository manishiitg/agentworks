// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import AccountMcpPanel from './AccountMcpPanel'
import { TooltipProvider } from '../ui/tooltip'

const fixture = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), changed: undefined as undefined | (() => void) }))
vi.mock('../../services/liveFeed', () => ({ liveFeed: { subscribe: (_kinds: unknown, _scope: unknown, changed: () => void) => { fixture.changed = changed; return () => {} } } }))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, post: fixture.post } }))
vi.mock('../../hooks/useMcpOffers', () => ({ useMcpOffers: () => ({ catalog: { offers: {}, freshnessDays: 30 }, refresh: () => {} }) }))
const host = document.createElement('div')
document.body.append(host)
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); fixture.get.mockReset(); fixture.post.mockReset(); vi.restoreAllMocks() })
async function click(text: string, scope: ParentNode = document) {
  const button = [...scope.querySelectorAll<HTMLButtonElement>('button')].find(node => node.textContent === text || node.getAttribute('aria-label') === text || node.getAttribute('title') === text)
  expect(button, text).toBeTruthy()
  await act(async () => button!.click())
}
async function fill(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

// PLAT-746-977: a real Settings form, not an implicit chat, owns the mutation;
// only server-authorized rows expose edit/remove, and OAuth stays an explicit link.
it('adds and removes directly without project context and hides management for shared connections', async () => {
  vi.spyOn(window, 'open').mockReturnValue(null)
  const rows = [
    { id: 'c-00000001', label: 'My tools', provider: 'Example', status: 'active', can_manage: true, tools: [] },
    { id: 'c-00000002', label: 'Shared tools', provider: 'Example', status: 'active', can_manage: false, tools: [] },
  ]
  fixture.get.mockImplementation(async () => ({ data: { servers: [...rows], can_connect: true } }))
  fixture.post.mockImplementation(async (_path, body) => {
    if (body.operation === 'apps') return { data: { result: '{"apps":[]}' } }
    if (body.operation === 'edit_connection') rows.find(row => row.id === body.connection_id)!.label = body.label
    if (body.operation === 'remove_connection') rows.splice(rows.findIndex(row => row.id === body.connection_id), 1)
    return { data: { result: body.operation === 'connect' ? '{"auth_url":"https://auth.example.com/authorize","message":"Complete sign-in."}' : 'Removed connection.' } }
  })
  await act(async () => root.render(<TooltipProvider><AccountMcpPanel /></TooltipProvider>))
  expect(host.textContent).not.toContain('Manage in chat')
  expect(host.querySelector('[aria-label="Actions for Shared tools"]')).toBeNull()
  expect(host.querySelector('[aria-label="Actions for My tools"]')).toBeTruthy()
  await click('Actions for My tools')
  await click('Edit connection')
  const edit = document.querySelector('[role="dialog"]')!
  await fill(edit.querySelector('input')!, 'Edited tools')
  await act(async () => edit.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(fixture.post).toHaveBeenCalledWith('/api/my-vaults/op', { operation: 'edit_connection', connection_id: 'c-00000001', label: 'Edited tools' }, { skipSessionContext: true })
  await click('Add MCP server')
  const dialog = document.querySelector('[role="dialog"]')!
  const inputs = dialog.querySelectorAll('input')
  await fill(inputs[0], 'New tools')
  await fill(inputs[1], 'https://tools.example.com/mcp')
  await act(async () => dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(fixture.post).toHaveBeenCalledWith('/api/my-vaults/op', { operation: 'connect', name: 'New tools', label: 'New tools', url: 'https://tools.example.com/mcp' }, { skipSessionContext: true })
  expect(document.querySelector('[role="dialog"]')).toBeNull()
  expect(host.querySelector('a[href="https://auth.example.com/authorize"]')).toBeTruthy()
  await click('Actions for Edited tools')
  await click('Remove connection')
  const confirmation = document.querySelector('[role="dialog"]')!
  await click('Remove connection', confirmation)
  expect(fixture.post).toHaveBeenCalledWith('/api/my-vaults/op', { operation: 'remove_connection', connection_id: 'c-00000001' }, { skipSessionContext: true })
  expect(host.querySelector('[aria-label="Edited tools server"]')).toBeNull()
  expect(host.querySelector('[aria-label="Shared tools server"]')).toBeTruthy()
})

// Reported failure: catalog Connect opened a generic Save form and an already
// connected public server offered a no-op Sign in. Exercise the actual button.
it('connects catalog tools in one request and completes OAuth on return without a second test click', async () => {
  const rows: Array<{id: string; label: string; provider: string; status: string; can_manage: boolean; tools: unknown[]}> = []
  let finish!: (value: unknown) => void
  const opened = vi.spyOn(window, 'open').mockReturnValue(null)
  fixture.get.mockImplementation(async () => ({ data: { servers: [...rows], can_connect: true } }))
  fixture.post.mockImplementation(async (_path, body) => {
    if (body.operation === 'apps') return { data: { result: JSON.stringify({ apps: [{ name: 'Public search', oauth: false }, { name: 'Account tools', oauth: true }] }) } }
    if (body.provider === 'Public search') return new Promise(resolve => { finish = resolve })
    return { data: { result: JSON.stringify({ connection_id: 'c-00000002', auth_url: 'https://auth.example.com/authorize', message: 'Finish signing in.' }) } }
  })
  await act(async () => root.render(<TooltipProvider><AccountMcpPanel /></TooltipProvider>))
  await click('Available MCPs')
  const available = () => [...host.querySelectorAll('section[aria-label="Available servers"] button')].filter(node => node.textContent === 'Connect')
  await act(async () => (available().at(-1) as HTMLButtonElement).click())
  expect(document.querySelector('[role="dialog"]')).toBeNull()
  expect(host.textContent).toContain('Connecting to Public search')
  expect(fixture.post).toHaveBeenCalledWith('/api/my-vaults/op', { operation: 'connect', provider: 'Public search', label: 'Public search' }, { skipSessionContext: true })
  rows.push({ id: 'c-00000001', label: 'Public search', provider: 'Public search', status: 'active', can_manage: true, tools: [{ name: 'public__search', input_schema: {} }] })
  await act(async () => finish({ data: { result: JSON.stringify({ connected: true, connection_id: 'c-00000001', message: 'Public search connected · 1 tools available.' }) } }))
  expect(host.textContent).toContain('Public search connected')
  expect(opened).not.toHaveBeenCalled()
  expect(host.querySelector('section[aria-label="Available servers"]')).toBeNull()
  expect(host.querySelector('[aria-label="Public search server"]')?.getAttribute('data-selected')).toBe('true')
  expect(host.textContent).toContain('No sign-in required')
  await click('Actions for Public search')
  expect([...document.querySelectorAll('button')].some(button => button.textContent === 'Sign in')).toBe(false)
  // Close the action popup before changing tabs.
  await click('Actions for Public search')
  await click('Available MCPs')
  await click('View connection')
  expect(fixture.post.mock.calls.filter(([, body]) => body.operation !== 'apps')).toHaveLength(1)
  await click('Available MCPs')
  await act(async () => (available()[0] as HTMLButtonElement).click())
  expect(opened).toHaveBeenCalledWith('https://auth.example.com/authorize', '_blank', 'noopener,noreferrer')
  expect(host.querySelector('a[href="https://auth.example.com/authorize"]')).toBeTruthy()
  rows.push({ id: 'c-00000002', label: 'Account tools', provider: 'Account tools', status: 'active', can_manage: true, tools: [{ name: 'account__search', input_schema: {} }] })
  await act(async () => fixture.changed?.())
  expect(host.textContent).toContain('Account tools connected')
  expect(host.querySelector('a[href="https://auth.example.com/authorize"]')).toBeNull()
  expect(fixture.post.mock.calls.filter(([, body]) => body.operation !== 'apps')).toHaveLength(2)
})
