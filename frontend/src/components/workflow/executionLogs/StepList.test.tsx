// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { ExecutionLogsResponse } from '../../../services/api-types'

vi.mock('../../ui/MarkdownRenderer', () => ({ MarkdownRenderer: () => null, ConversationMarkdownRenderer: () => null }))

import { StepList } from './StepList'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const failedStep = {
  step_id: 'step-1', type: 'scripted', title: 'Classify intent',
  description: 'Classify the inbound message.',
  execution_tier: 'high',
  executions: [{
    attempt: 1, iteration: 0,
    content: { error: 'rate limit exceeded', model: 'model-one', duration_ms: 120000, prompt_tokens: 10000, completion_tokens: 2400 },
  }],
  validations: [{ content: { execution_status: 'FAILED', errors: [{ message: 'gate says no' }] } }],
}
const okStep = {
  step_id: 'step-2', type: 'scripted', title: 'Draft reply',
  executions: [{ attempt: 1, iteration: 0, content: { execution_result: 'Drafted three variants.', model: 'model-one' } }],
}
const logs = { steps: { 'step-1': failedStep, 'step-2': okStep } } as unknown as ExecutionLogsResponse

async function mount(props: Partial<React.ComponentProps<typeof StepList>> = {}) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const toggleStep = vi.fn()
  await act(async () => root.render(
    <StepList
      logs={logs}
      focusedStepId={undefined}
      routeFilterKey={null}
      expandedSteps={new Set()}
      toggleStep={toggleStep}
      renderStepContent={() => null}
      {...props}
    />,
  ))
  return { host, toggleStep, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('shows one status pill per step on a neutral card', async () => {
  const { host, unmount } = await mount()
  try {
    expect(host.textContent).toContain('Failed run')
    expect(host.textContent).toContain('Completed')
    expect(host.querySelector('.absolute.left-0')).toBeNull()
    const cards = host.querySelectorAll(':scope > div')
    expect(cards.length).toBe(2)
    for (const card of cards) {
      expect(card.className).toContain('border-border')
      expect(card.className).toContain('bg-card')
      expect(card.className).not.toContain('shadow-[')
      expect(card.className).not.toContain('pulse')
      expect(card.className).not.toContain('indigo')
    }
  } finally {
    await unmount()
  }
})

it('keeps collapsed chips to model, duration, and total tokens', async () => {
  const { host, unmount } = await mount()
  try {
    expect(host.textContent).toContain('model-one')
    expect(host.textContent).toContain('2m 0s')
    expect(host.textContent).toContain('12.4k tok total')
    expect(host.textContent).toContain('1 exec')
    expect(host.textContent).not.toContain('10k in')
    expect(host.textContent).not.toContain('2.4k out')
    expect(host.textContent).not.toContain('instr')
  } finally {
    await unmount()
  }
})

it('leads a failed card with the error excerpt', async () => {
  const { host, unmount } = await mount()
  try {
    expect(host.textContent).toContain('rate limit exceeded')
    expect(host.textContent).toContain('Drafted three variants.')
  } finally {
    await unmount()
  }
})

it('filters to failed steps on request', async () => {
  const { host, unmount } = await mount({ showFailedOnly: true })
  try {
    expect(host.textContent).toContain('Classify intent')
    expect(host.textContent).not.toContain('Draft reply')
  } finally {
    await unmount()
  }
})
