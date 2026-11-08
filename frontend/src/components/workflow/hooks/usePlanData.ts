import { useState, useCallback, useEffect, useRef } from 'react'
import { agentApi } from '../../../services/api'
import type { PlanStep, PlanningResponse, StepConfig, AgentConfigs } from '../../../utils/stepConfigMatching'
import { isAgentStep } from '../../../utils/stepConfigMatching'

// Module-level cache to dedupe loadPlan calls across multiple hook instances
// and to preserve per-workspace data across workflow switches.
interface PlanCacheEntry {
  promise: Promise<PlanningResponse | null> | null
  data: PlanningResponse | null
  timestamp: number
}

const planCache = new Map<string, PlanCacheEntry>()
const MAX_PLAN_CACHE_ENTRIES = 5

function prunePlanCache(keepWorkspacePath?: string) {
  while (planCache.size > MAX_PLAN_CACHE_ENTRIES) {
    const evictKey = Array.from(planCache.keys()).find(key => key !== keepWorkspacePath)
    if (!evictKey) return
    planCache.delete(evictKey)
  }
}

function getPlanCacheEntry(workspacePath: string): PlanCacheEntry {
  const existing = planCache.get(workspacePath)
  if (existing) {
    planCache.delete(workspacePath)
    planCache.set(workspacePath, existing)
    return existing
  }

  const created: PlanCacheEntry = {
    promise: null,
    data: null,
    timestamp: 0
  }
  planCache.set(workspacePath, created)
  prunePlanCache(workspacePath)
  return created
}

// Types for plan change detection
export type ChangeType = 'added' | 'updated' | 'deleted'

export interface PlanChanges {
  added: string[]      // Step IDs that were added
  updated: string[]    // Step IDs that were updated
  deleted: string[]    // Step IDs that were deleted
  hasChanges: boolean
}

export interface UsePlanDataReturn {
  plan: PlanningResponse | null
  loading: boolean
  error: string | null
  changes: PlanChanges | null  // Latest detected changes (set via setChanges from granular events)
  loadPlan: () => Promise<boolean>  // Loads plan without comparison
  refresh: () => Promise<boolean>  // Fetches fresh plan/config data without clearing the displayed plan
  clearChanges: () => void  // Clear the changes state
  setChanges: (changes: PlanChanges | null) => void  // Set changes directly (for granular events)
}

/**
 * Normalize step_config.json content to array format
 * Handles object format: { "steps": [{ "id": "...", "agent_configs": {...} }] }
 */
function normalizeStepConfigFile(rawContent: unknown): StepConfig[] {
  // Handle object format with "steps" field: { "steps": [...] }
  if (rawContent && typeof rawContent === 'object' && !Array.isArray(rawContent)) {
    const obj = rawContent as Record<string, unknown>
    if ('steps' in obj && Array.isArray(obj.steps)) {
      return obj.steps as StepConfig[]
    }
  }

  // Legacy support: If it's already an array, return it (for backward compatibility during migration)
  if (Array.isArray(rawContent)) {
    console.warn('[normalizeStepConfigFile] Detected legacy array format, please migrate to { "steps": [...] } format')
    return rawContent as StepConfig[]
  }

  // Default: empty array
  return []
}

/**
 * Merge agent_configs from step_config.json into plan steps
 * This adds the agent_configs (including use_code_execution_mode) to each step
 */
function mergeStepConfigs(
  plan: PlanningResponse,
  stepConfigs: StepConfig[] | null
): PlanningResponse {
  if (!stepConfigs || stepConfigs.length === 0) return plan

  // Create map: step ID -> agent_configs
  const configMap = new Map<string, AgentConfigs>()
  for (const config of stepConfigs) {
    if (config.id && config.agent_configs) {
      configMap.set(config.id, config.agent_configs)
    }
  }

  // Recursively merge configs into a single step (handles nested structures)
  const mergeIntoStep = (step: PlanStep): PlanStep => {
    const config = step.id ? configMap.get(step.id) : undefined
    let mergedStep: PlanStep = {
      ...step,
      ...(config ? { agent_configs: config } : {})
    }
    
    // Agent routes may live on the canonical agent shape or the
    // legacy orchestrator/todo_task compatibility shape.
    if (isAgentStep(step)) {
      mergedStep = {
        ...mergedStep,
        predefined_routes: step.predefined_routes ? step.predefined_routes.map(route => ({
          ...route,
          sub_agent_step: route.sub_agent_step ? mergeIntoStep(route.sub_agent_step) : undefined
        })) : undefined,
      } as PlanStep
    }

    return mergedStep
  }

  // Recursively merge configs into steps
  const mergeIntoSteps = (steps: PlanStep[]): PlanStep[] => {
    return steps.map(step => mergeIntoStep(step))
  }

  return {
    ...plan,
    steps: mergeIntoSteps(plan.steps),
    orphan_steps: plan.orphan_steps ? mergeIntoSteps(plan.orphan_steps) : plan.orphan_steps,
  }
}

function resolveOrphanStepRefs(plan: PlanningResponse): PlanningResponse {
  if (!plan.orphan_steps || plan.orphan_steps.length === 0) return plan

  const orphanMap = new Map(plan.orphan_steps.map(step => [step.id, step]))

  const cloneStep = (step: PlanStep): PlanStep => JSON.parse(JSON.stringify(step)) as PlanStep

  const resolveStep = (step: PlanStep, orphanChain: string[] = []): PlanStep => {
    if (isAgentStep(step)) {
      return {
        ...step,
        predefined_routes: step.predefined_routes?.map(route => {
          let resolvedSubAgent = route.sub_agent_step ? resolveStep(route.sub_agent_step, orphanChain) : undefined

          if (!resolvedSubAgent && route.orphan_step_ref) {
            if (orphanChain.includes(route.orphan_step_ref)) {
              console.warn('[usePlanData] Detected cyclic orphan_step_ref chain', {
                parentStepId: step.id,
                routeId: route.route_id,
                orphanChain,
                orphanStepRef: route.orphan_step_ref,
              })
              return {
                ...route,
                sub_agent_step: undefined,
              }
            }
            const orphanStep = orphanMap.get(route.orphan_step_ref)
            const allowedOrchestrators = orphanStep?.shared_with?.agent_ids ?? []
            const isAllowed = !!orphanStep && allowedOrchestrators.includes(step.id)

            if (orphanStep && isAllowed) {
              resolvedSubAgent = cloneStep(orphanStep)
              resolvedSubAgent.id = route.route_id || resolvedSubAgent.id
              if (!resolvedSubAgent.title?.trim()) {
                resolvedSubAgent.title = route.route_name || resolvedSubAgent.title
              }
              resolvedSubAgent = resolveStep(resolvedSubAgent, [...orphanChain, route.orphan_step_ref])
            } else if (route.orphan_step_ref) {
              console.warn('[usePlanData] Failed to resolve orphan_step_ref for route', {
                parentStepId: step.id,
                routeId: route.route_id,
                orphanStepRef: route.orphan_step_ref,
                isAllowed,
              })
            }
          }

          return {
            ...route,
            sub_agent_step: resolvedSubAgent,
          }
        }),
      }
    }

    return step
  }

  return {
    ...plan,
    steps: plan.steps.map(step => resolveStep(step)),
    orphan_steps: plan.orphan_steps.map(step => resolveStep(step, [step.id])),
  }
}

/**
 * Hook to read and write plan.json from the workspace
 * @param workspacePath - The workspace path (e.g., "Workflow/HRMS PR Review")
 */
export function usePlanData(workspacePath: string | null): UsePlanDataReturn {
  const [plan, setPlan] = useState<PlanningResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [changes, setChanges] = useState<PlanChanges | null>(null)

  // Track workspace path to detect workflow switches
  const currentWorkspaceRef = useRef<string | null>(null)
  const requestVersionRef = useRef(0)


  // Construct the plan file path
  const getPlanFilePath = useCallback(() => {
    if (!workspacePath) return null
    return `${workspacePath}/planning/plan.json`
  }, [workspacePath])

  // Construct the step config file path
  const getStepConfigFilePath = useCallback(() => {
    if (!workspacePath) return null
    return `${workspacePath}/planning/step_config.json`
  }, [workspacePath])

  // Internal function to actually fetch plan data (used by cached loadPlan)
  const fetchPlanData = useCallback(async (): Promise<PlanningResponse | null> => {
    const planPath = getPlanFilePath()
    const stepConfigPath = getStepConfigFilePath()
    if (!planPath) {
      throw new Error('No workspace path provided')
    }

    const [response, stepConfigResponse] = await Promise.all([
      agentApi.getPlannerFileContent(planPath, 20000),
      stepConfigPath ? agentApi.getPlannerFileContent(stepConfigPath, 20000).catch(() => null) : Promise.resolve(null),
    ])

    if (response.success && response.data && response.data.content && typeof response.data.content === 'string') {
      let planData: PlanningResponse = JSON.parse(response.data.content)

      // Also load step_config.json to merge agent_configs
      if (stepConfigPath) {
        try {
          if (stepConfigResponse?.success && stepConfigResponse.data && stepConfigResponse.data.content && typeof stepConfigResponse.data.content === 'string') {
            const rawStepConfig = JSON.parse(stepConfigResponse.data.content)
            const stepConfigs = normalizeStepConfigFile(rawStepConfig)
            planData = mergeStepConfigs(planData, stepConfigs)
          }
        } catch {
          // step_config.json doesn't exist or couldn't be loaded - that's okay
        }
      }

      // Step overrides are now loaded from workflow.json in WorkflowLayout.tsx

      return resolveOrphanStepRefs(planData)
    }

    return null
  }, [getPlanFilePath, getStepConfigFilePath])

  // Load plan from workspace with caching to prevent duplicate API calls
  const loadPlan = useCallback(async (): Promise<boolean> => {
    if (!workspacePath) {
      setError('No workspace path provided')
      return false
    }

    currentWorkspaceRef.current = workspacePath
    const requestVersion = ++requestVersionRef.current
    const isCurrent = () => currentWorkspaceRef.current === workspacePath && requestVersionRef.current === requestVersion
    const cacheEntry = getPlanCacheEntry(workspacePath)
    setError(null)
    if (cacheEntry.data !== null) {
      setPlan(cacheEntry.data)
      setLoading(false)
      return true
    }
    setLoading(true)
    setPlan(null)
    if (!cacheEntry.promise) {
      const promise = fetchPlanData().then(data => {
        if (cacheEntry.promise === promise) {
          cacheEntry.data = data
          cacheEntry.timestamp = Date.now()
        }
        return data
      }).finally(() => { if (cacheEntry.promise === promise) cacheEntry.promise = null })
      cacheEntry.promise = promise
    }
    try {
      const data = await cacheEntry.promise
      if (isCurrent()) setPlan(data)
      return data !== null
    } catch (err) {
      if (!isCurrent()) return false
      const status = (err as { response?: { status?: number } })?.response?.status
      const message = err instanceof Error ? err.message : String(err)
      if (status === 404 || /not found|does not exist|no such file/i.test(message)) {
        setPlan(null)
        return false
      }
      setError(message || 'Failed to load plan')
      return false
    } finally {
      if (isCurrent()) setLoading(false)
    }
  }, [workspacePath, fetchPlanData])

  // Refresh reads through the shared cache.
  const invalidatePlanCache = useCallback(() => {
    if (!workspacePath) return
    const cacheEntry = getPlanCacheEntry(workspacePath)
    cacheEntry.data = null
    cacheEntry.timestamp = 0
    cacheEntry.promise = null
  }, [workspacePath])

  // Refresh from the server without clearing the currently rendered graph.
  // A failed read keeps the old graph visible; a genuinely missing plan shows
  // the empty state. Older in-flight loads cannot overwrite this result.
  const refresh = useCallback(async (): Promise<boolean> => {
    if (!workspacePath) return false
    const requestVersion = ++requestVersionRef.current
    const isCurrent = () => currentWorkspaceRef.current === workspacePath && requestVersionRef.current === requestVersion
    invalidatePlanCache()
    const cacheEntry = getPlanCacheEntry(workspacePath)
    const promise = fetchPlanData().then(data => {
      if (cacheEntry.promise === promise) {
        cacheEntry.data = data
        cacheEntry.timestamp = Date.now()
      }
      return data
    }).finally(() => { if (cacheEntry.promise === promise) cacheEntry.promise = null })
    cacheEntry.promise = promise
    try {
      const data = await promise
      if (isCurrent()) {
        setPlan(data)
        setError(null)
        setLoading(false)
      }
      return data !== null
    } catch (err) {
      if (!isCurrent()) return false
      const status = (err as { response?: { status?: number } })?.response?.status
      const message = err instanceof Error ? err.message : String(err)
      if (status === 404 || /not found|does not exist|no such file/i.test(message)) {
        setPlan(null)
        setError(null)
      } else {
        setError(message || 'Failed to load plan')
      }
      setLoading(false)
      return false
    }
  }, [workspacePath, invalidatePlanCache, fetchPlanData])

  // Clear changes state (call after highlighting animation completes)
  const clearChanges = useCallback(() => {
    setChanges(null)
  }, [])

  // Auto-load plan when workspace path changes
  // Note: loadPlan has internal caching that prevents duplicate API calls,
  // so we don't need to re-run this effect when loadPlan is recreated
  useEffect(() => {
    if (workspacePath) {
      loadPlan()
    } else {
      // Reset everything when no workspace
      setPlan(null)
      setLoading(false)
      currentWorkspaceRef.current = null
      requestVersionRef.current += 1
      setError(null)
      setChanges(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspacePath]) // Only re-run when workspace changes, not when loadPlan is recreated

  return {
    plan,
    loading,
    error,
    changes,
    loadPlan,
    refresh,
    clearChanges,
    setChanges
  }
}

export default usePlanData
