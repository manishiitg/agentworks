// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { AgentNode } from './AgentNode'
import type { AgentNodeData } from '../hooks/usePlanToFlow'
import { getNodeLayoutDimensions } from '../hooks/usePlanToFlow'
import { configuredAgentItems, type AgentPlanStep } from '../../../utils/stepConfigMatching'

vi.mock('@xyflow/react', () => ({ Handle: () => null, Position: { Top: 'top', Bottom: 'bottom' } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const step: AgentPlanStep = {
  id: 'fixtures', type: 'agent', title: 'Prepare Regression Fixtures',
  prompt_contract: 'description-first-v1',
  description: '## Goal\r\nCreate **test_fixtures.json** with `run_label`.\r\n\r\n## Inputs\r\nNone.',
  items: [],
}

async function renderStep(saved: AgentPlanStep, check: (host: HTMLElement, data: AgentNodeData) => void) {
  const host = document.createElement('div')
  const root = createRoot(host)
  const data: AgentNodeData = {
    id: saved.id, title: saved.title, description: saved.description,
    items: configuredAgentItems(saved), step: saved, status: 'pending', stepIndex: 0,
  }
  try {
    await act(async () => root.render(<AgentNode data={data} />))
    check(host, data)
  } finally {
    await act(async () => root.unmount())
  }
}

it('shows the task preview once without a generated row or item count', async () => {
  await renderStep(step, (host, data) => {
    expect(host.textContent).toContain('Create test_fixtures.json with run_label.')
    expect(host.textContent).not.toContain('##')
    expect(host.textContent).not.toContain('**')
    expect(host.textContent).not.toContain('Step task')
    expect(host.textContent).not.toContain('1 item')
    expect(host.textContent).not.toContain('No items defined')
    expect(host.textContent).not.toContain('Inputs')
    expect(data.items?.map(item => item.id)).toEqual(['__step_description__'])
    expect(data.step.description).toBe(step.description)
    const node = {id: data.id, type: 'agent' as const, position: {x: 0, y: 0}, data}
    expect(getNodeLayoutDimensions(node).height).toBeLessThan(200)
    expect(getNodeLayoutDimensions({...node, data: {...data, items: []}})).toEqual(getNodeLayoutDimensions(node))
  })
})

it('previews emphasis, quoted text and numbered lists as plain text', async () => {
  await renderStep({...step, description: '## Goal\n> 1. Create *fixtures* with `run_label`.'}, host => {
    expect(host.textContent).toContain('Create fixtures with run_label.')
    expect(host.textContent).not.toContain('1. Create')
    expect(host.textContent).not.toContain('*fixtures*')
    expect(host.textContent).not.toContain('>')
  })
})

it('counts authored follow-ups and retains their runtime numbering and order', async () => {
  const saved: AgentPlanStep = { ...step, items: Array.from({ length: 7 }, (_, index) => ({
    id: `later-${index}`, type: 'user_message', title: `Follow-up ${index + 1}`, message: 'Review the evidence.',
  })) }
  await renderStep(saved, (host, data) => {
    expect(host.textContent).toContain('7 follow-ups')
    expect(host.textContent).not.toContain('Step task')
    expect(host.textContent).toContain('+1 more follow-up')
    const rows = Array.from(host.querySelectorAll('.space-y-1\\.5 > div')).slice(0, 6)
    expect(rows.map(row => row.textContent)).toEqual(Array.from({ length: 6 }, (_, index) => `${index + 2}messageFollow-up ${index + 1}`))
    expect(data.items).toHaveLength(8)
    expect(saved.items).toHaveLength(7)
  })
})

it.each([false, true])('keeps legacy/custom authored item counts and rows (authored=%s)', async authored => {
  await renderStep({ ...step, prompt_contract: authored ? 'description-first-v1' : undefined,
    authored_prompt: authored, items: [{ id: 'work', type: 'user_message', message: 'Create the fixture.' }],
  }, host => {
    expect(host.textContent).toContain('1 item')
    expect(host.textContent).toContain('1messageCreate the fixture.')
  })
})
