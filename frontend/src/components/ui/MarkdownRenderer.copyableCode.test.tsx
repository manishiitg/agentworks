// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { MarkdownRenderer } from './MarkdownRenderer'
import { ChatElementDisclosureContext } from './ChatElement'
import { sharedReportLink } from '../../utils/sharedLinks'

const { dashboardPost } = vi.hoisted(() => ({ dashboardPost: vi.fn() }))

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
  default: { post: dashboardPost },
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
  await act(async () => root.render(<MarkdownRenderer content={content} copyableCode={copyableCode} chatElements={chatElements} />))
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
})

const fence = (spec: unknown) => '```chat-ui\n' + JSON.stringify(spec) + '\n```'

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
    '{"type":"chart",',
    '{"type":"chart","title":"Bad","style":"line","data":[{"label":"A","value":1e999}]}',
    JSON.stringify({ type: 'timeline', title: 'Too many', items: Array.from({ length: 21 }, () => ({ when: 'Now', title: 'Event' })) }),
    JSON.stringify({ type: 'details', title: 'Too much', body: 'x'.repeat(12001) }),
  ]
  const content = specs.map(spec => '```chat-ui\n' + spec + '\n```').join('\n\n')
  const container = await renderContent(content, true, true)
  expect(container.querySelector('[data-chat-element]')).toBeNull()
  expect(container.querySelectorAll('pre').length).toBeGreaterThanOrEqual(4)
  const ordinary = await renderContent(fence({ type: 'details', title: 'Code example', body: 'Example' }), false)
  expect(ordinary.querySelector('[data-chat-element]')).toBeNull()
  expect(ordinary.querySelector('pre')?.textContent).toContain('Code example')
})

// PLAT-750: a number split at its exponent must never become an invented point.
it('progressively renders complete records without raw JSON and keeps the widget mounted', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const update = async (json: string, stream: 'progressive' | 'pending' | 'complete' = 'progressive') => {
    await act(async () => root.render(<MarkdownRenderer content={'```chat-ui\n' + json} chatElements chatElementStream={stream === 'complete' ? undefined : stream} />))
  }
  const prefix = '{"type":"chart","title":"Requests","style":"line","data":['
  await update(prefix + '{"label":"A","value":1e')
  expect(container.querySelector('[data-chat-element-pending]')).not.toBeNull()
  expect(container.querySelector('pre, code')).toBeNull()
  await update(prefix + '{"label":"A","value":1e2},{"label":"B","value":')
  const chart = container.querySelector('figure')
  expect(chart?.querySelectorAll('circle')).toHaveLength(1)
  expect(chart?.querySelector('svg')?.getAttribute('aria-label')).toContain('A: 100')
  await update(prefix + '{"label":"A","value":1e2},{"label":"B","value":20}]}')
  expect(container.querySelector('figure')).toBe(chart)
  expect(chart?.querySelectorAll('circle')).toHaveLength(2)
  await update(prefix + '{"label":"A","value":100}]}', 'pending')
  expect(container.querySelector('svg, pre, code')).toBeNull()
  await update(prefix + '{"label":"A","value":7}]}', 'complete')
  expect(container.querySelector('svg')?.getAttribute('aria-label')).toContain('A: 7')
  await act(async () => root.unmount())
})

it('keeps an opened streaming disclosure through the committed-message remount (PLAT-750-80)', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const values = new Map<string, boolean>()
  const update = async (source: string, message = 'live', complete = false) => {
    await act(async () => root.render(<ChatElementDisclosureContext.Provider value={{ values, scope: 'user-1:reply:0' }}>
      <MarkdownRenderer key={message} content={'```chat-ui\n' + source} chatElements chatElementStream={complete ? undefined : 'progressive'} />
    </ChatElementDisclosureContext.Provider>))
  }
  const timeline = '{"type":"timeline","title":"History","items":[{"when":"09:00","title":"Received"},'
  await update(timeline + '{"when":"10:00","title":"Work')
  expect(container.querySelectorAll('[data-chat-element="timeline"] li')).toHaveLength(1)
  await update(timeline + '{"when":"10:00","title":"Working"}]}')
  expect(container.querySelectorAll('[data-chat-element="timeline"] li')).toHaveLength(2)
  const details = '{"type":"details","title":"Evidence","body":"'
  await update(details + 'First **fact**')
  const disclosure = container.querySelector('details[data-chat-element="details"]') as HTMLDetailsElement
  await act(async () => {
    disclosure.open = true
    disclosure.dispatchEvent(new Event('toggle'))
  })
  await update(details + 'First **fact**\\nSecond \\u263')
  expect(container.querySelector('details[data-chat-element="details"]')).toBe(disclosure)
  expect(disclosure.open).toBe(true)
  expect(disclosure.textContent).toContain('Second')
  expect(container.querySelector('pre, code')).toBeNull()
  await update(details + 'First **fact**\\nSecond fact"}', 'canonical', true)
  const committed = container.querySelector('details[data-chat-element="details"]') as HTMLDetailsElement
  expect(committed).not.toBe(disclosure)
  expect(committed.open).toBe(true)
  expect(committed.textContent).toContain('Second fact')
  await update(JSON.stringify({ type: 'details', title: 'Different evidence', body: 'Another block' }), 'different', true)
  expect(container.querySelector<HTMLDetailsElement>('details')?.open).toBe(false)
  await act(async () => root.unmount())
})


it('opens a known same-server dashboard link in its server-resolved Crew panel (PLAT-750-82)', async () => {
  const original = window.location.pathname + window.location.search
  const external = vi.spyOn(window, 'open').mockImplementation(() => null)
  window.history.replaceState({}, '', '/crew/projects/current')
  dashboardPost.mockResolvedValue({ data: { dashboards: [{
    dashboard_id: 'overview', project_id: 'resolved-project', product: 'crew',
    workspace: 'Crew/sample', document_path: 'db/reports/managed/overview/index.html',
  }], has_more: false, next_offset: 1 } })
  try {
    const href = sharedReportLink(window.location.origin, 'Crew/sample', 'db/reports/managed/overview/index.html')
    const container = await renderContent(`[Overview](${href})`, true, true)
    await act(async () => { container.querySelector<HTMLAnchorElement>('a')!.click() })
    expect(window.location.pathname).toBe('/crew/projects/resolved-project/dashboard')
    expect(external).not.toHaveBeenCalled()
    expect(window.localStorage.getItem('report-document:Crew/sample')).toBe('db/reports/managed/overview/index.html')
  } finally {
    external.mockRestore()
    dashboardPost.mockReset()
    window.history.replaceState({}, '', original)
    window.localStorage.removeItem('report-document:Crew/sample')
  }
})
