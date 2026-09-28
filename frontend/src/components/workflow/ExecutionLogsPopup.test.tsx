// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const { hookState } = vi.hoisted(() => ({ hookState: { current: null as null | Record<string, unknown> } }))
const { askMessages } = vi.hoisted(() => ({ askMessages: [] as string[] }))
vi.mock('./executionLogs/useExecutionLogsData', () => ({ useExecutionLogsData: () => hookState.current }))
const { stepListProps } = vi.hoisted(() => ({ stepListProps: [] as Array<Record<string, unknown>> }))
vi.mock('./executionLogs/StepList', () => ({
  StepList: (props: Record<string, unknown>) => {
    stepListProps.push(props)
    const logs = props.logs as { steps: Record<string, { title: string }> }
    const showFailedOnly = props.showFailedOnly as boolean
    return (
      <div data-testid="step-list">
        {Object.entries(logs?.steps || {}).filter(([, step]) => !showFailedOnly || (step as { title: string }).title === 'Classify intent').map(([id, step]) => (
          <div key={id}>{(step as { title: string }).title}</div>
        ))}
      </div>
    )
  },
}))
vi.mock('../../services/api', () => ({ agentApi: { getExecutionLogs: vi.fn(), getExecutionWebhookPayload: vi.fn(), getLogFile: vi.fn() } }))
vi.mock('../../services/llm-config-api', () => ({ llmConfigService: {} }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: () => ({}) }))
vi.mock('../../stores/useSecretsStore', () => ({ useSecretsStore: () => ({}) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: () => ({}) }))
vi.mock('../../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: () => ({}) }))
vi.mock('../../api/secrets', () => ({ secretsApi: {} }))
vi.mock('../../ui/MarkdownRenderer', () => ({ MarkdownRenderer: () => null, ConversationMarkdownRenderer: () => null }))
vi.mock('./ConversationViewer', () => ({ ConversationViewer: () => null }))
vi.mock('./AskAIButton', () => ({
  AskAIButton: (props: Record<string, unknown>) => {
    askMessages.push(String(props.message))
    return <button type="button" data-testid="ask-ai" data-message={String(props.message)}>Ask AI</button>
  },
}))

import ExecutionLogsPopup from './ExecutionLogsPopup'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const failedStep = {
  step_id: 'step-1', type: 'regular', title: 'Classify intent',
  executions: [{ attempt: 1, iteration: 0, content: { error: 'rate limit exceeded', model: 'model-one' } }],
  validations: [{ content: { execution_status: 'FAILED', errors: [{ message: 'gate says no' }] } }],
}
const okStep = {
  step_id: 'step-2', type: 'regular', title: 'Draft reply',
  executions: [{ attempt: 1, iteration: 0, content: { execution_result: 'Drafted.', model: 'model-one' } }],
}

function state(overrides: Record<string, unknown> = {}) {
  return {
    runFolderOptions: ['iteration-1/default'],
    loading: false,
    logs: { steps: { 'step-1': failedStep, 'step-2': okStep } },
    error: null,
    expandedSteps: new Set<string>(),
    expandedValidations: new Set<string>(),
    expandedExecutions: new Set<string>(),
    expandedArchived: new Set<string>(),
    selectedRunFolder: 'iteration-1/default',
    setSelectedRunFolder: vi.fn(),
    stepSearchQueries: {},
    setStepSearchQueries: vi.fn(),
    routeFilterKey: null,
    setRouteFilterKey: vi.fn(),
    routingRouteGroups: [],
    expandedFiles: new Set<string>(),
    fileContents: {},
    loadingFiles: new Set<string>(),
    focusedStepId: undefined,
    stepDetailScrolled: false,
    handleStepDetailScroll: vi.fn(),
    loadLogs: vi.fn(),
    toggleStep: vi.fn(),
    toggleValidation: vi.fn(),
    toggleExecution: vi.fn(),
    toggleArchived: vi.fn(),
    toggleFileExpansion: vi.fn(),
    ...overrides,
  }
}

async function mount() {
  askMessages.length = 0
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(
    <ExecutionLogsPopup
      workspacePath="Workflow/test"
      runFolder="iteration-1/default"
      runFolders={['iteration-1/default']}
    />,
  ))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('filters to failed steps from the header chip', async () => {
  hookState.current = state()
  const { host, unmount } = await mount()
  try {
    const chip = [...host.querySelectorAll('button')].find(button => button.textContent === 'Failed · 1')
    expect(chip).not.toBeUndefined()
    expect(chip?.getAttribute('aria-pressed')).toBe('false')
    await act(async () => (chip as HTMLButtonElement).click())
    expect(host.textContent).toContain('Classify intent')
    expect(host.textContent).not.toContain('Draft reply')
    expect(chip?.getAttribute('aria-pressed')).toBe('true')
    await act(async () => (chip as HTMLButtonElement).click())
    expect(host.textContent).toContain('Draft reply')
  } finally {
    await unmount()
  }
})

it('auto-expands the first failed step once', async () => {
  const toggleStep = vi.fn()
  hookState.current = state({ toggleStep })
  const { unmount } = await mount()
  try {
    expect(toggleStep).toHaveBeenCalledWith('step-1')
    expect(toggleStep).toHaveBeenCalledTimes(1)
  } finally {
    await unmount()
  }
})

it('asks about the run with failures named', async () => {
  hookState.current = state()
  const { host, unmount } = await mount()
  try {
    expect(host.querySelector('[data-testid="ask-ai"]')).toBeNull()
    await act(async () => {
      (host.querySelector('[aria-label="Walkthrough: Execution Logs"]') as HTMLButtonElement).click()
    })
    const runMessage = askMessages.find(message => message.includes('steps failed'))
    expect(runMessage).toContain('1 of 2 steps failed: "Classify intent"')
    expect(runMessage).toContain('rate limit exceeded')
  } finally {
    await unmount()
  }
})

it('offers an ask when no run is selected', async () => {
  hookState.current = state({ logs: null, selectedRunFolder: '' })
  const { host, unmount } = await mount()
  try {
    expect(host.textContent).toContain('Select an iteration or group')
    expect(askMessages.some(message => message.includes('Help me run this workflow'))).toBe(true)
  } finally {
    await unmount()
  }
})
