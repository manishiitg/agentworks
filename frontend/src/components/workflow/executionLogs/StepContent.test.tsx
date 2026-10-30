// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

vi.mock('../../ui/MarkdownRenderer', () => ({ MarkdownRenderer: () => null, ConversationMarkdownRenderer: () => null }))
vi.mock('../ConversationViewer', () => ({ ConversationViewer: () => null }))

import { StepContent } from './StepContent'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const baseProps = {
  stepId: 's1',
  logs: null,
  stepSearchQueries: {},
  setStepSearchQueries: (() => {}) as unknown as React.Dispatch<React.SetStateAction<Record<string, string>>>,
  toggleValidation: () => {},
  toggleExecution: () => {},
  toggleArchived: () => {},
  expandedFiles: new Set<string>(),
  fileContents: {},
  loadingFiles: new Set<string>(),
  toggleFileExpansion: () => {},
}

async function mount(stepLogs: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(
    <StepContent
      {...baseProps}
      stepLogs={stepLogs}
      expandedValidations={new Set<string>()}
      expandedExecutions={new Set<string>()}
      expandedArchived={new Set<string>()}
      {...extra}
    />,
  ))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

const scriptStep = {
  type: 'scripted', title: 'Run script', description: 'Run the thing.',
  execution_tier: 'high',
  executions: [{
    attempt: 1, iteration: 0, fast_path: true,
    content: { success: false, exit_code: 1, output: '', error: 'script blew up' },
  }],
}

it('shows detail chips and script errors', async () => {
  const { host, unmount } = await mount(scriptStep, { expandedExecutions: new Set(['s1-exec-1-0']) })
  try {
    expect(host.textContent).toContain('high')
    const errorBlock = host.querySelector('pre')
    expect(errorBlock?.className).toContain('text-destructive')
    expect(errorBlock?.className).not.toContain('rose')
  } finally {
    await unmount()
  }
})

it('uses card-title section headings without kickers', async () => {
  const { host, unmount } = await mount(scriptStep)
  try {
    const headings = host.querySelectorAll('h4')
    expect(headings.length).toBeGreaterThan(0)
    for (const heading of headings) {
      expect(heading.className).not.toContain('uppercase')
      expect(heading.className).not.toContain('tracking-')
    }
  } finally {
    await unmount()
  }
})
