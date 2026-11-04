import { describe, expect, it } from 'vitest'
import { getClipboardText } from './clipboardText'

const clipboard = (html: string, plain = '') => ({ getData: (type: string) => type === 'text/html' ? html : plain })

describe('formatted chat paste (PLAT-746-309)', () => {
  it('preserves Slack message formatting as Markdown', () => {
    const text = getClipboardText(clipboard('<div><b>Plan</b><br><i>Today</i></div><ul><li>Ship <a href="https://example.com/pr/1">the PR</a></li><li><s>Draft</s></li></ul><blockquote>Ready to review</blockquote><pre>const done = true;\n</pre>', 'Plan\nToday\nShip the PR\nDraft'))
    expect(text).toBe('**Plan**\n*Today*\n\n- Ship [the PR](<https://example.com/pr/1>)\n- ~~Draft~~\n\n> Ready to review\n\n```\nconst done = true;\n```')
  })

  it.each([
    ['plain clipboard', '', 'npm run dev\n  --flag', 'npm run dev\n  --flag'],
    ['unformatted HTML', '<span>some text</span>', 'some text', 'some text'],
    ['styled bold', '<span style="font-weight:700">Strong</span>', 'Strong', '**Strong**'],
    ['ordered list', '<ol start="3"><li>First</li><li>Next</li></ol>', 'First\nNext', '3. First\n4. Next'],
    ['literal Markdown characters', '<b>*literal*</b>', '*literal*', '**\\*literal\\***'],
    ['unusable HTML', '<strong></strong>', 'keep my text', 'keep my text'],
  ])('%s', (_, html, plain, expected) => {
    expect(getClipboardText(clipboard(html, plain))).toBe(expected)
  })

  it('keeps pasted code fences intact', () => {
    expect(getClipboardText(clipboard('<pre>```js\nhello\n```</pre>'))).toBe('````\n```js\nhello\n```\n````')
  })
})
