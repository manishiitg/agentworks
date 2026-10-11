import { inputTokens } from './costTokens'
import type { CostAggregate, CostExecutionAggregate, CostSummary, WorkflowActivityTimingAggregate, WorkflowActivityTimingSummary } from '../services/api-types'

export interface ActivityTiming {
  duration_ms: number
  llm_duration_ms: number
  tool_duration_ms: number
}

export interface CostActivityCategory {
  id: 'builder' | 'pulse' | 'workflow' | 'evaluation' | 'schedule'
  label: string
  description: string
  total: CostAggregate
  timing: ActivityTiming
  executions: Array<{ id: string; label: string; cost: CostExecutionAggregate; timing: ActivityTiming }>
}

const emptyCost = (): CostAggregate => ({
  input_tokens: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  reasoning_tokens: 0,
  cache_read_tokens: 0,
  cache_write_tokens: 0,
  total_cost_usd: 0,
  call_count: 0,
  llm_generation_duration_ms: 0,
})

const finiteNumber = (value: unknown): number => {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

const addCost = (target: CostAggregate, source?: Partial<CostAggregate>) => {
  if (!source) return
  target.input_tokens = (target.input_tokens ?? 0) + inputTokens(source)
  target.unpriced_call_count = (target.unpriced_call_count ?? 0) + finiteNumber(source.unpriced_call_count)
  target.missing_usage_call_count = (target.missing_usage_call_count ?? 0) + finiteNumber(source.missing_usage_call_count)
  target.prompt_tokens += finiteNumber(source.prompt_tokens)
  target.completion_tokens += finiteNumber(source.completion_tokens)
  target.reasoning_tokens += finiteNumber(source.reasoning_tokens)
  target.cache_read_tokens += finiteNumber(source.cache_read_tokens)
  target.cache_write_tokens += finiteNumber(source.cache_write_tokens)
  target.total_cost_usd += finiteNumber(source.total_cost_usd)
  target.call_count += finiteNumber(source.call_count)
  target.llm_generation_duration_ms = finiteNumber(target.llm_generation_duration_ms) + finiteNumber(source.llm_generation_duration_ms)
}

const emptyExecutionCost = (): CostExecutionAggregate => emptyCost()

// addExecutionCost merges one execution's cost AND its phase breakdown
// (PLAT-166) into a running total. Phase merging matters because
// executionGroup below can fold several raw execution ids (retries/dispatch
// variants of the same canonical step) into one row — each may carry its own
// `by_phase`, and those must sum per-phase, not just at the combined total.
const addExecutionCost = (target: CostExecutionAggregate, source?: CostExecutionAggregate) => {
  addCost(target, source)
  if (!source?.by_phase) return
  if (!target.by_phase) target.by_phase = {}
  for (const [phase, phaseCost] of Object.entries(source.by_phase)) {
    const existing = target.by_phase[phase] || emptyCost()
    addCost(existing, phaseCost)
    target.by_phase[phase] = existing
  }
}

const emptyTiming = (): ActivityTiming => ({ duration_ms: 0, llm_duration_ms: 0, tool_duration_ms: 0 })

const addTiming = (target: ActivityTiming, source?: Partial<WorkflowActivityTimingAggregate>) => {
  if (!source) return
  target.duration_ms += source.duration_ms || 0
  target.llm_duration_ms += source.llm_duration_ms || 0
  target.tool_duration_ms += source.tool_duration_ms || 0
}

const executionLabel = (id: string) => {
  if (id === 'unattributed') return 'Unattributed work'
  if (id.startsWith('session:')) return 'Interactive session'
  if (id.startsWith('step:')) return id.slice('step:'.length).replace(/[-_]+/g, ' ')
  if (id.startsWith('evaluation:')) return id.slice('evaluation:'.length).replace(/[-_]+/g, ' ')
  return id
    .replace(/^(exec|bg|eval-full|query)[-_:]+/i, '')
    .replace(/[-_]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim() || id
}

// phaseLabel prettifies a costledger.Entry.Phase value for display (PLAT-166,
// generalized per-agent-item in PLAT-167). Unlike executionLabel,
// this is not an id-grouping key — it is shown verbatim as a sub-line label
// under an execution row, so it stays a plain string→string function.
export const phaseLabel = (phase: string) => {
  if (phase === 'execution_only') return 'Execution'
  if (phase === 'reflection') return 'Reflection'
  if (phase.startsWith('item:')) return phase.slice('item:'.length).replace(/[-_]+/g, ' ').trim() || phase
  return phase.replace(/[-_]+/g, ' ').trim() || phase
}

const executionGroup = (category: CostActivityCategory['id'], scope: string, id: string) => {
  if (category === 'builder') {
    return scope === 'chat' ? 'interactive workflow chat' : 'workflow builder'
  }
  if (category === 'workflow') {
    const canonicalStep = (value: string) => value.replace(/^step:/, '').replace(/^step-/, '')
    if (id.startsWith('step:')) return `step:${canonicalStep(id)}`
    const dispatchedStep = id.match(/^exec-step-(.+)-\d{16,}$/)?.[1]
    if (dispatchedStep) return `step:${canonicalStep(dispatchedStep)}`
    const directStep = id.match(/^exec-(.+?)(?:-\d{16,})?$/)?.[1]
    return directStep ? `step:${canonicalStep(directStep)}` : id.replace(/-\d{16,}$/, '')
  }
  if (category === 'evaluation') {
    const run = id.match(/^eval-full-(.+)-\d{16,}$/)?.[1]
    return run ? `evaluation:${run}` : id.replace(/-\d{16,}$/, '')
  }
  if (category === 'pulse') {
    // Historical executions retain the engineering-review ID, but the product
    // presents this consolidated technical pass as Pulse Review.
    if (/engineering-review|workflow-review/i.test(id)) return 'pulse review and fix'
    if (/strategy-auditor/i.test(id)) return 'strategy auditor'
    if (/goal-advisor/i.test(id)) return 'goal advisor'
    if (/pulse-fixer|fixer/i.test(id)) return 'pulse fixer'
    if (/stores-health/i.test(id)) return 'stores health'
    if (/^query[_-]/i.test(id)) return 'pulse main turn'
  }
  return id.replace(/-\d{16,}$/, '')
}

const definitions: Array<Omit<CostActivityCategory, 'total' | 'timing' | 'executions'> & { scopes: string[] }> = [
  {
    id: 'builder',
    label: 'Builder',
    description: 'Workflow building and interactive workflow chat',
    scopes: ['builder', 'chat'],
  },
  {
    id: 'pulse',
    label: 'Pulse',
    description: 'Includes Pulse background agents: reviews, fixes, strategy, and goal advice',
    scopes: ['pulse'],
  },
  {
    id: 'workflow',
    label: 'Workflow',
    description: 'Production runs and their individual steps',
    scopes: ['workflow_execution'],
  },
  {
    id: 'evaluation',
    label: 'Evaluation',
    description: 'Evaluation runs and scoring steps',
    scopes: ['evaluation'],
  },
  {
    id: 'schedule',
    label: 'Schedules',
    description: 'Project schedules, reminders and triggers, with their sub-agents',
    scopes: ['schedule'],
  },
]

export const buildCostActivityBreakdown = (
  summary?: Pick<CostSummary, 'by_scope'> | null,
  timingSummary?: Pick<WorkflowActivityTimingSummary, 'by_scope'> | null,
): CostActivityCategory[] => {
  const byScope = summary?.by_scope || {}
  const timingByScope = timingSummary?.by_scope || {}
  return definitions.map(definition => {
    const total = emptyCost()
    const timing = emptyTiming()
    const byExecution = new Map<string, CostExecutionAggregate>()
    const timingByExecution = new Map<string, ActivityTiming>()

    for (const scopeName of definition.scopes) {
      const scope = byScope[scopeName]
      addCost(total, scope)
      for (const [executionID, cost] of Object.entries(scope?.by_execution || {})) {
        const groupID = executionGroup(definition.id, scopeName, executionID)
        const aggregate = byExecution.get(groupID) || emptyExecutionCost()
        addExecutionCost(aggregate, cost)
        byExecution.set(groupID, aggregate)
      }
      const scopeTiming = timingByScope[scopeName]
      addTiming(timing, scopeTiming)
      for (const [executionID, executionTiming] of Object.entries(scopeTiming?.by_execution || {})) {
        const groupID = executionGroup(definition.id, scopeName, executionID)
        const aggregate = timingByExecution.get(groupID) || emptyTiming()
        addTiming(aggregate, executionTiming)
        timingByExecution.set(groupID, aggregate)
      }
    }

    const executionIDs = new Set([...byExecution.keys(), ...timingByExecution.keys()])
    const executions = Array.from(executionIDs)
      .map(id => ({
        id,
        label: executionLabel(id),
        cost: byExecution.get(id) || emptyExecutionCost(),
        timing: timingByExecution.get(id) || emptyTiming(),
      }))
      .sort((a, b) => b.cost.total_cost_usd - a.cost.total_cost_usd || b.timing.duration_ms - a.timing.duration_ms)

    return { ...definition, total, timing, executions }
  })
}
