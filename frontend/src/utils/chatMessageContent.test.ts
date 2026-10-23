import { describe, expect, it } from 'vitest'
import { getDisplaySafeUserMessageContent, getUserMessageAttachmentPaths, isImageAttachmentPath } from './chatMessageContent'

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
  it('labels Autopilot automatic turns and leaves plain messages alone', () => {
    expect(getDisplaySafeUserMessageContent('whats is your purpose')).toBe('whats is your purpose')
    expect(getDisplaySafeUserMessageContent('PULSE TURN: daily goal check, 2026-10-08.\n\nPULSE DAILY GOAL CHECK...')).toBe('Autopilot: daily goal check')
  })
})

describe('getUserMessageAttachmentPaths', () => {
  it('lists the attached files from the stored request context, once each', () => {
    expect(getUserMessageAttachmentPaths('what is this?\n\n📁 Files in context: Chats/Work/p/uploads/a.png, Chats/Work/p/uploads/b.pdf, Chats/Work/p/uploads/a.png'))
      .toEqual(['Chats/Work/p/uploads/a.png', 'Chats/Work/p/uploads/b.pdf'])
    expect(getUserMessageAttachmentPaths('just text')).toEqual([])
    expect(isImageAttachmentPath('x/Screenshot 1.PNG')).toBe(true)
    expect(isImageAttachmentPath('x/report.pdf')).toBe(false)
  })
})
