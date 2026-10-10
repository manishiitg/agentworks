// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewNode } from './CrewNode'
import type { CrewStepNodeData } from '../hooks/usePlanToFlow'
import type { ProductAPITrigger } from '../../../api/productWebhooks'
vi.mock('@xyflow/react', () => ({ Handle: () => null, Position: { Top: 'top', Bottom: 'bottom' } }))
const lookups = vi.hoisted(() => ({
  trigger: null as ProductAPITrigger | null | undefined,
  alias: null as string | null | undefined,
}))
vi.mock('../canvas/useCrewStepLookups', () => ({
  useCrewTrigger: () => lookups.trigger,
  useCrewAttachmentAlias: () => lookups.alias,
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const crewStep = {
  type: 'crew',
  id: 'crew-1',
  title: 'Review with project-a',
  crew_profile_id: 'work',
  crew_project_id: 'project-a',
  trigger_id: 'trig-1',
  instruction: 'Review the PR',
  context_output: 'review.md',
} as const

const data = (overrides: Partial<CrewStepNodeData> = {}): CrewStepNodeData => ({
  id: 'crew-1',
  title: 'Review with project-a',
  crew_profile_id: 'work',
  crew_project_id: 'project-a',
  trigger_id: 'trig-1',
  status: 'pending',
  stepIndex: 0,
  step: { ...crewStep },
  ...overrides,
})

async function renderNode(nodeData: CrewStepNodeData, selected = false): Promise<{ host: HTMLElement; unmount: () => Promise<void> }> {
  const host = document.createElement('div')
  const root = createRoot(host)
  await act(async () => root.render(<CrewNode data={nodeData} selected={selected} />))
  return { host, unmount: () => act(async () => root.unmount()) }
}

it('shows the crew project, trigger, and response file', async () => {
  lookups.trigger = null
  lookups.alias = null
  const { host, unmount } = await renderNode(data())
  try {
    expect(host.textContent).toContain('Review with project-a')
    expect(host.textContent).toContain('project-a')
    expect(host.textContent).toContain('trig-1')
    expect(host.textContent).toContain('review.md')
  } finally {
    await unmount()
  }
})

it('prefers the crew alias and trigger name over raw ids', async () => {
  lookups.alias = 'crew_news_monitor'
  lookups.trigger = { id: 'trig-1', name: 'Nightly scan' } as ProductAPITrigger
  const { host, unmount } = await renderNode(data({
    crew_project_id: '2f63209f-bb00-5237-9e3c-d49aaaaaa',
    trigger_id: '648b540f-179c-4fd4-8f18-c6bbbbbbb',
    step: { ...crewStep, crew_project_id: '2f63209f-bb00-5237-9e3c-d49aaaaaa', trigger_id: '648b540f-179c-4fd4-8f18-c6bbbbbbb' },
  }))
  try {
    expect(host.textContent).toContain('crew_news_monitor')
    expect(host.textContent).toContain('Nightly scan')
    expect(host.textContent).not.toContain('2f63209f')
    expect(host.textContent).not.toContain('648b540f')
    const tooltips = Array.from(host.querySelectorAll('dd[title]')).map((dd) => dd.getAttribute('title'))
    expect(tooltips).toContain('2f63209f-bb00-5237-9e3c-d49aaaaaa')
    expect(tooltips).toContain('648b540f-179c-4fd4-8f18-c6bbbbbbb')
  } finally {
    lookups.trigger = null
    lookups.alias = null
    await unmount()
  }
})

it('defaults the response file to response.md', async () => {
  const { host, unmount } = await renderNode(data({ step: { ...crewStep, context_output: undefined } }))
  try {
    expect(host.textContent).toContain('response.md')
  } finally {
    await unmount()
  }
})

it('marks running and failed runs', async () => {
  const running = await renderNode(data({ status: 'running' }))
  try {
    expect(running.host.textContent).toContain('Running')
  } finally {
    await running.unmount()
  }
  const failed = await renderNode(data({ status: 'failed' }))
  try {
    expect(failed.host.textContent).toContain('Failed')
  } finally {
    await failed.unmount()
  }
})

it('highlights plan changes', async () => {
  const { host, unmount } = await renderNode(data({ changeType: 'added' }))
  try {
    // Badge text is lowercase in the DOM; CSS capitalize renders it as Added.
    expect(host.textContent).toContain('added')
  } finally {
    await unmount()
  }
})
