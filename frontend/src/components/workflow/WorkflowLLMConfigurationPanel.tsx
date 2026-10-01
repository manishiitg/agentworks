import ProviderAccounts, { ACCOUNT_GROUPS, NO_LONGER_AVAILABLE, accountConfigured, accountRelation, accountUsable } from '../providers/ProviderAccounts'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { stripRetiredLLMFallbacks } from '../../utils/retiredLLMFallbacks'
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ArrowLeft, CheckCircle2, ChevronDown, ChevronRight, Loader2, Lock, RefreshCw, Search, UserRound, ShieldCheck } from 'lucide-react'
import LLMRoleSelector from '../LLMRoleSelector'
import { WorkflowProviderCredentialField } from '../WorkflowProviderCredentialField'
import { CodingAgentSection } from '../llm/CodingAgentSection'
import { providerStatus } from '../llm/providerStatus'
import type { AgentLLMConfig, LLMProvider, PresetLLMConfig } from '../../services/api-types'
import { llmConfigService, type DynamicModelEntry, type ProviderManifestEntry } from '../../services/llm-config-api'
import { useLLMStore } from '../../stores/useLLMStore'
import { READ_ONLY_TITLE, useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import type { LLMOption } from '../../types/llm'
import { llmOptionsKey } from '../../utils/llmConfigDisplay'
import { resolvePiModelGroup } from '../../utils/llmDisplay'
import { getWorkflowLLMOptions, getWorkflowLLMTierDefaults, getWorkflowProviderOptions } from '../../utils/workflowLLMTierDefaults'
import { effectiveLLMUnderLock } from '../../utils/effectiveLLM'

type RoleKey = 'tier_1' | 'tier_2' | 'tier_3' | 'builder_llm' | 'pulse_llm'
const ROLE_KEYS: RoleKey[] = ['tier_1', 'tier_2', 'tier_3', 'builder_llm', 'pulse_llm']

type RoleRow = {
  key: RoleKey
  label: string
  description: string
  group: 'Execution' | 'Workflow agents'
}

const ROLE_ROWS: RoleRow[] = [
  { key: 'tier_1', label: 'High reasoning', description: 'First runs and complex execution.', group: 'Execution' },
  { key: 'tier_2', label: 'Medium reasoning', description: 'Execution after useful learnings exist, and Pulse upkeep (Plan Drift, Technical, Architecture).', group: 'Execution' },
  { key: 'tier_3', label: 'Low reasoning', description: 'Validation and mature learned tasks.', group: 'Execution' },
  { key: 'builder_llm', label: 'Builder', description: 'Chat, planning, evaluation design, scheduled runs, and the post-run Pulse conversation.', group: 'Workflow agents' },
  { key: 'pulse_llm', label: 'Pulse Goal Work', description: 'Goal Work: the Pulse pass that does work toward your goals. Pick your strongest model here.', group: 'Workflow agents' },
]

const CODING_AGENT_PROVIDER_ORDER = ['claude-code', 'codex-cli', 'cursor-cli', 'pi-cli', 'muse-cli', 'agy-cli']
const codingAgentProviderRank = (provider: string) => {
  const index = CODING_AGENT_PROVIDER_ORDER.indexOf(provider)
  return index === -1 ? 999 : index
}

// Pi CLI is one manifest entry that routes to several model backends (Gemini,
// Z.AI, Kimi, ...). The user thinks in terms of those backends, so the list
// shows one row per backend under a synthetic id; nothing here is a real
// manifest entry, and a selection has to be written as explicit per-role
// config because a provider profile can't say "pi-cli, but only Gemini".
// One row per model under each backend: the curated catalog models plus any
// published Pi entries, so "Gemini 3.7 Flash" and "Gemini 3.1 Pro" are both
// pickable without opening the drill-in.
const PI_GROUP_PREFIX = 'pi-cli::'
const piRowId = (group: string, modelId: string) => `${PI_GROUP_PREFIX}${group}::${modelId}`
const piGroupFromRowId = (rowId: string): string | null =>
  rowId.startsWith(PI_GROUP_PREFIX) ? rowId.slice(PI_GROUP_PREFIX.length).split('::')[0] || null : null

type ProviderRow = {
  id: string
  entry: ProviderManifestEntry
  name: string
  modelId: string | null
  selectable: boolean
  groupFilter?: string
  /** Row comes from a published LLM entry rather than Pi's curated catalog. */
  published?: boolean
}

// "Models per role" starts collapsed; once the user opens it, it stays open on
// later visits (a single preference for all workflows, kept in localStorage).
const ROLES_OPEN_STORAGE_KEY = 'workflow_llm_roles_open'
const readRolesOpen = (): boolean => {
  try { return window.localStorage.getItem(ROLES_OPEN_STORAGE_KEY) === '1' } catch { return false }
}
const writeRolesOpen = (open: boolean) => {
  try { window.localStorage.setItem(ROLES_OPEN_STORAGE_KEY, open ? '1' : '0') } catch { /* storage unavailable */ }
}

type WorkflowLLMConfigurationPanelProps = {
  workspacePath: string | null
  llmConfig?: PresetLLMConfig
  onChange: (config: PresetLLMConfig) => void
  /** Persist a provider choice immediately (the drill-in's "Use in this
   * workflow"), instead of leaving it as a draft for the footer Save. */
  onUseProvider?: (config: PresetLLMConfig) => void | Promise<void>
  /** Reuse this configuration surface outside workflows without leaking workflow terminology. */
  scopeNoun?: string
  canWriteOverride?: boolean
  allowedProviderIds?: readonly string[]
  /** Work selects Pi as one coding CLI, then chooses its model separately. */
  splitPiProviders?: boolean
  showModelsPerRole?: boolean
  /** Relays expose the Builder choice here; execution models are selected on agent steps. */
  builderOnly?: boolean
  readOnlyReason?: string
  /** Product-profile bindings remain authoritative under the deployment-wide
   * workflow/chat lock, matching the server's agent-profile resolution. */
  configurationSource?: 'agent_profile'
  /** Product the settings belong to (agentworks, work, code) when the path alone does not say. */
  product?: string
}

const hasOptions = (options?: Record<string, unknown>) => Boolean(options && Object.keys(options).length > 0)

function toAgentLLMConfig(llm: LLMOption): AgentLLMConfig {
  return {
    ...(llm.id ? { published_llm_id: llm.id } : {}),
    provider: llm.provider as LLMProvider,
    model_id: llm.model,
    ...(hasOptions(llm.options) ? { options: llm.options } : {}),
  }
}

function configKey(config: { provider?: string; model_id?: string; published_llm_id?: string; connection_id?:string; options?: Record<string, unknown> }): string {
  return config.published_llm_id
    ? `id:${config.published_llm_id}/${config.connection_id || "global"}`
    : `model:${config.provider}/${config.model_id}/${llmOptionsKey(config.options)}/${config.connection_id || "global"}`
}

function roleConfig(config: PresetLLMConfig | undefined, key: RoleKey): AgentLLMConfig | undefined {
  if (key === 'tier_1') return config?.tiered_config?.tier_1
  if (key === 'tier_2') return config?.tiered_config?.tier_2
  if (key === 'tier_3') return config?.tiered_config?.tier_3
  return config?.[key]
}

const statusToneClasses = {
  ready: { dot: 'bg-emerald-500', text: 'text-emerald-600 dark:text-emerald-400' },
  warn: { dot: 'bg-amber-500', text: 'text-amber-600 dark:text-amber-400' },
  managed: { dot: 'bg-blue-500', text: 'text-blue-600 dark:text-blue-400' },
}

function statusTone(label: string) {
  if (label === 'Ready') return statusToneClasses.ready
  if (label === 'Managed') return statusToneClasses.managed
  return statusToneClasses.warn
}

// "Ready" is the default for most rows, so spelling it out adds noise and
// buries the two rows that actually need attention. Working providers get a
// green dot and nothing else; only a provider that needs something from the
// user gets words, phrased as the thing to do.
const STATUS_ACTION_TEXT: Record<string, string> = {
  'Needs key': 'Add API key',
  'Not installed': 'Not installed',
  'Setup needed': 'Needs setup',
  'Account unavailable': 'Account unavailable',
}

function statusActionText(label: string): string | null {
  return STATUS_ACTION_TEXT[label] ?? null
}

function statusTitle(label: string): string {
  if (label === 'Ready') return 'Installed and signed in — ready to use'
  if (label === 'Managed') return 'Configured by your administrator'
  if (label === 'Needs key') return 'Installed, but needs an API key before it can run'
  if (label === 'Not installed') return 'This CLI is not installed on this machine'
  return 'Needs setup before it can run'
}

export default function WorkflowLLMConfigurationPanel({
  workspacePath,
  llmConfig,
  onChange,
  onUseProvider,
  scopeNoun = 'workflow',
  canWriteOverride,
  allowedProviderIds,
  splitPiProviders = true,
  showModelsPerRole = true,
  builderOnly = false,
  readOnlyReason,
  configurationSource,
  product,
}: WorkflowLLMConfigurationPanelProps) {
  const {
    availableLLMs,
    providerManifest,
    providerManifestLoaded,
    loadProviderManifest,
    defaultsLoaded,
    loadDefaultsFromBackend,
    getProviderDynamicModels,
    isProviderSupported,
    llmConfigLocked,
    lockedProviders,
    publishedLLMs,
    setShowLLMModal,
  } = useLLMStore(useShallow(state => ({
    availableLLMs: state.availableLLMs,
    providerManifest: state.providerManifest,
    providerManifestLoaded: state.providerManifestLoaded,
    loadProviderManifest: state.loadProviderManifest,
    defaultsLoaded: state.defaultsLoaded,
    loadDefaultsFromBackend: state.loadDefaultsFromBackend,
    getProviderDynamicModels: state.getProviderDynamicModels,
    isProviderSupported: state.isProviderSupported,
    llmConfigLocked: state.llmConfigLocked,
    lockedProviders: state.lockedProviders,
    publishedLLMs: state.savedLLMs,
    setShowLLMModal: state.setShowLLMModal,
  })))

  // Provider selection goes through the panel's Save, but the drill-ins (API
  // keys, publish, Pi keys) and role edits write immediately -- disable all of
  // it for read-only users rather than only the Save button upstream.
  // A locked deployment (LLM_CONFIG_LOCKED) shows the same list, fully
  // disabled: the published provider reads "In use", every other row is
  // visible for reference but cannot be tested, selected or configured.
  const workflowCanWrite = useCanWriteWorkflow(workspacePath)
  const globalLockApplies = llmConfigLocked && configurationSource !== 'agent_profile'
  const readOnly = !(canWriteOverride ?? workflowCanWrite) || globalLockApplies
  const disabledTitle = globalLockApplies ? 'Set by your administrator for this deployment' : readOnlyReason || READ_ONLY_TITLE
  const [activeProviderId, setActiveProviderId] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [tokenOpen, setTokenOpen] = useState(false)
  const [accountSelectionError, setAccountSelectionError] = useState<string | null>(null)
  const [expandedAccountProviders, setExpandedAccountProviders] = useState<Set<string>>(() => new Set())
  const [accountRecords, setAccountRecords] = useState<import('../../services/llm-config-api').ProviderConnection[]>([])
  const [refreshing, setRefreshing] = useState(false)
  const [rowUsing, setRowUsing] = useState<string | null>(null)
  const [piCliModels, setPiCliModels] = useState<DynamicModelEntry[]>([])
  const [piCliGroups, setPiCliGroups] = useState<string[]>([])

  const advanced = llmConfig?.mode === 'explicit'
  // A workflow that already runs on a provider opens on the compact "runs on
  // X" line; the provider list only appears on "Change provider".
  const [changing, setChanging] = useState(false)
  const [rolesOpen, setRolesOpen] = useState<boolean>(() => readRolesOpen())
  // Pi backends list one company row each; its models unfold underneath on
  // click. The backend the workflow runs on is always unfolded.
  const [openPiGroups, setOpenPiGroups] = useState<Set<string>>(() => new Set())
  const togglePiGroup = (group: string) => setOpenPiGroups(current => {
    const next = new Set(current)
    if (next.has(group)) next.delete(group)
    else next.add(group)
    return next
  })
  const toggleRolesOpen = () => setRolesOpen(open => { writeRolesOpen(!open); return !open })

  useEffect(() => {
    if (!providerManifestLoaded) void loadProviderManifest()
    if (!defaultsLoaded) void loadDefaultsFromBackend()
  }, [defaultsLoaded, loadDefaultsFromBackend, loadProviderManifest, providerManifestLoaded])

  const isProviderLocked = useCallback((provider: string) => {
    const base = piGroupFromRowId(provider) ? 'pi-cli' : provider
    return lockedProviders.includes('all') || lockedProviders.includes(base)
  }, [lockedProviders])

  const [privateProviderIds, setPrivateProviderIds] = useState<string[]>([])
 const [privateConnections,setPrivateConnections]=useState<import("../../services/llm-config-api").ProviderConnection[]>([])
  useEffect(() => {
    let cancelled = false
    // Ask for the accounts usable here: the server decides from the
    // workflow, Crew or Code path (and product) which ones this person may pick.
    const refreshAccounts = () => { void llmConfigService.getProviderConnections(workspacePath || product ? { workspacePath, product } : undefined).then(records => {
      if (!cancelled) {
        const userAccounts = records.filter(record => record.scope === 'user' && accountRelation(record) !== 'admin_view')
        setAccountRecords(records)
        setPrivateProviderIds(userAccounts.filter(record => accountUsable(record) && accountConfigured(record)).map(record => record.provider))
        setPrivateConnections(userAccounts)
      }
    }).catch(() => undefined) }
    refreshAccounts()
    window.addEventListener('provider-connections-changed', refreshAccounts)
    return () => { cancelled = true; window.removeEventListener('provider-connections-changed', refreshAccounts) }
  }, [workspacePath, product])

  const providerIsReadyForUse = useCallback((row: ProviderRow) => {
    const status = providerStatus(row.entry, isProviderLocked(row.id))
    return status.label === 'Ready' || status.label === 'Managed' || (privateProviderIds.includes(row.entry.id) && row.entry.runtime_available !== false)
  }, [isProviderLocked, privateProviderIds])

  const manifestEntries = useMemo(() => providerManifest.filter(entry => {
    if (entry.integration_kind !== 'coding_agent') return false
    if (allowedProviderIds && !allowedProviderIds.includes(entry.id)) return false
    return isProviderSupported(entry.id as LLMProvider)
  }), [allowedProviderIds, isProviderSupported, providerManifest])

  const hasPiCli = manifestEntries.some(entry => entry.id === 'pi-cli')
  useEffect(() => {
    if (!hasPiCli) return
    let cancelled = false
    getProviderDynamicModels('pi-cli', false).then(result => {
      if (cancelled || !result) return
      setPiCliModels(result.models || [])
      if (result.groups?.length) setPiCliGroups(result.groups)
    })
    return () => { cancelled = true }
  }, [getProviderDynamicModels, hasPiCli])

  // The group's designated default, else its first listed model.
  const piGroupDefaultModel = useCallback((group: string): string | null => {
    const inGroup = piCliModels.filter(model => model.group === group)
    if (inGroup.length === 0) return null
    return (inGroup.find(model => model.is_default) ?? inGroup[0]).model_id
  }, [piCliModels])

  const providerOptions = useMemo(() => getWorkflowProviderOptions(providerManifest), [providerManifest])
  const workflowOptions = useMemo(
    () => getWorkflowLLMOptions(availableLLMs, providerManifest),
    [availableLLMs, providerManifest],
  )

  const rows = useMemo<ProviderRow[]>(() => {
    const codingAgents = manifestEntries
      .filter(entry => entry.integration_kind === 'coding_agent')
      .sort((a, b) => codingAgentProviderRank(a.id) - codingAgentProviderRank(b.id) || a.display_name.localeCompare(b.display_name))

    const result: ProviderRow[] = []
    codingAgents.forEach(entry => {
      if (entry.id === 'pi-cli' && splitPiProviders) {
        const publishedPi = availableLLMs.filter(option => option.provider === 'pi-cli')
        piCliGroups.forEach(group => {
          const seenModels = new Set<string>()
          const defaultModelId = piGroupDefaultModel(group)
          // Default first, then the rest of the curated catalog for this backend.
          const catalog = piCliModels
            .filter(model => model.group === group)
            .sort((a, b) => Number(b.model_id === defaultModelId) - Number(a.model_id === defaultModelId))
          catalog.forEach(model => {
            if (seenModels.has(model.model_id)) return
            seenModels.add(model.model_id)
            result.push({ id: piRowId(group, model.model_id), entry, name: group, modelId: model.model_id, selectable: true, groupFilter: group })
          })
          // Published entries for this backend that the catalog does not list.
          publishedPi
            .filter(option => resolvePiModelGroup(option.model) === group && !seenModels.has(option.model))
            .forEach(option => {
              seenModels.add(option.model)
              result.push({ id: piRowId(group, option.model), entry, name: group, modelId: option.model, selectable: true, groupFilter: group, published: true })
            })
        })
        return
      }
      const selectable = providerOptions.some(option => option.provider === entry.id)
      result.push({
        id: entry.id,
        entry,
        name: entry.display_name,
        modelId: entry.default_tier_models?.high.model_id || entry.default_model_id || null,
        selectable,
      })
    })
    return result
  }, [availableLLMs, manifestEntries, piCliGroups, piCliModels, piGroupDefaultModel, providerOptions, splitPiProviders])

  // The row for a Pi backend, at a specific model when one is given and
  // listed, else the backend's first (default) row.
  const piRowFor = useCallback((group: string | null, modelId?: string | null): string | null => {
    if (!group) return null
    const inGroup = rows.filter(row => row.groupFilter === group)
    if (inGroup.length === 0) return null
    return (inGroup.find(row => row.modelId === modelId) ?? inGroup[0]).id
  }, [rows])

  // Which row the current config corresponds to, if any.
  const selectedRowId = useMemo<string | null>(() => {
    if (globalLockApplies) {
      // What actually runs under the lock (server: resolveLockedLLM), whatever
      // this workflow saved -- so the published provider is the one "In use".
      const effective = effectiveLLMUnderLock(
        { provider: llmConfig?.builder_llm?.provider ?? llmConfig?.provider, model_id: llmConfig?.builder_llm?.model_id },
        true,
        publishedLLMs,
      )
      if (effective) return effective.provider
    }
    if (!llmConfig) return null
    if (llmConfig.mode === 'provider_profile') {
      if (llmConfig.provider === 'pi-cli') {
        // A bare `{mode: provider_profile, provider: "pi-cli"}` config (no
        // per-role override) is fully valid and actively resolved on the
        // backend: ResolveProviderProfileConfig reads it straight from
        // pi-cli's default tier models -- there's no separate "group"
        // concept there. The UI only groups pi-cli's models by family
        // (Gemini, MiniMax, ...) for display, so without this the row
        // lookup below never matches a bare "pi-cli" id and a workflow
        // that is genuinely running on pi-cli's default model shows as
        // "No provider selected". Resolve the provider's own default
        // (High tier) model back to its display group instead.
        const piEntry = manifestEntries.find(entry => entry.id === 'pi-cli')
        const defaultModelId = piEntry?.default_tier_models?.high.model_id
        return piRowFor(defaultModelId ? resolvePiModelGroup(defaultModelId) : null, defaultModelId)
      }
      return llmConfig.provider ?? null
    }
    const roles = ROLE_KEYS.map(key => roleConfig(llmConfig, key))
    if (roles.some(role => !role)) return null
    const providers = new Set(roles.map(role => role!.provider))
    if (providers.size !== 1) return null
    const [provider] = Array.from(providers)
    if (provider === 'pi-cli') {
      const groups = new Set(roles.map(role => resolvePiModelGroup(role!.model_id)))
      if (groups.size !== 1) return null
      const [group] = Array.from(groups)
      // Every role on the same model highlights that model's row; a mixed
      // per-role setup within one backend falls back to the backend default row.
      const models = new Set(roles.map(role => role!.model_id))
      return piRowFor(group, models.size === 1 ? Array.from(models)[0] : null)
    }
    // A per-role setup that stays on one coding-agent provider (e.g. every
    // role on Claude Code, just different models/efforts) still "runs on"
    // that provider: match its row so the compact status line and the
    // Models per role section show instead of "custom per-role setup".
    return manifestEntries.some(entry => entry.id === provider && entry.integration_kind === 'coding_agent') ? provider : null
  }, [globalLockApplies, llmConfig, manifestEntries, piRowFor, publishedLLMs])

  const visibleRows = useMemo(() => {
    const q = query.trim().toLowerCase()
    const filtered = q
      ? rows.filter(row => [row.name, row.id, row.modelId, row.entry.description]
          .some(value => value?.toLowerCase().includes(q)))
      : rows
    // The row this workflow is actually using bubbles to the top -- scanning
    // status dots down the list otherwise can't tell "in use" from "also ready".
    // Pi rows keep their catalog order: the company in use is already
    // marked, and reordering models under it would just look like a jump.
    if (!selectedRowId) return filtered
    return [...filtered].sort((a, b) => {
      if (a.groupFilter || b.groupFilter) return 0
      if (a.id === selectedRowId) return -1
      if (b.id === selectedRowId) return 1
      return 0
    })
  }, [query, rows, selectedRowId])

  const selectedRow = useMemo(() => rows.find(row => row.id === selectedRowId) ?? null, [rows, selectedRowId])

  // A custom per-role setup has no provider row to read; the per-role list is
  // the only place that shows what the workflow runs on, so it opens itself.
  const customPerRole = advanced && Boolean(llmConfig) && !selectedRow
  useEffect(() => {
    if (customPerRole) setRolesOpen(true)
  }, [customPerRole])
  const selectedBaseProvider = selectedRow ? (selectedRow.groupFilter ? 'pi-cli' : selectedRow.id) : null

  const selectedProfile = useMemo(() => {
    const provider = llmConfig?.mode === 'provider_profile'
      ? llmConfig.provider
      : llmConfig?.builder_llm?.provider
    return providerOptions.find(option => option.provider === provider) ?? null
  }, [llmConfig, providerOptions])

  const piGroupOption = useCallback((row: ProviderRow): LLMOption | null => {
    if (!row.groupFilter || !row.modelId) return null
    return { provider: 'pi-cli', model: row.modelId, label: row.name, section: 'published_model' }
  }, [])

  const explicitConnectionID = llmConfig?.mode === 'provider_profile'
    ? llmConfig.connection_id
    : llmConfig?.builder_llm?.connection_id || llmConfig?.connection_id
  // Crew and Code with no account chosen run on the person's own signed-in account for the
  // provider, newest first (the server does the same: ownDefaultProviderAccountID).
  const ownDefaultConnectionID = configurationSource === 'agent_profile' && !explicitConnectionID
    ? [...privateConnections]
      .filter(record => record.provider === selectedProfile?.provider && accountRelation(record) === 'own' && accountUsable(record) && accountConfigured(record) !== false)
      .sort((a, b) => String(b.updated_at || '').localeCompare(String(a.updated_at || '')))[0]?.id
    : undefined
  const selectedConnectionID = explicitConnectionID || ownDefaultConnectionID
  // The selected user account, usable here or not (kept for its name).
  const selectedAccountRecord = selectedConnectionID && !selectedConnectionID.startsWith('global:')
    ? privateConnections.find(record => record.id === selectedConnectionID && record.provider === selectedProfile?.provider)
    : undefined
  const selectedPrivateAccount = selectedAccountRecord && accountUsable(selectedAccountRecord) ? selectedAccountRecord : undefined
  const selectedServerRecord = selectedConnectionID?.startsWith('global:') ? accountRecords.find(record => record.id === selectedConnectionID) : undefined
  const selectedAccountUnavailable = Boolean(selectedConnectionID && (selectedConnectionID.startsWith('global:')
    ? selectedServerRecord?.usable === false
    : !selectedPrivateAccount))
  // Role defaults to compare against / reset to. For a pi backend that's the
  // backend's model for every role; otherwise the provider profile's manifest
  // defaults.
  const defaults = useMemo(() => {
    if (selectedRow?.groupFilter) {
      const option = piGroupOption(selectedRow)
      return option ? getWorkflowLLMTierDefaults(option, providerManifest) : null
    }
    return selectedProfile ? getWorkflowLLMTierDefaults(selectedProfile, providerManifest) : null
  }, [piGroupOption, providerManifest, selectedProfile, selectedRow])

  // The workflow-level config a row stands for: a Pi group pins every role
  // to that family's defaults (a provider profile can't say "pi-cli, but
  // only Gemini"); any other coding agent is a plain provider profile.
  const configForRow = (row: ProviderRow, accountID?: string): PresetLLMConfig | null => {
    if (!row.selectable) return null
    if (!accountID && row.entry.integration_kind === 'coding_agent' && !providerIsReadyForUse(row)) return null
    const existingID=llmConfig?.provider===row.entry.id ? llmConfig.connection_id : llmConfig?.builder_llm?.provider===row.entry.id ? llmConfig.builder_llm.connection_id : undefined
 const personal=privateConnections.find(record=>record.provider===row.entry.id && accountRelation(record)==='own' && accountUsable(record) && (!row.groupFilter || row.modelId?.startsWith(record.underlying_provider+"/")))
 const connection_id=accountID || existingID || (!row.entry.usable ? personal?.id : undefined)
 if (row.groupFilter) {
      const option = piGroupOption(row)
      if (!option) return null
      const groupDefaults = getWorkflowLLMTierDefaults(option, providerManifest)
      return {
        schema_version: 2,
        mode: 'explicit',
        connection_id,
 builder_llm: {...groupDefaults.builder,connection_id},
        pulse_llm: {...groupDefaults.pulse,connection_id},
        tiered_config: { tier_1: {...groupDefaults.tier1,connection_id}, tier_2: {...groupDefaults.tier2,connection_id}, tier_3: {...groupDefaults.tier3,connection_id} },
      }
    }
    return { schema_version: 2, mode: 'provider_profile', provider: row.id as LLMProvider, connection_id }
  }

  const selectRow = (row: ProviderRow) => {
    const config = configForRow(row)
    if (!config) return
    onChange(stripRetiredLLMFallbacks(config))
  }

  // "Use in this workflow": select the provider and persist right away. Used
  // by the inline row button for sign-in CLIs and by the Pi drill-in.
  const applyRowToWorkflow = async (row: ProviderRow) => {
    const config = configForRow(row)
    if (!config) return
    setRowUsing(row.id)
    try {
      onChange(stripRetiredLLMFallbacks(config))
      if (onUseProvider) await onUseProvider(config)
      setChanging(false)
    } finally {
      setRowUsing(null)
    }
  }

  const applyActiveRowToWorkflow = async () => {
    if (!activeRow) return
    await applyRowToWorkflow(activeRow)
    setActiveProviderId(null)
  }

  const useManagedDefaults = () => {
    if (!selectedProfile) return
    onChange({ schema_version: 2, mode: 'provider_profile', provider: selectedProfile.provider as LLMProvider, connection_id: llmConfig?.connection_id || llmConfig?.builder_llm?.connection_id })
    // Back on the provider's defaults there is nothing per-role left to look
    // at, so fold the section (and remember it folded).
    setRolesOpen(false)
    writeRolesOpen(false)
  }

  // Pinning any one role switches the workflow from the managed provider
  // profile to per-role (explicit) mode. On that first switch every OTHER
  // role has to be seeded too -- from the current config if it already had
  // one (rare: switching back from a prior explicit setup), else from the
  // provider's own defaults -- or an explicit config with only the touched
  // role set would leave the rest with no model at all. Previously this
  // bailed out entirely while still on a provider profile (silent no-op:
  // the very first edit did not save) and, even past that, only wrote the
  // single touched role.
  const updateRole = (key: RoleKey, next: AgentLLMConfig) => {
    const seed = (roleKey: RoleKey): AgentLLMConfig | undefined =>
      key === roleKey ? {...next,connection_id:next.connection_id || (roleConfig(llmConfig,roleKey)?.provider===next.provider ? roleConfig(llmConfig,roleKey)?.connection_id : next.provider===selectedProfile?.provider ? llmConfig?.connection_id : undefined)} : (roleConfig(llmConfig, roleKey) ?? defaultForRole(roleKey))
    const nextConfig: PresetLLMConfig = {
      ...llmConfig,
      schema_version: 2,
      mode: 'explicit',
      builder_llm: seed('builder_llm'),
      pulse_llm: seed('pulse_llm'),
      tiered_config: {
        tier_1: seed('tier_1') ?? next,
        tier_2: seed('tier_2') ?? next,
        tier_3: seed('tier_3') ?? next,
      },
    }
    onChange(stripRetiredLLMFallbacks(nextConfig))
  }

  const defaultForRole = (key: RoleKey): AgentLLMConfig | undefined => {
    if (!defaults) return undefined
    const value = key === 'tier_1' ? defaults.tier1
      : key === 'tier_2' ? defaults.tier2
        : key === 'tier_3' ? defaults.tier3
          : key === 'builder_llm' ? defaults.builder
                    : defaults.pulse
    return value ? { ...value, connection_id: llmConfig?.connection_id } : undefined
  }

  const resetRole = (key: RoleKey) => {
    const defaultValue = defaultForRole(key)
    if (!advanced || !defaultValue) return
    updateRole(key, defaultValue)
  }

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      await Promise.all([loadProviderManifest(), loadDefaultsFromBackend()])
    } finally {
      setRefreshing(false)
    }
  }

  const activeRow = useMemo(() => rows.find(row => row.id === activeProviderId) ?? null, [activeProviderId, rows])

  // ---- Drill-in: connect / configure one provider ------------------------

  if (activeProviderId !== null) {
    const locked = isProviderLocked(activeProviderId)
    return (
      <div className="space-y-4">


        <Button
          type="button"
          variant="link"
          size="sm"
          onClick={() => setActiveProviderId(null)}
        >
          <ArrowLeft className="h-4 w-4" />
          Back to providers
        </Button>

        {!activeRow ? (
          <div className="py-8 text-center text-sm text-muted-foreground">Loading provider info...</div>
        ) : locked ? (
          <div className="flex items-start gap-2 rounded-md border border-dashed border-border px-4 py-5 text-sm text-muted-foreground">
            <Lock className="mt-0.5 h-4 w-4 shrink-0" />
            <div>
              <div className="font-medium text-foreground">Configured by admin</div>
              <div className="mt-0.5 text-xs">The credentials for this provider are set server-side. Contact your administrator to change them.</div>
            </div>
          </div>
        ) : (
          <CodingAgentSection
            key={activeProviderId}
            provider={activeRow.entry}
            groupFilter={activeRow.groupFilter}
            readOnly={readOnly}
            variant="workflow"
            initialModelId={activeRow.modelId ?? undefined}
            inUse={activeRow.id === selectedRowId}
            onUseInWorkflow={applyActiveRowToWorkflow}
            scopeNoun={scopeNoun}
          />
        )}
      </div>
    )
  }

  // ---- Main view ----------------------------------------------------------

  const applyAccountToWorkflow = async (row: ProviderRow, connectionID: string) => {
    const config = configForRow(row, connectionID)
    if (!config || readOnly) return
    setRowUsing(row.id)
    setAccountSelectionError(null)
    try {
      onChange(stripRetiredLLMFallbacks(config))
      if (onUseProvider) await onUseProvider(config)
    } catch {
      setAccountSelectionError('Could not save account selection. Try again.')
    } finally { setRowUsing(null) }
  }

  const renderAccountTree = (row: ProviderRow) => {
    const serverID = `global:${row.entry.id}`
    const global = accountRecords.find(record => record.id === serverID)
    const server = global || { id: serverID, provider: row.entry.id, display_name: 'Server account', scope: 'global' as const, auth_method: 'server' }
    const userAccounts = privateConnections.filter(record => record.provider === row.entry.id && (!row.groupFilter || row.modelId?.startsWith(`${record.underlying_provider}/`)))
    const selectedProvider = selectedRow?.entry.id === row.entry.id && (!row.groupFilter || selectedRow?.groupFilter === row.groupFilter)
    const activeID = selectedConnectionID || serverID
    // Only accounts usable here are listed; the one this item already uses
    // stays visible when it is not, so saving never silently switches it.
    const listed = [server, ...userAccounts].filter(account => accountUsable(account) || (selectedProvider && account.id === activeID))
    const missingSelected = selectedProvider && !listed.some(account => account.id === activeID)
    const renderAccount = (account: typeof server | import('../../services/llm-config-api').ProviderConnection, unavailable: boolean) => {
      const inUse = selectedProvider && activeID === account.id
      const needsSetup = 'configured' in account && account.configured === false
      const available = !unavailable && !needsSetup && row.entry.runtime_available !== false && (account.scope === 'user' ? global?.personal_accounts_allowed !== false : Boolean(row.entry.usable))
      const relation = accountRelation(account)
      const note = relation === 'own' ? ('sharing' in account && account.sharing?.mode === 'shared' ? 'Yours, shared' : 'Private')
        : relation === 'server' ? `Shared${'identity' in account && account.identity ? ` · ${account.identity}` : ''}`
          : `Shared${'owner_name' in account && account.owner_name ? ` by ${account.owner_name}` : ''}`
      return <div key={account.id} className="flex flex-wrap items-center gap-2 py-2">
        {account.scope === 'global' ? <ShieldCheck className="h-3.5 w-3.5 text-muted-foreground" /> : <UserRound className="h-3.5 w-3.5 text-muted-foreground" />}
        <span className="min-w-0 break-words text-xs font-medium text-foreground">{relation === 'server' ? 'Shared account' : account.display_name}</span>
        <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{note}</span>
        {unavailable && <span className="text-[10px] font-medium text-amber-600 dark:text-amber-400">{NO_LONGER_AVAILABLE}</span>}
        {inUse && <span className="text-[10px] font-medium text-primary">In use</span>}
        <span className="min-w-0 flex-1" />
        {needsSetup && !inUse
          ? <Button type="button" variant="outline" size="xs" disabled={readOnly} onClick={() => useLLMStore.getState().setShowLLMModal(true)} title={`${account.display_name} is not signed in yet: set it up on the Providers page`} aria-label={`Set up ${row.name} account ${account.display_name}`}>Set up</Button>
          : <Button type="button" variant="outline" size="xs" disabled={readOnly || !available || inUse || rowUsing === row.id} onClick={() => void applyAccountToWorkflow(row, account.id)} title={unavailable ? NO_LONGER_AVAILABLE : !available ? 'Account needs setup or CLI is unavailable' : `Use ${account.display_name}`} aria-label={`Use ${row.name} account ${account.display_name}`}>{inUse ? 'Selected' : 'Use account'}</Button>}
      </div>
    }
    const showHeadings = listed.some(account => accountRelation(account) !== 'server' && accountRelation(account) !== 'own')
    return (
      <div className="ml-5 border-l border-border py-1 pl-3 pr-3">
        {ACCOUNT_GROUPS.map(group => {
          const accounts = listed.filter(account => group.relations.includes(accountRelation(account)))
          if (accounts.length === 0) return null
          return <div key={group.label}>
            {showHeadings && <div className="pt-2 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{group.label}</div>}
            {accounts.map(account => renderAccount(account, !accountUsable(account)))}
          </div>
        })}
        {missingSelected && <div className="flex flex-wrap items-center gap-2 py-2">
          <UserRound className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="min-w-0 break-words text-xs font-medium text-foreground">Selected account</span>
          <span className="text-[10px] font-medium text-amber-600 dark:text-amber-400">{NO_LONGER_AVAILABLE}</span>
        </div>}
        {selectedProvider && selectedAccountUnavailable && <p role="alert" className="py-2 text-xs text-amber-600 dark:text-amber-400">The selected account is unavailable. Choose another configured account or manage accounts in Providers.</p>}
      </div>
    )
  }

  const renderStatusLine = () => {
    if (selectedRow) {
      const status = selectedAccountUnavailable
        ? { label: 'Account unavailable' }
        : selectedPrivateAccount && selectedRow.entry.runtime_available !== false
          ? { label: 'Ready' }
          : providerStatus(selectedRow.entry, isProviderLocked(selectedRow.id))
      const tone = statusTone(status.label)
      const usable = status.label === 'Ready' || status.label === 'Managed'
      return (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <span className="text-muted-foreground">This {scopeNoun} runs on</span>
          <span className="inline-flex items-center gap-1.5 font-medium text-foreground">
            <span className={`h-2 w-2 rounded-full ${tone.dot}`} />
            {selectedRow.name}
          </span>
          {selectedRow.groupFilter && selectedRow.modelId && (
            <span className="truncate font-mono text-[11px] text-muted-foreground">{selectedRow.modelId}</span>
          )}
          {selectedPrivateAccount && (
            <span className="text-xs text-muted-foreground">· {selectedPrivateAccount.display_name} (private)</span>
          )}
          {globalLockApplies && (
            <span className="inline-flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-[11px] text-muted-foreground" title={`Every ${scopeNoun} on this deployment uses this provider`}>
              <Lock className="h-3 w-3" /> Set by your administrator
            </span>
          )}
          {!usable && !globalLockApplies && (
            <>
              <span className={`text-xs font-medium ${tone.text}`} title={statusTitle(status.label)}>
                {statusActionText(status.label) ?? status.label}
              </span>
              <Button
                type="button"
                size="xs"
                onClick={() => selectedRow.entry.integration_kind === 'coding_agent'
                  ? setShowLLMModal(true)
                  : setActiveProviderId(selectedRow.id)}
                disabled={readOnly}
              >
                {selectedRow.entry.integration_kind === 'coding_agent' ? 'Manage in Providers' : 'Set up'}
              </Button>
            </>
          )}
          {!readOnly && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => setChanging(open => !open)}
              className="ml-auto"
            >
              {changing ? 'Cancel' : 'Change provider'}
            </Button>
          )}
        </div>
      )
    }
    if (advanced) {
      // Roles spread across more than one provider, so no single row is "in
      // use". The per-role section below is where that setup is visible and
      // editable; "Change provider" replaces all of it with one provider.
      return (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <span className="text-muted-foreground">This {scopeNoun} uses a</span>
          <span className="font-medium text-foreground">custom per-role setup</span>
          <span className="text-muted-foreground">— see Models per role below.</span>
          {!readOnly && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => setChanging(open => !open)}
              className="ml-auto"
            >
              {changing ? 'Cancel' : 'Change provider'}
            </Button>
          )}
        </div>
      )
    }
    return <div className="text-sm text-muted-foreground">No provider selected — pick one below.</div>
  }

  const renderTokenLine = () => {
    if (selectedConnectionID && !selectedConnectionID.startsWith('global:')) return null
    if (selectedBaseProvider !== 'claude-code' && selectedBaseProvider !== 'cursor-cli') return null
    const isClaude = selectedBaseProvider === 'claude-code'
    return (
      <div className="mt-2">
        <button
          type="button"
          onClick={() => setTokenOpen(open => !open)}
          aria-expanded={tokenOpen}
          className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          <ChevronRight className={`h-3 w-3 transition-transform ${tokenOpen ? 'rotate-90' : ''}`} />
          {scopeNoun.charAt(0).toUpperCase() + scopeNoun.slice(1)} {isClaude ? 'token' : 'API key'}
          <span className="text-muted-foreground/70">· scoped to this {scopeNoun}, falls back to the saved login</span>
        </button>
        {tokenOpen && (
          <div className="mt-2 rounded-md border border-border bg-muted/20 p-3">
            {isClaude ? (
              <WorkflowProviderCredentialField
                provider="claude-code"
                inputId="workflow-claude-code-token"
                workflowCredentialPath={workspacePath || undefined}
                readOnly={readOnly}
                copy={{
                  heading: 'Claude Code token',
                  hint: <>Use a token from <code className="rounded bg-background px-1 py-0.5 font-mono text-foreground">claude setup-token</code>, or leave this empty to use the saved Claude login.</>,
                  fallbackLabel: 'Using saved login',
                  inputPlaceholder: 'Paste Claude Code token',
                  replacePlaceholder: 'Paste a replacement token',
                  noun: 'token',
                  savedMessage: 'Workflow Claude Code token saved.',
                  removedMessage: 'Workflow Claude Code token removed; saved Claude login will be used.',
                }}
              />
            ) : (
              <WorkflowProviderCredentialField
                provider="cursor-cli"
                inputId="workflow-cursor-api-key"
                workflowCredentialPath={workspacePath || undefined}
                readOnly={readOnly}
                copy={{
                  heading: 'Cursor API key',
                  hint: <>Paste an API key from <code className="rounded bg-background px-1 py-0.5 font-mono text-foreground">cursor.com</code> settings, or leave this empty to use the saved Cursor login.</>,
                  fallbackLabel: 'Using saved login',
                  inputPlaceholder: 'Paste Cursor API key',
                  replacePlaceholder: 'Paste a replacement API key',
                  noun: 'API key',
                  savedMessage: 'Workflow Cursor API key saved.',
                  removedMessage: 'Workflow Cursor API key removed; saved Cursor login will be used.',
                }}
              />
            )}
          </div>
        )}
      </div>
    )
  }

  const renderRow = (row: ProviderRow) => {
    const status = row.id === selectedRowId && selectedAccountUnavailable ? { label: 'Account unavailable' } : providerStatus(row.entry, isProviderLocked(row.id))
    const tone = statusTone(status.label)
    const selected = row.id === selectedRowId
    const inlineActions = row.entry.integration_kind === 'coding_agent' && !row.groupFilter
    if (inlineActions) {
      // Sign-in CLI: authentication and diagnostics live in the platform-level
      // Providers panel. This workflow-level row only reports that state and
      // lets the user choose which connected CLI to use.
      const connected = selected
        ? !selectedAccountUnavailable && (selectedPrivateAccount ? row.entry.runtime_available !== false : Boolean(row.entry.usable) || isProviderLocked(row.id))
        : providerIsReadyForUse(row)
      const open = expandedAccountProviders.has(row.id) || selected
      return (
        <div key={row.id}>
        <div className={`flex items-center gap-2 px-3 py-2 ${selected ? 'bg-primary/5' : ''}`}>
          <button type="button" aria-expanded={open} aria-label={`Show ${row.name} accounts`} onClick={() => setExpandedAccountProviders(current => { const next = new Set(current); if (next.has(row.id)) next.delete(row.id); else next.add(row.id); return next })} className="inline-flex items-center gap-2 text-left">
            <ChevronRight className={`h-3.5 w-3.5 text-muted-foreground ${open ? 'rotate-90' : ''}`} />
            <span className={`text-sm ${selected ? 'font-semibold' : 'font-medium'} text-foreground`}>{row.name}</span>
          </button>
          {selected && (
            <span className="shrink-0 rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">
              In use
            </span>
          )}
          <span className="min-w-0 flex-1" />
          {connected ? (
            <span className="inline-flex shrink-0 items-center gap-1 text-[11px] font-medium text-emerald-600 dark:text-emerald-400" title={selectedPrivateAccount && selected ? "Private account configured; authentication is checked when it runs" : "Provider account configured"}>
              <CheckCircle2 className="h-3 w-3" /> {selected && selectedPrivateAccount ? 'Account configured' : 'Connected'}
            </span>
          ) : (
            <span
              className={`inline-flex shrink-0 items-center gap-1 text-[11px] font-medium ${tone.text}`}
              title={statusTitle(status.label)}
            >
              <span className={`h-1.5 w-1.5 rounded-full ${tone.dot}`} />
              {statusActionText(status.label)}
            </span>
          )}
          {connected && !selected ? (
            <Button
              type="button"
              size="xs"
              onClick={() => void applyRowToWorkflow(row)}
              disabled={readOnly || !row.selectable || rowUsing === row.id}
              title={readOnly ? disabledTitle : `Use ${row.name} for this ${scopeNoun}`}
              className="shrink-0"
            >
              {rowUsing === row.id ? <><Loader2 className="h-3 w-3 animate-spin" /> Saving…</> : 'Use'}
            </Button>
          ) : !connected ? (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => setShowLLMModal(true)}
              title={`Manage ${row.name} accounts in Providers`}
              className="shrink-0"
            >
              Manage in Providers
            </Button>
          ) : null}
        </div>
        {open && renderAccountTree(row)}
        </div>
      )
    }
    return (
      <div key={row.id} className={`flex items-center gap-2 px-3 py-2 ${selected ? 'bg-primary/5' : ''}`}>
        {row.entry.integration_kind === 'coding_agent' ? (
          <button
            type="button"
            role="radio"
            aria-checked={selected}
            disabled={readOnly || !row.selectable}
            onClick={() => selectRow(row)}
            title={readOnly ? disabledTitle : row.selectable ? `Use ${row.name} for this ${scopeNoun}` : 'Loading models…'}
            className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-border transition-colors hover:border-primary disabled:cursor-not-allowed disabled:opacity-40"
          >
            {selected && <span className="h-2 w-2 rounded-full bg-primary" />}
          </button>
        ) : (
          <span className="h-4 w-4 shrink-0" aria-hidden />
        )}
        <button
          type="button"
          onClick={() => setActiveProviderId(row.id)}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          title={`Connect or configure ${row.name}`}
        >
          <span className={`shrink-0 text-sm ${selected ? 'font-semibold' : 'font-medium'} text-foreground`}>{row.name}</span>
          {selected && (
            <span className="shrink-0 rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">
              In use
            </span>
          )}
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{row.modelId ?? ''}</span>
          {row.published && (
            <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground" title="From your published LLMs">
              Published
            </span>
          )}
          <span
            className={`inline-flex shrink-0 items-center gap-1 text-[11px] font-medium ${tone.text}`}
            title={statusTitle(status.label)}
          >
            <span className={`h-1.5 w-1.5 rounded-full ${tone.dot}`} />
            {statusActionText(status.label)}
          </span>
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        </button>
      </div>
    )
  }

  // Two kinds of provider live in one list and want different flows: a
  // coding-agent CLI is signed in once and just needs test + use, while a
  // Pi-backed model family needs its own API key saved first. Label them so
  // the list reads as two categories instead of ten look-alike rows.
  const renderGroup = (title: string, hint: string, groupRows: ProviderRow[], renderRows: (rows: ProviderRow[]) => ReactNode = rows => rows.map(renderRow)) => {
    if (groupRows.length === 0) return null
    return (
      <div key={title} className="divide-y divide-border">
        <div className="bg-muted/30 px-3 py-1.5">
          <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{title}</div>
          <div className="text-[11px] text-muted-foreground/80">{hint}</div>
        </div>
        {renderRows(groupRows)}
      </div>
    )
  }

  // Pi backends: one company row (Gemini, MiniMax, ...) that unfolds into its
  // models. Selection happens on a model row; the company row carries the
  // status and the way into keys / test / publish.
  const renderPiGroups = (groupRows: ProviderRow[]) => {
    const byGroup = new Map<string, ProviderRow[]>()
    groupRows.forEach(row => {
      const group = row.groupFilter ?? ''
      byGroup.set(group, [...(byGroup.get(group) ?? []), row])
    })
    const searching = query.trim().length > 0
    return Array.from(byGroup.entries()).map(([group, models]) => {
      const head = models[0]
      const status = providerStatus(head.entry, isProviderLocked(head.id))
      const tone = statusTone(status.label)
      const selectedInGroup = models.find(row => row.id === selectedRowId) ?? null
      const connected = providerIsReadyForUse(head)
      const open = searching || openPiGroups.has(group) || selectedInGroup !== null
      return (
        <div key={group} className="divide-y divide-border">
          <div className={`flex items-center gap-2 px-3 py-2 ${selectedInGroup ? 'bg-primary/5' : ''}`}>
            <button
              type="button"
              onClick={() => togglePiGroup(group)}
              aria-expanded={open}
              className="flex min-w-0 flex-1 items-center gap-2 text-left"
              title={open ? `Hide ${group} models` : `Show ${group} models`}
            >
              <ChevronDown className={`h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform ${open ? '' : '-rotate-90'}`} />
              <span className={`shrink-0 text-sm ${selectedInGroup ? 'font-semibold' : 'font-medium'} text-foreground`}>{group}</span>
              {selectedInGroup && (
                <span className="shrink-0 rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                  In use
                </span>
              )}
              <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">
                {selectedInGroup ? selectedInGroup.modelId : `${models.length} model${models.length === 1 ? '' : 's'}`}
              </span>
            </button>
            <span
              className={`inline-flex shrink-0 items-center gap-1 text-[11px] font-medium ${tone.text}`}
              title={statusTitle(status.label)}
            >
              <span className={`h-1.5 w-1.5 rounded-full ${tone.dot}`} />
              {statusActionText(status.label)}
            </span>
            <Button
              type="button"
              variant="link"
              size="xs"
              onClick={() => setShowLLMModal(true)}
              className="shrink-0"
              title={`${connected ? 'Manage' : 'Set up'} Pi in global Providers`}
            >
              {connected ? 'Providers' : 'Set up in Providers'} <ChevronRight className="h-3 w-3" />
            </Button>
          </div>
          {open && renderAccountTree(selectedInGroup || head)}
          {open && models.map(row => {
            const selected = row.id === selectedRowId
            const saving = rowUsing === row.id
            const disabled = readOnly || !connected || !row.selectable || selected || rowUsing !== null
            // Picking a model is the whole action: it is saved to the
            // workflow and the list folds back to the "runs on" line, the
            // same as Use on a CLI row.
            const pick = () => { void applyRowToWorkflow(row) }
            return (
              <div key={row.id} className={`flex items-center gap-2 py-1.5 pl-9 pr-3 ${selected ? 'bg-primary/5' : ''}`}>
                <button
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  disabled={disabled}
                  onClick={pick}
                  title={readOnly ? disabledTitle : !connected ? 'Set up Pi in global Providers first' : selected ? 'In use' : `Use ${row.modelId} for this ${scopeNoun}`}
                  className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-border transition-colors hover:border-primary disabled:cursor-not-allowed disabled:opacity-40"
                >
                  {selected && <span className="h-2 w-2 rounded-full bg-primary" />}
                </button>
                <button
                  type="button"
                  disabled={disabled}
                  onClick={pick}
                  className="flex min-w-0 flex-1 items-center gap-2 text-left disabled:cursor-not-allowed"
                >
                  <span className={`min-w-0 flex-1 truncate font-mono text-[11px] ${selected ? 'font-semibold text-foreground' : 'text-foreground'}`}>{row.modelId}</span>
                  {row.published && (
                    <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground" title="From your published LLMs">
                      Published
                    </span>
                  )}
                  {saving ? (
                    <span className="inline-flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" /> Saving…</span>
                  ) : selected ? (
                    <span className="shrink-0 rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">In use</span>
                  ) : (
                    <span className="shrink-0 text-[11px] font-medium text-primary">Use</span>
                  )}
                </button>
              </div>
            )
          })}
        </div>
      )
    })
  }

  // One flat row per role: what it runs on now, and the picker right there.
  // Changing a role switches the workflow to per-role (explicit) mode via
  // updateRole, which seeds the other roles from the provider's defaults, so
  // there is no separate "pin models" step.
  const renderRole = (row: RoleRow) => {
    const value = roleConfig(llmConfig, row.key) ?? defaultForRole(row.key)
    const defaultValue = defaultForRole(row.key)
    const isCustomized = advanced && (configKey(value ?? {}) !== configKey(defaultValue ?? {}))

    return (
      <div key={row.key} className="flex flex-col gap-2 border-t border-border px-3 py-2.5 first:border-t-0 sm:flex-row sm:items-center sm:gap-3">
        <div className="min-w-0 sm:w-[38%]">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs font-medium text-foreground">{row.label}</span>
            {isCustomized && (
              <span className="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">Customized</span>
            )}
          </div>
          <div className="mt-0.5 text-[11px] text-muted-foreground">{builderOnly && row.key === 'builder_llm' ? 'Used for the Relay Builder chat and graph editing.' : row.description}</div>
        </div>
        <div className="min-w-0 flex-1 space-y-1.5">
          {value ? (
            <>
            <LLMRoleSelector availableLLMs={workflowOptions} value={value} onLLMSelect={llm => updateRole(row.key, toAgentLLMConfig(llm))} disabled={readOnly} />
            {value?.provider && ["claude-code","codex-cli","cursor-cli","pi-cli","muse-cli"].includes(value.provider) && <ProviderAccounts key={value.provider} provider={value.provider} selectedId={value.connection_id} disabled={readOnly} selectionOnly workspacePath={workspacePath} product={product} onSelect={connection_id=>updateRole(row.key,{...value,connection_id})} />}
            </>
          ) : (
            <span className="text-xs text-muted-foreground">Select a provider first.</span>
          )}
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            {isCustomized && defaultValue && (
              <Button type="button" variant="link" size="xs" onClick={() => resetRole(row.key)} disabled={readOnly} title={readOnly ? disabledTitle : undefined}>
                Reset to provider default
              </Button>
            )}

          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {builderOnly && <p className="text-xs text-muted-foreground">Choose the Builder model here. Ask the Builder to set an execution model for each agent step as needed.</p>}
      <div className="rounded-lg border border-border bg-muted/20 p-3">
        {renderStatusLine()}
        {renderTokenLine()}
      </div>
      {!changing && selectedRow && selectedRow.entry.integration_kind === 'coding_agent' && (
        <div aria-label={`${selectedRow.name} accounts`} className="overflow-hidden rounded-lg border border-border bg-background">
          {renderRow(selectedRow)}
        </div>
      )}
      {accountSelectionError && <p role="alert" className="text-xs text-red-600 dark:text-red-400">{accountSelectionError}</p>}
      {selectedPrivateAccount && <p className="text-xs text-muted-foreground">New runs use this account. Start a new Builder conversation to switch an existing chat.</p>}

      {((!selectedRow && !advanced) || changing || globalLockApplies) && (
      <>
      {globalLockApplies && (
        <p className="text-xs text-muted-foreground">
          Every {scopeNoun} on this deployment uses the provider marked “In use”. The others are shown for reference and cannot be selected here — ask your administrator to enable one.
        </p>
      )}
      <div className="flex items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            value={query}
            onChange={event => setQuery(event.target.value)}
            placeholder="Search providers"
            aria-label="Search providers"
            className="pl-9"
          />
        </div>
        <Button
          type="button"
          variant="outline"
          size="icon"
          onClick={() => { void handleRefresh() }}
          disabled={refreshing}
          title="Refresh providers"
          aria-label="Refresh providers"
          className="shrink-0"
        >
          <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
        </Button>
      </div>

      <div role="radiogroup" aria-label={`Provider for this ${scopeNoun}`} className="divide-y divide-border overflow-hidden rounded-md border border-border bg-background">
        {!providerManifestLoaded ? (
          <div className="py-6 text-center text-sm text-muted-foreground">Loading providers…</div>
        ) : visibleRows.length === 0 ? (
          <div className="py-6 text-center text-sm text-muted-foreground">
            {query.trim() ? `No providers match "${query}".` : 'No providers available.'}
          </div>
        ) : (
          <>
            {renderGroup(
              'Coding agent CLIs',
              `Open a provider to choose a shared or private account, or add your own.`,
              visibleRows.filter(row => row.entry.integration_kind === 'coding_agent' && !row.groupFilter),
            )}
            {renderGroup(
              'Models via Pi',
              'Each provider needs its own API key saved. Set up the key, then open a provider and pick the model to use.',
              visibleRows.filter(row => Boolean(row.groupFilter)),
              renderPiGroups,
            )}
          </>
        )}
      </div>
      </>
      )}

      {builderOnly && !changing && (selectedRow || advanced) && (
        <div className="overflow-hidden rounded-md border border-border bg-background">{renderRole(ROLE_ROWS.find(row => row.key === 'builder_llm')!)}</div>
      )}
      {!builderOnly && showModelsPerRole && !changing && (selectedRow || advanced) && (
      <div className="rounded-md border border-border">
        <button
          type="button"
          onClick={toggleRolesOpen}
          aria-expanded={rolesOpen}
          className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm font-medium text-foreground transition-colors hover:bg-muted/50"
        >
          <ChevronRight className={`h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform ${rolesOpen ? 'rotate-90' : ''}`} />
          Models per role
          <span className="font-normal text-muted-foreground">
            — {advanced ? 'pinned per role' : "following the selected provider's defaults"}
          </span>
        </button>
        {rolesOpen && (
          <div className="space-y-3 border-t border-border p-3">
            {advanced && (
              <div className="flex justify-end">
                <Button
                  type="button"
                  variant="link"
                  size="xs"
                  onClick={useManagedDefaults}
                  disabled={readOnly || !selectedProfile}
                  title={readOnly ? disabledTitle : selectedProfile ? undefined : 'No provider profile to return to'}
                  className="shrink-0"
                >
                  Use provider defaults for all roles
                </Button>
              </div>
            )}
            {(['Execution', 'Workflow agents'] as const).map(group => (
              <div key={group}>
                <div className="mb-1.5 px-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{group}</div>
                <div className="overflow-hidden rounded-md border border-border bg-background">
                  {ROLE_ROWS.filter(row => row.group === group).map(renderRole)}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
      )}
    </div>
  )
}
