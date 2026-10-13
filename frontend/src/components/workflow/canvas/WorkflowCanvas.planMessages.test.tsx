// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ReadOnlyStepDetailPanel } from './WorkflowCanvas'
import type { WorkflowNode } from '../hooks/usePlanToFlow'
import { configuredAgentItems, type PlanStep } from '../../../utils/stepConfigMatching'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const description = '## Goal\nCreate a regression fixture.\n\n## Done when\nThe fixture exists.'
const step: PlanStep = {
  id: 'fixture', type: 'agent', title: 'Prepare fixtures',
  prompt_contract: 'description-first-v1', description, items: [],
}

async function inspect(savedStep: PlanStep, check: (host: HTMLElement) => void) {
  const host = document.createElement('div')
  const root = createRoot(host)
  const node: WorkflowNode = {
    id: savedStep.id, type: 'agent', position: { x: 0, y: 0 },
    data: { id: savedStep.id, title: savedStep.title, status: 'pending', stepIndex: 0, step: savedStep },
  }
  try {
    await act(async () => root.render(<ReadOnlyStepDetailPanel node={node} workspacePath={null} onClose={() => {}} />))
    check(host)
  } finally {
    await act(async () => root.unmount())
  }
}

it('renders the description-first task once without inventing a follow-up', async () => {
  await inspect(step, host => {
    expect(host.textContent?.match(/Create a regression fixture\./g)).toHaveLength(1)
    expect(host.textContent).toContain('First user message')
    expect(host.textContent).toContain('No optional follow-ups configured.')
    expect(host.querySelector('ol')).toBeNull()
  })
  // The Plan view must not remove the first turn from execution/log matching.
  expect(configuredAgentItems(step).map(item => item.id)).toEqual(['__step_description__'])
})

it('keeps real follow-ups in order, including an intentionally repeated instruction', async () => {
  const savedStep: PlanStep = { ...step, items: [
    { id: 'repeat', type: 'user_message', message: description },
    { id: 'review', type: 'user_message', message: 'Review the produced evidence.' },
  ] }
  await inspect(savedStep, host => {
    expect(host.textContent?.match(/Create a regression fixture\./g)).toHaveLength(2)
    expect(host.textContent).toContain('Optional follow-ups (2)')
    const rows = Array.from(host.querySelectorAll('ol > li'))
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toMatch(/^2message/)
    expect(rows[0].textContent).toContain(description)
    expect(rows[1].textContent).toMatch(/^3message/)
    expect(rows[1].textContent).toContain('Review the produced evidence.')
  })
  expect(savedStep.items).toHaveLength(2)
  expect(configuredAgentItems(savedStep)).toHaveLength(3)
})

it.each([false, true])('preserves legacy/custom authored descriptions and messages (authored=%s)', async authored => {
  await inspect({ ...step, prompt_contract: authored ? 'description-first-v1' : undefined,
    authored_prompt: authored, system_prompt: authored ? 'Follow the supplied task.' : undefined,
    items: [{ id: 'work', type: 'user_message', message: 'Complete the task.' }],
  }, host => {
    expect(host.textContent).toContain('Description')
    expect(host.textContent).not.toContain('First user message')
    expect(host.querySelectorAll('ol > li')).toHaveLength(1)
    expect(host.querySelector('ol > li')?.textContent).toMatch(/^1/)
    expect(host.textContent).toContain('Complete the task.')
  })
})
