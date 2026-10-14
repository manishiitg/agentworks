// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, it, expect, vi } from 'vitest'
const api = vi.hoisted(() => ({ list: vi.fn(), manage: vi.fn(), history: vi.fn() }))
vi.mock('../../api/conditionTriggers', () => ({ conditionTriggersApi: api }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn() }))
import ConditionTriggersView from './ConditionTriggersView'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
describe('ConditionTriggersView', () => {
  it('shows the check, target and durable history and uses the current project for actions', async () => {
    api.list.mockResolvedValue({ triggers: [{ id: 'c1', name: 'Tagged tickets', enabled: true, script: 'code/triggers/notion.py', cron_expression: '*/5 * * * *', timezone: 'UTC', target: { kind: 'function', name: 'Triage', function: 'process_ticket' } }] })
    api.history.mockResolvedValue({ pending: 0, checks: [{ id: 'r1', status: 'success', started_at: '2026-10-14T00:00:00Z', final_response: '1 new matching events queued' }], events: [{ event: { id: 'ticket-123', payload: { ticket_id: '123' } }, status: 'dispatched', receipt: 'fn-123' }] })
    api.manage.mockResolvedValue({ checked: true })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<ConditionTriggersView scope={{ profileId: 'code', projectId: 'project-a' }} />))
    try {
      expect(host.textContent).toContain('Tagged tickets'); expect(host.textContent).toContain('process_ticket')
      const find = (label: string) => Array.from(host.querySelectorAll('button')).find(b => b.textContent === label)!
      await act(async () => { find('History').click(); await Promise.resolve() })
      expect(api.history).toHaveBeenCalledWith('project-a', 'c1'); expect(host.textContent).toContain('ticket-123'); expect(host.textContent).toContain('fn-123')
      await act(async () => { find('Check now').click(); await Promise.resolve() })
      expect(api.manage).toHaveBeenCalledWith('project-a', 'check', 'c1', undefined)
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
