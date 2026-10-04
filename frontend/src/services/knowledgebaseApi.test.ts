import { describe, expect, it, vi } from 'vitest'
const get = vi.hoisted(() => vi.fn())
vi.mock('./api', () => ({ default: { get } }))
import { knowledgebaseApi, normalizeKnowledgeAccess, normalizeKnowledgeEvents, normalizeKnowledgeSearch } from './knowledgebaseApi'

describe('Knowledge Base viewer wire contract', () => {
  it('uses permission-filtered paged items rather than an unpaged alias', async () => {
    get.mockResolvedValueOnce({ data: { items: [{ entry_id: 'visible', kind: 'entry' }, { kind: 'folder', path: 'Payments' }], entries: [{ entry_id: 'outside-page' }], next_cursor: 'next' } })
    const result = await knowledgebaseApi.entries({ folder_path: 'Payments' })
    expect(result.entries.map(entry => entry.entry_id)).toEqual(['visible'])
    expect(result.next_cursor).toBe('next')
    expect(get).toHaveBeenCalledWith('/api/knowledgebase/entries', expect.objectContaining({ params: { folder_path: 'Payments', limit: 50 } }))
  })
  it('normalizes domain roles and inherited grants', () => {
    const result = normalizeKnowledgeAccess({ folder_path: 'Payments', effective_role: 'Owner' as never, grants: [{ identity_id: 'priya', role: 'Reader' as never, folder_path: '', inherited: true }], identities: [], acl_version: 'v1' })
    expect(result.effective_role).toBe('owner')
    expect(result.grants[0]).toMatchObject({ role: 'reader', inherited: true })
  })
  it('renders domain activity attribution and keyword search snippets', () => {
    expect(normalizeKnowledgeEvents({ items: [{ id: 'event', actor: 'priya', at: '2026-10-04T01:00:00Z', action: 'update' }] })).toMatchObject([{ identity_id: 'priya', timestamp: '2026-10-04T01:00:00Z' }])
    expect(normalizeKnowledgeSearch({ items: [{ entry_id: 'entry', path: 'Payments/checkout.md', snippet: 'Checkout retries', line_number: 3 }] })).toMatchObject([{ entry: { entry_id: 'entry' }, excerpt: 'Checkout retries' }])
  })
  it('uses the actual backup status field and never requests a mutation route', async () => {
    get.mockResolvedValueOnce({ data: { configured: true, entries: [{ entry_id: 'entry', path: 'note.md', backup_status: 'backed_up' }] } })
    expect((await knowledgebaseApi.backup('')).entries[0].status).toBe('backed_up')
    expect(get).toHaveBeenLastCalledWith('/api/knowledgebase/backup', expect.objectContaining({ params: { folder_path: '' } }))
  })
})
