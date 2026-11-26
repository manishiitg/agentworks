import { randomUUID } from '../utils/randomUUID'
import { create } from 'zustand'
import { agentApi } from '../services/api'
import { useChatStore } from './useChatStore'
import {
  POLLING_INTERVALS,
  WORKFLOW_LIMITS,
  EVENT_CONFIG,
  VALIDATION_CONFIG,
  STORAGE_KEYS
} from '../constants/runningWorkflows'
import {
  hasWorkflowCompletion,
  hasWorkflowError
} from '../utils/workflowEventProcessor'
import { getWorkspaceScopedStorageKey } from './useWorkspaceConnectionStore'

// Running workflow interface for tracking active workflows
export interface RunningWorkflow {
  id: string                    // Unique ID for this running workflow
  presetId: string              // For context restoration
  presetName: string            // Display name
  workspacePath: string         // Workspace context
  sessionId: string             // Backend session ID (for reconnection)
  runFolder: string             // Run folder context
  phaseId: string               // Which phase (planning, execution, etc.)
  phaseName: string             // Display name
  status: 'running' | 'waiting_for_input' | 'completed' | 'failed' | 'paused'
  waitingForInputSince?: number // Timestamp (ms) when waiting_for_input was first set
  waitingMessage?: string       // Message from the blocking event
  currentStepTitle?: string     // Title of the currently executing step
  selectedGroupIds?: string[]   // Selected group IDs (for batch execution)
  minimizedAt: number           // Timestamp when minimized
  lastUpdated: number           // Last status check
  lastProcessedEventIndex: number // Last event index processed (for incremental updates)
  failedPollCount: number       // Number of consecutive failed polls
  lastPollError?: string        // Last poll error message
}

// Helper functions for persistence
const loadRunningWorkflowsFromStorage = (): RunningWorkflow[] => {
  try {
    const storageKey = getWorkspaceScopedStorageKey(STORAGE_KEYS.RUNNING_WORKFLOWS)
    const saved = localStorage.getItem(storageKey)
    if (saved) {
      const workflows = JSON.parse(saved) as RunningWorkflow[]
      const now = Date.now()
      const STALE_THRESHOLD = 3600000 // 1 hour

      const cleaned = workflows
        .map(wf => {
          // Mark any "running" or "waiting_for_input" workflows that are older than 1 hour as stale
          if ((wf.status === 'running' || wf.status === 'waiting_for_input') && (now - wf.lastUpdated) > STALE_THRESHOLD) {
            return { ...wf, status: 'failed' as const }
          }
          return wf
        })
        // Remove completed/failed/paused workflows on load - they have no active sessions
        .filter(wf => wf.status === 'running' || wf.status === 'waiting_for_input')

      // Persist cleaned state back
      localStorage.setItem(storageKey, JSON.stringify(cleaned))
      return cleaned
    }
  } catch (error) {
    console.error('[RunningWorkflowsStore] Failed to load from localStorage:', error)
  }
  return []
}

const saveRunningWorkflowsToStorage = (workflows: RunningWorkflow[]) => {
  try {
    localStorage.setItem(getWorkspaceScopedStorageKey(STORAGE_KEYS.RUNNING_WORKFLOWS), JSON.stringify(workflows))
  } catch (error) {
    console.error('[RunningWorkflowsStore] Failed to save to localStorage:', error)
  }
}

interface RunningWorkflowsStore {
  // State
  runningWorkflows: RunningWorkflow[]
  showRunningDrawer: boolean
  runningPollingInterval: NodeJS.Timeout | null
  isRestoringWorkflow: boolean
  lastValidationTime: number | null
  drawerIsOpen: boolean  // Track drawer state for adaptive polling

  // Actions
  minimizeWorkflow: (params: {
    presetId: string
    presetName: string
    workspacePath: string
    sessionId: string
    runFolder: string
    phaseId: string
    phaseName: string
    selectedGroupIds?: string[]
  }) => void
  restoreWorkflow: (runningWorkflowId: string) => RunningWorkflow | undefined
  removeRunningWorkflow: (id: string) => void
  updateRunningWorkflowStatus: (id: string, updates: Partial<RunningWorkflow>) => void
  setShowRunningDrawer: (show: boolean) => void
  setIsRestoringWorkflow: (isRestoring: boolean) => void
  getRunningWorkflowCount: () => { running: number; total: number }
  refreshRunningWorkflowStatuses: () => void

  // Polling
  startRunningPolling: (drawerOpen?: boolean) => void
  stopRunningPolling: () => void
  pollRunningWorkflows: () => Promise<void>

  // Validation
  validateRunningWorkflows: (force?: boolean) => Promise<void>

  // Cleanup
  cleanupCompletedWorkflows: () => void
}

export const useRunningWorkflowsStore = create<RunningWorkflowsStore>()(
    (set, get) => ({
      // Initial State
      runningWorkflows: loadRunningWorkflowsFromStorage(),
      showRunningDrawer: false,
      runningPollingInterval: null,
      isRestoringWorkflow: false,
      lastValidationTime: null,
      drawerIsOpen: false,

      // Minimize workflow and add to tracked list
      minimizeWorkflow: (params) => {
        const state = get()
        const minimizedAt = Date.now()

        // Check if this workflow is already tracked (by sessionId)
        const existingIndex = state.runningWorkflows.findIndex(
          bg => bg.sessionId === params.sessionId
        )

        if (existingIndex >= 0) {
          // Update existing entry instead of creating duplicate
          const updated = [...state.runningWorkflows]
          updated[existingIndex] = {
            ...updated[existingIndex],
            ...params,
            status: 'running',
            selectedGroupIds: params.selectedGroupIds,
            lastUpdated: Date.now(),
            failedPollCount: 0,  // Reset failure count
            lastProcessedEventIndex: updated[existingIndex].lastProcessedEventIndex || 0
          }
          set({ runningWorkflows: updated })
          saveRunningWorkflowsToStorage(updated)
        } else {
          // Create new running workflow entry
          const runningWorkflow: RunningWorkflow = {
            id: randomUUID(),
            presetId: params.presetId,
            presetName: params.presetName,
            workspacePath: params.workspacePath,
            sessionId: params.sessionId,
            runFolder: params.runFolder,
            phaseId: params.phaseId,
            phaseName: params.phaseName,
            status: 'running',
            selectedGroupIds: params.selectedGroupIds,
            minimizedAt,
            lastUpdated: Date.now(),
            lastProcessedEventIndex: 0,
            failedPollCount: 0
          }

          // Limit to max tracked workflows (remove oldest if exceeded)
          let newRunningWorkflows = [...state.runningWorkflows, runningWorkflow]
          if (newRunningWorkflows.length > WORKFLOW_LIMITS.MAX_TRACKED_WORKFLOWS) {
            // Sort by minimizedAt and keep the most recent
            newRunningWorkflows = newRunningWorkflows
              .sort((a, b) => b.minimizedAt - a.minimizedAt)
              .slice(0, WORKFLOW_LIMITS.MAX_TRACKED_WORKFLOWS)
          }

          set({ runningWorkflows: newRunningWorkflows })
          saveRunningWorkflowsToStorage(newRunningWorkflows)
        }

        // Start polling (adaptive based on drawer state)
        get().startRunningPolling(state.drawerIsOpen)

        // Mirror minimization state into the workflow-owned running registry.
        agentApi.updateRunningWorkflow(params.sessionId, {
          phase_id: params.phaseId,
          phase_name: params.phaseName,
          is_minimized: true,
          minimized_at: minimizedAt,
        }).catch(error => {
          console.warn('[RunningWorkflowsStore] Failed to update running workflow:', error)
        })
      },

      // Restore workflow from tracked list
      restoreWorkflow: (runningWorkflowId: string) => {
        const state = get()
        const runningWorkflow = state.runningWorkflows.find(bg => bg.id === runningWorkflowId)

        if (!runningWorkflow) {
          console.warn(`[RunningWorkflowsStore] Running workflow ${runningWorkflowId} not found`)
          return undefined
        }

        // Remove from running workflows list
        const newRunningWorkflows = state.runningWorkflows.filter(bg => bg.id !== runningWorkflowId)
        set({ runningWorkflows: newRunningWorkflows })
        saveRunningWorkflowsToStorage(newRunningWorkflows)


        // Clear is_minimized flag in the running-workflow registry.
        agentApi.updateRunningWorkflow(runningWorkflow.sessionId, {
          is_minimized: false,
          current_step_title: runningWorkflow.currentStepTitle,
        }).catch(error => {
          console.warn('[RunningWorkflowsStore] Failed to clear minimization:', error)
        })

        return runningWorkflow
      },

      // Remove workflow from tracked list
      removeRunningWorkflow: (id: string) => {
        const state = get()
        const workflow = state.runningWorkflows.find(w => w.id === id)

        // Cleanup events for this session
        if (workflow?.sessionId) {
          useChatStore.getState().cleanupTabEvents?.(
            workflow.sessionId,
            EVENT_CONFIG.MAX_EVENTS_PER_COMPLETED_SESSION
          )
        }

        const newRunningWorkflows = state.runningWorkflows.filter(bg => bg.id !== id)
        set({ runningWorkflows: newRunningWorkflows })
        saveRunningWorkflowsToStorage(newRunningWorkflows)
      },

      // Update workflow status
      updateRunningWorkflowStatus: (id: string, updates: Partial<RunningWorkflow>) => {
        const state = get()
        const index = state.runningWorkflows.findIndex(bg => bg.id === id)

        if (index < 0) return

        const updated = [...state.runningWorkflows]
        updated[index] = {
          ...updated[index],
          ...updates,
          lastUpdated: Date.now()
        }

        set({ runningWorkflows: updated })
        saveRunningWorkflowsToStorage(updated)
      },

      // Show/hide drawer
      setShowRunningDrawer: (show: boolean) => {
        set({ showRunningDrawer: show, drawerIsOpen: show })

        // Restart polling with new rate when drawer state changes
        const state = get()
        if (state.runningPollingInterval) {
          get().startRunningPolling(show)
        }
      },

      setIsRestoringWorkflow: (isRestoring: boolean) => {
        set({ isRestoringWorkflow: isRestoring })
      },

      // Get count of running workflows
      getRunningWorkflowCount: () => {
        const state = get()
        const running = state.runningWorkflows.filter(bg => bg.status === 'running' || bg.status === 'waiting_for_input').length
        return { running, total: state.runningWorkflows.length }
      },

      // Refresh statuses from stored events
      refreshRunningWorkflowStatuses: () => {
        const state = get()
        const chatStore = useChatStore.getState()

        const updated = state.runningWorkflows.map(bg => {
          const events = chatStore.tabEvents[bg.sessionId] || []

          if (events.length > 0) {
            const hasCompletion = hasWorkflowCompletion(events)
            const hasError = hasWorkflowError(events)

            if (hasError) {
              return { ...bg, status: 'failed' as const, lastUpdated: Date.now() }
            }
            if (hasCompletion) {
              return { ...bg, status: 'completed' as const, lastUpdated: Date.now() }
            }
          }

          return bg
        })

        const hasChanges = updated.some((bg, i) => bg.status !== state.runningWorkflows[i].status)
        if (hasChanges) {
          set({ runningWorkflows: updated })
          saveRunningWorkflowsToStorage(updated)
        }
      },

      // Start adaptive polling
      startRunningPolling: (drawerOpen = false) => {
        const state = get()

        // Clear existing interval
        if (state.runningPollingInterval) {
          clearInterval(state.runningPollingInterval)
        }

        const runningCount = state.runningWorkflows.filter(w => w.status === 'running' || w.status === 'waiting_for_input').length

        // Choose polling interval based on context
        let interval: number = POLLING_INTERVALS.BACKGROUND
        if (drawerOpen) {
          interval = POLLING_INTERVALS.ACTIVE
        } else if (runningCount === 0) {
          interval = POLLING_INTERVALS.IDLE
        }

        const pollingInterval = setInterval(() => {
          get().pollRunningWorkflows().catch(error => {
            console.error('[RunningWorkflowsStore] Error polling running workflows:', error)
          })
        }, interval)

        set({ runningPollingInterval: pollingInterval })
      },

      // Stop polling
      stopRunningPolling: () => {
        const state = get()
        if (state.runningPollingInterval) {
          clearInterval(state.runningPollingInterval)
          set({ runningPollingInterval: null })
        }
      },

      // Poll events for all running workflows (with optimizations)
      pollRunningWorkflows: async () => {
        const state = get()
        // Poll running and waiting_for_input workflows
        const workflowsToPoll = state.runningWorkflows.filter(bg =>
          bg.status === 'running' || bg.status === 'waiting_for_input'
        )

        if (workflowsToPoll.length === 0) {
          // Stop polling when no workflows to poll
          if (state.runningPollingInterval) {
            get().stopRunningPolling()
          }
          return
        }

        const chatStore = useChatStore.getState()

        for (const bg of workflowsToPoll) {
          // Auto-stop workflows that have been waiting for input too long
          if (bg.status === 'waiting_for_input' && bg.waitingForInputSince) {
            if (Date.now() - bg.waitingForInputSince > WORKFLOW_LIMITS.WAITING_INPUT_AUTO_STOP_MS) {
              console.warn(`[RunningWorkflowsStore] Auto-stopping ${bg.id} (waited for input > 30min)`)
              agentApi.stopSession(bg.sessionId).catch(err => {
                console.warn('[RunningWorkflowsStore] Auto-stop failed:', err)
              })
              get().updateRunningWorkflowStatus(bg.id, { status: 'paused', waitingForInputSince: undefined, waitingMessage: undefined })
              continue
            }
          }

          // Mark as failed if too many consecutive poll failures
          if (bg.failedPollCount >= VALIDATION_CONFIG.MAX_POLL_RETRIES) {
            if (bg.status === 'running' || bg.status === 'waiting_for_input') {
              console.warn(`[RunningWorkflowsStore] Marking ${bg.id} as failed (too many poll failures)`)
              get().updateRunningWorkflowStatus(bg.id, { status: 'failed' })
            }
            continue
          }

          try {
            // Get last processed event index (use 0 if not set)
            const lastIndex = Math.max(0, bg.lastProcessedEventIndex || 0)

            // Poll for new events
            const response = await agentApi.getSessionEvents(bg.sessionId, lastIndex)

            // Check session status from response
            // For workflows, we prioritize completion events (orchestrator_end via
            // hasWorkflowCompletion) over session_status because session_status
            // might be 'completed' when a single agent finishes, not when the
            // whole workflow finishes
            let shouldMarkCompleted = false
            let shouldMarkPaused = false
            let shouldMarkFailed = false
            let hasRunningEvents = false  // Track if we see events indicating workflow is still running
            let shouldMarkWaitingForInput = false
            let shouldResumeFromWaiting = false
            let newWaitingMessage: string | undefined

            if (response.session_status === 'active' || response.session_status === 'running') {
              if (bg.status === 'completed' || bg.status === 'failed') {
                // Workflow was marked completed/failed but is now active again - resume tracking
                get().updateRunningWorkflowStatus(bg.id, {
                  status: 'running',
                  failedPollCount: 0,
                  lastPollError: undefined
                })
              }
            } else if (response.session_status === 'stopped') {
              shouldMarkPaused = true
            } else if (response.session_status === 'error') {
              shouldMarkFailed = true
            }
            // Note: We don't mark as completed based on session_status alone for workflows
            // We'll check events first for a completion signal (hasWorkflowCompletion)

            if (response.events && response.events.length > 0) {
              // Add events to store (for later restore)
              chatStore.addTabEvents(bg.sessionId, response.events)

              // Update last event index
              const newLastIndex = response.last_processed_index ?? (lastIndex + response.events.length)
              chatStore.setTabLastEventIndex(bg.sessionId, newLastIndex)

              // Process events for progress updates
              for (const event of response.events) {
                // Check for events that indicate workflow is still running
                const runningEventTypes = [
                  'agent_start',
                  'tool_call_start',
                  'tool_call_end',
                  'llm_generation_end',
                  'orchestrator_agent_start',
                  'orchestrator_agent_end'
                ]
                if (event.type && runningEventTypes.includes(event.type)) {
                  hasRunningEvents = true
                }

                // Blocking-input events — workflow is waiting for human response
                if (event.type === 'blocking_human_feedback' || event.type === 'request_human_feedback' || event.type === 'plan_approval') {
                  shouldMarkWaitingForInput = true
                  shouldResumeFromWaiting = false
                  const d = event.data as Record<string, unknown> | undefined
                  for (const key of ['question', 'message', 'prompt', 'title', 'action_description']) {
                    const val = d?.[key]
                    if (typeof val === 'string' && val) {
                      newWaitingMessage = val.length > 160 ? val.slice(0, 157) + '...' : val
                      break
                    }
                  }
                }

                // Resume events — workflow got a response or ended
                if (event.type === 'conversation_end' || event.type === 'context_cancelled' || event.type === 'human_feedback_resolved') {
                  shouldResumeFromWaiting = true
                  shouldMarkWaitingForInput = false
                }

                // Check for completion/error events (primary check for workflows)
                if (event.type && hasWorkflowCompletion([event])) {
                  shouldMarkCompleted = true
                } else if (event.type && hasWorkflowError([event])) {
                  shouldMarkFailed = true
                }
              }

              // Update last processed event index
              get().updateRunningWorkflowStatus(bg.id, {
                lastProcessedEventIndex: newLastIndex
              })
            }

            // Apply status updates after processing events
            if (hasRunningEvents && bg.status === 'completed') {
              get().updateRunningWorkflowStatus(bg.id, {
                status: 'running',
                failedPollCount: 0,
                lastPollError: undefined
              })
            } else if (shouldMarkFailed) {
              get().updateRunningWorkflowStatus(bg.id, { status: 'failed', waitingForInputSince: undefined, waitingMessage: undefined })
              continue
            } else if (shouldMarkCompleted) {
              get().updateRunningWorkflowStatus(bg.id, { status: 'completed', waitingForInputSince: undefined, waitingMessage: undefined })
              chatStore.cleanupTabEvents?.(bg.sessionId, EVENT_CONFIG.MAX_EVENTS_PER_COMPLETED_SESSION)
              continue
            } else if (shouldMarkPaused) {
              get().updateRunningWorkflowStatus(bg.id, { status: 'paused', waitingForInputSince: undefined, waitingMessage: undefined })
              continue
            } else if (shouldMarkWaitingForInput && bg.status !== 'waiting_for_input') {
              get().updateRunningWorkflowStatus(bg.id, {
                status: 'waiting_for_input',
                waitingForInputSince: Date.now(),
                waitingMessage: newWaitingMessage,
              })
            } else if (shouldResumeFromWaiting && bg.status === 'waiting_for_input') {
              get().updateRunningWorkflowStatus(bg.id, {
                status: 'running',
                waitingForInputSince: undefined,
                waitingMessage: undefined,
              })
            }

            // Reset failure count on success
            if (bg.failedPollCount > 0) {
              get().updateRunningWorkflowStatus(bg.id, {
                failedPollCount: 0,
                lastPollError: undefined
              })
            }

          } catch (error) {
            console.error(`[RunningWorkflowsStore] Error polling workflow ${bg.id}:`, error)

            const newFailureCount = bg.failedPollCount + 1
            get().updateRunningWorkflowStatus(bg.id, {
              failedPollCount: newFailureCount,
              lastPollError: error instanceof Error ? error.message : 'Unknown error'
            })

            // Show toast on max failures
            if (newFailureCount >= VALIDATION_CONFIG.MAX_POLL_RETRIES) {
              chatStore.addToast?.(
                `Failed to update automation "${bg.presetName}" - check connection`,
                'error'
              )
            }
          }
        }

        // Auto-cleanup old completed workflows
        get().cleanupCompletedWorkflows()
      },

      // Validate running workflows against backend (with caching)
      validateRunningWorkflows: async (force = false) => {
        const state = get()
        const now = Date.now()

        // Skip if recently validated (unless forced)
        if (!force && state.lastValidationTime) {
          const age = now - state.lastValidationTime
          if (age < VALIDATION_CONFIG.VALIDATION_CACHE_TTL) {
            return
          }
        }

        if (state.runningWorkflows.length === 0) return

        // Fetch the server-side list once — it now includes needs_user_input.
        const backendMap: Map<string, { needs_user_input?: boolean; waiting_message?: string; waiting_since?: string }> = new Map()
        try {
          const { running } = await agentApi.listRunningWorkflows()
          for (const wf of running) {
            backendMap.set(wf.session_id, wf)
          }
        } catch {
          // Fall through — we'll still do per-session status checks below
        }

        const validWorkflows: RunningWorkflow[] = []

        for (const bg of state.runningWorkflows) {
          try {
            const sessionStatus = await agentApi.getSessionStatus(bg.sessionId)

            if (sessionStatus) {
              let updatedStatus = bg.status
              const backend = backendMap.get(bg.sessionId)

              if (sessionStatus.status === 'error') {
                updatedStatus = 'failed'
              } else if (sessionStatus.status === 'completed') {
                updatedStatus = 'completed'
              } else if (sessionStatus.status === 'stopped') {
                updatedStatus = 'paused'
              } else if (sessionStatus.status === 'active' || sessionStatus.status === 'running') {
                // Use backend needs_user_input if available; otherwise keep existing waiting state
                if (backend?.needs_user_input) {
                  updatedStatus = 'waiting_for_input'
                } else if (bg.status === 'waiting_for_input') {
                  // Backend says active but no blocking event found — resume running
                  updatedStatus = 'running'
                } else {
                  updatedStatus = 'running'
                }
              }

              validWorkflows.push({
                ...bg,
                status: updatedStatus,
                waitingMessage: updatedStatus === 'waiting_for_input' ? (backend?.waiting_message ?? bg.waitingMessage) : undefined,
                waitingForInputSince: updatedStatus === 'waiting_for_input'
                  ? (bg.waitingForInputSince ?? (backend?.waiting_since ? new Date(backend.waiting_since).getTime() : now))
                  : undefined,
                lastUpdated: now
              })
            } else {
              // Session not found on backend — remove it
              console.warn(`[RunningWorkflowsStore] Session ${bg.sessionId} not found, removing`)
            }
          } catch {
            console.warn(`[RunningWorkflowsStore] Session ${bg.sessionId} not found, removing`)
          }
        }

        set({ runningWorkflows: validWorkflows, lastValidationTime: now })
        saveRunningWorkflowsToStorage(validWorkflows)
      },

      // Cleanup old completed workflows
      cleanupCompletedWorkflows: () => {
        const state = get()
        const now = Date.now()

        const filtered = state.runningWorkflows.filter(wf => {
          // Keep active workflows (running or waiting for input)
          if (wf.status === 'running' || wf.status === 'waiting_for_input') return true

          // Remove old completed/failed workflows
          const age = now - wf.lastUpdated
          if (age > WORKFLOW_LIMITS.COMPLETED_WORKFLOW_TTL) {
            // Cleanup events
            if (wf.sessionId) {
              useChatStore.getState().cleanupTabEvents?.(
                wf.sessionId,
                EVENT_CONFIG.MAX_EVENTS_PER_COMPLETED_SESSION
              )
            }
            return false
          }

          return true
        })

        if (filtered.length !== state.runningWorkflows.length) {
          set({ runningWorkflows: filtered })
          saveRunningWorkflowsToStorage(filtered)
        }
      },
    })
)

// Auto-validate on startup: check all "running" workflows against backend
// Runs once after store creation with a short delay to let the app initialize
const initialWorkflows = useRunningWorkflowsStore.getState().runningWorkflows
if (initialWorkflows.some(wf => wf.status === 'running')) {
  setTimeout(() => {
    useRunningWorkflowsStore.getState().validateRunningWorkflows(true).catch(err => {
      console.warn('[RunningWorkflowsStore] Startup validation failed:', err)
    })
  }, 3000) // 3s delay to let backend connection establish
}

// Selector hooks
export const useRunningWorkflows = () => useRunningWorkflowsStore(state => state.runningWorkflows)
export const useShowRunningDrawer = () => useRunningWorkflowsStore(state => state.showRunningDrawer)
export const useRunningWorkflowsRunningCount = () => useRunningWorkflowsStore(
  state => state.runningWorkflows.filter(bg => bg.status === 'running' || bg.status === 'waiting_for_input').length
)
export const useRunningWorkflowsTotalCount = () => useRunningWorkflowsStore(
  state => state.runningWorkflows.length
)
