import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from './api'
import type {
  LLMDefaultsResponse,
  APIKeyValidationRequest,
  APIKeyValidationResponse,
  DelegationTierConfig,
  SavedLLM,
  LLMDiscoveryResponse,
  ProductDefault,
  ProviderAccountCosts,
} from './api-types'

export type ProviderAccountSharingMode = 'private' | 'shared'

export interface ProviderAccountSharing {
  mode: ProviderAccountSharingMode
  /** Workflow IDs. */
  workflows?: string[]
  /** Crew roots. */
  crews?: string[]
  /** User IDs. */
  users?: string[]
}

/** Who may use a server account: everyone, admins, or products and people. */
export type ProviderAvailableTo = 'all' | 'admins' | { admins?: boolean; products?: string[]; users?: string[] }

export interface ServerAccountAvailability {
  available_to: ProviderAvailableTo
  text: string
  source: 'default' | 'installation' | 'admin'
  pinned: boolean
}

export type ProviderAccountKind = 'installed' | 'admin' | 'user'
export type ProviderAccountRelation = 'server' | 'own' | 'shared_with_you' | 'shared_with_workflow' | 'shared_with_crew' | 'admin_view'

export interface ProviderConnection {
  id: string
  provider: string
  display_name: string
  scope: 'global' | 'user'
  personal_accounts_allowed?: boolean
  auth_method: string
  underlying_provider?: string
  owner_user_id?: string
  updated_at?: string
  sharing?: ProviderAccountSharing
  // Account view fields. Optional so an older server still reads.
  kind?: ProviderAccountKind
  relation?: ProviderAccountRelation
  owner_name?: string
  source?: string
  availability?: ServerAccountAvailability
  availability_editable?: boolean
  /** Whether the caller may select it where the list was requested. */
  usable?: boolean
  /** The caller's runs on it run MCP-only (someone else's shared account). */
  native_tools_off?: boolean
  can_manage?: boolean
  can_view_usage?: boolean
}

export interface ProviderShareTargets {
  workflows: { id: string; name: string }[]
  crews: { id: string; name: string; owner?: string }[]
  users: { id: string; name: string; email?: string }[]
}

export type ProductDefaultChange = { provider: string; model: string } | null

/** Server validation errors come back as plain text; show them as they are. */
export function providerApiErrorText(error: unknown, fallback: string): string {
  const data = (error as { response?: { data?: unknown } })?.response?.data
  if (typeof data === 'string' && data.trim() && !data.trim().startsWith('<')) return data.trim()
  if (data && typeof data === 'object' && typeof (data as { error?: unknown }).error === 'string') return (data as { error: string }).error
  return fallback
}

export interface ModelMetadata {
  model_id: string
  model_name: string
  context_window: number
  input_cost_per_1m: number
  output_cost_per_1m: number
  reasoning_cost_per_1m?: number
  cached_input_cost_per_1m?: number
  cached_input_cost_write_per_1m?: number
  provider: string
  supports_reasoning_effort?: boolean
  reasoning_effort_levels?: string[]
  supports_thinking_level?: boolean
  thinking_levels?: string[]
  supports_thinking_budget?: boolean
  model_selection_mode?: 'fixed_tier' | 'dynamic'
}

// --- Provider Manifest types (API-driven provider discovery) ---

export interface ProviderManifestEntry {
  id: string
  display_name: string
  description: string
  kind: 'local_cli' | 'api'
  integration_kind: 'coding_agent' | 'api_model' | 'audio_provider'
  model_selection_mode: 'fixed_tier' | 'dynamic'
  auth_description: string
  runtime_command?: string
  runtime_available?: boolean
  install_command?: string
  installed_version?: string
  min_supported_version?: string
  update_status?: 'supported' | 'unsupported' | 'unknown'
  auth_configured: boolean
  auth_source?: string
  usable: boolean
  setup_hint?: string
  deprecated?: boolean
  deprecation_reason?: string
  replacement_provider?: string
  requires_api_key: boolean
  supports_dynamic_models: boolean
  default_model_id: string
  default_tier_models?: ProviderDefaultTierModels
  models: ModelMetadata[]
  capabilities: string[]
  coding_agent?: {
    transport: 'tmux' | string
    supports_live_input: boolean
    supports_interrupt: boolean
    supports_status_line?: boolean
    uses_mcp_bridge?: boolean
    supports_bridge_only_tools?: boolean
    supports_native_resume?: boolean
    handles_tmux_session_loss?: boolean
  }
  api_key_env?: string
  api_key_url?: string
}

export interface ProviderTierModelRef {
  provider: string
  model_id: string
  options?: Record<string, unknown>
}

export interface ProviderDefaultTierModels {
  builder?: ProviderTierModelRef
  high: ProviderTierModelRef
  medium: ProviderTierModelRef
  low: ProviderTierModelRef
  maintenance?: ProviderTierModelRef
  pulse?: ProviderTierModelRef
  // Read-only compatibility for a backend that is still restarting during a
  // desktop update. New provider manifests never emit these fields.
  main?: ProviderTierModelRef
  phase?: ProviderTierModelRef
  auto_improve?: ProviderTierModelRef
}

export interface IntegrationKindInfo {
  label: string
  description: string
}

export interface ProviderManifestResponse {
  providers: ProviderManifestEntry[]
  integration_kinds: Record<string, IntegrationKindInfo>
  provider_order: string[]
}

export interface DynamicModelEntry {
  model_id: string
  model_name: string
  group?: string
  is_default?: boolean
  is_free?: boolean
  context_window?: number
  cost_input?: number
  cost_output?: number
}

export interface DynamicModelsResponse {
  provider: string
  model_selection_mode: string
  models: DynamicModelEntry[]
  groups?: string[]
  supports_custom_model?: boolean
  custom_model_hint?: string
  source: string
  cached_at?: string
  cache_ttl_seconds?: number
  error?: string
}

export interface GetModelMetadataResponse {
  models: ModelMetadata[]
}

export type ProviderSetupAction = 'authenticate' | 'inspect' | 'usage'

export interface ProviderSetupSession {
  id: string
  provider: string
  action: ProviderSetupAction
  status: 'running' | 'completed' | 'failed' | 'cancelled'
  exit_code?: number
  error?: string
  created_at: string
  updated_at: string
}

// Create axios instance for LLM configuration API (use Vite env so deploy URL works)
const llmConfigApi = axios.create({
  baseURL: getApiBaseUrl(),
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// Add auth token interceptor
llmConfigApi.interceptors.request.use((config) => {
  const authToken = getAuthToken()
  if (authToken && config.headers) {
    config.headers['Authorization'] = `Bearer ${authToken}`
  }
  return config
})

// LLM Configuration API service
export const llmConfigService = {
  // Get LLM configuration defaults from backend
  getLLMDefaults: async (): Promise<LLMDefaultsResponse> => {
    const response = await llmConfigApi.get('/api/llm-config/defaults')
    return response.data
  },

  // Discover local CLI providers and server/workspace auth without making model calls
  discoverLLMSetup: async (): Promise<LLMDiscoveryResponse> => {
    const response = await llmConfigApi.get('/api/llm-config/discovery')
    return response.data
  },

  // Validate API key with backend
  validateAPIKey: async (request: APIKeyValidationRequest): Promise<APIKeyValidationResponse> => {
    const response = await llmConfigApi.post('/api/llm-config/validate-key', request, { timeout: 120000 })
    return response.data
  },

  // Get metadata for all available models
  getModelMetadata: async (): Promise<GetModelMetadataResponse> => {
    const response = await llmConfigApi.get('/api/llm-config/models/metadata')
    return response.data
  },

  // Get deployed models from Azure (requires endpoint and API key)
  getAzureDeployedModels: async (endpoint: string, apiKey: string): Promise<GetModelMetadataResponse & { error?: string }> => {
    const response = await llmConfigApi.post('/api/llm-config/azure/deployments', {
      endpoint,
      api_key: apiKey
    })
    return response.data
  },

  // Get comprehensive provider manifest (replaces hardcoded provider info)
  // Accounts the caller can see. With a workspace path (workflow, Crew or
  // Code) or product, `usable` says whether the caller may select it there.
  getProviderConnections: async (scope?: { workspacePath?: string | null; product?: string }): Promise<ProviderConnection[]> => {
    const params: Record<string, string> = {}
    if (scope?.workspacePath) params.workspace_path = scope.workspacePath
    if (scope?.product) params.product = scope.product
    const response = await llmConfigApi.get('/api/provider-connections', Object.keys(params).length ? { params } : undefined)
    return response.data.connections
  },
  addProviderConnection: async (connection: { provider: string; display_name: string; credential?: string; auth_method?: string; underlying_provider?: string; sharing?: ProviderAccountSharing }): Promise<ProviderConnection> => {
    const response = await llmConfigApi.post('/api/provider-connections', connection)
    return response.data
  },

  updateProviderConnection: async (id: string, changes: { display_name?: string; credential?: string; sharing?: ProviderAccountSharing }): Promise<void> => { await llmConfigApi.patch(`/api/provider-connections/${encodeURIComponent(id)}`, changes) },

  // Admin: who may use a server account. null returns to the installation policy.
  setServerAccountAvailability: async (provider: string, availableTo: ProviderAvailableTo | null): Promise<void> => {
    await llmConfigApi.patch(`/api/provider-connections/${encodeURIComponent(`global:${provider}`)}`, { available_to: availableTo })
  },

  deleteProviderConnection: async (id: string): Promise<void> => { await llmConfigApi.delete(`/api/provider-connections/${encodeURIComponent(id)}`) },

  getProviderShareTargets: async (): Promise<ProviderShareTargets> => {
    const response = await llmConfigApi.get('/api/provider-connections/share-targets')
    return { workflows: response.data?.workflows || [], crews: response.data?.crews || [], users: response.data?.users || [] }
  },

  getProductDefaults: async (): Promise<Record<string, ProductDefault>> => {
    const response = await llmConfigApi.get('/api/provider-accounts/product-defaults')
    return response.data?.product_defaults || {}
  },

  setProductDefaults: async (changes: Record<string, ProductDefaultChange>): Promise<void> => {
    await llmConfigApi.put('/api/provider-accounts/product-defaults', { product_defaults: changes })
  },

  getProviderAccountCosts: async (from: string, to: string, signal?: AbortSignal): Promise<ProviderAccountCosts> => {
    const response = await llmConfigApi.get('/api/provider-accounts/costs', { params: { from, to }, signal })
    return { ...response.data, providers: response.data?.providers || [] }
  },

  getProviderManifest: async (): Promise<ProviderManifestResponse> => {
    const response = await llmConfigApi.get('/api/llm-config/providers')
    return response.data
  },

  // Get dynamic model list for a provider (cursor-cli, pi-cli, etc.)
  getProviderModels: async (provider: string, full?: boolean, availableOnly?: boolean): Promise<DynamicModelsResponse> => {
    const query = new URLSearchParams()
    if (full) query.set('full', 'true')
    if (availableOnly) query.set('available_only', 'true')
    const suffix = query.size > 0 ? `?${query.toString()}` : ''
    const url = `/api/llm-config/providers/${provider}/models${suffix}`
    const response = await llmConfigApi.get(url)
    return response.data
  },

  // Start an owner-only, allowlisted interactive setup command on the server.
  startProviderSetup: async (
    provider: string,
    action: ProviderSetupAction,
    cols?: number,
    rows?: number,
    workspacePath?: string,
    replaceRunning?: boolean,
    connectionId?: string,
  ): Promise<ProviderSetupSession> => {
    const response = await llmConfigApi.post('/api/provider-setup/sessions', {
      provider,
      action,
      connection_id: connectionId,
      cols,
      rows,
      workspace_path: workspacePath,
      replace_running: replaceRunning || undefined,
    })
    return response.data.session
  },

  getProviderSetup: async (sessionId: string): Promise<ProviderSetupSession> => {
    const response = await llmConfigApi.get(`/api/provider-setup/sessions/${encodeURIComponent(sessionId)}`)
    return response.data.session
  },

  cancelProviderSetup: async (sessionId: string): Promise<void> => {
    await llmConfigApi.delete(`/api/provider-setup/sessions/${encodeURIComponent(sessionId)}`)
  },

  getProviderSetupStreamUrl: (sessionId: string): string => {
    const httpBase = getApiBaseUrl() || (typeof window !== 'undefined' ? window.location.origin : '')
    const url = new URL(`/api/provider-setup/sessions/${encodeURIComponent(sessionId)}/stream`, httpBase.replace(/^http/i, 'ws'))
    const token = getAuthToken()
    if (token) url.searchParams.set('token', token)
    return url.toString()
  },

  // Get delegation tier defaults from environment variables
  getDelegationTierDefaults: async (): Promise<DelegationTierConfig> => {
    const response = await llmConfigApi.get('/api/llm-config/delegation-tiers')
    return response.data
  },

  // Load published LLMs from the workspace config folder
  getPublishedLLMs: async (): Promise<SavedLLM[]> => {
    const response = await llmConfigApi.get('/api/published-llms')
    return response.data
  },

  // Save published LLMs to the workspace config folder
  savePublishedLLMs: async (llms: SavedLLM[]): Promise<{ status: string }> => {
    const response = await llmConfigApi.put('/api/published-llms', llms)
    return response.data
  },
}

export default llmConfigService
