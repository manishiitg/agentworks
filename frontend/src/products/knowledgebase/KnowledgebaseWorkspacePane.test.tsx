// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ entries: vi.fn(), read: vi.fn(), search: vi.fn(), access: vi.fn(), backup: vi.fn(), folders: vi.fn() }))
vi.mock('../../services/knowledgebaseApi', async original => ({ ...await original<typeof import('../../services/knowledgebaseApi')>(), knowledgebaseApi: mocks }))
vi.mock('../../services/api', () => ({ default: {}, authApi: { listAccessTokens: async () => ({ tokens: [] }) }, getApiBaseUrl: () => 'https://knowledge.example' }))
import { KnowledgebaseWorkspacePane } from './KnowledgebaseWorkspacePane'
import type { KnowledgeEntry } from '../../services/knowledgebaseApi'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const entry: KnowledgeEntry = { entry_id: 'entry', path: 'Payments/checkout.md', folder_path: 'Payments', filename: 'checkout.md', title: 'Checkout skill', type: 'skill', description: 'Payments procedure', tags: ['checkout'], version: 'v1', updated_at: '2026-10-04T01:00:00Z', updated_by: 'priya' }
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.clearAllMocks(); document.body.innerHTML = '' })
beforeEach(() => {
  mocks.folders.mockResolvedValue({ folders: [] })
  mocks.entries.mockResolvedValue({ entries: [entry] })
  mocks.read.mockResolvedValue({ entry, content: '# Procedure\n\nRead-only instructions.\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n![remote](https://tracker.example/image)', version: 'v1' })
  mocks.backup.mockResolvedValue({ configured: true, entries: [{ entry_id: 'entry', status: 'backed_up' }] })
  mocks.access.mockResolvedValue({ folder_path: 'Payments', effective_role: 'owner', grants: [{ identity_id: 'priya', role: 'reader', folder_path: '', inherited: true }], identities: [{ id: 'priya', name: 'Priya', type: 'user' }], acl_version: 'v1' })
})
async function mount() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanups.push(() => act(() => root.unmount()))
  const onAsk = vi.fn()
  const render = async (props: Partial<React.ComponentProps<typeof KnowledgebaseWorkspacePane>> = {}) => { await act(async () => { root.render(<KnowledgebaseWorkspacePane view="library" folder="Payments" onFolder={() => {}} onAsk={onAsk} revision={0} {...props} />) }) }
  await render()
  return { host, render, onAsk }
}
describe('Knowledge Base workspace', () => {
  it('shows a backup failure while content remains readable and clears it after recovery', async () => {
    mocks.backup.mockResolvedValue({ configured: true, entries: [{ entry_id: 'entry', status: 'pending' }], last_backup_error: 'Push failed before remote publication.' })
    const { host, render } = await mount()
    expect(host.querySelector('[role="status"]')?.textContent).toContain('Push failed before remote publication.')
    await act(async () => { [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Checkout skill'))!.click() })
    expect(host.textContent).toContain('Read-only instructions.')
    expect(host.querySelector('[role="status"]')?.textContent).toContain('Git backup needs attention')
    mocks.backup.mockResolvedValue({ configured: true, entries: [{ entry_id: 'entry', status: 'backed_up' }], last_backup_error: '' })
    await render({ revision: 1 })
    expect(host.querySelector('[role="status"]')).toBeNull()
    expect(host.textContent).toContain('Read-only instructions.')
    expect(host.textContent).toContain('Backed up')
  })
  it('reads saved content with attribution and backup status, without content or Git controls', async () => {
    const { host } = await mount()
    await act(async () => { [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Checkout skill'))!.click() })
    expect(host.textContent).toContain('Read-only instructions.')
    expect(host.textContent).toContain('priya')
    expect(host.textContent).toContain('Backed up')
    expect(host.querySelector('textarea')).toBeNull()
    expect(host.querySelector('[contenteditable]')).toBeNull()
    expect(host.querySelector('script')).toBeNull()
    expect(host.querySelector('img')).toBeNull()
    expect(host.querySelector('a[href^="javascript:"]')).toBeNull()
    expect([...host.querySelectorAll('button')].some(button => /^(Edit|Save|Commit|Push)$/.test(button.textContent || ''))).toBe(false)
  })
  it('removes visible content when a refresh discovers revoked access', async () => {
    const { host, render } = await mount()
    await act(async () => { [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Checkout skill'))!.click() })
    mocks.entries.mockRejectedValue(new Error('Resource not found.'))
    mocks.read.mockRejectedValue(new Error('Resource not found.'))
    await render({ revision: 1 })
    expect(host.textContent).not.toContain('Read-only instructions.')
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Resource not found')
  })
  it('shows model settings without folder navigation or access fetches', async () => {
    const { host, render } = await mount()
    mocks.access.mockClear(); mocks.folders.mockClear(); mocks.entries.mockClear()
    await render({ view: 'models', modelSettings: <div>Shared model settings</div> })
    expect(host.textContent).toContain('Shared model settings')
    expect(host.textContent).not.toContain('Organization')
    expect(mocks.access).not.toHaveBeenCalled()
    expect(mocks.folders).not.toHaveBeenCalled()
    expect(mocks.entries).not.toHaveBeenCalled()
  })
  it('labels inherited grants and sends access management to the chat', async () => {
    const { host, render, onAsk } = await mount()
    await render({ view: 'access' })
    expect(host.textContent).toContain('Priya')
    expect(host.textContent).toContain('Inherited')
    expect(host.querySelector('table')?.textContent).toContain('Organization root')
    await act(async () => { [...host.querySelectorAll('button')].find(button => button.textContent === 'Manage access in chat')!.click() })
    expect(onAsk).toHaveBeenCalledOnce()
    expect(host.querySelector('select')).toBeNull()
  })
})
