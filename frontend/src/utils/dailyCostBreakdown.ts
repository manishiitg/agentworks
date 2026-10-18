import type {
  ModelTokenUsage,
  PhaseTokenUsageFile,
  StepExecutionLogs,
  TokenUsageFile,
} from '../services/api-types'

export interface DailyCostRun {
  runFolder: string
  tokenUsage: TokenUsageFile | null
  evaluationTokenUsage?: TokenUsageFile | null
  steps?: Record<string, StepExecutionLogs>
}

export interface PhaseDailyCostInput {
  date: string
  tokenUsage: PhaseTokenUsageFile
}

export interface RunDailyCostInput {
  date: string
  scope: string
  groupFolder: string
  runFolder: string
  tokenUsage: TokenUsageFile
}

export interface DailyModelCostEntry {
  modelID: string
  provider: string
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  reasoningTokens: number
  llmCalls: number
  totalCost: number
}

export interface DailyStepCostEntry {
  key: string
  stepID: string
  stepTitle: string
  stageLabel: string
  groupLabel: string
  runFolder: string
  totalCost: number
  inputTokens: number
  outputTokens: number
  llmCalls: number
  models: DailyModelCostEntry[]
}

export const formatPhaseTitle = (phaseID: string) => {
  const phaseTitles: Record<string, string> = {
    'workflow-builder': 'Automation Builder',
    'report-execution': 'Dashboard Execution',
    planning: 'Planning',
    'plan-improvement': 'Plan Improvement',
    'evaluation-builder': 'Evaluation Builder',
    'output-builder': 'Output Builder'
  }

  if (phaseTitles[phaseID]) return phaseTitles[phaseID]

  return phaseID
    .split(/[-_]/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

export type StageBucket = 'execution' | 'learning' | 'reflection' | 'evaluation' | 'knowledgebase' | 'routing' | 'workshop' | 'other'

export const classifyPhase = (phase: string): StageBucket => {
  if (phase === 'execution_only') return 'execution'
  // Must precede the 'learning' check. The reflection turn (PLAT-055) replaced
  // the separate learning and KB turns, so it writes BOTH stores and belongs in
  // neither legacy bucket. It gets its own so "what does reflection cost" is
  // answerable — which is the whole reason the Go side attributes it separately.
  if (phase === 'reflection') return 'reflection'
  if (phase === 'success_learning' || phase === 'failure_learning' || phase.includes('learning')) return 'learning'
  if (phase === 'evaluation_scoring' || phase.startsWith('evaluation')) return 'evaluation'
  if (phase.startsWith('kb_')) return 'knowledgebase'
  if (phase === 'workflow_orchestrator' || phase === 'todo_task' || phase === 'routing' || phase.includes('routing')) return 'routing'
  if (phase === 'review_step_code' || phase === 'goal_advisor' || phase === 'plan_change' || phase === 'replan_workflow_from_results') return 'workshop'
  return 'other'
}

const formatStageLabel = (phase: string, scope?: string) => {
  if (scope === 'evaluation') return 'Evaluation'
  const labels: Record<StageBucket, string> = {
    execution: 'Execution',
    learning: 'Historical learning',
    reflection: 'Historical reflection',
    evaluation: 'Evaluation',
    knowledgebase: 'Knowledgebase',
    routing: 'Routing',
    workshop: 'Workshop',
    other: formatPhaseTitle(phase)
  }
  return labels[classifyPhase(phase)]
}

const addModelUsage = (target: DailyModelCostEntry, usage: ModelTokenUsage) => {
  target.inputTokens += usage.input_tokens || 0
  target.outputTokens += usage.output_tokens || 0
  target.cacheReadTokens += usage.cache_read_tokens || usage.cache_tokens || 0
  target.cacheWriteTokens += usage.cache_write_tokens || 0
  target.reasoningTokens += usage.reasoning_tokens || 0
  target.llmCalls += usage.llm_call_count || 0
  target.totalCost += usage.total_cost_usd || 0
}

const runDate = (run: DailyCostRun) => {
  const timestamp = run.tokenUsage?.updated_at || run.evaluationTokenUsage?.updated_at ||
    run.tokenUsage?.created_at || run.evaluationTokenUsage?.created_at
  if (!timestamp) return null
  const parsed = new Date(timestamp)
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString().slice(0, 10)
}

const groupLabel = (daily: Pick<RunDailyCostInput, 'groupFolder' | 'runFolder'>) => {
  if (daily.groupFolder && daily.groupFolder !== '__ungrouped__') return daily.groupFolder
  const parts = daily.runFolder.split('/').filter(Boolean)
  return parts.length > 1 ? parts.slice(1).join('/') : ''
}

const rowKey = (daily: RunDailyCostInput, stepKey: string) =>
  `${daily.scope}:${daily.groupFolder}:${daily.runFolder}:${stepKey}`

export const buildDailyStepCostsByDate = (
  runCosts: DailyCostRun[],
  runDailyCosts: RunDailyCostInput[],
  phaseDailyCosts: PhaseDailyCostInput[]
) => {
  const stepInfoById = new Map<string, { stepNum: number; title: string }>()
  runCosts.forEach(runCost => {
    Object.entries(runCost.steps || {}).forEach(([key, stepData]) => {
      if (!stepData.step_id) return
      const match = key.match(/step-(\d+)/)
      stepInfoById.set(stepData.step_id, {
        stepNum: match ? parseInt(match[1], 10) : 0,
        title: stepData.title || stepData.step_id
      })
    })
  })

  const byDate = new Map<string, Map<string, DailyStepCostEntry & { modelMap: Map<string, DailyModelCostEntry> }>>()
  const ensureStep = (
    date: string,
    key: string,
    stepID: string,
    stepTitle: string,
    stageLabel: string,
    stepGroup = '',
    runFolder = ''
  ) => {
    let dateMap = byDate.get(date)
    if (!dateMap) {
      dateMap = new Map()
      byDate.set(date, dateMap)
    }
    let step = dateMap.get(key)
    if (!step) {
      step = {
        key,
        stepID,
        stepTitle,
        stageLabel,
        groupLabel: stepGroup,
        runFolder,
        totalCost: 0,
        inputTokens: 0,
        outputTokens: 0,
        llmCalls: 0,
        models: [],
        modelMap: new Map<string, DailyModelCostEntry>()
      }
      dateMap.set(key, step)
    }
    return step
  }

  const addUsageToStep = (
    step: DailyStepCostEntry & { modelMap: Map<string, DailyModelCostEntry> },
    modelID: string,
    usage: ModelTokenUsage
  ) => {
    step.inputTokens += usage.input_tokens || 0
    step.outputTokens += usage.output_tokens || 0
    step.llmCalls += usage.llm_call_count || 0
    step.totalCost += usage.total_cost_usd || 0
    let model = step.modelMap.get(modelID)
    if (!model) {
      model = {
        modelID,
        provider: usage.provider || 'unknown',
        inputTokens: 0,
        outputTokens: 0,
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        reasoningTokens: 0,
        llmCalls: 0,
        totalCost: 0
      }
      step.modelMap.set(modelID, model)
    }
    addModelUsage(model, usage)
  }

  runDailyCosts.forEach(daily => {
    const stepGroup = groupLabel(daily)
    const matchingRun = runCosts.find(runCost => runCost.runFolder === daily.runFolder)

    // Reused iteration folders only retain the latest execution logs. Seed
    // zero-cost rows solely on that latest date so old days are not rewritten.
    if (daily.scope === 'execution' && matchingRun && runDate(matchingRun) === daily.date) {
      Object.values(matchingRun.steps || {}).forEach(stepData => {
        if (!stepData.step_id) return
        ensureStep(
          daily.date,
          rowKey(daily, `execution_only:${stepData.step_id}`),
          stepData.step_id,
          stepData.title || stepData.step_id,
          'Execution',
          stepGroup,
          daily.runFolder
        )
      })
    }

    const stepMap = daily.tokenUsage.by_step_and_model || {}
    Object.entries(stepMap).forEach(([stepKey, modelMap]) => {
      const parts = stepKey.split(':')
      const phase = parts[0] || ''
      const stepID = parts.slice(1).join(':') || phase || 'run'
      const info = stepInfoById.get(stepID)
      const stepTitle = stepID === 'workflow_orchestrator'
        ? 'Workflow Orchestrator'
        : info
          ? (info.stepNum > 0 ? `Step ${info.stepNum}: ${info.title}` : info.title)
          : stepID
      const step = ensureStep(
        daily.date,
        rowKey(daily, stepKey),
        stepID,
        stepTitle,
        stepID === 'workflow_orchestrator' ? 'Orchestration' : formatStageLabel(phase, daily.scope),
        stepGroup,
        daily.runFolder
      )
      Object.entries(modelMap).forEach(([modelID, usage]) => addUsageToStep(step, modelID, usage))
    })

    // Reconcile model totals against attributed step totals. This handles both
    // fully historical step-less files and partially attributed ledgers while
    // guaranteeing that detail rows still add up to the daily total.
    const unattributedByModel = Object.entries(daily.tokenUsage.by_model || {}).flatMap(([modelID, total]) => {
      const attributed = Object.values(stepMap).reduce<ModelTokenUsage | null>((sum, modelMap) => {
        const modelUsage = modelMap[modelID]
        if (!modelUsage) return sum
        if (!sum) return { ...modelUsage }
        return {
          ...sum,
          input_tokens: sum.input_tokens + (modelUsage.input_tokens || 0),
          output_tokens: sum.output_tokens + (modelUsage.output_tokens || 0),
          cache_tokens: sum.cache_tokens + (modelUsage.cache_tokens || 0),
          cache_read_tokens: (sum.cache_read_tokens || 0) + (modelUsage.cache_read_tokens || 0),
          cache_write_tokens: (sum.cache_write_tokens || 0) + (modelUsage.cache_write_tokens || 0),
          reasoning_tokens: sum.reasoning_tokens + (modelUsage.reasoning_tokens || 0),
          llm_call_count: sum.llm_call_count + (modelUsage.llm_call_count || 0),
          total_cost_usd: (sum.total_cost_usd || 0) + (modelUsage.total_cost_usd || 0),
        }
      }, null)
      const remainder: ModelTokenUsage = {
        ...total,
        input_tokens: Math.max(0, (total.input_tokens || 0) - (attributed?.input_tokens || 0)),
        output_tokens: Math.max(0, (total.output_tokens || 0) - (attributed?.output_tokens || 0)),
        cache_tokens: Math.max(0, (total.cache_tokens || 0) - (attributed?.cache_tokens || 0)),
        cache_read_tokens: Math.max(0, (total.cache_read_tokens || 0) - (attributed?.cache_read_tokens || 0)),
        cache_write_tokens: Math.max(0, (total.cache_write_tokens || 0) - (attributed?.cache_write_tokens || 0)),
        reasoning_tokens: Math.max(0, (total.reasoning_tokens || 0) - (attributed?.reasoning_tokens || 0)),
        llm_call_count: Math.max(0, (total.llm_call_count || 0) - (attributed?.llm_call_count || 0)),
        total_cost_usd: Math.max(0, (total.total_cost_usd || 0) - (attributed?.total_cost_usd || 0)),
      }
      const hasRemainder = remainder.input_tokens > 0 || remainder.output_tokens > 0 ||
        (remainder.cache_tokens || 0) > 0 || (remainder.reasoning_tokens || 0) > 0 ||
        remainder.llm_call_count > 0 || (remainder.total_cost_usd || 0) > 0.0000001
      return hasRemainder ? [[modelID, remainder] as const] : []
    })

    if (unattributedByModel.length > 0) {
      const isEvaluation = daily.scope === 'evaluation'
      const stepID = isEvaluation ? 'unattributed-evaluation' : 'unattributed-execution'
      const step = ensureStep(
        daily.date,
        rowKey(daily, 'unattributed'),
        stepID,
        isEvaluation ? 'Evaluation (unattributed)' : 'Workflow Orchestrator (unattributed)',
        isEvaluation ? 'Evaluation' : 'Orchestration',
        stepGroup,
        daily.runFolder
      )
      unattributedByModel.forEach(([modelID, modelUsage]) => addUsageToStep(step, modelID, modelUsage))
    }
  })

  phaseDailyCosts.forEach(daily => {
    const phaseMap = daily.tokenUsage.by_phase_and_model || {}
    Object.entries(phaseMap).forEach(([phaseID, modelMap]) => {
      const step = ensureStep(daily.date, `builder:${phaseID}`, phaseID, formatPhaseTitle(phaseID), 'Builder')
      Object.entries(modelMap).forEach(([modelID, usage]) => addUsageToStep(step, modelID, usage))
    })
    if (Object.keys(phaseMap).length === 0 && daily.tokenUsage.by_model) {
      const step = ensureStep(daily.date, 'builder:total', 'builder', 'Automation Builder', 'Builder')
      Object.entries(daily.tokenUsage.by_model).forEach(([modelID, usage]) => addUsageToStep(step, modelID, usage))
    }
  })

  return new Map(Array.from(byDate.entries()).map(([date, stepMap]) => {
    const steps = Array.from(stepMap.values()).map(({ modelMap, ...step }) => ({
      ...step,
      models: Array.from(modelMap.values()).sort((a, b) => b.totalCost - a.totalCost || a.modelID.localeCompare(b.modelID))
    }))
    steps.sort((a, b) => {
      const groupCompare = a.groupLabel.localeCompare(b.groupLabel)
      if (groupCompare !== 0) return groupCompare
      return b.totalCost - a.totalCost || a.stepTitle.localeCompare(b.stepTitle)
    })
    return [date, steps] as const
  }))
}
