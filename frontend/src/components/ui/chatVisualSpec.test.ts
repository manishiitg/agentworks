import { describe, expect, it } from 'vitest'
import { compileChatVisual } from './chatVisualSpec'

const widget = (type: string, props: unknown) => [
  JSON.stringify({ op: 'add', path: '/root', value: 'visual' }),
  JSON.stringify({ op: 'add', path: '/elements/visual', value: { type, props, children: [] } }),
].join('\n')
const image = (src: string) => widget('image', { src })

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


describe('coding summaries preserve reported evidence (PLAT-746)', () => {
  it('requires an explicit check state and never fills unknown line counts with zero', () => {
    const changes = compileChatVisual(widget('file_changes', { title: 'Changed files', files: [{ path: 'src/example.ts' }] }))
    expect(changes?.elements.visual.props.files).toEqual([{ path: 'src/example.ts' }])
    expect(compileChatVisual(widget('checks', { title: 'Checks', checks: [{ name: 'Build', status: 'not_run' }] }))?.elements.visual.props.checks).toEqual([{ name: 'Build', status: 'not_run' }])
    expect(compileChatVisual(widget('checks', { title: 'Checks', checks: [{ name: 'Build' }] }))).toBeNull()
  })
  it('rejects executable and traversing links and impossible line counts', () => {
    for (const href of ['javascript:alert(1)', 'Crew/example/../private/log.txt']) {
      expect(compileChatVisual(widget('checks', { title: 'Checks', checks: [{ name: 'Build', status: 'failed', href }] }))).toBeNull()
      expect(compileChatVisual(widget('file_changes', { title: 'Files', files: [{ path: 'src/example.ts', href }] }))).toBeNull()
    }
    expect(compileChatVisual(widget('file_changes', { title: 'Files', files: [{ path: 'src/example.ts', added: -1 }] }))).toBeNull()
    expect(compileChatVisual(widget('file_changes', { title: 'Files', files: [{ path: 'src/example.ts', added: 1, removed: 0, href: 'https://example.com/changes' }] }))).not.toBeNull()
  })
})
