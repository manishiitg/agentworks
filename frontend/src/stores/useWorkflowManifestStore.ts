import { create } from 'zustand'
import { useChatStore } from './useChatStore'
import { workflowManifestApi } from '../services/api'
import type {
  WorkflowManifest,
  DiscoveredWorkflow,
  WorkflowCapabilities,
  WorkflowExecutionDefaults,
  WorkflowOwnership,
  WorkflowScheduleEntry,
  PulseAutonomyLevel, PulseAutonomyRun, PulseReviewerModule, PulsePace } from '../services/api-types'
import { normalizeWorkspacePath } from '../utils/workspacePathUtils'

export interface WorkflowManifestState {
  // Discovered workflows from manifest scan
  workflows: DiscoveredWorkflow[]
  isLoading: boolean
  lastRefreshed: number | null

  // Active workflow (selected for editing/execution)
  activeWorkflowId: string | null

  // Actions
  refreshWorkflows: () => Promise<void>
  getActiveWorkflow: () => DiscoveredWorkflow | null
  setActiveWorkflowId: (id: string | null) => void
  getWorkflowByPath: (workspacePath: string) => DiscoveredWorkflow | undefined
  getWorkflowById: (workflowId: string) => DiscoveredWorkflow | undefined
  replaceWorkflowManifest: (workspacePath: string, manifest: WorkflowManifest) => void

  // CRUD
  createWorkflow: (label: string, workspacePath: string, capabilities?: Partial<WorkflowCapabilities>, icon?: string) => Promise<WorkflowManifest>
  updateWorkflow: (workspacePath: string, updates: {
    label?: string
    icon?: string
    query?: string
    capabilities?: WorkflowCapabilities
    execution_defaults?: WorkflowExecutionDefaults
    ownership?: WorkflowOwnership
    schedules?: WorkflowScheduleEntry[]
    pulse_enabled?: boolean
    pulse_disabled_review_modules?: PulseReviewerModule[]
    pulse_autonomy_level?: number
    pulse_pace?: PulsePace
    pulse_autonomy_run?: PulseAutonomyRun
    pulse_autonomy_outward?: PulseAutonomyLevel
    pulse_autonomy_change?: PulseAutonomyLevel
    pulse_focus_areas?: string[]
  }) => Promise<WorkflowManifest>
  deleteWorkflow: (workspacePath: string) => Promise<void>
  duplicateWorkflow: (sourceWorkspacePath: string, targetWorkspacePath: string, newLabel?: string) => Promise<WorkflowManifest>
}

export const useWorkflowManifestStore = create<WorkflowManifestState>((set, get) => ({
  workflows: [],
  isLoading: false,
  lastRefreshed: null,
  activeWorkflowId: null,

  refreshWorkflows: async () => {
    set({ isLoading: true })
    try {
      const response = await workflowManifestApi.listWorkflowManifests()
      set({
        workflows: response.workflows || [],
        lastRefreshed: Date.now(),
      })
    } catch (error) {
      console.error('[WorkflowManifestStore] Failed to refresh workflows:', error)
    } finally {
      set({ isLoading: false })
    }
  },

  getActiveWorkflow: () => {
    const { workflows, activeWorkflowId } = get()
    if (!activeWorkflowId) return null
    return workflows.find(w => w.manifest.id === activeWorkflowId) ?? null
  },

  setActiveWorkflowId: (id: string | null) => {
    set({ activeWorkflowId: id })
  },

  getWorkflowByPath: (workspacePath: string) => {
    const normalized = normalizeWorkspacePath(workspacePath)
    return get().workflows.find(w => normalizeWorkspacePath(w.workspace_path) === normalized)
  },

  getWorkflowById: (workflowId: string) => {
    return get().workflows.find(w => w.manifest.id === workflowId)
  },

  replaceWorkflowManifest: (workspacePath, manifest) => {
    const normalized = normalizeWorkspacePath(workspacePath)
    set(state => {
      const exists = state.workflows.some(
        workflow => normalizeWorkspacePath(workflow.workspace_path) === normalized,
      )
      return {
        workflows: exists
          ? state.workflows.map(workflow =>
              normalizeWorkspacePath(workflow.workspace_path) === normalized
                ? { ...workflow, manifest }
                : workflow,
            )
          : [...state.workflows, { workspace_path: normalized, manifest }],
        lastRefreshed: Date.now(),
      }
    })
  },

  createWorkflow: async (label, workspacePath, capabilities, icon) => {
    const response = await workflowManifestApi.createWorkflowManifest({
      label,
      icon,
      workspace_path: workspacePath,
      capabilities,
    })
    // Refresh the list to include the new workflow
    await get().refreshWorkflows()
    return response.manifest
  },

  updateWorkflow: async (workspacePath, updates) => {
    const response = await workflowManifestApi.updateWorkflowManifest({
      workspace_path: workspacePath,
      ...updates,
    })
    // Refresh to pick up changes
    await get().refreshWorkflows()
    return response.manifest
  },

  deleteWorkflow: async (workspacePath) => {
    const result = await workflowManifestApi.deleteWorkflowFolder(workspacePath)
    useChatStore.getState().addToast(
      result.cleanup_pending
        ? 'Automation deleted. An administrator needs to finish removing some sandbox files.'
        : 'Automation deleted.',
      result.cleanup_pending ? 'warning' : 'success',
    )
    // Refresh to remove from list
    const { activeWorkflowId, workflows } = get()
    const deleted = workflows.find(w => w.workspace_path === workspacePath)
    if (deleted && deleted.manifest.id === activeWorkflowId) {
      set({ activeWorkflowId: null })
    }
    await get().refreshWorkflows()
  },

  duplicateWorkflow: async (sourceWorkspacePath, targetWorkspacePath, newLabel) => {
    const response = await workflowManifestApi.duplicateWorkflowManifest({
      source_workspace_path: sourceWorkspacePath,
      target_workspace_path: targetWorkspacePath,
      new_label: newLabel,
    })
    await get().refreshWorkflows()
    return response.manifest
  },
}))
