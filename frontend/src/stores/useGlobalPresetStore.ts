import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { useShallow } from 'zustand/react/shallow'
import { agentApi, workflowManifestApi } from '../services/api'
import type { PlannerFile, PresetLLMConfig } from '../services/api-types'
import type { CustomPreset, PredefinedPreset } from '../types/preset'
import { useAppStore } from './useAppStore'
import { type ModeCategory } from './useModeStore'
import { useWorkspaceStore } from './useWorkspaceStore'
import { useMCPStore } from './useMCPStore'
import { useLLMStore } from './useLLMStore'
import { useWorkflowStore } from './useWorkflowStore'
import { useSecretsStore } from './useSecretsStore'
import { useWorkflowManifestStore } from './useWorkflowManifestStore'

// Build workflow presets from manifests
function buildWorkflowPresetsFromManifests(): CustomPreset[] {
  const workflows = useWorkflowManifestStore.getState().workflows || []
  return workflows.map(wf => {
    const caps = wf.manifest.capabilities
    return {
      id: wf.manifest.id || wf.workspace_path,
      label: wf.manifest.label || wf.workspace_path.split('/').pop() || wf.workspace_path,
      icon: wf.manifest.icon,
      createdAt: new Date(wf.manifest.created_at || 0).getTime(),
      agentMode: 'workflow' as const,
      selectedFolder: {
        filepath: wf.workspace_path,
        content: '',
        last_modified: wf.manifest.updated_at || '',
        type: 'folder' as const,
        children: []
      },
      selectedServers: caps?.selected_servers || [],
      selectedTools: caps?.selected_tools || [],
      selectedSkills: caps?.selected_skills || [],
      selectedSecrets: caps?.selected_secrets || [],
      selectedGlobalSecretNames: caps?.selected_global_secret_names ?? null,
      browserMode: (caps?.browser_mode || 'none') as CustomPreset['browserMode'],
      cdpPorts: caps?.cdp_ports || [],
      useCodeExecutionMode: caps?.use_code_execution_mode || false,
      llmConfig: caps?.llm_config ? { ...caps.llm_config } : undefined,
    }
  })
}

export interface PresetApplicationResult {
  success: boolean
  preset?: CustomPreset | PredefinedPreset
  error?: string
}

interface GlobalPresetState {
  // File-backed workflow presets (from manifests)
  workflowPresets: CustomPreset[]
  workflowPresetsLoaded: boolean

  loading: boolean
  error: string | null
  refreshPromise: Promise<void> | null

  // Active preset tracking per mode category
  activePresetIds: Record<Exclude<ModeCategory, null>, string | null>

  // Current preset application state
  currentPresetServers: string[]
  currentPresetTools: string[] // Array of "server:tool" strings
  selectedPresetFolder: string | null
  currentQuery: string

  // Recently accessed preset IDs (most recent first) for quick switcher ordering
  recentPresetOrder: string[]
  recentPresetAccessedAt: Record<string, number>

  // Actions for manifest management
  refreshPresets: () => Promise<void>
  savePreset: (label: string, query?: string, selectedServers?: string[], selectedTools?: string[], selectedSkills?: string[], agentMode?: 'multi-agent' | 'workflow', selectedFolder?: PlannerFile, llmConfig?: PresetLLMConfig, useCodeExecutionMode?: boolean, id?: string, selectedSecrets?: string[], selectedGlobalSecretNames?: string[] | null, browserMode?: 'none' | 'auto' | 'headless' | 'cdp', cdpPorts?: number[], icon?: string) => Promise<CustomPreset | null>
  duplicatePreset: (presetId: string) => Promise<CustomPreset | null>

  // Actions for preset application
  applyPreset: (presetOrId: CustomPreset | PredefinedPreset | string, modeCategory: Exclude<ModeCategory, null>) => PresetApplicationResult
  clearActivePreset: (modeCategory: Exclude<ModeCategory, null>) => void
  getActivePreset: (modeCategory: Exclude<ModeCategory, null>) => CustomPreset | PredefinedPreset | null

  // Actions for current state management
  setCurrentPresetServers: (servers: string[]) => void
  setCurrentPresetTools: (tools: string[]) => void
  setSelectedPresetFolder: (folderPath: string | null) => void
  setCurrentQuery: (query: string) => void
  clearPresetState: () => void
  setActivePreset: (modeCategory: Exclude<ModeCategory, null>, presetId: string | null) => void
  
  // Helper actions
  getPresetsForMode: (modeCategory: Exclude<ModeCategory, null>) => (CustomPreset | PredefinedPreset)[]
  isPresetActive: (presetId: string, modeCategory: Exclude<ModeCategory, null>) => boolean
}

export const useGlobalPresetStore = create<GlobalPresetState>()(
  persist(
    (set, get) => ({
      // Initial state
      workflowPresets: [],
      workflowPresetsLoaded: false,
      loading: false,
      error: null,
      refreshPromise: null,
      
      activePresetIds: {
        'workflow': null,
        'multi-agent': null,
      },

      currentPresetServers: [],
      currentPresetTools: [],
      selectedPresetFolder: null,
      currentQuery: '',
      recentPresetOrder: [],
      recentPresetAccessedAt: {},

      // Manifest management actions
      refreshPresets: async () => {
        const existingPromise = get().refreshPromise
        if (existingPromise) return existingPromise

        const refreshPromise = (async () => {
          set({ loading: true, error: null })
          try {
            // Refresh workflow manifests and rebuild workflow presets
            await useWorkflowManifestStore.getState().refreshWorkflows().catch(() => {})
            const workflowPresets = buildWorkflowPresetsFromManifests()
            const workflowPresetIds = new Set(workflowPresets.map(p => p.id))
            set(state => ({
              workflowPresets,
              workflowPresetsLoaded: true,
              loading: false,
              activePresetIds: {
                ...state.activePresetIds,
                workflow: state.activePresetIds.workflow && workflowPresetIds.has(state.activePresetIds.workflow)
                  ? state.activePresetIds.workflow
                  : null,
              },
              recentPresetOrder: state.recentPresetOrder.filter(id => workflowPresetIds.has(id)),
            }))
          } catch (error) {
            console.error('[PRESET] Error refreshing presets:', error)
            set({
              error: error instanceof Error ? error.message : 'Failed to refresh presets',
              workflowPresetsLoaded: true,
              loading: false
            })
          } finally {
            set({ refreshPromise: null })
          }
        })()

        set({ refreshPromise })
        return refreshPromise
      },
      
      savePreset: async (label, query, selectedServers, selectedTools, selectedSkills, agentMode, selectedFolder, llmConfig, useCodeExecutionMode, id, selectedSecrets, selectedGlobalSecretNames, browserMode, cdpPorts, icon) => {
        const toolsForBackend = selectedTools?.filter(t => !t.endsWith(':*')) || []
        const existingPreset = id ? get().workflowPresets.find(p => p.id === id) : undefined
        const effectiveCDPPorts = cdpPorts ?? existingPreset?.cdpPorts ?? []
        const globalSecretNamesForBackend = selectedGlobalSecretNames === undefined
          ? (existingPreset?.selectedGlobalSecretNames === undefined ? [] : existingPreset.selectedGlobalSecretNames)
          : selectedGlobalSecretNames

        // Selections are secret names; the server resolves them from the
        // workflow box and globals.
        const secretNamesForBackend = selectedSecrets
          ?.filter((n): n is string => !!n) || []

        // Multi-agent chat has a single file-backed capability profile, parallel
        // to workflow.json capabilities rather than DB presets.
        if (agentMode !== 'workflow' || !selectedFolder?.filepath) {
          await agentApi.saveMultiAgentChatCapabilities({
            selected_servers: selectedServers || [],
            selected_tools: toolsForBackend,
            selected_skills: selectedSkills || [],
            selected_secrets: secretNamesForBackend,
            selected_global_secret_names: globalSecretNamesForBackend,
            browser_mode: browserMode || 'none',
            cdp_ports: browserMode === 'cdp' || browserMode === 'auto' ? effectiveCDPPorts : [],
            use_code_execution_mode: useCodeExecutionMode ?? false,
            llm_config: llmConfig || undefined,
          })

          const savedChatPreset: CustomPreset = {
            id: 'multiagent-chat-config',
            label: label || 'Multi-agent chat',
            query,
            createdAt: Date.now(),
            agentMode: 'multi-agent',
            selectedServers: selectedServers || [],
            selectedTools: toolsForBackend,
            selectedSkills: selectedSkills || [],
            selectedSecrets: secretNamesForBackend,
            selectedGlobalSecretNames: globalSecretNamesForBackend,
            browserMode: (browserMode || 'none') as CustomPreset['browserMode'],
            cdpPorts: browserMode === 'cdp' || browserMode === 'auto' ? effectiveCDPPorts : [],
            useCodeExecutionMode: useCodeExecutionMode ?? false,
            llmConfig: llmConfig || undefined,
          }
          set(state => ({
            activePresetIds: {
              ...state.activePresetIds,
              'multi-agent': savedChatPreset.id,
            },
            currentPresetServers: savedChatPreset.selectedServers || [],
            currentPresetTools: savedChatPreset.selectedTools || [],
            currentQuery: query || '',
          }))
          useSecretsStore.getState().setSelectedGlobalSecretNames(globalSecretNamesForBackend)
          return savedChatPreset
        }

        try {
          if (id) {
            // Update existing workflow manifest
            await workflowManifestApi.updateWorkflowManifest({
              workspace_path: selectedFolder.filepath,
              label,
              icon: icon === undefined ? (existingPreset?.icon || '') : icon.trim(),
              capabilities: {
                selected_servers: selectedServers || [],
                selected_tools: toolsForBackend,
                selected_skills: selectedSkills || [],
                selected_secrets: secretNamesForBackend,
                selected_global_secret_names: globalSecretNamesForBackend,
                browser_mode: browserMode || 'none',
                cdp_ports: browserMode === 'cdp' || browserMode === 'auto' ? effectiveCDPPorts : [],
                use_code_execution_mode: useCodeExecutionMode ?? false,
                llm_config: llmConfig || undefined,
              },
            })
          } else {
            // Create new workflow manifest
            await workflowManifestApi.createWorkflowManifest({
              label,
              icon: icon?.trim() || undefined,
              workspace_path: selectedFolder.filepath,
              capabilities: {
                selected_servers: selectedServers || [],
                selected_tools: toolsForBackend,
                selected_skills: selectedSkills || [],
                selected_secrets: secretNamesForBackend,
                selected_global_secret_names: globalSecretNamesForBackend,
                browser_mode: browserMode || 'none',
                cdp_ports: browserMode === 'cdp' || browserMode === 'auto' ? effectiveCDPPorts : [],
                use_code_execution_mode: useCodeExecutionMode ?? false,
                llm_config: llmConfig || undefined,
              },
            })
          }

          // Refresh workflow presets from manifests
          await useWorkflowManifestStore.getState().refreshWorkflows().catch(() => {})
          const updatedPresets = buildWorkflowPresetsFromManifests()
          set({ workflowPresets: updatedPresets })

          // Sync global secret selection to secrets store if this is the active preset
          if (id) {
            const activeWorkflowId = get().activePresetIds.workflow
            if (id === activeWorkflowId) {
              useSecretsStore.getState().setSelectedGlobalSecretNames(globalSecretNamesForBackend)
            }
          }

          // Return the preset from the refreshed list
          const savedPreset = updatedPresets.find(p =>
            p.selectedFolder?.filepath === selectedFolder.filepath
          )
          return savedPreset || null
        } catch (error) {
          console.error('[PRESET_STORE] Error saving preset:', error)
          throw error
        }
      },

      duplicatePreset: async (presetId) => {
        try {
          const state = get()
          const originalPreset = state.workflowPresets.find(p => p.id === presetId)
          
          if (!originalPreset) {
            throw new Error('Preset not found')
          }
          
          // Find next available version number
          const baseName = originalPreset.label
          const versionRegex = /-v(\d+)$/
          const match = baseName.match(versionRegex)
          const baseNameWithoutVersion = match ? baseName.slice(0, match.index) : baseName
          
          // Find all presets with the same base name
          const existingVersions = state.workflowPresets
            .filter(p => {
              const pMatch = p.label.match(versionRegex)
              const pBaseName = pMatch ? p.label.slice(0, pMatch.index) : p.label
              return pBaseName === baseNameWithoutVersion
            })
            .map(p => {
              const pMatch = p.label.match(versionRegex)
              return pMatch ? parseInt(pMatch[1], 10) : 0
            })
          
          // Find next available version
          let nextVersion = 2
          while (existingVersions.includes(nextVersion)) {
            nextVersion++
          }
          
          const newLabel = `${baseNameWithoutVersion}-v${nextVersion}`
          
          // Handle folder duplication if exists
          let newFolder: PlannerFile | undefined = undefined
          if (originalPreset.selectedFolder?.filepath) {
            const originalFolderPath = originalPreset.selectedFolder.filepath
            const folderPathParts = originalFolderPath.split('/')
            const folderName = folderPathParts[folderPathParts.length - 1]
            const folderNameMatch = folderName.match(versionRegex)
            const baseFolderName = folderNameMatch ? folderName.slice(0, folderNameMatch.index) : folderName
            const newFolderName = `${baseFolderName}-v${nextVersion}`
            
            // Build new folder path
            folderPathParts[folderPathParts.length - 1] = newFolderName
            const newFolderPath = folderPathParts.join('/')
            
            // Create empty new folder instead of copying
            try {
              await agentApi.createPlannerFolder(newFolderPath, `Create folder for duplicated preset ${newLabel}`)
              // Create new folder object with updated path
              newFolder = {
                ...originalPreset.selectedFolder,
                filepath: newFolderPath,
                type: 'folder' as const
              }
              
              // For workflow presets, copy plan.json, step_config.json, and variables.json
              if (originalPreset.agentMode === 'workflow') {
                const planJsonPath = `${originalFolderPath}/planning/plan.json`
                const stepConfigJsonPath = `${originalFolderPath}/planning/step_config.json`
                const variablesJsonPath = `${originalFolderPath}/variables/variables.json`
                const newPlanJsonPath = `${newFolderPath}/planning/plan.json`
                const newStepConfigJsonPath = `${newFolderPath}/planning/step_config.json`
                const newVariablesJsonPath = `${newFolderPath}/variables/variables.json`
                
                // Create planning subdirectory if it doesn't exist
                const planningFolderPath = `${newFolderPath}/planning`
                try {
                  await agentApi.createPlannerFolder(planningFolderPath, `Create planning folder for duplicated workflow preset ${newLabel}`)
                } catch {
                  // Planning folder might already exist, continue
                }
                
                // Create variables subdirectory if it doesn't exist
                const variablesFolderPath = `${newFolderPath}/variables`
                try {
                  await agentApi.createPlannerFolder(variablesFolderPath, `Create variables folder for duplicated workflow preset ${newLabel}`)
                } catch {
                  // Variables folder might already exist, continue
                }
                
                try {
                  // Copy plan.json
                  const planResponse = await agentApi.getPlannerFileContent(planJsonPath)
                  if (planResponse.success && planResponse.data?.content) {
                    await agentApi.updatePlannerFile(
                      newPlanJsonPath,
                      planResponse.data.content,
                      `Copy plan.json for duplicated workflow preset ${newLabel}`
                    )
                  }
                } catch {
                  // Continue even if plan.json copy fails
                }
                
                try {
                  // Copy step_config.json
                  const stepConfigResponse = await agentApi.getPlannerFileContent(stepConfigJsonPath)
                  if (stepConfigResponse.success && stepConfigResponse.data?.content) {
                    await agentApi.updatePlannerFile(
                      newStepConfigJsonPath,
                      stepConfigResponse.data.content,
                      `Copy step_config.json for duplicated workflow preset ${newLabel}`
                    )
                  }
                } catch {
                  // Continue even if step_config.json copy fails
                }
                
                try {
                  // Copy variables.json
                  const variablesResponse = await agentApi.getPlannerFileContent(variablesJsonPath)
                  if (variablesResponse.success && variablesResponse.data?.content) {
                    await agentApi.updatePlannerFile(
                      newVariablesJsonPath,
                      variablesResponse.data.content,
                      `Copy variables.json for duplicated workflow preset ${newLabel}`
                    )
                  }
                } catch {
                  // Continue even if variables.json copy fails
                }
              }
            } catch {
              // Continue without folder if creation fails
              // Reset newFolder to undefined if folder creation failed
              newFolder = undefined
            }
          }
          
          // Create new preset with duplicated data
          const newPreset = await state.savePreset(
            newLabel,
            originalPreset.query,
            originalPreset.selectedServers,
            originalPreset.selectedTools,
            originalPreset.selectedSkills, // Skill folder names
            originalPreset.agentMode,
            newFolder,
            originalPreset.llmConfig,
            originalPreset.useCodeExecutionMode,
            undefined, // id (new preset)
            originalPreset.selectedSecrets, // Copy secret selections
            originalPreset.selectedGlobalSecretNames, // Copy global secret selection
            originalPreset.browserMode,
            originalPreset.cdpPorts,
            originalPreset.icon
          )
          
          // If original preset had a workflow, create a new workflow for the duplicated preset
          if (originalPreset.agentMode === 'workflow' && newPreset) {
            try {
              const workflowStatus = await agentApi.getWorkflowStatus(presetId)
              if (workflowStatus.success && workflowStatus.workflow) {
                // Create new workflow with same status and selected options
                await agentApi.createWorkflow(newPreset.id, false) // humanVerificationRequired = false
                
                // Update workflow with same status and selected options if they exist
                if (workflowStatus.workflow.workflow_status || workflowStatus.workflow.selected_options) {
                  await agentApi.updateWorkflow(
                    newPreset.id,
                    workflowStatus.workflow.workflow_status,
                    workflowStatus.workflow.selected_options || null
                  )
                }
              }
            } catch {
              // Continue even if workflow duplication fails
            }
          }
          
          return newPreset
        } catch (error) {
          console.error('[PRESET] Error duplicating preset:', error)
          throw error
        }
      },
      
      // Unified preset application function - handles both preset objects and preset IDs
      applyPreset: (presetOrId, modeCategory) => {
        try {
          let preset: CustomPreset | PredefinedPreset | null = null
          
          // Handle different input types
          if (typeof presetOrId === 'string') {
            // If string, treat as preset ID and find in workflow presets
            const state = get()
            preset = state.workflowPresets.find(p => p.id === presetOrId) || null
            
            if (!preset) {
              return {
                success: false,
                error: 'Preset not found'
              }
            }
          } else {
            // If object, use it directly
            preset = presetOrId as CustomPreset | PredefinedPreset
          }
          
          // Note: Session IDs are now managed per-tab, not globally
          // When a preset is applied, tabs will be created with new session IDs as needed

          // Handle workflow state when switching workflows
          if (modeCategory === 'workflow') {
            const workflowStore = useWorkflowStore.getState()

            // Save current preset's settings before switching away
            const currentPresetId = get().activePresetIds.workflow
            if (currentPresetId && currentPresetId !== preset.id) {
              workflowStore.saveSettings()
            }

            // Switch to new preset - resets context and loads saved settings in one update
            workflowStore.switchToPreset(preset.id)
          }

          // Set the current query in both stores (only if query exists and is non-empty)
          if (preset.query && preset.query.trim()) {
            set({ currentQuery: preset.query })

            // Also update the AppStore's currentQuery for ChatInput/ChatArea components
            useAppStore.getState().setCurrentQuery(preset.query)
          }
          
          // Set server selection
          const servers = preset.selectedServers || []
          set({ currentPresetServers: servers })

          // Set tool selection from preset
          const tools = preset.selectedTools || []
          set({ currentPresetTools: tools })

          // Keep MCP store in sync so UI reflects selection (mode-specific)
          // Workflow mode: sync to workflowSelectedServers
          // Chat mode: don't sync - let user's manual selection persist
          if (modeCategory === 'workflow') {
            try {
              const { setWorkflowSelectedServers } = useMCPStore.getState()
              if (typeof setWorkflowSelectedServers === 'function') {
                setWorkflowSelectedServers(servers)
              }
            } catch (error) {
              console.warn('[GlobalPresetStore] Failed to sync MCP store:', error)
            }
          }
          // For chat mode, don't sync to global MCP store - let user's manual selection persist in chatSelectedServers
          
          // Set folder selection
          const folderPath = preset.selectedFolder?.filepath || null
          set({ selectedPresetFolder: folderPath })
          
          // Apply LLM configuration if preset has one (mode-specific)
          if (preset.llmConfig) {
            const llmState = useLLMStore.getState()
            const {
              getConfigForMode,
              setChatPrimaryConfig,
              setWorkflowPrimaryConfig,
              setPrimaryConfig // Also update legacy for backward compatibility
            } = llmState

            // Get the appropriate mode-specific config as base
            const mode: 'multi-agent' | 'workflow' = modeCategory === 'workflow' ? 'workflow' : 'multi-agent'
            const modeConfig = getConfigForMode(mode)
            const currentPrimaryConfig = modeConfig.primaryConfig

            const profileBuilder = preset.llmConfig.mode === 'provider_profile' && preset.llmConfig.provider
              ? llmState.providerManifest.find(provider => provider.id === preset.llmConfig?.provider)?.default_tier_models?.builder
              : undefined
            const selectedBuilder = preset.llmConfig.builder_llm || profileBuilder
            const updatedConfig = {
              ...currentPrimaryConfig, // Preserve existing mode-specific configuration
              provider: (selectedBuilder?.provider || currentPrimaryConfig.provider) as typeof currentPrimaryConfig.provider,
              model_id: selectedBuilder?.model_id || currentPrimaryConfig.model_id,
              options: selectedBuilder?.options || currentPrimaryConfig.options,
            }

            // Update mode-specific config
            if (modeCategory === 'workflow') {
              setWorkflowPrimaryConfig(updatedConfig)
            } else {
              setChatPrimaryConfig(updatedConfig)
            }

            // Also update legacy config for backward compatibility
            setPrimaryConfig(updatedConfig)
          }
          
          // Sync per-preset global secret selection to secrets store
          if ('selectedGlobalSecretNames' in preset) {
            const presetGlobalSecrets = (preset as CustomPreset).selectedGlobalSecretNames
            useSecretsStore.getState().setSelectedGlobalSecretNames(presetGlobalSecrets ?? null)
          }

          // Handle workspace folder selection
          if (folderPath) {
            // Clear any previously selected file in workspace
            useWorkspaceStore.getState().setSelectedFile(null)
            
            // Select the preset folder in workspace
            useWorkspaceStore.getState().setSelectedFile({
              name: folderPath.split('/').pop() || folderPath,
              path: folderPath
            })
            
            // Clear file content view to show folder structure
            useWorkspaceStore.getState().setShowFileContent(false)
            
            // Expand the folder to show its contents
            const { expandFoldersForFile } = useWorkspaceStore.getState()
            expandFoldersForFile(folderPath)
            
            // Note: File context is now preset-specific (stored in preset.selectedFolder)
            // No need to manipulate global file context
          } else {
            // Clear workspace selection if no folder
            useWorkspaceStore.getState().setSelectedFile(null)
          }
          
          // Set active preset ID and update recent access order
          set(state => ({
            activePresetIds: {
              ...state.activePresetIds,
              [modeCategory]: preset.id
            },
            recentPresetOrder: [
              preset.id,
              ...state.recentPresetOrder.filter(id => id !== preset.id)
            ].slice(0, 20),
            recentPresetAccessedAt: {
              ...state.recentPresetAccessedAt,
              [preset.id]: Date.now()
            }
          }))

          return {
            success: true,
            preset
          }
        } catch (error) {
          console.error('[PRESET] Error applying preset:', error)
          return {
            success: false,
            error: error instanceof Error ? error.message : 'Failed to apply preset'
          }
        }
      },
      
      // Clear active preset for a mode category
      clearActivePreset: (modeCategory) => {
        set(state => ({
          activePresetIds: {
            ...state.activePresetIds,
            [modeCategory]: null
          }
        }))
      },
      
      getActivePreset: (modeCategory) => {
        const state = get()
        const presetId = state.activePresetIds[modeCategory]
        if (!presetId) return null
        return state.workflowPresets.find(p => p.id === presetId) ?? null
      },
      
      // Current state management
      setCurrentPresetServers: (servers) => {
        set({ currentPresetServers: servers })
      },
      
      setCurrentPresetTools: (tools) => {
        set({ currentPresetTools: tools })
      },
      
      setSelectedPresetFolder: (folderPath) => {
        set({ selectedPresetFolder: folderPath })
      },
      
      setCurrentQuery: (query) => {
        set({ currentQuery: query })
      },
      
      clearPresetState: () => {
        set({
          currentPresetServers: [],
          currentPresetTools: [],
          selectedPresetFolder: null,
          currentQuery: '',
          activePresetIds: {
            'workflow': null,
            'multi-agent': null,
          }
        })
      },

      setActivePreset: (modeCategory: Exclude<ModeCategory, null>, presetId: string | null) => {
        set(state => ({
          activePresetIds: {
            ...state.activePresetIds,
            [modeCategory]: presetId
          }
        }))
      },
      
      // Helper actions
      getPresetsForMode: (modeCategory) => {
        if (modeCategory === 'workflow') {
          return get().workflowPresets
        }
        // Non-workflow modes no longer have DB-backed presets
        return []
      },
      
      isPresetActive: (presetId, modeCategory) => {
        return get().activePresetIds[modeCategory] === presetId
      }
    }),
    {
      name: 'global-preset-storage',
      // Only persist UI session state — presets come from manifests on load.
      partialize: (state) => ({
        activePresetIds: state.activePresetIds,
        currentPresetServers: state.currentPresetServers,
        currentPresetTools: state.currentPresetTools,
        selectedPresetFolder: state.selectedPresetFolder,
        currentQuery: state.currentQuery,
        recentPresetOrder: state.recentPresetOrder,
        recentPresetAccessedAt: state.recentPresetAccessedAt
      })
    }
  )
)

// Export convenience hooks for specific functionality
export const usePresetApplication = () => {
  return useGlobalPresetStore(useShallow(store => ({
    applyPreset: store.applyPreset,
    clearActivePreset: store.clearActivePreset,
    getActivePreset: store.getActivePreset,
    clearPresetState: store.clearPresetState,
    isPresetActive: store.isPresetActive,
    getPresetsForMode: store.getPresetsForMode,
    currentPresetServers: store.currentPresetServers,
    currentPresetTools: store.currentPresetTools,
    activePresetIds: store.activePresetIds,
    workflowPresets: store.workflowPresets,
  })))
}

export const usePresetManagement = () => {
  return useGlobalPresetStore(useShallow(store => ({
    workflowPresets: store.workflowPresets,
    workflowPresetsLoaded: store.workflowPresetsLoaded,
    loading: store.loading,
    error: store.error,
    refreshPresets: store.refreshPresets,
    savePreset: store.savePreset,
    duplicatePreset: store.duplicatePreset,
  })))
}

export const usePresetState = () => {
  return useGlobalPresetStore(useShallow(store => ({
    currentPresetServers: store.currentPresetServers,
    selectedPresetFolder: store.selectedPresetFolder,
    currentQuery: store.currentQuery,
    setCurrentPresetServers: store.setCurrentPresetServers,
    setSelectedPresetFolder: store.setSelectedPresetFolder,
    setCurrentQuery: store.setCurrentQuery,
  })))
}
