import { describe, expect, it } from 'vitest'
import { compileChatVisual } from './chatVisualSpec'

const image = (src: string) => [
  JSON.stringify({ op: 'add', path: '/root', value: 'visual' }),
  JSON.stringify({ op: 'add', path: '/elements/visual', value: { type: 'image', props: { src }, children: [] } }),
].join('\n')

describe('inline image source validation (PLAT-746)', () => {
  it('accepts an existing-format workspace path without a card title, or an HTTP(S) image', () => {
    expect(compileChatVisual(image('Crew/example/assets/screenshot.png'))?.elements.visual.type).toBe('image')
    expect(compileChatVisual(image('https://example.com/image.png'))?.elements.visual.type).toBe('image')
  })
  it('rejects executable sources, absolute local paths and workspace traversal', () => {
    for (const src of ['javascript:alert(1)', 'file:///private/image.png', '/Users/example/image.png', 'Crew/example/../private/image.png']) {
      expect(compileChatVisual(image(src))).toBeNull()
    }
  })
})
