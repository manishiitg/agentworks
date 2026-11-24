// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { PollingEvent } from '../../services/api-types'
import { EditMessageButton, EditResendEditor, MessageEditContext, type MessageEditActions } from './EditResendMessage'
import { changedFileNames, eventsBefore, fileChangesAfter } from '../../utils/editResend'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const fileEvent = (snapshot: string, start: number, end: number, path: string): PollingEvent => ({
  id: `file-snapshot:${snapshot}`, type: 'reply_file_changes', session_id: 's1',
  data: { data: { snapshot_id: snapshot, workspace_path: '/w', start_sequence: start, end_sequence: end, summary: { files: 1, added: 1, removed: 0, groups: [{ files: [{ path }] }] } } },
} as unknown as PollingEvent)

const events = [fileEvent('old', 1, 3, 'early.go'), fileEvent('a', 6, 9, 'src/upload.go'), fileEvent('b', 10, 14, 'README.md')]

function actions(overrides: Partial<MessageEditActions> = {}): MessageEditActions {
  return {
    events,
    preview: vi.fn(async () => ({ ok: true, later_messages: 2, provider: 'claude-code', native: true })),
    resend: vi.fn(async () => undefined),
    putInComposer: vi.fn(),
    ...overrides,
  }
}

const click = async (element: Element | null | undefined) => {
  if (!element) throw new Error('missing element')
  await act(async () => { (element as HTMLElement).click() })
}

it('lists later replies newest first and their file names', () => {
  expect(fileChangesAfter(events, 5).map(ref => ref.snapshotId)).toEqual(['b', 'a'])
  expect(changedFileNames(events, 5)).toEqual(['upload.go', 'README.md'])
  expect(fileChangesAfter(events, 20)).toEqual([])
})

it('edits in place and resends with the file revert choice', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const value = actions()
  const onClose = vi.fn()
  try {
    await act(async () => root.render(<MessageEditContext.Provider value={value}><EditResendEditor eventId="user:1" sequence={5} text="Add a retry" onClose={onClose} /></MessageEditContext.Provider>))
    expect(value.preview).toHaveBeenCalledWith('user:1')
    expect(host.textContent).toContain('The 2 messages after this one will leave the chat.')
    expect(host.textContent).toContain('Claude Code goes back to its own session at that point.')
    expect(host.textContent).toContain('upload.go, README.md · 2 replies')
    expect(host.querySelector('[data-testid="edit-resend-team-copy"]')).toBeNull()
    const box = host.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(box.checked).toBe(false)
    await click(box)
    const textarea = host.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea.value).toBe('Add a retry')
    await click(host.querySelector('[data-testid="edit-resend-send"]'))
    expect(value.resend).toHaveBeenCalledWith({ eventId: 'user:1', sequence: 5, text: 'Add a retry', revertFiles: true })
    expect(onClose).toHaveBeenCalled()
  } finally {
    await act(async () => root.unmount())
  }
})

it('keeps the edited text and offers the message box when going back fails', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const value = actions({
    preview: vi.fn(async () => ({ ok: true, later_messages: 1, provider: 'cursor-cli', native: false, team_copy: true })),
    resend: vi.fn(async () => { throw { message: 'Couldn\'t go back: the saved conversation could not be updated. Nothing was changed.', code: 'rewind_failed' } }),
  })
  try {
    await act(async () => root.render(<MessageEditContext.Provider value={value}><EditResendEditor eventId="user:1" sequence={20} text="Hi" onClose={() => undefined} /></MessageEditContext.Provider>))
    expect(host.textContent).toContain('Cursor can\'t go back on its own, so it starts fresh with the chat up to that point.')
    expect(host.querySelector('input[type="checkbox"]')).toBeNull()
    expect(host.querySelector('[data-testid="edit-resend-team-copy"]')?.textContent).toContain('not changed')
    await click(host.querySelector('[data-testid="edit-resend-send"]'))
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Nothing was changed. Your edited text is kept.')
    expect(host.querySelector('[data-testid="edit-resend-send"]')?.textContent).toBe('Try again')
    const put = [...host.querySelectorAll('button')].find(button => button.textContent === 'Put the text in the message box')
    await click(put)
    expect(value.putInComposer).toHaveBeenCalledWith('Hi')
  } finally {
    await act(async () => root.unmount())
  }
})

it('shows the server refusal instead of the editor', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const value = actions({ preview: vi.fn(async () => { throw { message: 'The agent no longer has this message, so the chat can\'t go back to it. Send it again as a new message instead.', code: 'not_in_agent' } }) })
  try {
    await act(async () => root.render(<MessageEditContext.Provider value={value}><EditResendEditor eventId="user:1" sequence={5} text="Hi" onClose={() => undefined} /></MessageEditContext.Provider>))
    expect(host.querySelector('[data-testid="edit-resend-refusal"]')?.textContent).toContain('Send it again as a new message instead.')
    expect(host.querySelector('textarea')).toBeNull()
  } finally {
    await act(async () => root.unmount())
  }
})

it('says why Edit waits while the agent replies, and shows nothing outside an editable chat', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const onEdit = vi.fn()
  try {
    await act(async () => root.render(<EditMessageButton onEdit={onEdit} />))
    expect(host.textContent).toBe('')
    await act(async () => root.render(<MessageEditContext.Provider value={actions({ disabledReason: 'Edit is available when the agent finishes replying. Press Stop to end the reply now.' })}><EditMessageButton onEdit={onEdit} /></MessageEditContext.Provider>))
    await click(host.querySelector('[data-testid="edit-sent-message"]'))
    expect(onEdit).not.toHaveBeenCalled()
    expect(host.querySelector('[role="status"]')?.textContent).toContain('Press Stop')
  } finally {
    await act(async () => root.unmount())
  }
})

it('trims the chat to just before the edited message, or asks for a reload when it is not in memory', () => {
  const events = ['a', 'b', 'user:c', 'd'].map(id => ({ id, type: 'x' } as unknown as PollingEvent))
  expect(eventsBefore(events, 'user:c')?.map(event => event.id)).toEqual(['a', 'b'])
  expect(eventsBefore(events, 'user:z')).toBeNull()
})
