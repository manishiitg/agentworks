// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { MarkdownRenderer } from './MarkdownRenderer'

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
  workspaceApi: {},
  agentApi: {},
  getApiBaseUrl: () => '',
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const writeText = vi.fn(async () => undefined)
Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
Object.defineProperty(window, 'isSecureContext', { value: true })

async function renderContent(content: string, copyableCode: boolean): Promise<HTMLElement> {
  const container = document.createElement('div')
  const root = createRoot(container)
  await act(async () => root.render(<MarkdownRenderer content={content} copyableCode={copyableCode} />))
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
