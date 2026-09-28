import { useState, useCallback, useEffect, useRef } from 'react'
import { agentApi } from '../../../services/api'
import type { PlanStep, PlanningResponse, StepConfig, AgentConfigs } from '../../../utils/stepConfigMatching'
import { isMessageSequenceStep, isTodoTaskStep } from '../../../utils/stepConfigMatching'

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
  /** @deprecated Legacy method - prefer using updateStep() which calls backend APIs. See method documentation for when to use. */
  savePlan: (plan: PlanningResponse) => Promise<void>
  /** @deprecated Legacy method - prefer using agentApi.updateStepConfig(). See method documentation for when to use. */
  saveStepConfig: (stepId: string, agentConfigs: AgentConfigs | undefined) => Promise<void>
  /** @deprecated Legacy method - prefer using agentApi.batchUpdateSteps(). See method documentation for when to use. */
  batchSaveStepConfig: (stepConfigs: Array<{ stepId: string; agentConfigs: AgentConfigs | undefined }>) => Promise<void>
  updateStep: (stepIndex: number, updates: Partial<PlanStep>) => Promise<void>
  deleteStep: (stepIndex: number) => Promise<void>
  addStep: (step: PlanStep, afterIndex?: number) => Promise<void>
  refresh: () => Promise<boolean>  // Refreshes plan without comparison (alias for loadPlan)
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
    
    // Agent routes may live on the canonical message_sequence shape or the
    // legacy orchestrator/todo_task compatibility shape.
    if (isTodoTaskStep(step) || isMessageSequenceStep(step)) {
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
    if (isTodoTaskStep(step) || isMessageSequenceStep(step)) {
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
            const allowedOrchestrators = orphanStep?.shared_with?.orchestrator_ids ?? []
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

  /**
   * LEGACY METHOD: Direct file save for plan.json
   * 
   * ⚠️ WARNING: This method bypasses backend validation and should be used sparingly.
   * 
   * ✅ USE THIS METHOD FOR:
   *   - Initial plan creation (when creating a brand new plan.json file)
   *   - One-time migrations or bulk imports
   *   - Edge cases where backend API is unavailable
   * 
   * ❌ DO NOT USE THIS METHOD FOR:
   *   - Normal step updates (use updateStep() instead, which calls backend APIs)
   *   - Step configuration changes (use updateStepConfig API instead)
   *   - Any operation that should be validated by the backend
   * 
   * The backend APIs (updatePlanStep, updateStepConfig, batchUpdateSteps, etc.) provide:
   *   - Automatic validation
   *   - Consistent data structure
   *   - Prevention of agent_configs in plan.json
   *   - Atomic operations
   * 
   * @deprecated Prefer using backend APIs (updateStep, deleteStep, addStep) for normal operations
   */
  // Helper to invalidate the plan cache (call after mutations)
  const invalidatePlanCache = useCallback(() => {
    if (!workspacePath) return
    const cacheEntry = getPlanCacheEntry(workspacePath)
    cacheEntry.data = null
    cacheEntry.timestamp = 0
    cacheEntry.promise = null
  }, [workspacePath])

  const savePlan = useCallback(async (updatedPlan: PlanningResponse) => {
    const planPath = getPlanFilePath()
    if (!planPath) {
      throw new Error('No workspace path provided')
    }

    try {
      const content = JSON.stringify(updatedPlan, null, 2)
      const response = await agentApi.updatePlannerFile(
        planPath,
        content,
        'Updated plan via workflow canvas'
      )

      if (!response.success) {
        throw new Error(response.message || 'Failed to save plan')
      }

      // Update local state and cache
      setPlan(updatedPlan)
      if (workspacePath) {
        const cacheEntry = getPlanCacheEntry(workspacePath)
        cacheEntry.data = updatedPlan
        cacheEntry.timestamp = Date.now()
      }
    } catch (err) {
      console.error('[usePlanData] Failed to save plan:', err)
      throw err
    }
  }, [getPlanFilePath, workspacePath])

  /**
   * LEGACY METHOD: Direct file save for step_config.json
   * 
   * ⚠️ WARNING: This method bypasses backend validation and should be used sparingly.
   * 
   * ✅ USE THIS METHOD FOR:
   *   - Initial step config creation (when setting up a new workspace)
   *   - One-time migrations or bulk imports
   *   - Edge cases where backend API is unavailable
   * 
   * ❌ DO NOT USE THIS METHOD FOR:
   *   - Normal step config updates (use updateStepConfig API instead)
   *   - Updates from WorkflowCanvas (use updateStep() instead)
   *   - Bulk operations (use batchUpdateSteps API instead)
   *   - Any operation that should be validated by the backend
   * 
   * The backend API (updateStepConfig) provides:
   *   - Automatic validation
   *   - Consistent data structure
   *   - Prevention of orphaned configs
   *   - Atomic operations with plan.json updates
   * 
   * @deprecated Prefer using agentApi.updateStepConfig() for normal operations
   */
  const saveStepConfig = useCallback(async (stepId: string, agentConfigs: AgentConfigs | undefined) => {
    const stepConfigPath = getStepConfigFilePath()
    if (!stepConfigPath) {
      throw new Error('No workspace path provided')
    }

    try {
      // Read existing step_config.json (or create empty if doesn't exist)
      let stepConfigs: StepConfig[] = []
      try {
        const response = await agentApi.getPlannerFileContent(stepConfigPath)
        // Check if response has data AND content exists and is a string
        if (response.success && response.data && response.data.content && typeof response.data.content === 'string') {
          const rawContent = JSON.parse(response.data.content)
          // Normalize to array format
          stepConfigs = normalizeStepConfigFile(rawContent)
        }
      } catch {
        // File doesn't exist yet - use empty array
        console.log('[usePlanData] step_config.json doesn\'t exist yet, creating new file')
      }

      // Find existing step config or create new one
      const existingIndex = stepConfigs.findIndex(s => s.id === stepId)
      
      if (agentConfigs) {
        // Update or add step config
        const stepConfig: StepConfig = {
          id: stepId,
          agent_configs: agentConfigs
        }
        
        if (existingIndex >= 0) {
          // Update existing
          stepConfigs[existingIndex] = {
            ...stepConfigs[existingIndex],
            ...stepConfig
          }
        } else {
          // Add new
          stepConfigs.push(stepConfig)
        }
      } else {
        // If agentConfigs is undefined/null, remove the step config
        if (existingIndex >= 0) {
          stepConfigs.splice(existingIndex, 1)
        }
      }

      // Save updated step_config.json in object format: { "steps": [...] }
      const content = JSON.stringify({ steps: stepConfigs }, null, 2)
      const response = await agentApi.updatePlannerFile(
        stepConfigPath,
        content,
        `Updated step config for step ${stepId}`
      )

      if (!response.success) {
        throw new Error(response.message || 'Failed to save step config')
      }

      console.log('[usePlanData] Saved step_config.json for step:', stepId)
    } catch (err) {
      console.error('[usePlanData] Failed to save step config:', err)
      throw err
    }
  }, [getStepConfigFilePath])

  /**
   * LEGACY METHOD: Batch direct file save for step_config.json
   * 
   * ⚠️ WARNING: This method bypasses backend validation and should be used sparingly.
   * 
   * ✅ USE THIS METHOD FOR:
   *   - Initial bulk step config creation (when setting up a new workspace)
   *   - One-time migrations or bulk imports
   *   - Edge cases where backend API is unavailable
   * 
   * ❌ DO NOT USE THIS METHOD FOR:
   *   - Normal bulk operations (use batchUpdateSteps API instead)
   *   - Any operation that should be validated by the backend
   * 
   * The backend API (batchUpdateSteps) provides:
   *   - Automatic validation
   *   - Consistent data structure
   *   - Prevention of orphaned configs
   *   - Atomic operations with plan.json updates
   *   - Better error handling and rollback
   * 
   * @deprecated Prefer using agentApi.batchUpdateSteps() for normal operations
   */
  const batchSaveStepConfig = useCallback(async (stepConfigs: Array<{ stepId: string; agentConfigs: AgentConfigs | undefined }>) => {
    const stepConfigPath = getStepConfigFilePath()
    if (!stepConfigPath) {
      throw new Error('No workspace path provided')
    }

    try {
      // Read existing step_config.json (or create empty if doesn't exist)
      let existingConfigs: StepConfig[] = []
      try {
        const response = await agentApi.getPlannerFileContent(stepConfigPath)
        if (response.success && response.data && response.data.content && typeof response.data.content === 'string') {
          const rawContent = JSON.parse(response.data.content)
          existingConfigs = normalizeStepConfigFile(rawContent)
        }
      } catch {
        console.log('[usePlanData] step_config.json doesn\'t exist yet, creating new file')
      }

      // Create a map of existing configs by step ID
      const configMap = new Map<string, StepConfig>()
      existingConfigs.forEach(config => {
        if (config.id) {
          configMap.set(config.id, config)
        }
      })

      // Update or add each step config
      stepConfigs.forEach(({ stepId, agentConfigs }) => {
        if (agentConfigs) {
          // Update or add step config
          const existingConfig = configMap.get(stepId)
          if (existingConfig) {
            // Merge with existing config
            configMap.set(stepId, {
              ...existingConfig,
              agent_configs: {
                ...existingConfig.agent_configs,
                ...agentConfigs
              }
            })
          } else {
            // Add new config
            configMap.set(stepId, {
              id: stepId,
              agent_configs: agentConfigs
            })
          }
        } else {
          // Remove config if agentConfigs is undefined
          configMap.delete(stepId)
        }
      })

      // Convert map back to array
      const updatedConfigs = Array.from(configMap.values())

      // Save updated step_config.json in object format: { "steps": [...] }
      const content = JSON.stringify({ steps: updatedConfigs }, null, 2)
      const response = await agentApi.updatePlannerFile(
        stepConfigPath,
        content,
        `Batch updated step configs for ${stepConfigs.length} step(s)`
      )

      if (!response.success) {
        throw new Error(response.message || 'Failed to batch save step configs')
      }

      console.log('[usePlanData] Batch saved step_config.json for', stepConfigs.length, 'step(s)')
    } catch (err) {
      console.error('[usePlanData] Failed to batch save step configs:', err)
      throw err
    }
  }, [getStepConfigFilePath])

  // Update a single step
  const updateStep = useCallback(async (stepIndex: number, updates: Partial<PlanStep>) => {
    if (!plan || !workspacePath) {
      throw new Error('No plan loaded or workspace path missing')
    }

    const updatedSteps = [...plan.steps]
    if (stepIndex < 0 || stepIndex >= updatedSteps.length) {
      throw new Error('Invalid step index')
    }

    const stepId = updatedSteps[stepIndex].id
    if (!stepId) {
      throw new Error('Step is missing required ID field')
    }

    // Separate plan updates and config updates
    const { agent_configs, ...planUpdates } = updates

    // Send update instructions to backend
    const promises: Promise<{ success: boolean; message: string; data?: unknown }>[] = []

    // Update plan if there are plan-related fields
    if (Object.keys(planUpdates).length > 0) {
      promises.push(
        agentApi.updatePlanStep(workspacePath, stepId, planUpdates)
      )
    }

    // Update config if agent_configs is provided
    if (agent_configs !== undefined) {
      promises.push(
        agentApi.updateStepConfig(workspacePath, stepId, agent_configs)
      )
    }

    // Wait for all updates to complete
    await Promise.all(promises)

    // Refresh plan from backend
    await loadPlan()
  }, [plan, workspacePath, loadPlan])

  // Delete a step
  const deleteStep = useCallback(async (stepIndex: number) => {
    if (!plan || !workspacePath) {
      throw new Error('No plan loaded or workspace path missing')
    }

    const updatedSteps = [...plan.steps]
    if (stepIndex < 0 || stepIndex >= updatedSteps.length) {
      throw new Error('Invalid step index')
    }

    const stepId = updatedSteps[stepIndex].id
    if (!stepId) {
      throw new Error('Step is missing required ID field')
    }

    // Call backend API to delete step
    await agentApi.deleteStep(workspacePath, stepId)

    // Refresh plan from backend
    await loadPlan()
  }, [plan, workspacePath, loadPlan])

  // Add a new step
  const addStep = useCallback(async (step: PlanStep, afterIndex?: number) => {
    if (!plan || !workspacePath) {
      throw new Error('No plan loaded or workspace path missing')
    }

    // Remove agent_configs from step if present (validation)
    const { agent_configs, ...stepWithoutConfig } = step

    // Determine insert_after_step_id if afterIndex is provided
    let insertAfterStepId: string | undefined
    if (afterIndex !== undefined && afterIndex >= 0 && afterIndex < plan.steps.length) {
      insertAfterStepId = plan.steps[afterIndex].id
    }

    // Call backend API to add step
    await agentApi.addStep(workspacePath, stepWithoutConfig, {
      insertAfterStepId: insertAfterStepId
    })

    // If agent_configs was provided, update config separately
    if (agent_configs !== undefined) {
      await agentApi.updateStepConfig(workspacePath, step.id, agent_configs)
    }

    // Refresh plan from backend
    await loadPlan()
  }, [plan, workspacePath, loadPlan])

  // Save global step override

  // Refresh from a live notice without clearing the currently rendered graph.
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
    savePlan,
    saveStepConfig,
    batchSaveStepConfig,
    updateStep,
    deleteStep,
    addStep,
    refresh,
    clearChanges,
    setChanges
  }
}

export default usePlanData
