import { useEffect, useState, useMemo, useRef } from 'react'
import { inputTokens, totalTokens } from '../../../utils/costTokens'
import { agentApi } from '../../../services/api'
import { buildCostActivityBreakdown } from '../../../utils/costActivityBreakdown'
import type {
  CostSummary,
  StepExecutionLogs,
  WorkflowRunCostsEntry,
  WorkflowPhaseDailyCostsEntry,
  WorkflowRunDailyCostsEntry,
  WorkflowActivityTimingSummary,
} from '../../../services/api-types'
import {
  calculateCostSummary,
  calculatePhaseCostSummary,
  compareRunCosts,
  type CombinedDailyCostSummaryEntry,
  type PhaseCostSummary,
  type PhaseDailyCostSummaryEntry,
  type RunCosts,
  type RunDailyCostSummaryEntry,
} from './helpers'

interface UseCostsDataArgs {
  workspacePath: string | null
  selectedRunFolder: string | null
}

export function useCostsData({ workspacePath, selectedRunFolder }: UseCostsDataArgs) {
  const [loading, setLoading] = useState(false)
  const [runCosts, setRunCosts] = useState<RunCosts[]>([])
  const [phaseCostSummary, setPhaseCostSummary] = useState<PhaseCostSummary | null>(null)
  const [phaseDailyCostSummaries, setPhaseDailyCostSummaries] = useState<PhaseDailyCostSummaryEntry[]>([])
  const [runDailyCostSummaries, setRunDailyCostSummaries] = useState<RunDailyCostSummaryEntry[]>([])
  const [scopedCosts, setScopedCosts] = useState<CostSummary | null>(null)
  const [activityTiming, setActivityTiming] = useState<WorkflowActivityTimingSummary | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [expandedRunFolders, setExpandedRunFolders] = useState<Set<string>>(new Set())
  const [expandedCostModels, setExpandedCostModels] = useState<Set<string>>(new Set())
  const [costViewMode, setCostViewMode] = useState<Record<string, 'step' | 'model'>>({})
  // Route filter (PLAT-259 follow-up), keyed per run folder since each run
  // can take different routes. Same composite-key shape as ExecutionLogsPopup:
  // `${routeStepId}::${routeId}`, since route_id strings can collide across
  // unrelated routing/branch steps.
  const [routeFilterByRunFolder, setRouteFilterByRunFolder] = useState<Record<string, string | null>>({})
  const [expandedDailyDates, setExpandedDailyDates] = useState<Set<string>>(new Set())
  const [costHistory, setCostHistory] = useState<{ hasMore: boolean; nextBefore?: string } | null>(null)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const loadGenerationRef = useRef(0)
  const activityBreakdown = useMemo(() => buildCostActivityBreakdown(scopedCosts, activityTiming), [scopedCosts, activityTiming])
  const hasScopedActivity = Object.keys(scopedCosts?.by_scope || {}).length > 0

  // Load costs for all workflow runs
  useEffect(() => {
    if (workspacePath) {
      loadAllCosts()
    } else {
      setRunCosts([])
      setPhaseCostSummary(null)
      setPhaseDailyCostSummaries([])
      setRunDailyCostSummaries([])
      setScopedCosts(null)
      setActivityTiming(null)
      setError(null)
      setExpandedRunFolders(new Set())
      setExpandedCostModels(new Set())
      setCostViewMode({})
      setExpandedDailyDates(new Set())
      setCostHistory(null)
      setLoadingOlder(false)
    }
    return () => {
      loadGenerationRef.current += 1
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspacePath])

  // Auto-expand selected run folder when it changes
  useEffect(() => {
    if (selectedRunFolder && runCosts.some(c => c.runFolder === selectedRunFolder)) {
      setExpandedRunFolders(prev => {
        if (prev.has(selectedRunFolder!)) return prev
        const next = new Set(prev)
        next.add(selectedRunFolder!)
        return next
      })
    }
  }, [selectedRunFolder, runCosts])

  const loadAllCosts = async () => {
    if (!workspacePath) return

    const generation = ++loadGenerationRef.current
    setLoading(true)
    setError(null)
    try {
      const summaryResponse = await agentApi.getCosts(workspacePath, { view: 'summary', days: 30 })
      if (generation !== loadGenerationRef.current) return
      const summaryScopes = summaryResponse.scoped_costs?.by_scope || {}
      const hasAuthoritativeCosts = ['builder', 'chat', 'pulse', 'workflow_execution', 'evaluation']
        .some(scope => !!summaryScopes[scope])
      if (hasAuthoritativeCosts) {
        setScopedCosts(summaryResponse.scoped_costs ?? null)
        setActivityTiming(summaryResponse.activity_timing ?? null)
        setCostHistory({
          hasMore: summaryResponse.history?.has_more ?? false,
          nextBefore: summaryResponse.history?.next_before,
        })
      }

      // Load immutable per-run artifacts as well as the canonical summary.
      // Besides step-level detail, this reprices historical artifacts when a
      // rate card is added (for example Pi-routed Gemini webhook runs).
      const costsResponse = await agentApi.getCosts(workspacePath)
      if (generation !== loadGenerationRef.current) return
      setScopedCosts(costsResponse.scoped_costs ?? null)
      setActivityTiming(costsResponse.activity_timing ?? null)
      if (!hasAuthoritativeCosts) setCostHistory(null)
      const costEntriesByRunFolder = new Map<string, WorkflowRunCostsEntry>(
        (costsResponse.runs || []).map(entry => [entry.run_folder, entry])
      )
      let nextPhaseCostSummary: PhaseCostSummary | null = null
      let nextPhaseDailyCostSummaries: PhaseDailyCostSummaryEntry[] = []
      let nextRunDailyCostSummaries: RunDailyCostSummaryEntry[] = []
      const costs: RunCosts[] = []

      nextPhaseCostSummary = calculatePhaseCostSummary(costsResponse.phase_token_usage ?? null)
      nextPhaseDailyCostSummaries = (costsResponse.phase_daily_costs || [])
        .map((entry: WorkflowPhaseDailyCostsEntry) => {
          if (!entry.token_usage) return null
          const summary = calculatePhaseCostSummary(entry.token_usage ?? null)
          if (!summary) return null
          return {
            date: entry.date,
            tokenUsage: entry.token_usage,
            summary
          }
        })
        .filter((entry): entry is PhaseDailyCostSummaryEntry => entry !== null)
        .sort((a, b) => b.date.localeCompare(a.date))

      const runDailyEntriesByKey = new Map<string, WorkflowRunDailyCostsEntry>()
      ;(costsResponse.run_daily_costs || []).forEach((entry: WorkflowRunDailyCostsEntry) => {
        runDailyEntriesByKey.set(`${entry.date}:${entry.scope}:${entry.run_folder}`, entry)
      })

      nextRunDailyCostSummaries = Array.from(runDailyEntriesByKey.values())
        .map((entry: WorkflowRunDailyCostsEntry) => {
          if (!entry.token_usage) return null
          const summary = calculateCostSummary(entry.token_usage ?? null, null)
          if (!summary) return null
          return {
            date: entry.date,
            scope: entry.scope,
            groupFolder: entry.group_folder,
            runFolder: entry.run_folder,
            updatedAt: entry.token_usage?.updated_at || null,
            tokenUsage: entry.token_usage,
            summary
          }
        })
        .filter((entry): entry is RunDailyCostSummaryEntry => entry !== null)
        .sort((a, b) => {
          if (a.date !== b.date) return b.date.localeCompare(a.date)
          return a.runFolder.localeCompare(b.runFolder)
        })

      // Costs are stored keyed by "{iteration}/{group}" (e.g. "iteration-0/xspaces") —
      // multi-group workflows produce one entry per iteration-group pair. The
      // runFolders prop passed by parents can be stale or iteration-only (no group
      // info), so always trust the API cost keys as the source of truth.
      const allCostKeys = Array.from(costEntriesByRunFolder.keys())
      const foldersToLoad = allCostKeys

      for (const runFolder of foldersToLoad) {
        try {
          const data = costEntriesByRunFolder.get(runFolder)
          if (data?.token_usage) {
            // Fetch step titles only for the selected run. Loading the immutable
            // history must stay one bounded request rather than an N+1 request
            // for every webhook execution ever recorded.
            let steps: Record<string, StepExecutionLogs> | undefined
            if (runFolder === selectedRunFolder) {
              try {
                const logsData = await agentApi.getExecutionLogs(workspacePath, runFolder)
                steps = logsData.steps
              } catch (err) {
                // If we can't get steps, continue without them (costs will still work)
                console.warn(`Failed to load steps for ${runFolder}:`, err)
              }
            }
            const costSummary = calculateCostSummary(data.token_usage ?? null, null, steps)
            costs.push({
              runFolder,
              tokenUsage: data.token_usage ?? null,
              evaluationTokenUsage: null,
              steps, // Store steps for later use in model breakdown
              costSummary
            })
          }
        } catch (err) {
          console.warn(`Failed to process costs for ${runFolder}:`, err)
          // Continue loading other run folders
        }
      }

      costs.sort((a, b) => compareRunCosts(a, b, selectedRunFolder))

      setRunCosts(costs)
      setPhaseCostSummary(nextPhaseCostSummary)
      setPhaseDailyCostSummaries(nextPhaseDailyCostSummaries)
      setRunDailyCostSummaries(nextRunDailyCostSummaries)

      // Auto-expand selected run folder if provided
      if (selectedRunFolder && costs.some(c => c.runFolder === selectedRunFolder)) {
        setExpandedRunFolders(new Set([selectedRunFolder]))
      }
    } catch (err) {
      if (generation !== loadGenerationRef.current) return
      console.error('Failed to load costs:', err)
      setError('Failed to load cost data')
    } finally {
      if (generation === loadGenerationRef.current) setLoading(false)
    }
  }

  const loadOlderCosts = async () => {
    if (!workspacePath || !costHistory?.hasMore || !costHistory.nextBefore || loadingOlder) return
    setLoadingOlder(true)
    try {
      const response = await agentApi.getCosts(workspacePath, {
        view: 'summary',
        days: 30,
        before: costHistory.nextBefore,
      })
      if (response.scoped_costs) {
        setScopedCosts(current => current ? {
          ...current,
          by_date: {
            ...current.by_date,
            ...response.scoped_costs!.by_date,
          },
        } : response.scoped_costs!)
      }
      setCostHistory({
        hasMore: response.history?.has_more ?? false,
        nextBefore: response.history?.next_before,
      })
    } catch (err) {
      console.error('Failed to load older cost data:', err)
      setError('Failed to load older cost data')
    } finally {
      setLoadingOlder(false)
    }
  }

  const toggleRunFolder = (runFolder: string) => {
    setExpandedRunFolders(prev => {
      const next = new Set(prev)
      if (next.has(runFolder)) {
        next.delete(runFolder)
      } else {
        next.add(runFolder)
      }
      return next
    })
  }

  const toggleCostModel = (modelId: string) => {
    setExpandedCostModels(prev => {
      const next = new Set(prev)
      if (next.has(modelId)) {
        next.delete(modelId)
      } else {
        next.add(modelId)
      }
      return next
    })
  }

  const setViewModeForRunFolder = (runFolder: string, mode: 'step' | 'model') => {
    setCostViewMode(prev => ({
      ...prev,
      [runFolder]: mode
    }))
  }

  const setRouteFilterForRunFolder = (runFolder: string, filterKey: string | null) => {
    setRouteFilterByRunFolder(prev => ({
      ...prev,
      [runFolder]: filterKey
    }))
  }

  // Calculate aggregate summary across all visible run folders
  const aggregateSummary = useMemo(() => {
    if (runCosts.length === 0) return null

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
    let highestCost = 0
    let lowestCost = Infinity

    runCosts.forEach(runCost => {
      if (runCost.costSummary) {
        totalCost += runCost.costSummary.totalCost
        totalLLMCost += runCost.costSummary.totalLLMCost
        totalToolCost += runCost.costSummary.totalToolCost
        totalInputTokens += runCost.costSummary.totalInputTokens
        totalOutputTokens += runCost.costSummary.totalOutputTokens
        totalLLMCalls += runCost.costSummary.totalLLMCalls
        totalCacheReadTokens += runCost.costSummary.totalCacheReadTokens
        totalCacheWriteTokens += runCost.costSummary.totalCacheWriteTokens
        totalReasoningTokens += runCost.costSummary.totalReasoningTokens
        stageCosts.execution += runCost.costSummary.stageCosts.execution
        stageCosts.learning += runCost.costSummary.stageCosts.learning
        stageCosts.reflection += runCost.costSummary.stageCosts.reflection
        stageCosts.evaluation += runCost.costSummary.stageCosts.evaluation
        stageCosts.knowledgebase += runCost.costSummary.stageCosts.knowledgebase
        stageCosts.routing += runCost.costSummary.stageCosts.routing
        stageCosts.workshop += runCost.costSummary.stageCosts.workshop
        stageCosts.other += runCost.costSummary.stageCosts.other

        if (runCost.costSummary.totalCost > highestCost) {
          highestCost = runCost.costSummary.totalCost
        }
        if (runCost.costSummary.totalCost < lowestCost) {
          lowestCost = runCost.costSummary.totalCost
        }
      }
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
      highestCost: highestCost === 0 ? 0 : highestCost,
      lowestCost: lowestCost === Infinity ? 0 : lowestCost,
      totalRuns: runCosts.length
    }
  }, [runCosts])

  const overallSummary = useMemo(() => {
    if (scopedCosts && Object.keys(scopedCosts.by_scope || {}).length > 0) {
      return {
        totalCost: scopedCosts.total.total_cost_usd,
        totalTokens: totalTokens(scopedCosts.total),
        totalInputTokens: inputTokens(scopedCosts.total),
        totalOutputTokens: scopedCosts.total.completion_tokens,
        totalRuns: aggregateSummary?.totalRuns || 0
      }
    }
    if (!aggregateSummary && !phaseCostSummary) return null

    return {
      totalCost: (aggregateSummary?.totalCost || 0) + (phaseCostSummary?.totalCost || 0),
      totalTokens: (aggregateSummary?.totalTokens || 0) + (phaseCostSummary?.totalTokens || 0),
      totalInputTokens: (aggregateSummary?.totalInputTokens || 0) + (phaseCostSummary?.totalInputTokens || 0),
      totalOutputTokens: (aggregateSummary?.totalOutputTokens || 0) + (phaseCostSummary?.totalOutputTokens || 0),
      totalRuns: aggregateSummary?.totalRuns || 0
    }
  }, [aggregateSummary, phaseCostSummary, scopedCosts])

  const combinedDailyCostSummaries = useMemo(() => {
    const byDate = scopedCosts?.by_date || {}
    const hasDailyScopeAttribution = Object.values(byDate).some(total => Object.keys(total.by_scope || {}).length > 0)
    if (!hasDailyScopeAttribution) {
      const legacyByDate = new Map<string, CombinedDailyCostSummaryEntry & { runKeys: Set<string> }>()
      const ensureLegacyEntry = (date: string) => {
        let entry = legacyByDate.get(date)
        if (!entry) {
          entry = {
            date,
            builderCost: 0,
            pulseCost: null,
            workflowCost: 0,
            evaluationCost: 0,
            totalCost: 0,
            builderTokens: 0,
            pulseTokens: null,
            workflowTokens: 0,
            evaluationTokens: 0,
            totalTokens: 0,
            totalInputTokens: 0,
            totalOutputTokens: 0,
            llmDurationMS: 0,
            runCount: 0,
            runKeys: new Set<string>(),
          }
          legacyByDate.set(date, entry)
        }
        return entry
      }

      phaseDailyCostSummaries.forEach(daily => {
        const entry = ensureLegacyEntry(daily.date)
        entry.builderCost += daily.summary.totalCost
        entry.totalCost += daily.summary.totalCost
        entry.builderTokens += daily.summary.totalTokens
        entry.totalTokens += daily.summary.totalTokens
        entry.totalInputTokens = (entry.totalInputTokens ?? 0) + daily.summary.totalInputTokens
        entry.totalOutputTokens = (entry.totalOutputTokens ?? 0) + daily.summary.totalOutputTokens
      })
      runDailyCostSummaries.forEach(daily => {
        const entry = ensureLegacyEntry(daily.date)
        if (daily.scope === 'evaluation') {
          entry.evaluationCost += daily.summary.totalCost
          entry.evaluationTokens += daily.summary.totalTokens
        } else {
          entry.workflowCost += daily.summary.totalCost
          entry.workflowTokens += daily.summary.totalTokens
        }
        entry.totalCost += daily.summary.totalCost
        entry.totalTokens += daily.summary.totalTokens
        entry.totalInputTokens = (entry.totalInputTokens ?? 0) + daily.summary.totalInputTokens
        entry.totalOutputTokens = (entry.totalOutputTokens ?? 0) + daily.summary.totalOutputTokens
        entry.runKeys.add(`${daily.scope}:${daily.runFolder}`)
        entry.runCount = entry.runKeys.size
      })
      Object.entries(activityTiming?.by_date || {}).forEach(([date, timing]) => {
        const entry = ensureLegacyEntry(date)
        entry.llmDurationMS = Object.values(timing.by_scope || {}).reduce(
          (sum, scopeTiming) => sum + (scopeTiming.llm_duration_ms || 0),
          0,
        )
      })
      return Array.from(legacyByDate.values())
        .map(({ runKeys: _runKeys, ...entry }) => entry)
        .sort((a, b) => b.date.localeCompare(a.date))
    }

    const artifactByDate = new Map<string, { workflowCost: number; evaluationCost: number; workflowTokens: number; evaluationTokens: number; runs: Set<string> }>()
    runDailyCostSummaries.forEach(daily => {
      const current = artifactByDate.get(daily.date) || { workflowCost: 0, evaluationCost: 0, workflowTokens: 0, evaluationTokens: 0, runs: new Set<string>() }
      if (daily.scope === 'evaluation') {
        current.evaluationCost += daily.summary.totalCost
        current.evaluationTokens += daily.summary.totalTokens
      } else {
        current.workflowCost += daily.summary.totalCost
        current.workflowTokens += daily.summary.totalTokens
      }
      current.runs.add(`${daily.scope}:${daily.runFolder}`)
      artifactByDate.set(daily.date, current)
    })

    return Object.entries(byDate)
      .map(([date, total]): CombinedDailyCostSummaryEntry => {
        const byScope = total.by_scope || {}
        const costFor = (scope: string) => byScope[scope]?.total_cost_usd || 0
        const tokensFor = (scope: string) => totalTokens(byScope[scope])
        const artifacts = artifactByDate.get(date)
        const ledgerWorkflowCost = costFor('workflow_execution')
        const ledgerEvaluationCost = costFor('evaluation')
        // Old ledger rows can be explicitly unpriced while their immutable
        // run artifacts can now be repriced with a newly available rate card.
        const workflowCost = ledgerWorkflowCost === 0 && (artifacts?.workflowCost || 0) > 0 ? artifacts!.workflowCost : ledgerWorkflowCost
        const evaluationCost = ledgerEvaluationCost === 0 && (artifacts?.evaluationCost || 0) > 0 ? artifacts!.evaluationCost : ledgerEvaluationCost
        return {
          date,
          builderCost: costFor('builder') + costFor('chat'),
          pulseCost: costFor('pulse'),
          workflowCost,
          evaluationCost,
          totalCost: (total.total_cost_usd || 0) - ledgerWorkflowCost - ledgerEvaluationCost + workflowCost + evaluationCost,
          builderTokens: tokensFor('builder') + tokensFor('chat'),
          pulseTokens: tokensFor('pulse'),
          workflowTokens: tokensFor('workflow_execution') || artifacts?.workflowTokens || 0,
          evaluationTokens: tokensFor('evaluation') || artifacts?.evaluationTokens || 0,
          totalTokens: totalTokens(total),
          totalInputTokens: inputTokens(total),
          totalOutputTokens: total.completion_tokens,
          llmDurationMS: total.llm_generation_duration_ms || 0,
          runCount: total.workflow_run_count || artifacts?.runs.size || 0,
        }
      })
      .sort((a, b) => b.date.localeCompare(a.date))
  }, [activityTiming, phaseDailyCostSummaries, runDailyCostSummaries, scopedCosts])

  const dailyActivityBreakdown = useMemo(() => {
    const details = new Map<string, ReturnType<typeof buildCostActivityBreakdown>>()
    Object.entries(scopedCosts?.by_date || {}).forEach(([date, total]) => {
      if (Object.keys(total.by_scope || {}).length > 0) {
        details.set(date, buildCostActivityBreakdown(
          { by_scope: total.by_scope },
          activityTiming?.by_date?.[date] || null,
        ))
      }
    })
    return details
  }, [activityTiming, scopedCosts])

  return {
    loading,
    error,
    scopedCosts,
    runCosts,
    runDailyCostSummaries,
    phaseCostSummary,
    phaseDailyCostSummaries,
    hasScopedActivity,
    activityBreakdown,
    expandedRunFolders,
    expandedCostModels,
    costViewMode,
    routeFilterByRunFolder,
    expandedDailyDates,
    setExpandedDailyDates,
    costHistory,
    loadingOlder,
    loadAllCosts,
    loadOlderCosts,
    toggleRunFolder,
    toggleCostModel,
    setViewModeForRunFolder,
    setRouteFilterForRunFolder,
    aggregateSummary,
    overallSummary,
    combinedDailyCostSummaries,
    dailyActivityBreakdown,
  }
}

export type CostsData = ReturnType<typeof useCostsData>
