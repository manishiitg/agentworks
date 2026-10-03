import { useEffect, useState } from 'react'
import type { ModelMetadata } from '../../services/llm-config-api'
import ModelReasoningControl from '../../components/ui/ModelReasoningControl'
import { llmConfigService } from '../../services/llm-config-api'
import { useChatStore } from '../../stores/useChatStore'
import { buildAgentProfileEngineGroups, loadAgentProfileProviderOptions, modelReasoningLevels, type AgentProfileEngineGroup } from '../../utils/agentProfileCapabilities'
import { api } from './api'
import { applyFamilyEngineToOpenTabs, PARENT_PROFILE_ID } from './platform/PlatformChat'
import { CHILD_PROFILE_ID } from './platform/ChildPlatformChat'

type Role = 'parent' | 'child'
type Selection = { group: AgentProfileEngineGroup; model: string; effort: string }

export function LearningModelSettings({ engine, childName }: { engine: string; childName: string }) {
  const [selections, setSelections] = useState<Partial<Record<Role, Selection>>>({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [catalog, setCatalog] = useState<ModelMetadata[]>([])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setSelections({})
    setError('')
    void Promise.all([
      api.setup(),
      loadAgentProfileProviderOptions(PARENT_PROFILE_ID),
      loadAgentProfileProviderOptions(CHILD_PROFILE_ID),
      llmConfigService.getModelMetadata(),
    ]).then(([state, parentOptions, childOptions, catalog]) => {
      if (cancelled) return
      setCatalog(catalog.models || [])
      const next: Partial<Record<Role, Selection>> = {}
      const tabs = Object.values(useChatStore.getState().chatTabs)
      for (const role of ['parent', 'child'] as const) {
        const groups = buildAgentProfileEngineGroups(role === 'parent' ? parentOptions : childOptions, catalog.models || [])
        const selectedEngine = engine || state?.engine
        const group = selectedEngine ? groups.find(item => item.option.id === selectedEngine)
          : groups.find(item => item.option.default) || groups[0]
        if (!group?.models.length) continue
        const profile = role === 'parent' ? PARENT_PROFILE_ID : CHILD_PROFILE_ID
        const tab = tabs.find(item => item.metadata?.agentProfileId === profile && item.metadata.agentProfileEngine === group.option.id)
        const savedModel = (role === 'parent' ? state?.parent_model : state?.child_model) || tab?.metadata?.agentProfileModelID
        const model = group.models.some(item => item.id === savedModel) ? savedModel! : group.option.model_id || group.models[0].id
        const defaultEffort = group.option.options?.reasoning_effort
        const levels = modelReasoningLevels(group.option, catalog.models?.find(item => item.model_id === model && item.provider === group.option.provider))
        const requestedEffort = tab?.metadata?.agentProfileReasoningEffort || (typeof defaultEffort === 'string' ? defaultEffort : '')
        next[role] = {
          group, model,
          effort: levels.find(item => item.id === requestedEffort)?.id || levels[0]?.id || '',
        }
      }
      setSelections(next)
    }).catch(() => { if (!cancelled) setError('Could not load model choices. Close Settings and try again.') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [engine])

  async function select(role: Role, model: string, effort?: string) {
    const current = selections[role]
    if (!current || saving) return
    setSaving(true)
    setError('')
    try {
      const levels = modelReasoningLevels(current.group.option, catalog.find(item => item.model_id === model && item.provider === current.group.option.provider))
      const selectedEffort = levels.find(item => item.id === effort)?.id || levels[0]?.id || ''
      await api.selectEngine(role, current.group.option.id, model)
      applyFamilyEngineToOpenTabs(role, current.group.option.id, model, selectedEffort)
      setSelections(previous => ({ ...previous, [role]: { ...current, model, effort: selectedEffort } }))
    } catch {
      setError('Could not save the model choice. Please try again.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section aria-label="Learning models" className="fl-settings-models">
      <p className="fl-drawer-label" style={{ marginTop: '20px' }}>Models</p>
      <p className="fl-note">Choose a model for your chat and a separate one for {childName || 'your child'}’s tutor.</p>
      {loading && <p className="fl-note">Loading model choices…</p>}
      {(['parent', 'child'] as const).map(role => {
        const selection = selections[role]
        if (!selection) return null
        const { group, model, effort } = selection
        const defaultEffort = group.option.options?.reasoning_effort
        return (
          <div key={role} role="group" aria-label={role === 'parent' ? 'Parent chat model' : 'Child tutor model'} className="fl-settings-model-row">
            <span>{role === 'parent' ? 'Your chat' : `${childName || 'Your child'}’s tutor`}</span>
            <ModelReasoningControl
              engines={[{ id: group.option.id, label: group.option.label || group.option.id, models: group.models }]}
              currentEngineId={group.option.id}
              currentModelId={model}
              engineChangeable
              reasoningLevels={modelReasoningLevels(group.option, catalog.find(item => item.model_id === model && item.provider === group.option.provider))}
              currentReasoningEffort={effort}
              defaultReasoningEffort={typeof defaultEffort === 'string' ? defaultEffort : undefined}
              onSelect={(_, modelId, reasoningEffort) => { void select(role, modelId, reasoningEffort) }}
              disabled={saving}
            />
          </div>
        )
      })}
      {!loading && !error && !Object.keys(selections).length && <p className="fl-note">No model choices are available for this AI yet.</p>}
      {error && <p className="fl-note" role="alert">{error}</p>}
    </section>
  )
}
