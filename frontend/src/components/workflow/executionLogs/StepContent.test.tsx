// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

vi.mock('../../ui/MarkdownRenderer', () => ({ MarkdownRenderer: () => null, ConversationMarkdownRenderer: () => null }))
vi.mock('../ConversationViewer', () => ({ ConversationViewer: () => null }))
vi.mock('../AskAIButton', () => ({
  AskAIButton: (props: Record<string, unknown>) => (
    <button type="button" data-testid="explain" data-message={String(props.message)}>{String(props.label)}</button>
  ),
}))

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
  askContext: { workspacePath: 'Workflow/test', runFolder: 'iteration-1/default' },
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

it('shows detail chips and explains script errors', async () => {
  const { host, unmount } = await mount(scriptStep, { expandedExecutions: new Set(['s1-exec-1-0']) })
  try {
    expect(host.textContent).toContain('high')
    const explain = host.querySelector('[data-testid="explain"]')
    expect(explain?.textContent).toBe('Explain')
    expect(explain?.getAttribute('data-message')).toContain('script blew up')
    const errorBlock = host.querySelector('pre')
    expect(errorBlock?.className).toContain('text-destructive')
    expect(errorBlock?.className).not.toContain('rose')
  } finally {
    await unmount()
  }
})

it('explains failed validations', async () => {
  const { host, unmount } = await mount(
    {
      type: 'scripted', title: 'Gate check',
      executions: [],
      validations: [{
        kind: 'final', attempt: 1, phase: 'gate',
        content: { execution_status: 'FAILED', overall_pass: false, errors: [{ message: 'bad gate' }], feedback: [] },
      }],
    },
    { expandedValidations: new Set(['s1-val-final-1']) },
  )
  try {
    const explain = [...host.querySelectorAll('[data-testid="explain"]')].find(node => node.textContent === 'Explain this failure')
    expect(explain?.getAttribute('data-message')).toContain('bad gate')
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
