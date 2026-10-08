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

  // Pulse's automatic turns show a short label; a plain message stays as typed (2026-10-08).
  it('labels Pulse automatic turns and leaves plain messages alone', () => {
    expect(getDisplaySafeUserMessageContent('whats is your purpose')).toBe('whats is your purpose')
    expect(getDisplaySafeUserMessageContent('PULSE TURN: daily goal check, 2026-10-08.\n\nPULSE DAILY GOAL CHECK...')).toBe('Pulse: daily goal check')
  })
})
