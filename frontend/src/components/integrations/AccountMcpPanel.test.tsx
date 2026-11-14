// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import AccountMcpPanel from './AccountMcpPanel'
import { TooltipProvider } from '../ui/tooltip'

const fixture = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: fixture.get, post: fixture.post } }))
vi.mock('../../hooks/useMcpOffers', () => ({ useMcpOffers: () => ({ catalog: { offers: {}, freshnessDays: 30 }, refresh: () => {} }) }))
const host = document.createElement('div')
document.body.append(host)
const root = createRoot(host)
afterEach(() => { act(() => root.render(null)); fixture.get.mockReset(); fixture.post.mockReset() })
async function click(text: string, scope: ParentNode = document) {
  const button = [...scope.querySelectorAll<HTMLButtonElement>('button')].find(node => node.textContent === text || node.getAttribute('aria-label') === text)
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
