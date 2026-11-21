import { describe, expect, it } from 'vitest'
import { quoteBlock, withQuote } from './ReplyQuote'

describe('ReplyQuote', () => {
  it('turns the selection into a Markdown quote', () => {
    expect(quoteBlock('  first line\n\n\n\nsecond\r\nthird ')).toBe('> first line\n>\n> second\n> third')
  })
  it('adds quotes below the draft and leaves a line for the question', () => {
    const one = withQuote('', 'alpha')
    expect(one).toBe('> alpha\n\n')
    expect(withQuote(`${one}why?  `, 'beta')).toBe('> alpha\n\nwhy?\n\n> beta\n\n')
  })
  it('bounds a very long selection', () => {
    expect(quoteBlock('x'.repeat(5000)).length).toBeLessThanOrEqual(2002)
  })
})
