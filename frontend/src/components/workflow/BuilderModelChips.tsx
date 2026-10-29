import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useShallow } from 'zustand/react/shallow'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Settings2 } from 'lucide-react'
import { Chip, ChoiceItems, type Choice } from '../../products/work/ComposerModelChips'
import ChatContextItems, { menuRowClass } from '../../products/work/ChatContextItems'
import { CursorModelPicker } from '../ui/CursorModelPicker'
import { useLLMStore } from '../../stores/useLLMStore'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useMobileViewport } from '../../hooks/useMobileViewport'
import { workflowManifestApi } from '../../services/api'
import { llmConfigService, type DynamicModelEntry, type ProviderConnection } from '../../services/llm-config-api'
import type { AgentLLMConfig, LLMProvider } from '../../services/api-types'
import type { LLMOption } from '../../types/llm'
import { installedCodingProviders, readyCodingProviders } from '../../utils/providerCatalogFilter'
import { getWorkflowLLMOptions, withWorkflowBuilderLLM, workflowBuilderLLM } from '../../utils/workflowLLMTierDefaults'
import { formatLLMOptions, llmOptionsKey } from '../../utils/llmConfigDisplay'
import { hasModelLimit, isModelAllowed } from '../../utils/allowedModels'
import { stripRetiredLLMFallbacks } from '../../utils/retiredLLMFallbacks'

function effortLabel(option: LLMOption): string {
  const value = formatLLMOptions(option.options).replace(/^(reasoning|thinking)\s+/i, '').replace(/_/g, ' ')
  return value ? value.charAt(0).toUpperCase() + value.slice(1) : 'Default'
}

/**
 * Model and reasoning chips for a Goal or Flow Builder chat, matching Crew's. The
 * choices are the ones the Models page offers for the Builder's coding agent and
 * account (only models that account allows this person). A pick saves the Builder
 * role of the workflow's model setting through the same manifest save as Models;
 * the server checks access and the account's models when the turn runs.
 */
export default function BuilderModelChips({ workspacePath, sessionId, label, fallback }: {
  workspacePath: string
  /** This Builder tab's chat; its Context group (PLAT-872-71) shows when the server offers one. */
  sessionId?: string
  label: string
  fallback: ReactNode
}) {
  const phone = useMobileViewport()
  const { availableLLMs, providerManifest, providerManifestLoaded, loadProviderManifest, llmConfigLocked } = useLLMStore(useShallow(state => ({
    availableLLMs: state.availableLLMs,
    providerManifest: state.providerManifest,
    providerManifestLoaded: state.providerManifestLoaded,
    loadProviderManifest: state.loadProviderManifest,
    llmConfigLocked: state.llmConfigLocked,
  })))
  const llmConfig = useWorkflowManifestStore(state => state.getWorkflowByPath(workspacePath)?.manifest?.capabilities?.llm_config)
  const [accounts, setAccounts] = useState<ProviderConnection[] | null>(null)
  const [cursorModels, setCursorModels] = useState<DynamicModelEntry[]>([])
  const [cursorOpen, setCursorOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => { if (!providerManifestLoaded) void loadProviderManifest() }, [providerManifestLoaded, loadProviderManifest])
  useEffect(() => {
    let cancelled = false
    const refresh = () => {
      void llmConfigService.getProviderConnections({ workspacePath }).then(records => {
        if (!cancelled) setAccounts(Array.isArray(records) ? records : [])
      }).catch(() => { if (!cancelled) setAccounts([]) })
    }
    refresh()
    window.addEventListener('provider-connections-changed', refresh)
    return () => { cancelled = true; window.removeEventListener('provider-connections-changed', refresh) }
  }, [workspacePath])

  const builder = useMemo(() => workflowBuilderLLM(llmConfig, providerManifest), [llmConfig, providerManifest])
  const provider = builder?.provider
  const accountId = builder?.connection_id || (provider ? `global:${provider}` : '')
  // Cursor's models come from the account itself, as on the Models page (PLAT-872).
  const cursorAccountId = provider === 'cursor-cli' ? accountId : ''
  useEffect(() => {
    let cancelled = false
    setCursorModels([])
    if (cursorAccountId) {
      void llmConfigService.getProviderModels('cursor-cli', false, false, { connection_id: cursorAccountId, workspace_path: workspacePath, product: '' }).then(result => {
        if (!cancelled) setCursorModels(result.selection_disabled ? [] : result.models || [])
      }).catch(() => undefined)
    }
    return () => { cancelled = true }
  }, [cursorAccountId, workspacePath])

  const allowed = accounts?.find(account => account.id === accountId)?.allowed_models
  const options = useMemo(() => {
    if (!provider || accounts === null) return []
    const ready = readyCodingProviders(installedCodingProviders(providerManifest), accounts)
    if (!ready.some(entry => entry.id === provider)) return []
    return getWorkflowLLMOptions(availableLLMs.filter(option => option.provider === provider), ready)
      .filter(option => option.provider === provider && (!hasModelLimit(allowed) || isModelAllowed(option.model, allowed)))
  }, [provider, accounts, providerManifest, availableLLMs, allowed])

  if (!builder || !llmConfig || llmConfigLocked) return <>{fallback}</>

  const save = async (next: AgentLLMConfig) => {
    if (saving) return
    setSaving(true)
    try {
      // Save on top of the full current settings, as the Models page does.
      const { manifest } = await workflowManifestApi.getWorkflowManifest(workspacePath)
      const current = manifest.capabilities?.llm_config ?? llmConfig
      const capabilities = { ...manifest.capabilities, llm_config: stripRetiredLLMFallbacks(withWorkflowBuilderLLM(current, { ...next, connection_id: builder.connection_id }, providerManifest)) }
      const response = await workflowManifestApi.updateWorkflowManifest({ workspace_path: workspacePath, capabilities })
      useWorkflowManifestStore.getState().replaceWorkflowManifest(workspacePath, response.manifest)
    } catch (cause) {
      const reason = (cause as { response?: { data?: string | { error?: string } } })?.response?.data
      const text = typeof reason === 'string' ? reason.trim() : reason?.error
      useChatStore.getState().addToast(`Could not change the model${text ? `: ${text}` : ''}. The Builder keeps its current model.`, 'error')
    } finally { setSaving(false) }
  }

  const pickOption = (option: LLMOption) => void save({
    ...(option.id ? { published_llm_id: option.id } : {}),
    provider: option.provider as LLMProvider,
    model_id: option.model,
    ...(option.options && Object.keys(option.options).length > 0 ? { options: option.options } : {}),
  })
  const currentKey = llmOptionsKey(builder.options)
  const models: Choice[] = []
  for (const option of options) {
    if (!models.some(model => model.id === option.model)) models.push({ id: option.model, label: option.label?.split(' · ')[0]?.trim() || option.model })
  }
  const variants = options.filter(option => option.model === builder.model_id)
    .filter((option, index, all) => all.findIndex(other => llmOptionsKey(other.options) === llmOptionsKey(option.options)) === index)
  const levels: Choice[] = variants.map(option => ({ id: llmOptionsKey(option.options), label: effortLabel(option) }))
  const currentLevel = levels.find(level => level.id === currentKey)
  const pickModel = (modelId: string) => {
    const choices = options.filter(option => option.model === modelId)
    const choice = choices.find(option => llmOptionsKey(option.options) === currentKey) ?? choices[0]
    if (choice && !(modelId === builder.model_id && llmOptionsKey(choice.options) === currentKey)) pickOption(choice)
  }
  const pickEffort = (key: string) => {
    const choice = variants.find(option => llmOptionsKey(option.options) === key)
    if (choice && key !== currentKey) pickOption(choice)
  }
  const modelLabel = models.find(model => model.id === builder.model_id)?.label || label
  const showModel = models.length > 1
  const showEffort = levels.length > 1 && Boolean(currentLevel)

  // This tab's Context group (PLAT-872-71), then the way to the full Models page, as in Crew's menu.
  const contextItems = sessionId ? <>
    <ChatContextItems sessionId={sessionId} modelLabel={modelLabel} />
    <Menu.Separator className="my-1 h-px bg-border first:hidden" />
    <Menu.Item className={menuRowClass} onSelect={() => useWorkflowStore.getState().openWorkspaceView('identity', 'llm')}>
      <Settings2 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Change model or account…
    </Menu.Item>
  </> : null
  const cursorChoices = provider === 'cursor-cli'
    ? cursorModels.filter(model => !hasModelLimit(allowed) || isModelAllowed(model.model_id, allowed)) : []
  if (provider === 'cursor-cli') {
    const cursorLabel = cursorChoices.find(model => model.model_id === builder.model_id)?.model_name || label
    if (cursorChoices.length < 2) return <>{fallback}</>
    return <Chip label={cursorLabel} ariaLabel={`Model: ${cursorLabel}`} disabled={saving} open={cursorOpen} onOpenChange={setCursorOpen} wide>
      {/* The menu's type-to-jump would take the search box's keys. */}
      <div onKeyDown={event => event.stopPropagation()}>
        <CursorModelPicker models={cursorChoices} selectedModelId={builder.model_id} onSelect={modelId => {
          setCursorOpen(false)
          if (modelId !== builder.model_id) void save({ provider: 'cursor-cli' as LLMProvider, model_id: modelId })
        }} />
      </div>
      {contextItems}
    </Chip>
  }

  if (phone) {
    if (!showModel && !showEffort && !sessionId) return <>{fallback}</>
    const combined = showEffort && currentLevel ? `${modelLabel} · ${currentLevel.label}` : modelLabel
    return <Chip label={combined} ariaLabel={`Model: ${combined}`} disabled={saving}>
      {showModel && <ChoiceItems title="Model" choices={models} value={builder.model_id} onPick={pickModel} />}
      {showModel && showEffort && <Menu.Separator className="my-1 h-px bg-border" />}
      {showEffort && <ChoiceItems title="Reasoning" choices={levels} value={currentKey} onPick={pickEffort} />}
      {contextItems}
    </Chip>
  }
  return <>
    {showModel || sessionId
      ? <Chip label={modelLabel} ariaLabel={`Model: ${modelLabel}`} disabled={saving}>{showModel && <ChoiceItems choices={models} value={builder.model_id} onPick={pickModel} />}{contextItems}</Chip>
      : fallback}
    {showEffort && currentLevel && <Chip label={currentLevel.label} ariaLabel={`Reasoning: ${currentLevel.label}`} disabled={saving}><ChoiceItems choices={levels} value={currentKey} onPick={pickEffort} /></Chip>}
  </>
}
