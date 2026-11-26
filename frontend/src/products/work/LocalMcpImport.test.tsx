// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const post = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { get: vi.fn(), post } }))
import { LocalMcpImport, type LocalMcpServer } from './LocalMcpImport'

const servers: LocalMcpServer[] = [
  { id: 'a1', name: 'posthog', sources: ['Claude'], scope: 'project', kind: 'url', program: 'mcp.example.com', keys: ['Authorization'], importable: true },
  { id: 'b2', name: 'notes', sources: ['Codex', 'agy'], scope: 'all', kind: 'command', program: 'npx', importable: true, in_vault: true },
  { id: 'c3', name: 'missing', sources: ['Cursor'], scope: 'all', kind: 'command', program: 'tool', importable: false, note: 'Program tool is not installed on this Mac.' },
]

it('picks new importable servers, explains keys and Mac-only programs, and imports by ID only', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  post.mockResolvedValue({ data: { results: [{ id: 'a1', name: 'posthog', status: 'needs_sign_in', message: 'Saved. Sign in to finish.', auth_url: 'https://example.com/auth' }] } })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const onImported = vi.fn()
  await act(async () => root.render(<LocalMcpImport servers={servers} scope={{ projectId: 'p1' }} onImported={onImported} />))
  const box = (name: string) => host.querySelector<HTMLInputElement>(`input[aria-label="Import ${name}"]`)!
  expect(box('posthog').checked).toBe(true)
  expect(box('notes').checked).toBe(false)
  expect(box('missing').disabled).toBe(true)
  expect(host.textContent).toContain('runs npx on this Mac')
  expect(host.textContent).toContain('Keys Authorization move into your Vault, encrypted.')
  expect(host.textContent).toContain('already in your Vault')
  const button = [...host.querySelectorAll('button')].find(b => b.textContent?.startsWith('Import'))!
  await act(async () => { button.click() })
  expect(post).toHaveBeenCalledWith('/api/vault/local-mcp', { project_id: 'p1', ids: ['a1'] })
  expect(onImported).toHaveBeenCalled()
  expect(host.querySelector('a[href="https://example.com/auth"]')?.textContent).toBe('Sign in')
  act(() => root.unmount())
})
