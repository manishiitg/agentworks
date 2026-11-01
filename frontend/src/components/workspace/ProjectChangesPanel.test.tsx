// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('./ChatFileReview', () => ({ ChatFileReview: ({ reply, toolbar }: { reply: ReplyFileChangesRef; toolbar: React.ReactNode }) => <div>{toolbar}<pre data-reference>{JSON.stringify(reply)}</pre></div> }))
vi.mock('./ProjectTurnPanel', () => ({ ProjectTurnPanel: ({ snapshotId, onBack }: { snapshotId: string; onBack: () => void }) => <div data-source={snapshotId}><button onClick={onBack}>Back</button></div> }))
import { ProjectChangesPanel } from './ProjectChangesPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-286: changing checkout/history must open that exact captured version,
// even when another checkout has the same filename and runtime chat changes.
it('keeps checkout selection and historical snapshot paired', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  get.mockResolvedValue({ data: { checkouts: [
    { id: 'main-id', root: 'app', name: 'app', captures: [{ snapshot_id: 'main-new', at: '2026-11-01T01:00:00Z', files: 3 }, { snapshot_id: 'main-old', at: '2026-11-01T00:00:00Z', files: 1, source: { label: 'Check inbox', agent_name: 'Inbox helper', can_open: true } }] },
    { id: 'feature-id', root: 'feature', name: 'feature', captures: [{ snapshot_id: 'feature-new', at: '2026-11-01T01:00:00Z', files: 2 }] },
  ] } })
  const reference = () => JSON.parse(host.querySelector('[data-reference]')!.textContent!)
  const choose = async (label: string, value: string) => act(async () => { const picker = host.querySelector<HTMLSelectElement>(`[aria-label="${label}"]`)!; picker.value = value; picker.dispatchEvent(new Event('change', { bubbles: true })) })
  try {
    await act(async () => root.render(<ProjectChangesPanel workspacePath="Crew/example" sessionId="current-chat" tabId="tab" onBrowse={() => undefined} />))
    expect(reference()).toMatchObject({ historyScope: 'project', checkoutId: 'main-id', snapshotId: 'main-new', sessionId: 'current-chat' })
    await choose('Saved project history', 'main-old')
    expect(reference().snapshotId).toBe('main-old')
    expect(host.querySelector('option[value="main-old"]')?.textContent).toContain('Check inbox · Inbox helper')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Original turn')!.click())
    expect(host.querySelector('[data-source]')?.getAttribute('data-source')).toBe('main-old')
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Back')!.click())
    expect(reference().snapshotId).toBe('main-old')
    await choose('Worktree', 'feature-id')
    expect(reference()).toMatchObject({ checkoutId: 'feature-id', snapshotId: 'feature-new' })
    expect(get).toHaveBeenCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: { workspace_path: 'Crew/example', history_scope: 'project', op: 'snapshots' } }))
  } finally { await act(async () => root.unmount()); host.remove() }
})
