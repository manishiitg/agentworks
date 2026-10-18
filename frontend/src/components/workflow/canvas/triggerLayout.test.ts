import { describe, expect, it, vi } from 'vitest'
import type { ScheduledJob } from '../../../services/api-types'
import type { WorkflowNode, WorkflowEdge } from '../hooks/usePlanToFlow'
import { appendTriggerCards, traceTriggerGraph, triggerRouteSummary, TRIGGER_CARD_HEIGHT, TRIGGER_CARD_WIDTH } from './triggerLayout'

const nodes = [
  { id: 'start', type: 'start', position: { x: 0, y: 0 }, data: { id: 'start', title: 'Start' } },
  { id: 'prepare', type: 'step', position: { x: 0, y: 100 }, data: { id: 'prepare', title: 'Prepare' } },
  { id: 'router-node', type: 'routing', position: { x: 0, y: 200 }, data: { id: 'router-node', title: 'Choose task', step: { id: 'router' }, routes: [{ route_id: 'review', route_name: 'PR review' }, { route_id: 'audit', route_name: 'Audit' }] } },
  { id: 'review', type: 'step', position: { x: -200, y: 400 }, data: { id: 'review', title: 'Review' } },
  { id: 'audit', type: 'step', position: { x: 200, y: 400 }, data: { id: 'audit', title: 'Audit' } },
  { id: 'end', type: 'end', position: { x: 0, y: 600 }, data: { id: 'end', title: 'End' } },
] as WorkflowNode[]
const edges: WorkflowEdge[] = [
  { id: 'start-prepare', source: 'start', target: 'prepare' },
  { id: 'prepare-router', source: 'prepare', target: 'router-node' },
  { id: 'review-edge', source: 'router-node', target: 'review', sourceHandle: 'route-review' },
  { id: 'audit-edge', source: 'router-node', target: 'audit', sourceHandle: 'route-audit' },
  { id: 'review-end', source: 'review', target: 'end' }, { id: 'audit-end', source: 'audit', target: 'end' },
]
const job: ScheduledJob = { description: '', entity_type: 'workflow', cron_expression: '', timezone: 'UTC', run_count: 0, consecutive_failures: 0, id: 'hook', name: 'PR review hook', schedule_type: 'webhook', enabled: true, route_selections: { router: 'review' } }
const options = { loading: false, onSelect: vi.fn(), onSettings: vi.fn(), onRefresh: vi.fn() }
describe('plan triggers', () => {
  it('places cards above existing nodes without changing the saved plan or bypassing Start', () => {
    const jobs = Array.from({ length: 5 }, (_, i) => ({ ...job, id: String(i), enabled: i !== 0 }))
    const result = appendTriggerCards(nodes, edges, jobs, options)
    expect(result.nodes.slice(0, nodes.length)).toEqual(nodes)
    const cards = result.nodes.filter(node => node.type === 'workflow-trigger')
    expect(cards).toHaveLength(5)
    for (const card of cards) expect(card.position.y + TRIGGER_CARD_HEIGHT).toBeLessThan(0)
    for (let i = 1; i < cards.length; i++) expect(cards[i].position.x - cards[i - 1].position.x).toBeGreaterThan(TRIGGER_CARD_WIDTH)
    expect(result.edges.filter(edge => edge.id.startsWith('trigger-entry-')).every(edge => edge.target === 'start')).toBe(true)
    expect(edges).toHaveLength(6)
    for (const link of result.edges.filter(edge => edge.source.startsWith('workflow-trigger-'))) {
      expect(link.label).toBeUndefined()
      expect(link.style?.strokeWidth).toBe(1)
      expect(link.style?.opacity).toBeLessThanOrEqual(0.35)
    }
    for (const card of cards) expect(card.measured).toEqual({ width: TRIGGER_CARD_WIDTH, height: TRIGGER_CARD_HEIGHT })
  })
  it('connects selected routes for schedules and webhooks and full-workflow triggers to Start', () => {
    for (const schedule_type of ['cron', 'webhook'] as const) {
      const routeJob = { ...job, schedule_type }
      const flow = appendTriggerCards(nodes, edges, [routeJob], { ...options, selectedID: job.id })
      const links = flow.edges.filter(edge => edge.source === 'workflow-trigger-hook')
      expect(links.map(edge => [edge.target, edge.label])).toEqual([
        ['start', 'Starts workflow'], ['review', 'Selects: PR review'],
      ])
      const traced = traceTriggerGraph(flow.nodes, flow.edges, routeJob)
      expect(traced.edges.find(edge => edge.id === 'trigger-route-hook-review-edge')?.style?.opacity).toBe(1)
    }
    const full = appendTriggerCards(nodes, edges, [{ ...job, route_selections: {} }], { ...options, selectedID: job.id })
    expect(full.edges.filter(edge => edge.source === 'workflow-trigger-hook').map(edge => [edge.target, edge.label])).toEqual([['start', 'Full workflow']])
  })
  it('retains prerequisites and follows the saved route', () => {
    const flow = appendTriggerCards(nodes, edges, [job], options)
    const result = traceTriggerGraph(flow.nodes, flow.edges, job)
    for (const id of ['start', 'prepare', 'router-node', 'review', 'end', 'workflow-trigger-hook']) expect(result.nodes.find(node => node.id === id)?.style?.opacity).toBe(1)
    expect(result.nodes.find(node => node.id === 'audit')?.style?.opacity).toBe(0.14)
    expect(result.edges.find(edge => edge.id === 'audit-edge')?.style?.opacity).toBe(0.08)
  })
  it('does not pretend deleted routes execute the plan', () => {
    expect(triggerRouteSummary({ ...job, route_selections: { router: 'deleted' } }, nodes)).toEqual({ label: 'router: deleted (unavailable)', canTrace: false })
  })
  it('keeps runtime decisions as possible paths and terminates on loops', () => {
    const result = traceTriggerGraph(nodes, [...edges, { id: 'loop', source: 'review', target: 'router-node' }], { ...job, route_selections: {} })
    expect(result.nodes.find(node => node.id === 'audit')?.style?.opacity).toBe(1)
  })
  it('shows an empty or error section instead of inventing configured triggers', () => {
    const flow = appendTriggerCards(nodes, edges, [], { ...options, error: 'Unavailable' })
    expect(flow.nodes.filter(node => node.type === 'workflow-trigger')).toHaveLength(0)
    expect(flow.nodes.find(node => node.type === 'workflow-trigger-heading')?.data.error).toBe('Unavailable')
  })
})


it('connects a step webhook only to its target and does not trace successors', () => {
  const planNodes = nodes.map(node => node.id === 'review' ? { ...node, data: { ...node.data, step: { id: 'review-step' } } } : node) as WorkflowNode[]
  const stepJob = { ...job, step_id: 'review-step', route_selections: {} }
  const result = appendTriggerCards(planNodes, edges, [stepJob], { loading: false, selectedID: job.id, onSelect: vi.fn(), onSettings: vi.fn(), onRefresh: vi.fn() })
  expect(result.edges.filter(edge => edge.source === `workflow-trigger-${job.id}`).map(edge => edge.target)).toEqual(['review'])
  expect(triggerRouteSummary(stepJob, planNodes).label).toBe('Step only: Review')
  const traced = traceTriggerGraph(result.nodes, result.edges, stepJob)
  expect(traced.nodes.find(node => node.id === 'review')?.style?.opacity).toBe(1)
  expect(traced.nodes.find(node => node.id === 'prepare')?.style?.opacity).toBe(0.14)
  expect(traced.nodes.find(node => node.id === 'end')?.style?.opacity).toBe(0.14)
  expect(triggerRouteSummary({ ...stepJob, step_id: 'deleted' }, planNodes).canTrace).toBe(false)
})
