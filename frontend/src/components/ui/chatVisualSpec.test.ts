import { readFileSync } from 'node:fs'
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
    // An absolute path from an attached folder is resolved by the server's asset admission (9e59b2fcf7).
    expect(compileChatVisual(image('/Users/example/image.png'))?.elements.visual.type).toBe('image')
  })
  it('rejects executable sources, file URLs and workspace traversal', () => {
    for (const src of ['javascript:alert(1)', 'file:///private/image.png', 'Crew/example/../private/image.png']) {
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

// GitHub #3528: a widget starts with an empty list and gains one row per line,
// so it grows while the reply streams instead of waiting for one long line.
const op = (path: string, value: unknown) => JSON.stringify({ op: 'add', path, value })
const streamed = (type: string, props: Record<string, unknown>, appends: [string, unknown][]) => [
  op('/root', 'visual'),
  op('/elements/visual', { type, props, children: [] }),
  ...appends.map(([list, value]) => op(`/elements/visual/props/${list}/-`, value)),
]
const oneLine = (type: string, props: Record<string, unknown>, appends: [string, unknown][]) => {
  const full: Record<string, unknown[]> = {}
  for (const [list, value] of appends) (full[list] ??= []).push(value)
  return widget(type, { ...props, ...full })
}

const cases: { name: string; type: string; props: Record<string, unknown>; appends: [string, unknown][]; firstVisible: number }[] = [
  { name: 'diagram', type: 'diagram', props: { title: 'How a request moves', direction: 'vertical', nodes: [], edges: [] }, firstVisible: 2, appends: [
    ['nodes', { id: 'ask', title: 'Person asks' }],
    ['nodes', { id: 'work', title: 'Agent works', emphasis: true }],
    ['edges', { from: 'ask', to: 'work' }],
    ['nodes', { id: 'save', title: 'Result saved' }],
    ['edges', { from: 'work', to: 'save', label: 'done' }],
  ] },
  { name: 'chart', type: 'chart', props: { title: 'Requests per day', style: 'bar', unit: 'requests', data: [] }, firstVisible: 2, appends: [
    ['data', { label: 'Mon', value: 12 }], ['data', { label: 'Tue', value: 18 }], ['data', { label: 'Wed', value: 15 }],
  ] },
  { name: 'timeline', type: 'timeline', props: { title: 'Request history', items: [] }, firstVisible: 2, appends: [
    ['items', { when: '09:00', title: 'Received' }], ['items', { when: '09:15', title: 'Assigned', detail: 'A teammate started.' }],
  ] },
  { name: 'swimlane', type: 'swimlane', props: { title: 'From request to result', lanes: [], steps: [] }, firstVisible: 4, appends: [
    ['lanes', { id: 'person', title: 'Person' }], ['lanes', { id: 'agent', title: 'AI teammate' }],
    ['steps', { id: 'ask', lane: 'person', title: 'Send request' }], ['steps', { id: 'prepare', lane: 'agent', title: 'Prepare the work' }],
  ] },
  { name: 'sequence', type: 'sequence', props: { title: 'Task handoff', participants: [], messages: [] }, firstVisible: 4, appends: [
    ['participants', { id: 'lead', title: 'Lead agent' }],
    ['participants', { id: 'worker', title: 'Worker agent' }],
    ['messages', { from: 'lead', to: 'worker', label: 'Ask for a review' }],
    ['messages', { from: 'worker', to: 'lead', label: 'Send findings', reply: true }],
  ] },
]

describe('chat visuals build up one line at a time (#3528)', () => {
  for (const { name, type, props, appends, firstVisible } of cases) {
    it(`shows a streamed ${name} after each complete line and ends equal to the one-line form`, () => {
      const lines = streamed(type, props, appends)
      for (let count = 1; count <= lines.length; count++) {
        const shown = compileChatVisual(lines.slice(0, count).join('\n'), true)
        if (count <= firstVisible) {
          expect(shown).toBeNull()
          continue
        }
        expect(shown?.elements.visual.type).toBe(type)
        // A cut final line keeps the latest complete state, never a repaired value.
        const cut = lines[count]?.slice(0, -3)
        if (cut) expect(compileChatVisual([...lines.slice(0, count), cut].join('\n'), true)).toEqual(shown)
      }
      const final = compileChatVisual(lines.join('\n'))
      expect(final).not.toBeNull()
      expect(final).toEqual(compileChatVisual(oneLine(type, props, appends)))
      expect(compileChatVisual(lines.join('\n'), true)).toEqual(final)
    })
  }

  it('keeps the last valid state while streaming, but checks the finished block strictly', () => {
    const lines = streamed('diagram', { title: 'Flow', nodes: [] }, [
      ['nodes', { id: 'a', title: 'Start' }],
      ['edges', { from: 'a', to: 'b' }],
    ])
    const partial = compileChatVisual(lines.join('\n'), true)
    expect(partial?.elements.visual.props.nodes).toEqual([{ id: 'a', title: 'Start' }])
    expect(partial?.elements.visual.props.edges).toEqual([])
    expect(compileChatVisual(lines.join('\n'))).toBeNull()
  })
})

describe('common slips that change nothing a widget may hold (#3528)', () => {
  const chart = { type: 'chart', props: { title: 'Requests', style: 'line', data: [{ label: 'Mon', value: 1 }] } }
  it('accepts a block without the root line and an element without children', () => {
    expect(compileChatVisual(op('/elements/visual', chart))?.elements.visual.type).toBe('chart')
    expect(compileChatVisual([op('/root', 'visual'), op('/elements/visual', chart)].join('\n'))).toEqual(compileChatVisual(widget('chart', chart.props)))
  })
  it('still rejects another root, other elements, unknown widgets and unknown fields', () => {
    expect(compileChatVisual([op('/root', 'page'), op('/elements/visual', chart)].join('\n'))).toBeNull()
    expect(compileChatVisual([op('/elements/visual', chart), op('/elements/other', chart)].join('\n'))).toBeNull()
    expect(compileChatVisual(op('/elements/visual', { type: 'html', props: { body: '<b>x</b>' } }))).toBeNull()
    expect(compileChatVisual(op('/elements/visual', { ...chart, props: { ...chart.props, color: 'red' } }))).toBeNull()
    expect(compileChatVisual(op('/elements/visual', { ...chart, children: ['other'] }))).toBeNull()
    expect(compileChatVisual(op('/elements/visual/props/__proto__', {}))).toBeNull()
  })
})

it('renders the sequence example shipped in the chat-visuals guide (#3882)', () => {
  const guide = readFileSync(new URL('../../../../agent_go/pkg/skills/panels/chat-visuals/SKILL.md', import.meta.url), 'utf8')
  const sequence = Array.from(guide.matchAll(/^```chat-ui\n([\s\S]*?)^```/gm), match => match[1])
    .find(source => source.includes('"type":"sequence"'))
  expect(sequence).toBeDefined()
  const lines = sequence!.trim().split('\n')
  const firstMessage = compileChatVisual(lines.slice(0, -1).join('\n'), true)
  expect(firstMessage?.elements.visual.props.participants).toEqual([
    { id: 'lead', title: 'Lead agent' },
    { id: 'worker', title: 'Worker agent' },
  ])
  expect(firstMessage?.elements.visual.props.messages).toHaveLength(1)
  const final = compileChatVisual(sequence!)
  expect(final?.elements.visual.props.messages).toHaveLength(2)
  expect(compileChatVisual(sequence!, true)).toEqual(final)
})
