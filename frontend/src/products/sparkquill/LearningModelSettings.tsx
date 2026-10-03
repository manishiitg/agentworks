import { useEffect, useMemo, useState } from 'react'
import { TierModelSelector } from '../../components/ui/TierModelSelector'
import WorkflowLLMConfigurationPanel from '../../components/workflow/WorkflowLLMConfigurationPanel'
import type { LLMProvider, PresetLLMConfig } from '../../services/api-types'
import { llmConfigService, type ModelMetadata } from '../../services/llm-config-api'
import { useChatStore } from '../../stores/useChatStore'
import { buildAgentProfileEngineGroups, loadAgentProfileProviderOptions, modelReasoningLevels, type AgentProfileEngineGroup } from '../../utils/agentProfileCapabilities'
import { api } from './api'
import { applyFamilyEngineToOpenTabs, FAMILY_WORKSPACE, PARENT_PROFILE_ID } from './platform/PlatformChat'
import { CHILD_PROFILE_ID } from './platform/ChildPlatformChat'

type Role = 'parent' | 'child'
type Selection = { group: AgentProfileEngineGroup; model: string; effort: string }

export function LearningModelSettings({ engine, childName, onEngineChange }: { engine: string; childName: string; onEngineChange?: (engine: string) => void }) {
  const [selections, setSelections] = useState<Partial<Record<Role, Selection>>>({})
  const [groups, setGroups] = useState<AgentProfileEngineGroup[]>([])
  const [connectionId, setConnectionId] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [catalog, setCatalog] = useState<ModelMetadata[]>([])
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    void Promise.all([
      api.setup(),
      loadAgentProfileProviderOptions(PARENT_PROFILE_ID),
      loadAgentProfileProviderOptions(CHILD_PROFILE_ID),
      llmConfigService.getProviderManifest(),
    ]).then(async ([state, parentOptions, childOptions, manifest]) => {
      // Use the platform's model catalog, including models discovered by an installed CLI.
      const models = manifest.providers.flatMap(provider => provider.models || [])
      const dynamic = await Promise.all(manifest.providers.filter(provider => provider.runtime_available && provider.supports_dynamic_models)
        .map(async provider => ({ provider: provider.id, models: (await llmConfigService.getProviderModels(provider.id).catch(() => ({ models: [] }))).models })))
      for (const result of dynamic) for (const model of result.models) {
        if (!model.model_id || models.some(item => item.provider === result.provider && item.model_id === model.model_id)) continue
        models.push({ provider: result.provider, model_id: model.model_id, model_name: model.model_name, context_window: model.context_window || 0, input_cost_per_1m: 0, output_cost_per_1m: 0 })
      }
      if (cancelled) return
      setCatalog(models)
      setConnectionId(state?.connection_id)
      const next: Partial<Record<Role, Selection>> = {}
      const tabs = Object.values(useChatStore.getState().chatTabs)
      setGroups(buildAgentProfileEngineGroups(parentOptions, models))
      for (const role of ['parent', 'child'] as const) {
        const choices = buildAgentProfileEngineGroups(role === 'parent' ? parentOptions : childOptions, models)
        const selectedEngine = engine || state?.engine
        const group = selectedEngine ? choices.find(item => item.option.id === selectedEngine)
          : choices.find(item => item.option.default) || choices[0]
        if (!group?.models.length) continue
        const profile = role === 'parent' ? PARENT_PROFILE_ID : CHILD_PROFILE_ID
        const tab = tabs.find(item => item.metadata?.agentProfileId === profile && item.metadata.agentProfileEngine === group.option.id)
        const savedModel = (role === 'parent' ? state?.parent_model : state?.child_model) || tab?.metadata?.agentProfileModelID
        const model = group.models.some(item => item.id === savedModel) ? savedModel! : group.option.model_id || group.models[0].id
        const defaultEffort = group.option.options?.reasoning_effort
        const levels = modelReasoningLevels(group.option, models.find(item => item.model_id === model && item.provider === group.option.provider))
        const requestedEffort = (role === 'parent' ? state?.parent_reasoning_effort : state?.child_reasoning_effort) || tab?.metadata?.agentProfileReasoningEffort || (typeof defaultEffort === 'string' ? defaultEffort : '')
        next[role] = { group, model, effort: levels.find(item => item.id === requestedEffort)?.id || levels[0]?.id || '' }
      }
      setSelections(next)
    }).catch(() => { if (!cancelled) setError('Could not load AI settings. Please try again.') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [engine, revision])

  async function select(role: Role, model: string, effort?: string) {
    const current = selections[role]
    if (!current || saving) return
    setSaving(true); setError('')
    try {
      const metadata = catalog.find(item => item.model_id === model && item.provider === current.group.option.provider)
      const levels = modelReasoningLevels(current.group.option, metadata)
      const selectedEffort = levels.find(item => item.id === effort)?.id || levels[0]?.id || ''
      await api.selectEngine(role, current.group.option.id, model, undefined, selectedEffort)
      applyFamilyEngineToOpenTabs(role, current.group.option.id, model, selectedEffort)
      setSelections(previous => ({ ...previous, [role]: { ...current, model, effort: selectedEffort } }))
    } catch { setError('Could not save the model choice. Please try again.') }
    finally { setSaving(false) }
  }

  async function selectProvider(config: PresetLLMConfig) {
    const group = groups.find(item => item.option.provider === config.provider)
    if (!group || saving) return
    setSaving(true); setError('')
    try {
      const current = selections.parent
      const sameEngine = current?.group.option.id === group.option.id
      const model = sameEngine ? current.model : group.option.model_id || group.models[0]?.id || ''
      const effort = sameEngine ? current.effort : String(group.option.options?.reasoning_effort || '')
      const account = config.connection_id || `global:${config.provider}`
      await api.selectEngine('parent', group.option.id, model, account, effort)
      applyFamilyEngineToOpenTabs('parent', group.option.id, model, effort, account)
      setConnectionId(account)
      onEngineChange?.(group.option.id)
      setRevision(value => value + 1)
    } catch { setError('Could not save the provider account. Please try again.') }
    finally { setSaving(false) }
  }

  useEffect(() => {
    const changed = () => setRevision(value => value + 1)
    window.addEventListener('provider-connections-changed', changed)
    return () => window.removeEventListener('provider-connections-changed', changed)
  }, [])

  const providerIds = useMemo(() => groups.map(group => group.option.provider || group.option.id), [groups])
  const parent = selections.parent
  return (
    <section aria-label="Learning models" className="fl-settings-models fl-platform-ui">
      <p className="fl-drawer-label">AI provider and account</p>
      <p className="fl-note">Your chat and {childName || 'your child'}’s tutor use this account. Choose a separate model for each below.</p>
      {!loading && parent && <WorkflowLLMConfigurationPanel
        workspacePath={FAMILY_WORKSPACE} product="sparkquill" configurationSource="agent_profile" scopeNoun="family"
        canWriteOverride={!saving} splitPiProviders={false} showModelsPerRole={false}
        allowedProviderIds={providerIds}
        llmConfig={{ schema_version: 2, mode: 'provider_profile', provider: parent.group.option.provider as LLMProvider, connection_id: connectionId }}
        onChange={config => { void selectProvider(config) }}
      />}
      {loading && <p className="fl-note">Loading model choices…</p>}
      {(['parent', 'child'] as const).map(role => {
        const selection = selections[role]
        if (!selection || loading) return null
        const { group, model, effort } = selection
        const models = group.models.map(choice => catalog.find(item => item.provider === group.option.provider && item.model_id === choice.id) || {
          provider: group.option.provider || group.option.id, model_id: choice.id, model_name: choice.label, context_window: 0, input_cost_per_1m: 0, output_cost_per_1m: 0,
        })
        const levels = modelReasoningLevels(group.option, catalog.find(item => item.model_id === model && item.provider === group.option.provider))
        return <div key={role} role="group" aria-label={role === 'parent' ? 'Parent chat model' : 'Child tutor model'} className="mt-4 rounded-xl border border-border p-4">
          <p className="fl-drawer-label">{role === 'parent' ? 'Your chat' : `${childName || 'Your child'}’s tutor`}</p>
          <TierModelSelector models={models} selectedModelId={model} onSelect={id => { void select(role, id, effort) }} disabled={saving} className="mt-2" />
          {levels.length > 0 && <div role="group" aria-label="Reasoning effort" className="mt-3 flex flex-wrap items-center gap-2">
            <span className="fl-note">Reasoning effort</span>
            {levels.map(level => <button key={level.id} type="button" disabled={saving} aria-pressed={effort === level.id} className={`rounded-md border px-3 py-1.5 text-xs ${effort === level.id ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground'}`} onClick={() => { void select(role, model, level.id) }}>{level.label}</button>)}
          </div>}
        </div>
      })}
      {error && <p className="fl-note" role="alert">{error} <button type="button" onClick={() => setRevision(value => value + 1)}>Retry</button></p>}
    </section>
  )
}
