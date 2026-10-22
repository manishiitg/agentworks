import { describe, expect, it } from 'vitest'
import {
  effectiveExecutionMode,
  effectiveAgentItems,
  configuredAgentItems,
  type PlanStep,
} from '../stepConfigMatching'

// PLAT-287: a step's execution model is decided by its plan type.
describe('effectiveExecutionMode', () => {
  it('treats a scripted step as scripted and an agent step as agentic', () => {
    const scripted: PlanStep = { id: 'a', type: 'scripted', title: 'A', description: 'a' }
    const agent: PlanStep = { id: 'b', type: 'agent', title: 'B', description: 'b', items: [] }
    expect(effectiveExecutionMode(scripted)).toBe('scripted')
    expect(effectiveExecutionMode(agent)).toBe('agentic')
    expect(configuredAgentItems(scripted)).toEqual([])
  })

  it('has no execution mode for routing and human-input steps', () => {
    const routing = { id: 'r', type: 'routing', title: 'R', description: 'r', routing_question: 'q', routes: [] } as unknown as PlanStep
    const human = { id: 'h', type: 'human_input', title: 'H', description: 'h', question: 'q' } as unknown as PlanStep
    expect(effectiveExecutionMode(routing)).toBeUndefined()
    expect(effectiveExecutionMode(human)).toBeUndefined()
  })

  it('treats a plan step without a type as the default, scripted', () => {
    expect(effectiveExecutionMode({ agent_configs: {} })).toBe('scripted')
    expect(effectiveExecutionMode(undefined)).toBeUndefined()
  })
})

describe('effectiveAgentItems', () => {
  it('presents legacy orchestrator messages as the canonical Agent queue', () => {
    const step: PlanStep = {
      id: 'legacy-agent',
      type: 'orchestrator',
      title: 'Legacy agent',
      messages: [
        { id: 'ask', type: 'message', message: 'Inspect the workspace.' },
        { id: 'gate', type: 'prevalidation', validation_schema: { files: [] } },
        { id: 'rows', type: 'foreach', source: 'tasks.json', source_path: 'tasks', message: 'Process the row.' },
      ],
    }

    expect(effectiveAgentItems(step).map(item => item.type)).toEqual([
      'user_message',
      'prevalidation',
      'foreach',
    ])
  })

  it('gives a legacy agent without messages the runtime-compatible execution turn', () => {
    const step: PlanStep = { id: 'legacy-agent', type: 'todo_task', title: 'Legacy agent' }
    expect(effectiveAgentItems(step)).toMatchObject([
      { id: 'execute-step-charter', type: 'user_message' },
    ])
  })
})
