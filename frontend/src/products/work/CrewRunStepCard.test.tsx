// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { CrewRunStepCard } from './CrewRunStepCard'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function render(props: React.ComponentProps<typeof CrewRunStepCard>) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewRunStepCard {...props} />))
  return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('shows a step as one card: its name, when it ran and what it reported', async () => {
  const view = await render({ title: 'Fetch all issues', state: 'finished', when: '18:23 · 2s', checks: '1 check passed', detail: '140 issues' })
  try {
    const card = view.host.querySelector('[data-testid="run-step"]')
    expect(card?.textContent).toContain('Fetch all issues')
    expect(card?.textContent).toContain('18:23 · 2s')
    expect(card?.textContent).toContain('1 check passed')
    expect(card?.textContent).toContain('140 issues')
    expect(card?.querySelector('[data-testid="run-output"]')).toBeNull()
  } finally { await view.cleanup() }
})

it('says Passed or Failed, with the error in red', async () => {
  const failed = await render({ title: 'Read incoming message', state: 'stopped', detail: 'AutomationError: Slack route is inactive' })
  try {
    expect(failed.host.textContent).toContain('Failed')
    expect(failed.host.querySelector('p.text-red-600, div p.text-red-600')?.textContent).toContain('Slack route is inactive')
    expect(failed.host.querySelector('[data-testid="run-output"]')).toBeNull()
  } finally { await failed.cleanup() }
  const passed = await render({ title: 'Fetch all issues', state: 'finished' })
  try { expect(passed.host.textContent).toContain('Passed') } finally { await passed.cleanup() }
})

it('colours the card by state, says which step it is, and says Running now for the step in progress', async () => {
  const cases = [['finished', 'Passed', 'emerald'], ['stopped', 'Failed', 'red'], ['running', 'Running now', 'amber'], ['idle', 'Not run', 'border-border']] as const
  for (const [state, label, colour] of cases) {
    const view = await render({ title: 'Write report', state, description: 'Writes the report.', position: 'Step 3 of 4' })
    try {
      const card = view.host.querySelector('[data-testid="run-step"]')!
      expect(card.getAttribute('data-state')).toBe(state)
      expect(card.className).toContain(colour)
      expect(card.textContent).toContain(label)
      expect(card.textContent).toContain('Step 3 of 4')
      expect(card.textContent).toContain('Writes the report.')
    } finally { await view.cleanup() }
  }
})

it('marks only an AI step, with an icon, and never writes Scripted', async () => {
  const ai = await render({ title: 'Research', kind: 'agent', state: 'finished' })
  try {
    expect(ai.host.querySelector('[data-testid="ai-step-tag"]')?.textContent).toBe('AI step')
    expect(ai.host.querySelector('[data-testid="ai-step-tag"] svg')).not.toBeNull()
  } finally { await ai.cleanup() }
  const plain = await render({ title: 'Fetch', kind: 'script', state: 'finished' })
  try {
    expect(plain.host.querySelector('[data-testid="ai-step-tag"]')).toBeNull()
    expect(plain.host.textContent).not.toContain('Scripted')
  } finally { await plain.cleanup() }
})
