import { describe, expect, it } from 'vitest'
import { getDisplaySafeUserMessageContent } from './chatMessageContent'

describe('getDisplaySafeUserMessageContent', () => {
  it('keeps typed text and hides attached-file transport context', () => {
    expect(getDisplaySafeUserMessageContent(
      'check this file\n\n📁 Files in context: Chats/Work/project/uploads/report.zip',
    )).toBe('check this file')
  })

  it('hides restored-conversation transport context', () => {
    expect(getDisplaySafeUserMessageContent(
      'continue here\n\nPrevious workflow-builder conversation file: Chats/Work/history.json',
    )).toBe('continue here')
  })

  it('keeps ordinary user text unchanged', () => {
    expect(getDisplaySafeUserMessageContent('Explain the files in context')).toBe('Explain the files in context')
  })

  // The owner typed "whats is your purpose" in the Pulse tab and the chat showed pages of turn instructions (2026-10-08).
  it('shows only the words of a Pulse turn, or a short label for automatic turns', () => {
    const owner = 'PULSE TURN: a message from user in the Pulse tab, 2026-10-08, workspace_path="Workflow/substack":\n\n--- message ---\nwhats is your purpose\n--- end of message ---\n\nReply briefly and plainly. Your permission levels...'
    expect(getDisplaySafeUserMessageContent(owner)).toBe('whats is your purpose')
    expect(getDisplaySafeUserMessageContent('PULSE. You are the substack Pulse...\n\nPULSE TURN: daily goal check, 2026-10-08, ...')).toBe('Pulse: daily goal check')
  })
})
