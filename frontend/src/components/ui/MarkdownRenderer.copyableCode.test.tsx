// @vitest-environment happy-dom
import { act } from 'react'
import chatVisualFormats from '../../../../agent_go/pkg/skills/panels/chat-visuals/references/formats.md?raw'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { MarkdownRenderer } from './MarkdownRenderer'
import { ChatElementDisclosureContext } from './ChatElement'
import { sharedReportLink } from '../../utils/sharedLinks'
import { compileChatVisual } from './chatVisualSpec'

const { dashboardPost, dashboardGet } = vi.hoisted(() => ({ dashboardPost: vi.fn(), dashboardGet: vi.fn() }))

vi.mock('../../stores/useWorkspaceStore', () => ({
  useWorkspaceStore: (selector: (state: Record<string, unknown>) => unknown) => selector({}),
}))
vi.mock('../../stores/useAppStore', () => ({
  useAppStore: (selector: (state: Record<string, unknown>) => unknown) => selector({}),
}))
vi.mock('../../stores/useModeStore', () => ({
  useModeStore: { getState: () => ({ selectedModeCategory: null }) },
}))
vi.mock('../../stores/useWorkflowStore', () => ({
  useWorkflowStore: { getState: () => ({}) },
}))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: { getState: () => ({ productSurface: 'agentworks' }) },
}))
vi.mock('../../stores/useGlobalPresetStore', () => ({
  useGlobalPresetStore: { getState: () => ({}) },
}))
vi.mock('../../services/api', () => ({
  default: { post: dashboardPost, get: dashboardGet },
  workspaceApi: {},
  agentApi: {},
  getApiBaseUrl: () => '',
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const writeText = vi.fn(async () => undefined)
Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
Object.defineProperty(window, 'isSecureContext', { value: true })

async function renderContent(content: string, copyableCode: boolean, chatElements = false): Promise<HTMLElement> {
  const container = document.createElement('div')
  const root = createRoot(container)
  await act(async () => { root.render(<MarkdownRenderer content={content} copyableCode={copyableCode} chatElements={chatElements} />); await import('./ChatVisualRenderer') })
  return container
}

const reply = 'Run this yourself:\n\n```\nnpm install -g example\n```\n\n```bash\nls -la\n```\n'

it('puts a Copy button on each code block of a chat reply and copies that block', async () => {
  const container = await renderContent(reply, true)
  const buttons = container.querySelectorAll('button[aria-label="Copy code"]')
  expect(buttons).toHaveLength(2)
  await act(async () => { (buttons[0] as HTMLButtonElement).click() })
  expect(writeText).toHaveBeenCalledWith('npm install -g example')
})

it('leaves code blocks without a Copy button everywhere else', async () => {
  const container = await renderContent(reply, false)
  expect(container.querySelector('button')).toBeNull()
})


it('keeps comparison tables and displays retired HTML visuals as source without executing them', async () => {
  const html = '<html><head></head><body><script>alert(1)</script><img src="https://example.com/leak"></body></html>'
  const reply = '| Option | Benefit |\n| --- | --- |\n| A | Faster |\n| B | Lower cost |\n\n```visual\n' + html + '\n```'
  const container = await renderContent(reply, true)
  expect(container.querySelector('table')?.textContent).toContain('Lower cost')
  expect(container.querySelector('pre')?.textContent).toContain(html)
  expect(container.querySelector('iframe, script, img')).toBeNull()
  await act(async () => { (container.querySelector('button[aria-label="Copy table"]') as HTMLButtonElement).click() })
  expect(writeText).toHaveBeenCalledWith('Option\tBenefit\nA\tFaster\nB\tLower cost')
})

const patch = (path: string, value: unknown, op = 'add') => JSON.stringify({ op, path, value })
const widgetStream = (spec: Record<string, unknown>) => {
  const { type, ...props } = spec
  return patch('/root', 'visual') + '\n' + patch('/elements/visual', { type, props, children: [] })
}
const fence = (spec: Record<string, unknown>) => '```chat-ui\n' + widgetStream(spec) + '\n```'

// The shipped skill is the authoring contract for the migrated stream format.
it('compiles every shipped chat-visual example with the actual json-render catalog (PLAT-750-88)', () => {
  const examples = [...chatVisualFormats.matchAll(/```chat-ui\n([\s\S]*?)\n```/g)]
  expect(examples.length).toBeGreaterThan(0)
  for (const example of examples) expect(compileChatVisual(example[1]), example[1]).not.toBeNull()
})

it('renders the three bounded chat elements, keeps labels as text and uses normal safe Markdown in details', async () => {
  const reply = [
    fence({ type: 'chart', title: 'Daily requests', style: 'bar', data: [{ label: '<script>bad()</script>', value: -2 }, { label: 'Tue', value: 0 }] }),
    fence({ type: 'timeline', title: 'History', items: [{ when: '09:00', title: '<img src=x>', detail: 'Received' }] }),
    fence({ type: 'details', title: 'Evidence', body: '**Saved total**\n\n[Unsafe](javascript:alert(1))\n\n<script>bad()</script>\n\n' + fence({ type: 'details', title: 'Nested', body: 'Hidden' }) }),
  ].join('\n\n')
  const container = await renderContent(reply, true, true)
  expect(container.querySelectorAll('[data-chat-element]')).toHaveLength(3)
  expect(container.querySelector('svg')?.getAttribute('aria-label')).toContain('Tue: 0')
  expect(container.querySelector('rect')?.getAttribute('height')).toBe('120')
  expect(container.querySelector('[data-chat-element="timeline"]')?.textContent).toContain('<img src=x>')
  const details = container.querySelector('details[data-chat-element="details"]') as HTMLDetailsElement
  expect(details.open).toBe(false)
  expect(details.querySelector('strong')?.textContent).toBe('Saved total')
  expect(details.querySelector('pre')?.textContent).toContain('Nested')
  expect(container.querySelector('iframe, script, img, a[href^="javascript:"]')).toBeNull()
  expect(container.querySelector('pre [data-chat-element]')).toBeNull()
})

it('leaves malformed, oversized, nonfinite and non-chat blocks as source', async () => {
  const specs = [
    '{"op":"add",',
    widgetStream({ type: 'chart', title: 'Bad', style: 'line', data: [{ label: 'A', value: 1 }] }).replace('value\":1}', 'value\":1e999}'),
    widgetStream({ type: 'timeline', title: 'Too many', items: Array.from({ length: 21 }, () => ({ when: 'Now', title: 'Event' })) }),
    widgetStream({ type: 'details', title: 'Too much', body: 'x'.repeat(12001) }),
  ]
  const content = specs.map(spec => '```chat-ui\n' + spec + '\n```').join('\n\n')
  const container = await renderContent(content, true, true)
  expect(container.querySelector('[data-chat-element]')).toBeNull()
  expect(container.querySelectorAll('pre').length).toBeGreaterThanOrEqual(4)
  const ordinary = await renderContent(fence({ type: 'details', title: 'Code example', body: 'Example' }), false)
  expect(ordinary.querySelector('[data-chat-element]')).toBeNull()
  expect(ordinary.querySelector('pre')?.textContent).toContain('Code example')
})

// PLAT-750-88: a patch cut at its exponent must not invent a point; native
// updates and final replies use one json-render path without showing JSON.
it('streams completed json-render operations, preserves mounts and honors terminal placeholders', async () => {
  const container = document.createElement('div'), root = createRoot(container)
  const update = async (source: string, stream: 'progressive' | 'pending' | 'complete' = 'progressive') => {
    await act(async () => {
      root.render(<MarkdownRenderer content={'```chat-ui\n' + source} chatElements chatElementStream={stream === 'complete' ? undefined : stream} />)
      await import('./ChatVisualRenderer')
    })
  }
  const prefix = widgetStream({ type: 'chart', title: 'Requests', style: 'line', data: [] }) + '\n'
  const point = '{"op":"add","path":"/elements/visual/props/data/-","value":{"label":"A","value":1e'
  await update(prefix + point)
  expect(container.querySelector('[data-chat-element-pending]')).not.toBeNull()
  expect(container.querySelector('pre, code')).toBeNull()
  await update(prefix + point + '2}}\n')
  const chart = container.querySelector('figure')
  expect(chart?.querySelectorAll('circle')).toHaveLength(1)
  expect(chart?.querySelector('svg')?.getAttribute('aria-label')).toContain('A: 100')
  const two = prefix + point + '2}}\n' + patch('/elements/visual/props/data/-', { label: 'B', value: 20 })
  await update(two)
  expect(container.querySelector('figure')).toBe(chart)
  expect(chart?.querySelectorAll('circle')).toHaveLength(2)
  await update(two + '\n' + patch('/elements/visual/props/data/0/value', 7, 'replace'))
  expect(chart?.querySelector('svg')?.getAttribute('aria-label')).toContain('A: 7')
  await update(two, 'pending')
  expect(container.querySelector('svg, pre, code')).toBeNull()
  // The saved reply is authoritative, even when it differs from the live stream.
  await update(widgetStream({ type: 'chart', title: 'Requests', style: 'line', data: [{ label: 'Final', value: 9 }] }), 'complete')
  expect(container.querySelector('svg')?.getAttribute('aria-label')).toContain('Final: 9')
  await act(async () => root.unmount())
})

it('keeps an opened json-render disclosure through the committed-message remount (PLAT-750-80)', async () => {
  const container = document.createElement('div'), root = createRoot(container)
  const values = new Map<string, boolean>()
  const update = async (source: string, message = 'live', complete = false) => {
    await act(async () => {
      root.render(<ChatElementDisclosureContext.Provider value={{ values, scope: 'user-1:reply:0' }}>
        <MarkdownRenderer key={message} content={'```chat-ui\n' + source} chatElements chatElementStream={complete ? undefined : 'progressive'} />
      </ChatElementDisclosureContext.Provider>)
      await import('./ChatVisualRenderer')
    })
  }
  const initial = widgetStream({ type: 'details', title: 'Evidence', body: 'First **fact**' })
  await update(initial)
  const disclosure = container.querySelector('details[data-chat-element="details"]') as HTMLDetailsElement
  await act(async () => { disclosure.open = true; disclosure.dispatchEvent(new Event('toggle')) })
  const bodyPatch = '\n' + patch('/elements/visual/props/body', 'First **fact**\nSecond fact', 'replace')
  await update(initial + bodyPatch.slice(0, -3))
  expect(container.querySelector('details[data-chat-element="details"]')).toBe(disclosure)
  expect(disclosure.open).toBe(true)
  expect(container.querySelector('pre, code')).toBeNull()
  await update(initial + bodyPatch, 'canonical', true)
  const committed = container.querySelector('details[data-chat-element="details"]') as HTMLDetailsElement
  expect(committed).not.toBe(disclosure)
  expect(committed.open).toBe(true)
  expect(committed.textContent).toContain('Second fact')
  await update(widgetStream({ type: 'details', title: 'Different evidence', body: 'Another block' }), 'different', true)
  expect(container.querySelector<HTMLDetailsElement>('details')?.open).toBe(false)
  await act(async () => root.unmount())
})

it('streams cards and diagram patches without exposing raw source (PLAT-750-83)', async () => {
  const container = document.createElement('div'), root = createRoot(container)
  const update = async (source: string) => {
    await act(async () => {
      root.render(<MarkdownRenderer content={'```chat-ui\n' + source} chatElements chatElementStream="progressive" />)
      await import('./ChatVisualRenderer')
    })
  }
  const cards = widgetStream({ type: 'cards', title: 'Options', layout: 'grid', items: [{ title: 'One', description: 'A complete option' }] })
  await update(cards + '\n{"op":"add","path":"/elements/visual/props/items/-","value":{"title":"Unfinished')
  expect(container.querySelectorAll('[data-chat-element="cards"] li')).toHaveLength(1)
  const graph = widgetStream({ type: 'diagram', title: 'Flow', direction: 'vertical', edges: [], nodes: [{ id: 'first', title: '<script>label</script>' }] })
  await update(graph)
  expect(container.querySelectorAll('[data-diagram-node]')).toHaveLength(1)
  expect(container.querySelector('pre, code, script')).toBeNull()
  await update(graph + '\n' + patch('/elements/visual/props/nodes/-', { id: 'next', title: 'Next', items: [{ title: 'Worker', description: 'Grouped component' }] }) + '\n' + patch('/elements/visual/props/edges/-', { from: 'first', to: 'next' }))
  expect(container.querySelectorAll('[data-diagram-node]')).toHaveLength(2)
  expect(container.textContent).toContain('Grouped component')
  expect(container.querySelectorAll('path[marker-end]')).toHaveLength(1)
  await act(async () => root.unmount())
})

it('rejects malformed completed patches and executable or unbounded spec structures (PLAT-750-88)', () => {
  const good = widgetStream({ type: 'details', title: 'Evidence', body: 'Saved facts' })
  for (const extra of [
    '{"op":"add",',
    patch('/elements/visual/on', { press: { action: 'run' } }),
    patch('/elements/visual/props/body', { $state: '/secret' }),
    patch('/elements/visual/children', ['visual']),
    patch('/elements/visual/props/__proto__/leak', true),
    JSON.stringify({ op: 'copy', from: '/elements', path: '/elements/visual/props/x' }),
  ]) expect(compileChatVisual(good + '\n' + extra)).toBeNull()
  expect(compileChatVisual(good + '\n' + '{"op":"add",', true)).not.toBeNull()
  expect(compileChatVisual(widgetStream({ type: 'html', title: 'Bad', body: '<script>bad()</script>' }))).toBeNull()
})

it('refuses executable card URLs and ambiguous or dangling graph identities (PLAT-750-83)', () => {
  for (const image of ['data:image/svg+xml,<svg/>', 'file:///private/image.png', 'javascript:alert(1)', 'Crew/example/../private.png']) {
    expect(compileChatVisual(widgetStream({ type: 'cards', title: 'Images', items: [{ title: 'Card', image }] }))).toBeNull()
  }
  for (const href of ['javascript:alert(1)', 'https://example.com/x>)[Injected](https://example.com)']) {
    expect(compileChatVisual(widgetStream({ type: 'cards', title: 'Links', items: [{ title: 'Card', href }] }))).toBeNull()
  }
  const base = { type: 'diagram', title: 'Flow', nodes: [{ id: 'a', title: 'One' }], edges: [] }
  for (const bad of [
    { ...base, nodes: [...base.nodes, ...base.nodes] },
    { ...base, edges: [{ from: 'a', to: 'missing' }] },
    { ...base, nodes: Array.from({ length: 17 }, (_, i) => ({ id: `n${i}`, title: 'Node' })) },
    { ...base, edges: Array.from({ length: 25 }, () => ({ from: 'a', to: 'a' })) },
  ]) expect(compileChatVisual(widgetStream(bad))).toBeNull()
  expect(compileChatVisual(widgetStream({ type: 'cards', title: 'Images', items: [{ title: 'Card', image: 'https://example.com/photo.jpg' }, { title: 'Saved', image: 'Crew/example/photo.png' }] }))).not.toBeNull()
  expect(compileChatVisual(widgetStream({ ...base, edges: [{ from: 'a', to: 'a' }] }))).not.toBeNull()
})


it('opens same-server dashboards in a new tab without replacing the chat (PLAT-750)', async () => {
  const original = window.location.pathname + window.location.search
  const external = vi.spyOn(window, 'open').mockImplementation(() => null)
  window.history.replaceState({}, '', '/crew/projects/current')
  try {
    const href = sharedReportLink(window.location.origin, 'Crew/sample', 'db/reports/managed/overview/index.html')
    const revisionLink = sharedReportLink(window.location.origin, 'Crew/sample', 'db/reports/managed/overview/' + 'a'.repeat(64) + '/index.html')
    for (const link of [href, revisionLink, window.location.origin + '/d/23456789ab']) {
      const container = await renderContent(`[Overview](${link})`, true, true)
      await act(async () => { container.querySelector<HTMLAnchorElement>('a')!.click() })
      expect(window.location.pathname).toBe('/crew/projects/current')
      expect(external).toHaveBeenLastCalledWith(link, '_blank', 'noopener,noreferrer')
    }
  } finally {
    external.mockRestore()
    dashboardPost.mockReset()
    dashboardGet.mockReset()
    window.history.replaceState({}, '', original)
  }
})


it('renders native swimlanes and ordered call/reply sequences with strict actor references', async () => {
  const actors = [{ id: 'person', title: 'Person' }, { id: 'agent', title: '<script>Agent</script>' }]
  const sequence = { type: 'sequence', title: 'A question', participants: actors, messages: [{ from: 'person', to: 'agent', label: 'Ask' }, { from: 'agent', to: 'person', label: 'Answer', reply: true }] }
  const swimlane = { type: 'swimlane', title: 'Who does what', lanes: actors, steps: [{ id: 'ask', lane: 'person', title: 'Ask' }, { id: 'answer', lane: 'agent', title: 'Answer' }] }
  const container = document.createElement('div'), root = createRoot(container)
  try {
    for (const spec of [sequence, swimlane]) {
      await act(async () => root.render(<MarkdownRenderer content={fence(spec)} chatElements />))
      expect(container.querySelector(`[data-chat-element="${spec.type}"]`)).not.toBeNull()
      expect(container.querySelector('script')).toBeNull()
      expect(container.textContent).toContain('<script>Agent</script>')
      expect(container.querySelector('[data-diagram-viewport]')).not.toBeNull()
      expect(container.querySelector('[aria-label="Download diagram image"], [aria-label="Copy diagram as image"]')).not.toBeNull()
    }
    const prefix = widgetStream({ type: 'sequence', title: 'Streaming calls', participants: actors, messages: [sequence.messages[0]] }) + '\n'
    await act(async () => root.render(<MarkdownRenderer content={'```chat-ui\n' + prefix + '{"op":"add","path":"/elements/visual/props/messages/-","value":{"from":"agent"'} chatElements chatElementStream="progressive" />))
    expect(container.querySelectorAll('[data-sequence-message]')).toHaveLength(1)
    expect(compileChatVisual(widgetStream({ ...sequence, participants: [actors[0], actors[0]] }))).toBeNull()
    expect(compileChatVisual(widgetStream({ ...sequence, messages: [{ ...sequence.messages[0], to: 'missing' }] }))).toBeNull()
    expect(compileChatVisual(widgetStream({ ...swimlane, steps: [{ ...swimlane.steps[0], lane: 'missing' }] }))).toBeNull()
    expect(compileChatVisual(widgetStream({ ...swimlane, steps: [swimlane.steps[0], swimlane.steps[0]] }))).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
