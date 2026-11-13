import { describe, expect, it } from 'vitest'
import { hasChatSubmissionContent } from './chatSubmissionTarget'

describe('attachment-only submission (PLAT-746-938)', () => {
  it('accepts text, a committed file or a prepared upload, but not an empty draft', () => {
    expect(hasChatSubmissionContent('  ')).toBe(false)
    expect(hasChatSubmissionContent('hello')).toBe(true)
    expect(hasChatSubmissionContent('', [{ path: 'Crew/example/uploads/image.png', name: 'image.png', type: 'file' }])).toBe(true)
    expect(hasChatSubmissionContent('  ', [], [{ hash: 'a'.repeat(64), name: 'image.png' }])).toBe(true)
    expect(hasChatSubmissionContent('', [], [])).toBe(false)
  })
})
