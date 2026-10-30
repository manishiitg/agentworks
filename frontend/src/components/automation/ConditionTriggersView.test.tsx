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
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')?.click() })
    try {
      expect(host.textContent).toContain('Tagged tickets'); expect(host.textContent).toContain('process_ticket')
      const find = (label: string) => Array.from(host.querySelectorAll('button')).find(b => b.textContent === label)!
      await act(async () => { find('History').click(); await Promise.resolve() })
      expect(api.history).toHaveBeenCalledWith('project-a', 'c1', 'code'); expect(host.textContent).toContain('ticket-123'); expect(host.textContent).toContain('fn-123')
      await act(async () => { find('Check now').click(); await Promise.resolve() })
      expect(api.manage).toHaveBeenCalledWith('project-a', 'check', 'c1', undefined, undefined, 'code')
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('shows why "Edit in chat" failed instead of doing nothing', async () => {
    api.list.mockResolvedValue({ triggers: [{ id: 'c1', name: 'Tagged tickets', enabled: true, script: 'code/triggers/n.py', cron_expression: '*/5 * * * *', timezone: 'UTC', target: { kind: 'agent' } }] })
    const onAsk = vi.fn().mockRejectedValue(new Error('This project chat is not ready.'))
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<ConditionTriggersView scope={{ profileId: 'code', projectId: 'project-a' }} onAsk={onAsk} />))
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')?.click() })
    try {
      const edit = Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Edit in chat')!
      await act(async () => { edit.click(); await Promise.resolve(); await Promise.resolve() })
      expect(onAsk).toHaveBeenCalled()
      expect(host.querySelector('[role="alert"]')?.textContent).toContain('This project chat is not ready.')
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('lets the person pick the chat a watcher sends to: main, an open tab, or a new chat for each event', async () => {
    api.list.mockResolvedValue({ triggers: [{ id: 'c1', name: 'Tagged tickets', enabled: true, revision: 'r1', script: 'code/triggers/n.py', cron_expression: '*/5 * * * *', timezone: 'UTC', target: { kind: 'agent', message: 'go', chat_id: '373a2ae1' } }] })
    api.manage.mockResolvedValue({})
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<ConditionTriggersView scope={{ profileId: 'code', projectId: 'project-a' }} />))
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')?.click() })
    try {
      const select = host.querySelector('select[aria-label="Chat for Tagged tickets"]') as HTMLSelectElement
      expect(select.value).toBe('373a2ae1')
      expect(Array.from(select.options).map(option => option.value)).toEqual(['main', '373a2ae1', 'new'])
      await act(async () => { select.value = 'new'; select.dispatchEvent(new Event('change', { bubbles: true })); await Promise.resolve() })
      expect(api.manage).toHaveBeenCalledWith('project-a', 'update', 'c1', { target: { kind: 'agent', message: 'go', chat_id: 'new' } }, 'r1', 'code')
      await act(async () => { select.value = 'main'; select.dispatchEvent(new Event('change', { bubbles: true })); await Promise.resolve() })
      expect(api.manage).toHaveBeenLastCalledWith('project-a', 'update', 'c1', { target: { kind: 'agent', message: 'go', chat_id: '' } }, 'r1', 'code')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('saves One time and removes a finished wait when the open page refreshes', async () => {
    vi.useFakeTimers()
    const visible = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    const watcher = { id: 'c1', name: 'One ticket', enabled: true, revision: 'r1', script: 'code/triggers/n.py', cron_expression: '*/5 * * * *', timezone: 'UTC', target: { kind: 'agent' } }
    api.list.mockResolvedValueOnce({ triggers: [watcher] }).mockResolvedValueOnce({ triggers: [{ ...watcher, once: true }] }).mockResolvedValue({ triggers: [] })
    api.manage.mockResolvedValue({})
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<ConditionTriggersView scope={{ profileId: 'code', projectId: 'project-a' }} />))
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')?.click() })
    try {
      const select = host.querySelector('select[aria-label="Mode for One ticket"]') as HTMLSelectElement
      await act(async () => { select.value = 'once'; select.dispatchEvent(new Event('change', { bubbles: true })); await Promise.resolve() })
      expect(api.manage).toHaveBeenCalledWith('project-a', 'update', 'c1', { once: true }, 'r1', 'code')
      expect(host.textContent).toContain('One ticket')
      await act(async () => { await vi.advanceTimersByTimeAsync(30000) })
      expect(host.textContent).not.toContain('One ticket')
      expect(host.textContent).toContain('No watchers yet')
    } finally { await act(async () => root.unmount()); host.remove(); visible.mockRestore(); vi.useRealTimers() }
  })

  it('uses the Flow source for listing and creation guidance', async () => {
    api.list.mockResolvedValue({ triggers: [], callers: [{ owner_id: 'person', id: 'w1', name: 'Ticket ready', enabled: true, target: { kind: 'function', name: 'Relay', function: 'process' }, last_dispatch_at: '2026-10-14T00:00:00Z' }] })
    const onAsk = vi.fn().mockResolvedValue(undefined)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<ConditionTriggersView scope={{ profileId: 'relays', projectId: 'relay-a' }} onAsk={onAsk} />))
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')?.click() })
    try {
      expect(api.list).toHaveBeenCalledWith('relay-a', 'relays')
      expect(host.textContent).toContain('Watchers calling this product')
      expect(host.textContent).toContain('person')
      await act(async () => { Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Add in chat')!.click(); await Promise.resolve() })
      expect(onAsk).toHaveBeenCalledWith(expect.stringContaining('defaulting to this Flow'))
      expect(onAsk).toHaveBeenCalledWith(expect.stringContaining('script_content'))
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('shows a saved destination tab name even when the tab is not open', async () => {
    api.list.mockResolvedValue({ triggers: [{ id: 'c1', name: 'One ticket', enabled: false, check_mode: 'inbox', email_source: 'personal_gmail', script: 'code/triggers/check.py', cron_expression: '*/5 * * * *', timezone: 'UTC', target: { kind: 'agent', chat_id: 'ticket-chat' } }], chats: [{ id: 'ticket-chat', name: 'Ticket review' }] })
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<ConditionTriggersView scope={{ profileId: 'code', projectId: 'project-a' }} />))
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')?.click() })
    try {
      expect(host.textContent).toContain('Crew chat · Ticket review')
      expect(host.textContent).toContain('Your Gmail · new received mail')
      expect(host.textContent).toContain('Your mailbox stays private.')
      const select = host.querySelector('select[aria-label="Chat for One ticket"]') as HTMLSelectElement
      expect(select.selectedOptions[0]?.textContent).toBe('Ticket review')
      expect(select.value).toBe('ticket-chat')
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('shows the event connection and routes event setup to Builder chat', async () => {
    api.list.mockResolvedValue({ triggers: [{ id: 'c1', name: 'Ticket ready', enabled: true, check_mode: 'event', event_path: '/api/hooks/product/example', event_secret_name: 'WAKE_KEY', script: 'code/triggers/check.py', target: { kind: 'agent' } }] })
    const onAsk = vi.fn().mockResolvedValue(undefined)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<ConditionTriggersView scope={{ profileId: 'code', projectId: 'project-a' }} onAsk={onAsk} />))
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')?.click() })
    try {
      expect(host.textContent).toContain('When an event arrives')
      expect(host.textContent).toContain('/api/hooks/product/example')
      expect(host.textContent).toContain('Secret: WAKE_KEY')
      await act(async () => { Array.from(host.querySelectorAll('button')).find(b => b.textContent === 'Edit event connection')!.click(); await Promise.resolve() })
      expect(onAsk).toHaveBeenCalledWith(expect.stringContaining('Configure event wakeups for watcher c1'))
    } finally { await act(async () => root.unmount()); host.remove() }
  })

})
