import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { BarChart3, Check, ChevronDown, ChevronRight, Settings2 } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { workChatModelsQueryKey } from './useWorkChatModels'
import type { AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'
import { CursorModelPicker } from '../../components/ui/CursorModelPicker'
import { ownDefaultConnectionId, useWorkModelChoices, useWorkModelSources } from './useWorkModelChoices'
import ChatContextItems, { menuLabelClass, menuRowClass, openChatContextMenuEvent } from './ChatContextItems'

const chipClass = 'inline-flex max-w-40 items-center gap-0.5 rounded px-1 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 data-[state=open]:bg-muted'
const rowClass = menuRowClass
const labelClass = menuLabelClass
const contentClass = 'z-[100] max-h-[min(360px,var(--radix-dropdown-menu-content-available-height))] w-[min(260px,calc(100vw-24px))] overflow-y-auto overscroll-contain rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg'

export type Choice = { id: string; label: string }

export function ChoiceItems({ title, choices, value, onPick }: { title?: string; choices: Choice[]; value?: string; onPick: (id: string) => void }) {
  return <>
    {title && <Menu.Label className={labelClass}>{title}</Menu.Label>}
    <Menu.RadioGroup value={value} onValueChange={onPick}>
      {choices.map(choice => <Menu.RadioItem key={choice.id} value={choice.id} className={rowClass}>
        <span className="min-w-0 flex-1 truncate">{choice.label}</span>
        <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
      </Menu.RadioItem>)}
    </Menu.RadioGroup>
  </>
}

export function Chip({ label, ariaLabel, disabled, children, open, onOpenChange, wide }: { label: string; ariaLabel: string; disabled: boolean; children: ReactNode; open?: boolean; onOpenChange?: (open: boolean) => void; wide?: boolean }) {
  return <Menu.Root modal={false} open={open} onOpenChange={onOpenChange}>
    <Menu.Trigger type="button" disabled={disabled} aria-label={ariaLabel} title={ariaLabel} className={chipClass}>
      <span className="min-w-0 truncate">{label}</span>
      <ChevronDown className="h-3 w-3 shrink-0" aria-hidden="true" />
    </Menu.Trigger>
    <Menu.Portal><Menu.Content side="top" align="start" sideOffset={8} collisionPadding={12} className={wide ? `${contentClass} w-[min(340px,calc(100vw-24px))]` : contentClass}>{children}</Menu.Content></Menu.Portal>
  </Menu.Root>
}

/** Opens this chat's model menu when its usage chip in the chat box is clicked (ChatStatusBar). */
export function useOpenFromStatusBar(sessionId: string | undefined, open: () => void) {
  const openRef = useRef(open)
  openRef.current = open
  useEffect(() => {
    if (!sessionId) return
    const listener = (event: Event) => {
      if ((event as CustomEvent<{ sessionId?: string }>).detail?.sessionId === sessionId) openRef.current()
    }
    window.addEventListener(openChatContextMenuEvent, listener)
    return () => window.removeEventListener(openChatContextMenuEvent, listener)
  }, [sessionId])
}

/**
 * The model switcher in a Crew/Code chat's composer: one chip ("Sonnet 5.5 · High") opening Model, with every
 * coding agent this person can run here and its models, and Effort. The choices are the ones the Models page
 * offers; the server saves them for this chat and checks them. Only shown when the server lets this person open
 * Models (the project's owner); otherwise `fallback` (the plain label).
 */
export default function ComposerModelChips({ tabId, workspacePath, label, fallback }: {
  tabId: string
  workspacePath: string
  label: string
  fallback: ReactNode
}) {
  const queryClient = useQueryClient()
  const meta = useChatStore(state => state.chatTabs[tabId]?.metadata)
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId) || ''
  const [saving, setSaving] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  useOpenFromStatusBar(sessionId, () => setMenuOpen(true))
  const profileId = meta?.agentProfileId || ''
  const projectId = meta?.agentProfileProjectId || ''
  const key = meta?.agentProfileConversationKey
  const conversationKey = key && (key === projectId || key.startsWith(`${projectId}:chat:`)) ? key : meta?.agentProfileIsMainChat ? projectId : ''
  const enabled = Boolean(meta?.agentProfileCanOpenModels && !meta.isViewOnly && projectId && conversationKey && meta.agentProfileEngine)
  const sources = useWorkModelSources({ profileId, profileVersion: meta?.agentProfileVersion, accountProduct: profileId, workspacePath, enabled })
  const selectedOption = enabled ? sources.options.find(option => option.id === meta?.agentProfileEngine) : undefined
  const connectionId = meta?.agentProfileConnectionID || undefined
  const { workProviderIds, selectableModels, modelsForOption, cursorModels, cursorSelectionDisabled, defaultForOption, levelsForModel, effortForModel, effortForPick } = useWorkModelChoices(sources, {
    selectedOption, selectedConnectionId: connectionId,
    inUseConnectionId: connectionId || ownDefaultConnectionId(sources.accounts, selectedOption?.provider),
    workspacePath, accountProduct: profileId,
  })

  if (!selectedOption) return <>{fallback}</>
  const models: Choice[] = cursorSelectionDisabled ? [] : selectableModels.map(model => ({ id: model.model_id, label: model.model_name || model.model_id }))
  const currentModel = meta?.agentProfileModelID || defaultForOption(selectedOption, connectionId).modelId
  const levels = levelsForModel(currentModel)
  const currentEffort = effortForModel(currentModel, meta?.agentProfileReasoningEffort)
  const effortLabel = levels.find(level => level.id === currentEffort)?.label
  const modelLabel = models.find(model => model.id === currentModel)?.label || label
  // Every coding agent this person can run here, the chat's own first; Cursor and key accounts list
  // their models per account, so another one of those is one row that switches to its default model.
  const groups = [selectedOption, ...sources.options.filter(option => option.id !== selectedOption.id && workProviderIds.includes(option.provider || option.id))]
    .map(option => ({ option, current: option.id === selectedOption.id, models: option.id === selectedOption.id ? models : modelsForOption(option).map(model => ({ id: model.model_id, label: model.model_name || model.model_id })) }))
    .filter(group => group.current || group.models.length > 0 || group.option.provider === 'cursor-cli' || group.option.provider === 'pi-cli')

  const save = async (option: AgentProfileProviderOption, modelId: string, reasoningEffort: string | undefined) => {
    const current = option.id === selectedOption.id
    if (saving || (current && modelId === currentModel && reasoningEffort === currentEffort)) return
    setSaving(true)
    try {
      await agentApi.setAgentProfileChatModel(profileId, projectId, {
        // Another coding agent runs on the account the server picks for it (the person's own, else the server's).
        conversation_key: conversationKey, engine: option.id, model_id: modelId, reasoning_effort: reasoningEffort, connection_id: current ? connectionId : undefined,
      })
      await queryClient.invalidateQueries({ queryKey: workChatModelsQueryKey(profileId, projectId) })
    } catch (cause) {
      const reason = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
      useChatStore.getState().addToast(`Could not change the model${reason ? `: ${reason}` : ''}. This chat keeps its current model.`, 'error')
    } finally { setSaving(false) }
  }
  const pickModel = (option: AgentProfileProviderOption, modelId?: string) => {
    if (option.id === selectedOption.id) return void save(option, modelId || currentModel, effortForPick(modelId || currentModel, currentEffort))
    const defaults = defaultForOption(option)
    const model = modelId || defaults.modelId
    void save(option, model, effortForPick(model, defaults.reasoningEffort, option))
  }
  const pickEffort = (effort: string) => void save(selectedOption, currentModel, effort)
  // Quick ways out of the menu: the full Models page (provider and account) and this project's usage.
  const openModels = () => window.dispatchEvent(new CustomEvent('agentworks:open-chat-models', { detail: { projectId, conversationKey } }))
  const openUsage = () => window.dispatchEvent(new CustomEvent('agentworks:open-chat-usage', { detail: { projectId } }))
  // Cursor lists every variant of a model as its own entry; show them the way the Models page does:
  // by company, one row per model, variants as chips, older versions folded away.
  const cursorChoices = selectedOption.provider === 'cursor-cli' && !cursorSelectionDisabled
    ? cursorModels.filter(model => models.some(choice => choice.id === model.model_id)) : []
  const separator = <Menu.Separator className="my-1 h-px bg-border" />
  const subContentClass = cursorChoices.length > 1 ? `${contentClass} w-[min(340px,calc(100vw-24px))]` : contentClass
  const subRow = (title: string, value: string) => <Menu.SubTrigger className={rowClass}>
    <span className="flex-1">{title}</span>
    <span className="min-w-0 max-w-32 truncate text-muted-foreground">{value}</span>
    <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
  </Menu.SubTrigger>

  if (groups.length < 2 && models.length < 2 && levels.length < 2 && !sessionId) return <>{fallback}</>
  const chipLabel = effortLabel ? `${modelLabel} · ${effortLabel}` : modelLabel
  return <Chip label={chipLabel} ariaLabel={`Model: ${chipLabel}`} disabled={saving} open={menuOpen} onOpenChange={setMenuOpen}>
    <Menu.Sub>
      {subRow('Model', modelLabel)}
      <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} className={subContentClass}>
        {groups.map((group, index) => <Fragment key={group.option.id}>
          {index > 0 && separator}
          <Menu.Label className={labelClass}>{group.option.label || group.option.provider || group.option.id}</Menu.Label>
          {group.current && cursorChoices.length > 1
            // The menu's type-to-jump would take the search box's keys.
            ? <div onKeyDown={event => event.stopPropagation()}>
              <CursorModelPicker models={cursorChoices} selectedModelId={currentModel} onSelect={modelId => { setMenuOpen(false); pickModel(selectedOption, modelId) }} />
            </div>
            : group.models.length > 0
              ? <ChoiceItems choices={group.models} value={group.current ? currentModel : ''} onPick={modelId => pickModel(group.option, modelId)} />
              : <Menu.Item className={rowClass} disabled={group.current} onSelect={() => pickModel(group.option)}>
                <span className="min-w-0 flex-1 truncate">{group.current ? modelLabel : `Use ${group.option.label || group.option.provider}`}</span>
                {group.current && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
              </Menu.Item>}
        </Fragment>)}
        {separator}
        <Menu.Item className={rowClass} onSelect={openModels}><Settings2 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Manage accounts</Menu.Item>
      </Menu.SubContent></Menu.Portal>
    </Menu.Sub>
    {levels.length > 1 && effortLabel && <Menu.Sub>
      {subRow('Effort', effortLabel)}
      <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} className={contentClass}>
        <ChoiceItems choices={levels} value={currentEffort} onPick={pickEffort} />
      </Menu.SubContent></Menu.Portal>
    </Menu.Sub>}
    {/* This chat's Context group (PLAT-872-71): meter, plan limits, auto-compact, compact now, fresh chat. */}
    {sessionId && <ChatContextItems sessionId={sessionId} modelLabel={label} />}
    {separator}
    <Menu.Item className={rowClass} onSelect={openUsage}><BarChart3 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Usage</Menu.Item>
  </Chip>
}
