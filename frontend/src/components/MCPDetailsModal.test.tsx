// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const get = vi.hoisted(() => vi.fn(async () => ({ data: { servers: [{ id: 'shared', label: 'Company MCP', provider: 'test', tools: [] }] } })))
vi.mock('../services/api', () => ({ default: { get } }))
vi.mock('./OAuthStatusBadge', () => ({ OAuthStatusBadge: () => null }))
vi.mock('./MCPToolApiTester', () => ({ default: () => null }))
vi.mock('./ui/MarkdownRenderer', () => ({ MarkdownRenderer: () => null }))
vi.mock('../stores', () => ({ useMCPStore: () => ({ toolList: [], getServerGroups: () => ({}) }) }))
import MCPDetailsModal from './MCPDetailsModal'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('lists permitted Vault MCPs as available automatically, with nothing to tick, and keeps the chat selection', async () => {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const save = vi.fn()
  function Harness() {
    const [selected, setSelected] = useState(['my_private'])
    return <MCPDetailsModal onClose={() => {}} onOpenConfigEditor={() => {}} selectedServers={selected} onSelectedServersChange={next => { save(next); setSelected(next) }} />
  }
  try {
    await act(async () => root.render(<Harness />))
    expect(host.textContent).toContain('private to you')
    expect(get).toHaveBeenCalledWith('/api/me/mcp/vault')
    expect(host.textContent).toContain('Company MCP')
    expect(host.textContent).toContain('Available automatically')
    expect(host.querySelector('input[aria-label="Use Company MCP from Vault"]')).toBeNull()
    expect(save).not.toHaveBeenCalled()
  } finally { act(() => root.unmount()); host.remove() }
})
