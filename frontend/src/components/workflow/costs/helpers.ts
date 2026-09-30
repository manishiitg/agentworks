import {
  classifyPhase,
  formatPhaseTitle,
} from '../../../utils/dailyCostBreakdown'
import type {
  TokenUsageFile,
  StepExecutionLogs,
  PhaseTokenUsageFile,
} from '../../../services/api-types'
import { getDisplayTimeZone } from '../../../utils/displayTime'

// Currency formatting is deployment-independent and can be shared. Timestamp
// formatters are created after capabilities load so they use the deployment's
// configured timezone rather than the viewer's browser timezone.
const usdFormatter = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 4,
  maximumFractionDigits: 4
})
const timestampLabelFormatter = () => {
  const timeZone = getDisplayTimeZone()
  return new Intl.DateTimeFormat('en-US', {
    month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit',
    timeZone, timeZoneName: timeZone ? 'short' : undefined,
  })
}
const runBadgeFormatter = () => new Intl.DateTimeFormat('en-US', {
  month: 'short', day: 'numeric', timeZone: getDisplayTimeZone(),
})

// Format cost in USD
export const formatUSD = (amount?: number) => {
  if (amount === undefined || amount === null) return '$0.00'
  return usdFormatter.format(amount)
}

// Format token count (e.g., 1,234,567 -> 1.23M)
export const formatTokens = (count?: number) => {
  if (!count) return '0'
  if (count >= 1000000) {
    return (count / 1000000).toFixed(2) + 'M'
  }
  if (count >= 1000) {
    return (count / 1000).toFixed(1) + 'K'
  }
  return count.toString()
}

export const formatDuration = (milliseconds?: number) => {
  if (!milliseconds || milliseconds <= 0) return '—'
  const seconds = Math.round(milliseconds / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  if (minutes < 60) return remainingSeconds ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const remainingMinutes = minutes % 60
  return remainingMinutes ? `${hours}h ${remainingMinutes}m` : `${hours}h`
}

export interface RunCosts {
  runFolder: string
  tokenUsage: TokenUsageFile | null
  evaluationTokenUsage?: TokenUsageFile | null
  steps?: Record<string, StepExecutionLogs> // Store steps for title lookup
  costSummary: {
    totalCost: number
    totalLLMCost: number
    totalToolCost: number
    totalInputTokens: number
    totalOutputTokens: number
    totalTokens: number
    totalLLMCalls: number
    totalCacheReadTokens: number
    totalCacheWriteTokens: number
    totalReasoningTokens: number
    stageCosts: {
      execution: number
      learning: number
      reflection: number
      evaluation: number
      knowledgebase: number   // kb_update / kb_reorganize / kb_consolidate
      routing: number         // deterministic routing / todo_task orchestration
      workshop: number        // review_step_code / goal advisor / other workshop agents
      other: number
    }
    stepCosts: Array<{
      stepID: string        // Step ID (e.g., "fetch-pr-data" or phase name for phase-only agents)
      stepTitle: string     // Display title
      stepNum: number       // Step number (for sorting, 0 for non-step entries)
      // Which route (routing/branch major-fork concept, PLAT-259) this step
      // was reached through on this run, if any -- sourced from the same
      // Execution Logs step data already fetched for title lookup below.
      routeId?: string
      routeName?: string
      routeStepId?: string
      routeStepTitle?: string
      execution: number
      learning: number
      reflection: number
      evaluation: number
      knowledgebase: number
      routing: number
      workshop: number
      totalCost: number
      inputTokens: number
      outputTokens: number
      llmCalls: number
    }>
  } | null
}

export interface PhaseCostSummary {
  totalCost: number
  totalInputTokens: number
  totalOutputTokens: number
  totalTokens: number
  totalLLMCalls: number
  totalCacheReadTokens: number
  totalCacheWriteTokens: number
  totalReasoningTokens: number
  createdAt: string | null
  updatedAt: string | null
  phaseCosts: Array<{
    phaseID: string
    phaseTitle: string
    totalCost: number
    inputTokens: number
    outputTokens: number
    llmCalls: number
  }>
  modelCosts: Array<{
    modelID: string
    provider: string
    totalCost: number
    inputTokens: number
    outputTokens: number
    llmCalls: number
  }>
}

export interface PhaseDailyCostSummaryEntry {
  date: string
  tokenUsage: PhaseTokenUsageFile
  summary: PhaseCostSummary
}

export interface RunDailyCostSummaryEntry {
  date: string
  scope: string
  groupFolder: string
  runFolder: string
  updatedAt: string | null
  tokenUsage: TokenUsageFile
  summary: NonNullable<RunCosts['costSummary']>
}

export interface CombinedDailyCostSummaryEntry {
  date: string
  workflowCost: number
  evaluationCost: number
  builderCost: number
  pulseCost: number | null
  totalCost: number
  workflowTokens: number
  evaluationTokens: number
  builderTokens: number
  pulseTokens: number | null
  totalTokens: number
  totalInputTokens?: number
  totalOutputTokens?: number
  llmDurationMS: number
  runCount: number
}

export const getRunFolderDisplayName = (runFolder: string) => {
  // Show "iteration-N · group" so both dimensions are visible. Single-segment
  // values (legacy layout, or iteration-only) fall through unchanged.
  const parts = runFolder.split('/').filter(Boolean)
  if (parts.length >= 2) {
    return `${parts[0]} · ${parts.slice(1).join('/')}`
  }
  return parts[parts.length - 1] || runFolder
}

// classifyPhase maps a backend phase name to one of the UI stage buckets.
// Order matters: check specific prefixes before falling through to includes().
// Known phases (see comment in calculateCostSummary):
//   execution_only, success_learning, failure_learning,
//   routing, todo_task, kb_update, kb_reorganize,
//   kb_consolidate, review_step_code,
//   Goal Advisor proposal/application work, evaluation_scoring.
const getRunTimestamp = (runCost: Pick<RunCosts, 'tokenUsage' | 'evaluationTokenUsage'>) => {
  const timestamp =
    runCost.tokenUsage?.updated_at ||
    runCost.evaluationTokenUsage?.updated_at ||
    runCost.tokenUsage?.created_at ||
    runCost.evaluationTokenUsage?.created_at

  if (!timestamp) return null

  const parsed = new Date(timestamp)
  if (Number.isNaN(parsed.getTime())) return null
  return parsed
}

const formatRunTimestampLabel = (runCost: Pick<RunCosts, 'tokenUsage' | 'evaluationTokenUsage'>) => {
  const timestamp = getRunTimestamp(runCost)
  return timestamp ? timestampLabelFormatter().format(timestamp) : ''
}

export const formatTimestampLabel = (timestamp?: string | null) => {
  if (!timestamp) return ''
  const parsed = new Date(timestamp)
  if (Number.isNaN(parsed.getTime())) return ''
  return timestampLabelFormatter().format(parsed)
}

export const compareRunCosts = (a: RunCosts, b: RunCosts, selectedRunFolder: string | null) => {
  if (selectedRunFolder) {
    if (a.runFolder === selectedRunFolder && b.runFolder !== selectedRunFolder) return -1
    if (b.runFolder === selectedRunFolder && a.runFolder !== selectedRunFolder) return 1
  }

  const timestampA = getRunTimestamp(a)
  const timestampB = getRunTimestamp(b)
  if (timestampA && timestampB && timestampA.getTime() !== timestampB.getTime()) {
    return timestampB.getTime() - timestampA.getTime()
  }
  if (timestampA && !timestampB) return -1
  if (!timestampA && timestampB) return 1

  const displayCompare = getRunFolderDisplayName(a.runFolder).localeCompare(getRunFolderDisplayName(b.runFolder))
  if (displayCompare !== 0) return displayCompare

  return b.runFolder.localeCompare(a.runFolder)
}

export const getRunFolderSecondaryLabel = (runCost: RunCosts) => {
  const timestampLabel = formatRunTimestampLabel(runCost)
  if (timestampLabel) return timestampLabel

  const displayName = getRunFolderDisplayName(runCost.runFolder)
  return displayName === runCost.runFolder ? '' : runCost.runFolder
}

export const getRunFolderTitle = (runCost: RunCosts) => {
  const secondary = getRunFolderSecondaryLabel(runCost)
  return secondary ? `${runCost.runFolder}\n${secondary}` : runCost.runFolder
}

export const getRunBadgeLabel = (runCost: RunCosts) => {
  const timestamp = getRunTimestamp(runCost)
  return timestamp ? runBadgeFormatter().format(timestamp) : 'Run'
}

// Calculate cost summary from token usage
export const calculateCostSummary = (tokenUsage: TokenUsageFile | null, evaluationTokenUsage: TokenUsageFile | null | undefined, steps?: Record<string, StepExecutionLogs>): RunCosts['costSummary'] => {
  if (!tokenUsage?.by_model && !tokenUsage?.by_tool && !evaluationTokenUsage?.by_model && !evaluationTokenUsage?.by_tool) return null

  let totalCost = 0
  let totalLLMCost = 0
  let totalToolCost = 0
  let totalInputTokens = 0
  let totalOutputTokens = 0
  let totalLLMCalls = 0
  let totalCacheReadTokens = 0
  let totalCacheWriteTokens = 0
  let totalReasoningTokens = 0

  const stageCosts = {
    execution: 0,
    learning: 0,
    reflection: 0,
    evaluation: 0,
    knowledgebase: 0,
    routing: 0,
    workshop: 0,
    other: 0
  }

  const stepCosts: Record<string, {
    stepID: string
    stepNum: number
    stepTitle: string
    routeId?: string
    routeName?: string
    routeStepId?: string
    routeStepTitle?: string
    execution: number
    learning: number
    reflection: number
    evaluation: number
    knowledgebase: number
    routing: number
    workshop: number
    totalCost: number
    inputTokens: number
    outputTokens: number
    llmCalls: number
  }> = {}

  // Calculate totals from by_model
  if (tokenUsage?.by_model) {
    Object.values(tokenUsage.by_model).forEach(usage => {
      totalCost += usage.total_cost_usd || 0
      totalLLMCost += usage.total_cost_usd || 0
      totalInputTokens += usage.input_tokens || 0
      totalOutputTokens += usage.output_tokens || 0
      totalLLMCalls += usage.llm_call_count || 0
      totalCacheReadTokens += usage.cache_read_tokens || usage.cache_tokens || 0
      totalCacheWriteTokens += usage.cache_write_tokens || 0
      totalReasoningTokens += usage.reasoning_tokens || 0
    })
  }

  if (tokenUsage?.by_tool) {
    Object.values(tokenUsage.by_tool).forEach(usage => {
      const cost = usage.total_cost_usd || 0
      totalCost += cost
      totalToolCost += cost
    })
    if (!tokenUsage.by_step_and_tool) {
      stageCosts.execution += Object.values(tokenUsage.by_tool).reduce((sum, usage) => sum + (usage.total_cost_usd || 0), 0)
    }
  }

  // Helper to find step number, title, and route membership from stepID
  const findStepInfo = (stepID: string): {
    stepNum: number
    stepTitle: string
    routeId?: string
    routeName?: string
    routeStepId?: string
    routeStepTitle?: string
  } => {
    // Try to find the step in the steps data by matching the step ID
    if (steps) {
      for (const [key, stepData] of Object.entries(steps)) {
        if (stepData.step_id === stepID) {
          // Extract step number from key (e.g., "step-1" -> 1)
          const match = key.match(/step-(\d+)/)
          const stepNum = match ? parseInt(match[1], 10) : 0
          return {
            stepNum,
            stepTitle: stepData.title || stepID,
            ...(stepData.route_kind === 'routing' && stepData.route_id
              ? {
                  routeId: stepData.route_id,
                  routeName: stepData.route_name,
                  routeStepId: stepData.route_step_id,
                  routeStepTitle: stepData.route_step_title,
                }
              : {}),
          }
        }
      }
    }
    // If not found, it might be a phase-only agent (use phase name as display)
    return { stepNum: 0, stepTitle: stepID }
  }

  // Calculate stage costs and step-wise costs from by_step_and_model
  if (tokenUsage?.by_step_and_model) {
    Object.entries(tokenUsage.by_step_and_model).forEach(([key, modelMap]) => {
      const parts = key.split(':')
      const phase = parts[0]
      const stepID = parts[1] || ''  // New format: stepID instead of index

      let cost = 0
      let inputTokens = 0
      let outputTokens = 0
      let llmCalls = 0
      Object.values(modelMap).forEach(u => {
        cost += u.total_cost_usd || 0
        inputTokens += u.input_tokens || 0
        outputTokens += u.output_tokens || 0
        llmCalls += u.llm_call_count || 0
      })

      // Stage dispatch — must stay in sync with the Go phases that actually emit
      // token usage. See controller_execution.go (execution_only), controller_learning.go
      // (success_learning/failure_learning), deterministic routing,
      // controller_todo_task.go (todo_task), controller_kb_update.go (kb_*),
      // interactive_workshop_manager.go (review_step_code /
      // Goal Advisor proposal/application work), evaluation_scoring agents.
      const stageBucket = classifyPhase(phase)
      stageCosts[stageBucket] += cost

      // Step-wise costs - group by stepID
      const stepInfo = findStepInfo(stepID)
      const { stepNum, stepTitle } = stepInfo
      const stepKey = stepID  // Use stepID as the key

      if (!stepCosts[stepKey]) {
        stepCosts[stepKey] = {
          stepID,
          stepNum,
          stepTitle,
          routeId: stepInfo.routeId,
          routeName: stepInfo.routeName,
          routeStepId: stepInfo.routeStepId,
          routeStepTitle: stepInfo.routeStepTitle,
          execution: 0,
          learning: 0,
          reflection: 0,
          evaluation: 0,
          knowledgebase: 0,
          routing: 0,
          workshop: 0,
          totalCost: 0,
          inputTokens: 0,
          outputTokens: 0,
          llmCalls: 0
        }
      }
      stepCosts[stepKey].totalCost += cost
      stepCosts[stepKey].inputTokens += inputTokens
      stepCosts[stepKey].outputTokens += outputTokens
      stepCosts[stepKey].llmCalls += llmCalls
      if (stageBucket !== 'other') {
        stepCosts[stepKey][stageBucket] += cost
      }
    })
  }

  if (tokenUsage?.by_step_and_tool) {
    Object.entries(tokenUsage.by_step_and_tool).forEach(([key, toolMap]) => {
      const parts = key.split(':')
      const phase = parts[0]
      const stepID = parts[1] || ''
      const cost = Object.values(toolMap).reduce((sum, u) => sum + (u.total_cost_usd || 0), 0)
      const stageBucket = classifyPhase(phase)
      stageCosts[stageBucket] += cost
      const stepInfo = findStepInfo(stepID)
      const { stepNum, stepTitle } = stepInfo
      const stepKey = stepID || key
      if (!stepCosts[stepKey]) {
        stepCosts[stepKey] = {
          stepID: stepKey,
          stepNum,
          stepTitle: stepTitle || stepKey,
          routeId: stepInfo.routeId,
          routeName: stepInfo.routeName,
          routeStepId: stepInfo.routeStepId,
          routeStepTitle: stepInfo.routeStepTitle,
          execution: 0,
          learning: 0,
          reflection: 0,
          evaluation: 0,
          knowledgebase: 0,
          routing: 0,
          workshop: 0,
          totalCost: 0,
          inputTokens: 0,
          outputTokens: 0,
          llmCalls: 0
        }
      }
      stepCosts[stepKey].totalCost += cost
      if (stageBucket !== 'other') {
        stepCosts[stepKey][stageBucket] += cost
      }
    })
  }

  // Process evaluation token usage
  if (evaluationTokenUsage?.by_model) {
    Object.values(evaluationTokenUsage.by_model).forEach(usage => {
      totalCost += usage.total_cost_usd || 0
      totalLLMCost += usage.total_cost_usd || 0
      totalInputTokens += usage.input_tokens || 0
      totalOutputTokens += usage.output_tokens || 0
      totalLLMCalls += usage.llm_call_count || 0
      totalCacheReadTokens += usage.cache_read_tokens || usage.cache_tokens || 0
      totalCacheWriteTokens += usage.cache_write_tokens || 0
      totalReasoningTokens += usage.reasoning_tokens || 0
      // All evaluation by_model costs go to evaluation stage
      stageCosts.evaluation += usage.total_cost_usd || 0
    })
  }

  if (evaluationTokenUsage?.by_tool) {
    const evalToolCost = Object.values(evaluationTokenUsage.by_tool).reduce((sum, usage) => sum + (usage.total_cost_usd || 0), 0)
    totalCost += evalToolCost
    totalToolCost += evalToolCost
    if (!evaluationTokenUsage.by_step_and_tool) {
      stageCosts.evaluation += evalToolCost
    }
  }

  // Process evaluation step-wise costs
  if (evaluationTokenUsage?.by_step_and_model) {
    Object.entries(evaluationTokenUsage.by_step_and_model).forEach(([key, modelMap]) => {
      const parts = key.split(':')
      const stepID = parts[1] || parts[0]  // Use stepID from phase:stepID format

      let cost = 0
      let inputTokens = 0
      let outputTokens = 0
      let llmCalls = 0
      Object.values(modelMap).forEach(u => {
        cost += u.total_cost_usd || 0
        inputTokens += u.input_tokens || 0
        outputTokens += u.output_tokens || 0
        llmCalls += u.llm_call_count || 0
      })

      // Step-wise costs - group by stepID with "eval-" prefix
      const stepInfo = findStepInfo(stepID)
      const { stepNum, stepTitle } = stepInfo
      const stepKey = `eval-${stepID}`  // Prefix with eval- to distinguish from regular steps

      if (!stepCosts[stepKey]) {
        stepCosts[stepKey] = {
          stepID: stepKey,
          stepNum: stepNum > 0 ? stepNum + 1000 : 0, // Put eval steps after regular steps
          stepTitle: `[Eval] ${stepTitle}`,
          routeId: stepInfo.routeId,
          routeName: stepInfo.routeName,
          routeStepId: stepInfo.routeStepId,
          routeStepTitle: stepInfo.routeStepTitle,
          execution: 0,
          learning: 0,
          reflection: 0,
          evaluation: 0,
          knowledgebase: 0,
          routing: 0,
          workshop: 0,
          totalCost: 0,
          inputTokens: 0,
          outputTokens: 0,
          llmCalls: 0
        }
      }
      stepCosts[stepKey].totalCost += cost
      stepCosts[stepKey].inputTokens += inputTokens
      stepCosts[stepKey].outputTokens += outputTokens
      stepCosts[stepKey].llmCalls += llmCalls
      stepCosts[stepKey].evaluation += cost
    })
  }

  if (evaluationTokenUsage?.by_step_and_tool) {
    Object.entries(evaluationTokenUsage.by_step_and_tool).forEach(([key, toolMap]) => {
      const parts = key.split(':')
      const stepID = parts[1] || parts[0]
      const cost = Object.values(toolMap).reduce((sum, u) => sum + (u.total_cost_usd || 0), 0)
      const stepInfo = findStepInfo(stepID)
      const { stepNum, stepTitle } = stepInfo
      const stepKey = `eval-${stepID}`
      if (!stepCosts[stepKey]) {
        stepCosts[stepKey] = {
          stepID: stepKey,
          stepNum: stepNum > 0 ? stepNum + 1000 : 0,
          stepTitle: `[Eval] ${stepTitle}`,
          routeId: stepInfo.routeId,
          routeName: stepInfo.routeName,
          routeStepId: stepInfo.routeStepId,
          routeStepTitle: stepInfo.routeStepTitle,
          execution: 0,
          learning: 0,
          reflection: 0,
          evaluation: 0,
          knowledgebase: 0,
          routing: 0,
          workshop: 0,
          totalCost: 0,
          inputTokens: 0,
          outputTokens: 0,
          llmCalls: 0
        }
      }
      stageCosts.evaluation += cost
      stepCosts[stepKey].totalCost += cost
      stepCosts[stepKey].evaluation += cost
    })
  }

  // Sort by step number, then by stepID
  const sortedStepCosts = Object.values(stepCosts).sort((a, b) => {
    if (a.stepNum !== b.stepNum) return a.stepNum - b.stepNum
    return a.stepID.localeCompare(b.stepID)
  })

  return {
    totalCost,
    totalLLMCost,
    totalToolCost,
    totalInputTokens,
    totalOutputTokens,
    totalTokens: totalInputTokens + totalOutputTokens,
    totalLLMCalls,
    totalCacheReadTokens,
    totalCacheWriteTokens,
    totalReasoningTokens,
    stageCosts,
    stepCosts: sortedStepCosts
  }
}

export const calculatePhaseCostSummary = (tokenUsage: PhaseTokenUsageFile | null): PhaseCostSummary | null => {
  if (!tokenUsage?.by_model) return null

  let totalCost = 0
  let totalInputTokens = 0
  let totalOutputTokens = 0
  let totalLLMCalls = 0
  let totalCacheReadTokens = 0
  let totalCacheWriteTokens = 0
  let totalReasoningTokens = 0

  Object.values(tokenUsage.by_model).forEach(usage => {
    totalCost += usage.total_cost_usd || 0
    totalInputTokens += usage.input_tokens || 0
    totalOutputTokens += usage.output_tokens || 0
    totalLLMCalls += usage.llm_call_count || 0
    totalCacheReadTokens += usage.cache_read_tokens || usage.cache_tokens || 0
    totalCacheWriteTokens += usage.cache_write_tokens || 0
    totalReasoningTokens += usage.reasoning_tokens || 0
  })

  const phaseCosts = Object.entries(tokenUsage.by_phase_and_model || {})
    .map(([phaseID, modelMap]) => {
      let cost = 0
      let inputTokens = 0
      let outputTokens = 0
      let llmCalls = 0

      Object.values(modelMap).forEach(usage => {
        cost += usage.total_cost_usd || 0
        inputTokens += usage.input_tokens || 0
        outputTokens += usage.output_tokens || 0
        llmCalls += usage.llm_call_count || 0
      })

      return {
        phaseID,
        phaseTitle: formatPhaseTitle(phaseID),
        totalCost: cost,
        inputTokens,
        outputTokens,
        llmCalls
      }
    })
    .sort((a, b) => {
      if (b.totalCost !== a.totalCost) return b.totalCost - a.totalCost
      return a.phaseTitle.localeCompare(b.phaseTitle)
    })

  const modelCosts = Object.entries(tokenUsage.by_model || {})
    .map(([modelID, usage]) => ({
      modelID,
      provider: usage.provider || 'unknown',
      totalCost: usage.total_cost_usd || 0,
      inputTokens: usage.input_tokens || 0,
      outputTokens: usage.output_tokens || 0,
      llmCalls: usage.llm_call_count || 0
    }))
    .sort((a, b) => {
      if (b.totalCost !== a.totalCost) return b.totalCost - a.totalCost
      return a.modelID.localeCompare(b.modelID)
    })

  return {
    totalCost,
    totalInputTokens,
    totalOutputTokens,
    totalTokens: totalInputTokens + totalOutputTokens,
    totalLLMCalls,
    totalCacheReadTokens,
    totalCacheWriteTokens,
    totalReasoningTokens,
    createdAt: tokenUsage.created_at || null,
    updatedAt: tokenUsage.updated_at || null,
    phaseCosts,
    modelCosts
  }
}
