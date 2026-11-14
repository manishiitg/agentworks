import { describe, expect, it } from 'vitest'
import { hasChatSubmissionContent, submittedChatAttachmentReferences } from './chatSubmissionTarget'

describe('attachment-only submission (PLAT-746-938)', () => {
  it('accepts text, a committed file or a prepared upload, but not an empty draft', () => {
    expect(hasChatSubmissionContent('  ')).toBe(false)
    expect(hasChatSubmissionContent('hello')).toBe(true)
    expect(hasChatSubmissionContent('', [{ path: 'Crew/example/uploads/image.png', name: 'image.png', type: 'file' }])).toBe(true)
    expect(hasChatSubmissionContent('  ', [], [{ hash: 'a'.repeat(64), name: 'image.png' }])).toBe(true)
    expect(hasChatSubmissionContent('', [], [])).toBe(false)
  })
})

// PLAT-746-1000: finishing an upload must not discard its admitted identity.
it('sends committed and in-progress attachment references once, never typed paths', () => {
  const ref = { hash: 'a'.repeat(64), name: 'image.png' }
  const plain = { path: 'Crew/example/uploads/image.png', name: 'image.png', type: 'file' as const }
  expect(submittedChatAttachmentReferences([plain])).toEqual([])
  expect(submittedChatAttachmentReferences([{ ...plain, admittedAttachment: ref }], [ref])).toEqual([ref])
})
