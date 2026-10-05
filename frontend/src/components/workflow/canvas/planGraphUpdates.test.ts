import { expect, it } from 'vitest'
import { planEdgesChanged, planNodesChanged } from './planGraphUpdates'
import type { WorkflowNode, WorkflowEdge } from '../hooks/usePlanToFlow'

it('applies changed card and route content even when graph IDs and positions stay the same', () => {
  const nodes: WorkflowNode[] = [{
    id: 'output', type: 'workflow-artifact', position: { x: 10, y: 20 },
    data: { id: 'output', title: 'Output', kind: 'output', configured: true, detail: 'Old destination' },
  }]
  const edges: WorkflowEdge[] = [{
    id: 'route-to-output', source: 'route', target: 'output', sourceHandle: 'route-a',
    label: 'Old route', data: { selected: true }, style: { stroke: 'green' },
  }]
  const copy = <T,>(value: T): T => JSON.parse(JSON.stringify(value))
  expect(planNodesChanged(nodes, copy(nodes))).toBe(false)
  expect(planEdgesChanged(edges, copy(edges))).toBe(false)
  expect(planNodesChanged(nodes, [{ ...nodes[0], data: { ...nodes[0].data, detail: 'New destination' } }])).toBe(true)
  expect(planNodesChanged(nodes, [{ ...nodes[0], type: 'step' }])).toBe(true)
  expect(planEdgesChanged(edges, [{ ...edges[0], label: 'New route' }])).toBe(true)
  expect(planEdgesChanged(edges, [{ ...edges[0], sourceHandle: 'handoff-a' }])).toBe(true)
  expect(planEdgesChanged(edges, [{ ...edges[0], data: { selected: false }, style: { stroke: 'gray' } }])).toBe(true)
})
