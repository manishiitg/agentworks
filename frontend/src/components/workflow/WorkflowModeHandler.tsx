import React, { useState, useEffect, useCallback, forwardRef, useImperativeHandle } from 'react'
import { type WorkflowPhase } from '../../constants/workflow'
import { useAppStore } from '../../stores'
import { usePresetApplication, useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'

interface Preset {
  id: string
  name: string
  description: string
}

interface WorkflowModeHandlerProps {
  // Callbacks and children only
  onPresetSelected: (presetId: string, presetContent: string) => void
  children: React.ReactNode
  onWorkflowPhaseChange?: (phase: WorkflowPhase) => void  // Callback to update phase in ChatArea
}

export interface WorkflowModeHandlerRef {
  refreshPresets: () => Promise<void>
}

export const WorkflowModeHandler = forwardRef<WorkflowModeHandlerRef, WorkflowModeHandlerProps>(({
  onPresetSelected,
  children,
  onWorkflowPhaseChange
}, ref) => {

  // Store subscriptions
  const agentMode = useAppStore(state => state.agentMode)
  
  const { getActivePreset } = usePresetApplication()
  
  // Get active preset for workflow mode
  const activeWorkflowPreset = getActivePreset('workflow')
  const selectedWorkflowPreset = activeWorkflowPreset?.id || null
  
  const [availablePresets, setAvailablePresets] = useState<Preset[]>([])
  const [hasAttemptedLoad, setHasAttemptedLoad] = useState<boolean>(false)

  // Load presets function - reads from manifest store
  const loadPresets = useCallback(async () => {
    try {
      // Refresh manifests, then build from store
      await useGlobalPresetStore.getState().refreshPresets()
      const workflowPresets = useGlobalPresetStore.getState().workflowPresets
      const presets = workflowPresets
        .filter(p => p.agentMode === 'workflow')
        .map(p => ({
          id: p.id,
          name: p.label,
          description: p.query || p.label
        }))
      setAvailablePresets(presets)
    } catch (error) {
      console.error('[WORKFLOW] Failed to load presets:', error)
    }
  }, [])

  // Refresh presets function - exposed through ref
  const refreshPresets = useCallback(async () => {
    await loadPresets()
  }, [loadPresets])

  // Load presets when workflow mode is selected
  useEffect(() => {
    if (agentMode === 'workflow' && !hasAttemptedLoad) {
      setHasAttemptedLoad(true)
      loadPresets()
    }
  }, [agentMode, hasAttemptedLoad, loadPresets])

  // Note: No cleanup effect needed here. This component is conditionally rendered
  // (only when selectedModeCategory === 'workflow'), so unmounting handles local state cleanup.
  // Global workflow preset clearing is handled by handleModeSwitchWithPreset in ChatArea.

  // Handle preset restoration when switching to workflow mode
  useEffect(() => {
    let cancelled = false
    if (agentMode === 'workflow' && selectedWorkflowPreset) {
      // Find the preset in available presets
      const selectedPreset = availablePresets.find(p => p.id === selectedWorkflowPreset)
      if (selectedPreset) {
        onPresetSelected(selectedWorkflowPreset, selectedPreset.description)
        const defaultPhase = useWorkflowStore.getState().getDefaultPhase()
        onWorkflowPhaseChange?.(defaultPhase)
      } else {
        // If preset not found, load presets first
        if (!hasAttemptedLoad) {
          setHasAttemptedLoad(true)
          
          const loadPresetsAndSelect = async () => {
            try {
              await useGlobalPresetStore.getState().refreshPresets()
              // The user may have left Goals while manifests were loading.
              // An unmounted handler must never navigate back to its preset.
              if (cancelled) return
              const workflowPresets = useGlobalPresetStore.getState().workflowPresets
              const presets = workflowPresets
                .filter(p => p.agentMode === 'workflow')
                .map(p => ({
                  id: p.id,
                  name: p.label,
                  description: p.query || p.label
                }))
              setAvailablePresets(presets)

              // Now try to find the selected preset
              const foundPreset = presets.find(p => p.id === selectedWorkflowPreset)
              if (foundPreset) {
                onPresetSelected(selectedWorkflowPreset, foundPreset.description)
                const defaultPhase = useWorkflowStore.getState().getDefaultPhase()
                onWorkflowPhaseChange?.(defaultPhase)
              }
            } catch (error) {
              console.error('[WORKFLOW] Failed to load presets:', error)
            }
          }
          
          loadPresetsAndSelect()
        }
      }
    }
    return () => { cancelled = true }
  }, [agentMode, selectedWorkflowPreset, availablePresets, hasAttemptedLoad, onPresetSelected, onWorkflowPhaseChange])

  // Expose workflow handler through ref
  useImperativeHandle(ref, () => ({
    refreshPresets: refreshPresets
  }), [refreshPresets])


  // Show workflow components when in workflow mode
  // WorkflowPhaseHandler removed - phase selection now in WorkflowToolbar
  // Note: This component is only rendered when selectedModeCategory === 'workflow' in ChatArea
  // So we can safely render children here
  return <>{children}</>
})

WorkflowModeHandler.displayName = 'WorkflowModeHandler'
