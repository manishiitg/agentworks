import { stripRetiredLLMFallbacks } from '../utils/retiredLLMFallbacks'
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { LLMConfiguration, AgentLLMConfiguration, SavedLLM, LLMModel, DelegationTierConfig, LLMProvider } from '../services/api-types'
import type { DelegationTierDefaultsStatus } from '../utils/llmOnboarding'
import type { LLMOption } from '../types/llm'
import type { StoreActions } from './types'
import { llmConfigService, type ModelMetadata, type ProviderManifestEntry, type DynamicModelsResponse } from '../services/llm-config-api'
import { agentApi } from '../services/api'

type PublishedLLMMetadataSnapshot = {
  context_window?: number
  input_cost_per_1m?: number
  output_cost_per_1m?: number
  reasoning_cost_per_1m?: number
  cached_input_cost_per_1m?: number
  cached_input_cost_write_per_1m?: number
}

const DEFAULT_CHAT_PROVIDER: LLMProvider = 'codex-cli'
const DEFAULT_CHAT_MODEL = 'codex-cli'
// Only coding-agent CLIs run agents. Direct API providers (and the retired
// media providers) are dropped wherever they appear in saved or persisted
// config; their models are reachable only through Pi's sub-provider routing.
const FRONTEND_DEPRECATED_PROVIDER_IDS = new Set<string>([
  'openai',
  'anthropic',
  'vertex',
  'bedrock',
  'azure',
  'openrouter',
  'z-ai',
  'kimi',
  'minimax',
  'minimax-coding-plan',
  'elevenlabs',
  'deepgram',
])
const SUPPORTED_PROVIDERS_FALLBACK: LLMProvider[] = [
  'claude-code',
  'codex-cli',
  'cursor-cli',
  'pi-cli',
  'muse-cli',
  'agy-cli',
]

function isFrontendDeprecatedProvider(provider?: string): boolean {
  return !!provider && FRONTEND_DEPRECATED_PROVIDER_IDS.has(provider)
}

function hasUsableLLMIdentity(model?: { provider?: string; model_id?: string }): model is { provider: LLMProvider; model_id: string } {
  return !!model?.provider?.trim() && !!model?.model_id?.trim() && !isFrontendDeprecatedProvider(model.provider)
}

function defaultLLMConfiguration(): LLMConfiguration {
  return {
    provider: DEFAULT_CHAT_PROVIDER,
    model_id: DEFAULT_CHAT_MODEL,
  }
}

function normalizePrimaryConfig(config?: LLMConfiguration): LLMConfiguration {
  if (!config || !hasUsableLLMIdentity(config)) {
    return defaultLLMConfiguration()
  }
  return {
    published_llm_id: config.published_llm_id,
    provider: config.provider,
    model_id: config.model_id,
    options: config.options,
    api_keys: config.api_keys,
  }
}

function sanitizeLLMModel(model: LLMModel): LLMModel {
  return {
    provider: model.provider,
    model_id: model.model_id,
    region: model.region,
    options: model.options,
  }
}

function normalizeLLMModel(model?: LLMModel): LLMModel {
  if (!model || !hasUsableLLMIdentity(model)) {
    return {
      provider: DEFAULT_CHAT_PROVIDER,
      model_id: DEFAULT_CHAT_MODEL,
    }
  }
  return sanitizeLLMModel(model)
}

function sanitizeSavedLLM(llm: SavedLLM): SavedLLM {
  return {
    ...sanitizeLLMModel(llm),
    id: llm.id,
    name: llm.name,
    source: llm.source,
    created_at: llm.created_at,
  }
}

function isAutoPublishedLLM(llm: SavedLLM): boolean {
  return llm.source === 'auto_coding_agent' || llm.id?.startsWith('auto:')
}

function persistablePublishedLLMs(llms: SavedLLM[]): SavedLLM[] {
  return filterPublishedLLMs(llms).filter(llm => !isAutoPublishedLLM(llm))
}

function filterPublishedLLMs(llms: SavedLLM[]): SavedLLM[] {
  return llms.filter(llm =>
    hasUsableLLMIdentity(llm) &&
    !FRONTEND_DEPRECATED_PROVIDER_IDS.has(llm.provider)
  )
}

function sanitizeAgentConfig(config: AgentLLMConfiguration | null): AgentLLMConfiguration | null {
  if (!config) return null
  return {
    primary: normalizeLLMModel(config.primary),
  }
}

interface LLMState extends StoreActions {
  // Primary LLM configuration (unified from sidebar and chat input)
  // LEGACY: kept for backward compatibility, use mode-specific configs instead
  primaryConfig: LLMConfiguration

  // New unified configuration (Tiered Model Selection)
  // LEGACY: kept for backward compatibility, use mode-specific configs instead
  agentConfig: AgentLLMConfiguration | null

  // Mode-specific LLM configurations (multi-agent vs workflow)
  chatPrimaryConfig: LLMConfiguration
  chatAgentConfig: AgentLLMConfiguration | null
  workflowPrimaryConfig: LLMConfiguration
  workflowAgentConfig: AgentLLMConfiguration | null

  // Saved/Published LLM Library
  savedLLMs: SavedLLM[]
  
  // Modal state
  showLLMModal: boolean

  // Available LLMs for selection
  availableLLMs: LLMOption[]
  modelMetadataCatalog: ModelMetadata[]
  
  // Loading and error states
  isLoadingLLMs: boolean
  error: string | null
  defaultsLoaded: boolean

  // Supported providers (from backend, not persisted)
  supportedProviders: LLMProvider[]
  providerCapabilities: Partial<Record<LLMProvider, string[]>>
  isProviderSupported: (provider: string) => boolean

  // Provider manifest (API-driven provider discovery)
  providerManifest: ProviderManifestEntry[]
  providerManifestLoaded: boolean
  providerManifestLoading: boolean
  loadProviderManifest: () => Promise<void>
  getProviderInfo: (id: string) => ProviderManifestEntry | undefined
  getProviderDynamicModels: (provider: string, full?: boolean) => Promise<DynamicModelsResponse | null>

  // Delegation tier configuration
  delegationTierConfig: DelegationTierConfig | null
  delegationTierDefaultsStatus: DelegationTierDefaultsStatus
  setDelegationTierConfig: (config: DelegationTierConfig | null) => void
  loadDelegationTierDefaults: () => Promise<void>

  // Lock state from backend (not persisted; re-read on each load)
  llmConfigLocked: boolean
  lockedProviders: string[]
  defaultPublishedLLMsLocked: boolean

  // Actions
  setPrimaryConfig: (config: LLMConfiguration) => void
  setAgentConfig: (config: AgentLLMConfiguration | null) => void

  // Mode-specific config actions
  setChatPrimaryConfig: (config: LLMConfiguration) => void
  setChatAgentConfig: (config: AgentLLMConfiguration | null) => void
  setWorkflowPrimaryConfig: (config: LLMConfiguration) => void
  setWorkflowAgentConfig: (config: AgentLLMConfiguration | null) => void
  getConfigForMode: (mode: 'multi-agent' | 'workflow') => { primaryConfig: LLMConfiguration; agentConfig: AgentLLMConfiguration | null }
  setShowLLMModal: (show: boolean) => void
  loadDefaultsFromBackend: () => Promise<void>
  
  // Library management
  saveLLM: (llm: LLMModel, name: string, modelName?: string, authMethod?: 'api_key' | 'oauth' | 'none', metadata?: PublishedLLMMetadataSnapshot) => Promise<void>
  deleteSavedLLM: (id: string) => Promise<void>

  // Legacy actions (for backward compatibility)
  updateModel: (modelId: string) => void
  refreshAvailableLLMs: () => Promise<void>
  
  // Helper methods
  getCurrentLLMOption: () => LLMOption | null
  isConfigValid: () => boolean
  checkModelExists: (modelId: string) => Promise<boolean>
}

export const useLLMStore = create<LLMState>()(
    persist(
      (set, get) => ({
        // Initial state - will be loaded from backend
        // LEGACY: kept for backward compatibility
        primaryConfig: defaultLLMConfiguration(),

        agentConfig: null,

        // Mode-specific configs (initialized empty, will be migrated from legacy on first load)
        chatPrimaryConfig: defaultLLMConfiguration(),
        chatAgentConfig: null,
        workflowPrimaryConfig: defaultLLMConfiguration(),
        workflowAgentConfig: null,

        // Saved/Published LLM Library
        savedLLMs: [],
        
        // Modal state
        showLLMModal: false,

        availableLLMs: [],
        modelMetadataCatalog: [],
        isLoadingLLMs: false,
        error: null,
        defaultsLoaded: false,

        // Provider manifest (API-driven)
        providerManifest: [],
        providerManifestLoaded: false,
        providerManifestLoading: false,

        loadProviderManifest: async () => {
          if (get().providerManifestLoading) return
          set({ providerManifestLoading: true })
          try {
            const manifest = await llmConfigService.getProviderManifest()
            set({
              // A server that answers without a providers list (an older
              // build, a product-only deployment) must not leave the store
              // holding undefined: every consumer indexes this array.
              providerManifest: Array.isArray(manifest?.providers) ? manifest.providers : [],
              providerManifestLoaded: true,
              providerManifestLoading: false,
            })
          } catch (error) {
            console.warn('Failed to load provider manifest:', error)
            set({ providerManifestLoading: false })
          }
        },

        getProviderInfo: (id: string) => {
          return get().providerManifest.find(p => p.id === id)
        },

        getProviderDynamicModels: async (provider: string, full?: boolean) => {
          try {
            return await llmConfigService.getProviderModels(provider, full)
          } catch (error) {
            console.warn(`Failed to load dynamic models for ${provider}:`, error)
            return null
          }
        },

        // Delegation tier config
        delegationTierConfig: null,
        delegationTierDefaultsStatus: 'idle',

        // Supported providers (always load fresh from backend, default to all)
        supportedProviders: SUPPORTED_PROVIDERS_FALLBACK,
        providerCapabilities: {},
        llmConfigLocked: false,
        lockedProviders: [],
        defaultPublishedLLMsLocked: false,
        isProviderSupported: (provider) => {
          const supported = get().supportedProviders
          return supported.includes(provider as typeof supported[number])
        },

        // Actions
        setPrimaryConfig: (config) => {
          set({ primaryConfig: stripRetiredLLMFallbacks(config), error: null })
        },

        setAgentConfig: (config) => {
          set({ agentConfig: stripRetiredLLMFallbacks(config), error: null })
        },

        // Mode-specific config actions
        setChatPrimaryConfig: (config) => {
          set({ chatPrimaryConfig: stripRetiredLLMFallbacks(config), error: null })
        },

        setChatAgentConfig: (config) => {
          set({ chatAgentConfig: stripRetiredLLMFallbacks(config), error: null })
        },

        setWorkflowPrimaryConfig: (config) => {
          set({ workflowPrimaryConfig: stripRetiredLLMFallbacks(config), error: null })
        },

        setWorkflowAgentConfig: (config) => {
          set({ workflowAgentConfig: stripRetiredLLMFallbacks(config), error: null })
        },

        getConfigForMode: (mode) => {
          const state = get()
          if (mode === 'workflow') {
            return {
              primaryConfig: state.workflowPrimaryConfig,
              agentConfig: state.workflowAgentConfig
            }
          }
          return {
            primaryConfig: state.chatPrimaryConfig,
            agentConfig: state.chatAgentConfig
          }
        },

        setShowLLMModal: (show) => {
          set({ showLLMModal: show })
        },

        setDelegationTierConfig: (config) => {
          set({ delegationTierConfig: config })
          // Fire-and-forget sync to server so bot sessions can use it
          if (config) {
            agentApi.saveDelegationTierConfig(
              config as unknown as Record<string, unknown>,
            ).catch(() => {})
          }
        },

        loadDelegationTierDefaults: async () => {
          if (get().delegationTierDefaultsStatus === 'loading') return
          set({ delegationTierDefaultsStatus: 'loading' })
          try {
            // Try to load saved config from workspace file first
            try {
              const saved = await agentApi.getDelegationTierConfig()
              const hasSaved = saved && (saved.provider || saved.main || saved.high || saved.medium || saved.low ||
                (saved.custom && Object.keys(saved.custom as object).length > 0))
              if (hasSaved) {
                set({
                  delegationTierConfig: saved as unknown as DelegationTierConfig,
                  delegationTierDefaultsStatus: 'loaded',
                })
                return
              }
            } catch {
              // file not saved yet, fall through to env var defaults
            }
            // Fall back to env var defaults
            const defaults = await llmConfigService.getDelegationTierDefaults()
            const hasDefaults = defaults.main || defaults.high || defaults.medium || defaults.low ||
              (defaults.custom && Object.keys(defaults.custom).length > 0)
            if (hasDefaults) {
              set({ delegationTierConfig: defaults })
            }
            set({ delegationTierDefaultsStatus: 'loaded' })
          } catch (error) {
            console.warn('Failed to load delegation tier defaults:', error)
            set({ delegationTierDefaultsStatus: 'error' })
          }
        },

        // Library management
        saveLLM: async (llm, name, _modelName, _authMethod, _metadata) => {
          const { refreshAvailableLLMs, supportedProviders, providerManifest } = get()
          const knownProvider =
            !FRONTEND_DEPRECATED_PROVIDER_IDS.has(llm.provider) && (
              supportedProviders.includes(llm.provider) ||
              providerManifest.some(provider => provider.id === llm.provider && !provider.deprecated)
            )
          if (!knownProvider) {
            throw new Error(`Provider ${llm.provider} is not available as a published chat LLM`)
          }
          // Always fetch the current list from backend to avoid overwriting
          // previously published LLMs when frontend state is stale/empty
          const existingLLMs = filterPublishedLLMs(await llmConfigService.getPublishedLLMs().catch(() => get().savedLLMs))
          const newSavedLLM = sanitizeSavedLLM({
            ...llm,
            id: crypto.randomUUID(),
            name,
            created_at: new Date().toISOString()
          })
          const nextSavedLLMs = filterPublishedLLMs([...(existingLLMs || []), newSavedLLM])

          await llmConfigService.savePublishedLLMs(persistablePublishedLLMs(nextSavedLLMs))
          set({ savedLLMs: nextSavedLLMs })
          await refreshAvailableLLMs()
        },

        deleteSavedLLM: async (id) => {
          const { refreshAvailableLLMs } = get()
          // Always fetch the current list from backend to avoid overwriting
          const existingLLMs = filterPublishedLLMs(await llmConfigService.getPublishedLLMs().catch(() => get().savedLLMs))
          const nextSavedLLMs = (existingLLMs || []).filter(llm => llm.id !== id)

          await llmConfigService.savePublishedLLMs(persistablePublishedLLMs(nextSavedLLMs))
          set({ savedLLMs: nextSavedLLMs })
          await refreshAvailableLLMs()
        },

        // Load defaults from backend
        loadDefaultsFromBackend: async () => {
          try {
            set({ isLoadingLLMs: true })
            const [defaults, loadedPublishedLLMs] = await Promise.all([
              llmConfigService.getLLMDefaults(),
              llmConfigService.getPublishedLLMs().catch(() => undefined),
            ])

            // Get current state to check if user has already selected a model
            const currentState = get()
            // Check if user has made a selection (both provider and model_id should be set)
            const hasUserSelection = currentState.primaryConfig.provider && 
                                     currentState.primaryConfig.model_id && 
                                     currentState.primaryConfig.model_id.trim() !== ''
            
            const localPublishedLLMs = filterPublishedLLMs((currentState.savedLLMs || []).map(sanitizeSavedLLM))
            const localPersistedPublishedLLMs = persistablePublishedLLMs(localPublishedLLMs)
            const loadedSanitizedPublishedLLMs = Array.isArray(loadedPublishedLLMs)
              ? loadedPublishedLLMs.map(sanitizeSavedLLM)
              : []
            let workspacePublishedLLMs = Array.isArray(loadedPublishedLLMs)
              ? filterPublishedLLMs(loadedSanitizedPublishedLLMs)
              : []
            let workspacePersistedPublishedLLMs = persistablePublishedLLMs(workspacePublishedLLMs)
            const loadedPersistedPublishedLLMs = loadedSanitizedPublishedLLMs.filter(llm => !isAutoPublishedLLM(llm))
            if (Array.isArray(loadedPublishedLLMs) && loadedPersistedPublishedLLMs.length !== workspacePersistedPublishedLLMs.length) {
              try {
                await llmConfigService.savePublishedLLMs(workspacePersistedPublishedLLMs)
              } catch (error) {
                console.warn('Failed to remove deprecated published LLM providers from workspace storage:', error)
              }
            }
            if (workspacePersistedPublishedLLMs.length === 0 && localPersistedPublishedLLMs.length > 0) {
              try {
                await llmConfigService.savePublishedLLMs(localPersistedPublishedLLMs)
                workspacePublishedLLMs = [
                  ...workspacePublishedLLMs.filter(isAutoPublishedLLM),
                  ...localPersistedPublishedLLMs,
                ]
                workspacePersistedPublishedLLMs = localPersistedPublishedLLMs
              } catch (error) {
                console.warn('Failed to migrate published LLMs from legacy local storage:', error)
                workspacePublishedLLMs = [
                  ...workspacePublishedLLMs.filter(isAutoPublishedLLM),
                  ...localPersistedPublishedLLMs,
                ]
                workspacePersistedPublishedLLMs = localPersistedPublishedLLMs
              }
            }

            const locked = !!defaults.llm_config_locked
            const defaultPublishedLocked = !!defaults.default_published_llms_locked
            const defaultList = Array.isArray(defaults.default_published_llms)
              ? filterPublishedLLMs((defaults.default_published_llms as SavedLLM[]).map(sanitizeSavedLLM))
              : []

            let newSavedLLMs = workspacePublishedLLMs
            if (defaultList.length > 0) {
              if (defaultPublishedLocked) {
                newSavedLLMs = defaultList
              } else {
                const byId = new Map(newSavedLLMs.map((llm) => [llm.id, llm]))
                for (const d of defaultList) {
                  if (d.id && !byId.has(d.id)) {
                    byId.set(d.id, d)
                  } else if (d.provider && d.model_id) {
                    const key = `${d.provider}:${d.model_id}`
                    if (!Array.from(byId.values()).some((llm) => llm.provider === d.provider && llm.model_id === d.model_id)) {
                      byId.set(d.id || key, { ...d, id: d.id || key })
                    }
                  }
                }
                newSavedLLMs = Array.from(byId.values())
              }
            }

            let newPrimaryConfig = normalizePrimaryConfig(hasUserSelection ? currentState.primaryConfig : defaults.primary_config)
            if (locked && defaultList.length > 0) {
              const first = defaultList[0]
              newPrimaryConfig = {
                provider: first.provider,
                model_id: first.model_id,
              }
            }

            set({
              primaryConfig: newPrimaryConfig,
              savedLLMs: newSavedLLMs,
              supportedProviders: (() => {
                const sp = (defaults.supported_providers || SUPPORTED_PROVIDERS_FALLBACK).filter(provider =>
                  !isFrontendDeprecatedProvider(provider)
                )
                console.log('[useLLMStore] supported_providers from backend:', defaults.supported_providers, '→ using:', sp)
                return sp
              })(),
              providerCapabilities: defaults.provider_capabilities || {},
              llmConfigLocked: locked,
              lockedProviders: defaults.locked_providers || [],
              defaultPublishedLLMsLocked: defaultPublishedLocked,
              defaultsLoaded: true,
              error: null,
              isLoadingLLMs: false
            })

            if (locked && defaultList.length > 0) {
              const first = defaultList[0]
              get().setChatPrimaryConfig({ provider: first.provider, model_id: first.model_id })
              get().setWorkflowPrimaryConfig({ provider: first.provider, model_id: first.model_id })
              get().setAgentConfig({ primary: first })
            }

            // Load provider manifest in parallel (non-blocking)
            get().loadProviderManifest()

            // Refresh availableLLMs from savedLLMs (Published LLMs)
            get().refreshAvailableLLMs()
          } catch (error) {
            console.error('Failed to load LLM defaults from backend:', error)
            set({
              error: 'Failed to load LLM defaults from backend',
              defaultsLoaded: false,
              isLoadingLLMs: false
            })
          }
        },

        updateModel: (modelId) => {
          set((state) => ({
            primaryConfig: {
              ...state.primaryConfig,
              model_id: modelId
            },
            error: null
          }))
        },

        refreshAvailableLLMs: async () => {
          const state = get()
          // Don't build list until backend defaults are loaded (so supported_providers is set)
          if (!state.defaultsLoaded) {
            set({ availableLLMs: [], modelMetadataCatalog: [], isLoadingLLMs: false })
            return
          }

          set({ isLoadingLLMs: true, error: null })

          try {
            const currentState = get()
            const availableLLMs: LLMOption[] = []
            let modelMetadataCatalog: ModelMetadata[] = []

            // Fetch model metadata for cost/context info
            const metadataMap: Record<string, { contextWindow: number; inputCost: number; outputCost: number }> = {}
            try {
              const metadataResponse = await llmConfigService.getModelMetadata()
              modelMetadataCatalog = metadataResponse.models
              metadataResponse.models.forEach(m => {
                metadataMap[`${m.provider}:${m.model_id}`] = {
                  contextWindow: m.context_window,
                  inputCost: m.input_cost_per_1m,
                  outputCost: m.output_cost_per_1m
                }
              })
            } catch (e) {
              console.warn('Failed to fetch model metadata for dropdown:', e)
            }

            // Build availableLLMs from Published LLMs (savedLLMs)
            // This replaces the old provider-specific model lists
            const supportedProviders = currentState.supportedProviders || []
            currentState.savedLLMs.forEach(savedLLM => {
              // Skip if provider is not supported
              if (supportedProviders.length > 0 && !supportedProviders.includes(savedLLM.provider)) {
                return
              }

              const metadata = metadataMap[`${savedLLM.provider}:${savedLLM.model_id}`]
              availableLLMs.push({
                id: savedLLM.id,
                provider: savedLLM.provider,
                model: savedLLM.model_id,
                label: savedLLM.name || `${savedLLM.provider} - ${savedLLM.model_id}`,
                description: savedLLM.model_name || `Published ${savedLLM.provider} model`,
                options: savedLLM.options,
                contextWindow: metadata?.contextWindow,
                inputCostPer1M: metadata?.inputCost,
                outputCostPer1M: metadata?.outputCost
              })
            })

            set({ availableLLMs, modelMetadataCatalog, isLoadingLLMs: false })
          } catch (error) {
            set({
              error: error instanceof Error ? error.message : 'Failed to load LLMs',
              isLoadingLLMs: false
            })
          }
        },

        getCurrentLLMOption: () => {
          const state = get()

          // Use agentConfig primary if available (new tiered system)
          if (state.agentConfig?.primary) {
            const primary = state.agentConfig.primary
            // Try to find matching published LLM for better label
            const publishedLLM = state.savedLLMs.find(
              llm => llm.provider === primary.provider && llm.model_id === primary.model_id
            )

            return {
              provider: primary.provider,
              model: primary.model_id,
              label: publishedLLM?.name || `${primary.provider} - ${primary.model_id}`,
              description: publishedLLM?.model_name || 'Primary LLM'
            }
          }

          // Fallback to legacy primaryConfig
          const currentConfig = state.primaryConfig

          return {
            provider: currentConfig.provider,
            model: currentConfig.model_id,
            label: `${currentConfig.provider} - ${currentConfig.model_id}`,
            description: 'Current LLM configuration'
          }
        },

        isConfigValid: () => {
          const state = get()
          return !!(state.primaryConfig.provider && state.primaryConfig.model_id)
        },

        checkModelExists: async (modelId: string) => {
          // Fetch latest metadata from backend to ensure we have the most current info
          // and specifically to get costs/tokens for the new model
          try {
            const metadataResponse = await llmConfigService.getModelMetadata()
            const exists = metadataResponse.models.some(m => m.model_id === modelId)
            
            // Also refresh available LLMs to ensure the new model (if added) will have metadata
            if (exists) {
               await get().refreshAvailableLLMs()
            }
            
            return exists
          } catch (error) {
            console.error('Failed to validate model existence:', error)
            // Fallback to local state check if API call fails
            return get().modelMetadataCatalog.some(model => model.model_id === modelId)
          }
        },

        // Generic actions
        reset: () => {
          set({
            primaryConfig: defaultLLMConfiguration(),
            agentConfig: null,
            savedLLMs: [],
            showLLMModal: false,
            availableLLMs: [],
            modelMetadataCatalog: [],
            isLoadingLLMs: false,
            error: null
          })
        },

        setLoading: (loading) => {
          set({ isLoadingLLMs: loading })
        },

        setError: (error) => {
          set({ error })
        }
      }),
      {
        name: 'llm-store',
        partialize: (state) => ({
          // Persist user configurations and custom models, but keep secrets/workspace-backed
          // LLM library data out of localStorage.
          // Legacy configs (kept for backward compatibility)
          primaryConfig: stripRetiredLLMFallbacks(state.primaryConfig),
          agentConfig: sanitizeAgentConfig(state.agentConfig),
          // Mode-specific configs
          chatPrimaryConfig: stripRetiredLLMFallbacks(state.chatPrimaryConfig),
          chatAgentConfig: sanitizeAgentConfig(state.chatAgentConfig),
          workflowPrimaryConfig: stripRetiredLLMFallbacks(state.workflowPrimaryConfig),
          workflowAgentConfig: sanitizeAgentConfig(state.workflowAgentConfig),
          // showLLMModal (the Providers page) is not persisted: people land on their product.
          delegationTierConfig: stripRetiredLLMFallbacks(state.delegationTierConfig),
          // DO NOT persist defaultsLoaded - this should be reset on each app load
        }),
        // Migration: copy legacy config to mode-specific configs on first load
        onRehydrateStorage: () => (state) => {
          if (state) {
            // Older builds persisted the Providers page as open.
            state.showLLMModal = false
            state.delegationTierConfig = stripRetiredLLMFallbacks(state.delegationTierConfig)
            state.primaryConfig = normalizePrimaryConfig(state.primaryConfig)
            state.chatPrimaryConfig = normalizePrimaryConfig(state.chatPrimaryConfig)
            state.workflowPrimaryConfig = normalizePrimaryConfig(state.workflowPrimaryConfig)
            state.agentConfig = sanitizeAgentConfig(state.agentConfig)
            state.chatAgentConfig = sanitizeAgentConfig(state.chatAgentConfig)
            state.workflowAgentConfig = sanitizeAgentConfig(state.workflowAgentConfig)
            const hasLegacyConfig = state.primaryConfig?.provider && state.primaryConfig?.model_id
            const hasChatConfig = state.chatPrimaryConfig?.model_id && state.chatPrimaryConfig.model_id !== ''
            const hasWorkflowConfig = state.workflowPrimaryConfig?.model_id && state.workflowPrimaryConfig.model_id !== ''

            // Migrate legacy config to mode-specific configs if not already set
            if (hasLegacyConfig && !hasChatConfig) {
              state.chatPrimaryConfig = { ...state.primaryConfig }
              state.chatAgentConfig = state.agentConfig ? { ...state.agentConfig } : null
            }
            if (hasLegacyConfig && !hasWorkflowConfig) {
              state.workflowPrimaryConfig = { ...state.primaryConfig }
              state.workflowAgentConfig = state.agentConfig ? { ...state.agentConfig } : null
            }
          }
        }
      }
    )
)
