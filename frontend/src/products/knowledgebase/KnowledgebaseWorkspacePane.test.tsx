// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ entries: vi.fn(), read: vi.fn(), access: vi.fn(), folders: vi.fn(), workspaceRead: vi.fn(), workspaceGit: vi.fn(), gitGet: vi.fn(), gitPost: vi.fn() }))
vi.mock('../../services/knowledgebaseApi', async original => ({ ...await original<typeof import('../../services/knowledgebaseApi')>(), knowledgebaseApi: mocks }))
vi.mock('../../services/api', () => ({ default: { get:mocks.gitGet, post:mocks.gitPost }, agentApi: { getPlannerFileContent: mocks.workspaceRead }, workspaceApi: {}, authApi: {}, getApiBaseUrl: () => 'https://knowledge.example' }))
vi.mock('../../services/workspaceGit', () => ({ workspaceGitApi: { status: mocks.workspaceGit } }))
vi.mock('../../components/Workspace', () => ({ default: () => <div>Ordinary workspace</div> }))
import { KnowledgebaseWorkspacePane } from './KnowledgebaseWorkspacePane'
import type { KnowledgeEntry } from '../../services/knowledgebaseApi'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const entry: KnowledgeEntry = { entry_id: 'entry', path: 'Payments/checkout.md', folder_path: 'Payments', filename: 'checkout.md', title: 'Checkout skill', type: 'skill', description: 'Payments procedure', tags: ['checkout'], version: 'v1', updated_at: '2026-10-04T01:00:00Z', updated_by: 'priya' }
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.clearAllMocks(); document.body.innerHTML = '' })
beforeEach(() => {
  mocks.gitGet.mockResolvedValue({data:{repos:[],writable:false}})
  mocks.gitPost.mockResolvedValue({data:{ok:true}})
  mocks.folders.mockImplementation(async path => ({ folders: path === '' ? [{ path: 'Payments', name: 'Payments' }] : [] }))
  mocks.entries.mockImplementation(async () => ({ entries: [entry] }))
  mocks.read.mockResolvedValue({ entry, content: '# Procedure\n\nRead-only instructions.\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n![remote](https://tracker.example/image)', version: 'v1' })
  mocks.access.mockResolvedValue({ folder_path: 'Payments', effective_role: 'owner', grants: [{ identity_id: 'priya', role: 'reader', folder_path: '', inherited: true }], identities: [{ id: 'priya', name: 'Priya', type: 'user' }], acl_version: 'v1' })
})
async function mount() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanups.push(() => act(() => root.unmount()))
  const onAsk = vi.fn(); const onFolder = vi.fn()
  const render = async (props: Partial<React.ComponentProps<typeof KnowledgebaseWorkspacePane>> = {}) => { await act(async () => { root.render(<KnowledgebaseWorkspacePane view="library" folder="Payments" onFolder={onFolder} onAsk={onAsk} revision={0} {...props} />) }) }
  await render()
  return { host, render, onAsk, onFolder }
}
async function openEntry(host: HTMLElement) {
  await act(async () => { [...host.querySelectorAll('[data-filepath]')].find(item => item.textContent?.includes('Payments'))!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
  await act(async () => { [...host.querySelectorAll('[data-filepath]')].find(item => item.textContent?.includes('checkout.md'))!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
}
describe('Brain shared Files view', () => {
  it('uses the shared explorer and viewer while fetching only permission-checked KB content', async () => {
    const { host } = await mount()
    expect(host.textContent).toContain('Explorer')
    expect(host.querySelector('[aria-label="Search files"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Filter knowledge tag"]')).toBeNull()
    expect(host.textContent).not.toContain('Knowledge library')
    await openEntry(host)
    expect(host.querySelector('[aria-label="Open files"]')?.textContent).toContain('checkout.md')
    expect(host.textContent).toContain('Read-only instructions.')
    expect(host.textContent).toContain('priya')
    expect(host.querySelector('textarea')).toBeNull()
    expect(host.querySelector('[contenteditable]')).toBeNull()
    expect(host.querySelector('script')).toBeNull()
    expect(host.querySelector('img')).toBeNull()
    expect(host.querySelector('a[href^="javascript:"]')).toBeNull()
    expect(mocks.workspaceRead).not.toHaveBeenCalled()
    expect(mocks.workspaceGit).not.toHaveBeenCalled()
    expect([...host.querySelectorAll('button')].some(button => /^(Upload|Create folder|Edit|Save|Commit|Push)$/.test(button.textContent || ''))).toBe(false)
    expect(host.textContent).not.toContain('Backup not configured')
  })
  it('removes displayed content and its tab when a refresh discovers revoked access', async () => {
    const { host, render } = await mount(); await openEntry(host)
    mocks.read.mockRejectedValue(new Error('Resource not found.'))
    await render({ revision: 1 })
    expect(host.textContent).not.toContain('Read-only instructions.')
    expect(host.querySelector('[aria-label="Open files"]')).toBeNull()
    expect(host.textContent).toContain('Resource not found')
  })
  it('uses the shared Ask AI behavior for Folder access in the Files header', async () => {
    const { host, onAsk } = await mount()
    const button = [...host.querySelectorAll('button')].find(button => button.textContent === 'Folder access')!
    await act(async () => { button.click() })
    expect(button.textContent).toContain('Sure?')
    expect(onAsk).not.toHaveBeenCalled()
    await new Promise(resolve => setTimeout(resolve, 650))
    await act(async () => { button.click() })
    expect(onAsk).toHaveBeenCalledOnce()
  })
  it('shows Models without fetching hidden files or folder access', async () => {
    const { host, render } = await mount()
    mocks.access.mockClear(); mocks.folders.mockClear(); mocks.entries.mockClear()
    await render({ view: 'models', modelSettings: <div>Shared model settings</div> })
    expect(host.textContent).toContain('Shared model settings')
    expect(mocks.access).not.toHaveBeenCalled(); expect(mocks.folders).not.toHaveBeenCalled(); expect(mocks.entries).not.toHaveBeenCalled()
  })
})
