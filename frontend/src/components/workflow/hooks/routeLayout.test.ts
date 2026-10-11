// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest'
import { distributePrimaryRouteLanes, type WorkflowNode } from './usePlanToFlow'
import type { PlanningResponse } from '../../../utils/stepConfigMatching'
vi.mock('../../../hooks/useActiveWorkflowPreset', () => ({ useActiveWorkflowPreset: () => null }))
vi.mock('../../../stores/useLLMStore', () => ({ useLLMStore: () => ({}) }))
const step = (id: string, next_step_id = 'end') => ({ id, type: 'scripted', title: id, next_step_id })
const route = (id: string) => ({ route_id: id, route_name: id, next_step_id: id })
describe('major route layout', () => {
  it('gives six routes distinct lanes, orders edges instead of array order, and separates joins', () => {
    const plan = { steps: [
      { id: 'router', type: 'routing', routes: ['a', 'b', 'c', 'd', 'e', 'f'].map(route) },
      step('a', 'gate'), { id: 'gate', type: 'branch', routes: ['b', 'c', 'join'].map(route) },
      step('b', 'join'), step('c', 'join'), step('d-after', 'join'), step('d', 'd-after'),
      step('e', 'join-two'), step('f', 'join-two'), step('join', 'join-two'), step('join-two'),
    ] } as unknown as PlanningResponse
    const nodes = plan.steps.map(s => ({ id: s.id, type: s.type === 'routing' || s.type === 'branch' ? s.type : 'step',
      position: { x: 440, y: 400 }, data: { id: s.id, title: s.id, step: s, status: 'pending', routes: 'routes' in s ? s.routes : undefined },
    })) as WorkflowNode[]
    const result = distributePrimaryRouteLanes(nodes, plan)
    const pos = (id: string) => result.find(n => n.id === id)!.position
    const lanes = ['a', 'b', 'c', 'd', 'e', 'f'].map(id => pos(id).x).sort((a,b) => a-b)
    expect(new Set(lanes).size).toBe(6)
    expect(lanes.slice(1).every((x,i) => x - lanes[i] >= 520)).toBe(true)
    expect(pos('d').y).toBeLessThan(pos('d-after').y)
    expect(pos('join').y).toBeLessThan(pos('join-two').y)
    expect(pos('b').y).toBeGreaterThanOrEqual(pos('gate').y)
    expect(new Set(result.map(n => `${n.position.x},${n.position.y}`)).size).toBe(result.length)
  })
})
