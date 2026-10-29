import { useEffect, useRef, useState, type ReactNode } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { BarChart3, Check, ChevronDown, Settings2 } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { useMobileViewport } from '../../hooks/useMobileViewport'
import { workChatModelsQueryKey } from './useWorkChatModels'
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
 * Model and reasoning chips for a Crew/Code chat's composer. The choices are the
 * ones the Models page offers for this chat's current coding agent; the server
 * saves them for this chat and checks them. Only shown when the server lets this
 * person open Models (the project's owner); otherwise `fallback` (the plain label).
 */
export default function ComposerModelChips({ tabId, workspacePath, label, fallback }: {
  tabId: string
  workspacePath: string
  label: string
  fallback: ReactNode
}) {
  const queryClient = useQueryClient()
  const phone = useMobileViewport()
  const meta = useChatStore(state => state.chatTabs[tabId]?.metadata)
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId) || ''
  const [saving, setSaving] = useState(false)
  const [cursorOpen, setCursorOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const usesCursorPicker = useRef(false)
  useOpenFromStatusBar(sessionId, () => (usesCursorPicker.current ? setCursorOpen : setMenuOpen)(true))
  const profileId = meta?.agentProfileId || ''
  const projectId = meta?.agentProfileProjectId || ''
  const key = meta?.agentProfileConversationKey
  const conversationKey = key && (key === projectId || key.startsWith(`${projectId}:chat:`)) ? key : meta?.agentProfileIsMainChat ? projectId : ''
  const enabled = Boolean(meta?.agentProfileCanOpenModels && !meta.isViewOnly && projectId && conversationKey && meta.agentProfileEngine)
  const sources = useWorkModelSources({ profileId, profileVersion: meta?.agentProfileVersion, accountProduct: profileId, workspacePath, enabled })
  const selectedOption = enabled ? sources.options.find(option => option.id === meta?.agentProfileEngine) : undefined
  const connectionId = meta?.agentProfileConnectionID || undefined
  const { selectableModels, cursorModels, cursorSelectionDisabled, defaultForOption, levelsForModel, effortForModel, effortForPick } = useWorkModelChoices(sources, {
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
  const showModel = models.length > 1
  const showEffort = levels.length > 1

  const save = async (modelId: string, reasoningEffort: string | undefined) => {
    if (saving || (modelId === currentModel && reasoningEffort === currentEffort)) return
    setSaving(true)
    try {
      await agentApi.setAgentProfileChatModel(profileId, projectId, {
        conversation_key: conversationKey, engine: selectedOption.id, model_id: modelId, reasoning_effort: reasoningEffort, connection_id: connectionId,
      })
      await queryClient.invalidateQueries({ queryKey: workChatModelsQueryKey(profileId, projectId) })
    } catch (cause) {
      const reason = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
      useChatStore.getState().addToast(`Could not change the model${reason ? `: ${reason}` : ''}. This chat keeps its current model.`, 'error')
    } finally { setSaving(false) }
  }
  const pickModel = (modelId: string) => void save(modelId, effortForPick(modelId, currentEffort))
  const pickEffort = (effort: string) => void save(currentModel, effort)
  // Quick ways out of the menu: the full Models page (provider and account) and this project's usage.
  const openModels = () => window.dispatchEvent(new CustomEvent('agentworks:open-chat-models', { detail: { projectId, conversationKey } }))
  const openUsage = () => window.dispatchEvent(new CustomEvent('agentworks:open-chat-usage', { detail: { projectId } }))
  // This chat's Context group (PLAT-872-71): meter, plan limits, auto-compact, compact now, fresh chat.
  const contextItems = sessionId ? <ChatContextItems sessionId={sessionId} modelLabel={label} /> : null
  const footer = <>
    {contextItems}
    <Menu.Separator className="my-1 h-px bg-border" />
    <Menu.Item className={rowClass} onSelect={openModels}><Settings2 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Change provider or account…</Menu.Item>
    <Menu.Item className={rowClass} onSelect={openUsage}><BarChart3 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Usage</Menu.Item>
  </>
  // Cursor lists every variant of a model as its own entry; show them the way the Models page does:
  // by company, one row per model, variants as chips, older versions folded away.
  const cursorChoices = selectedOption.provider === 'cursor-cli' && !cursorSelectionDisabled
    ? cursorModels.filter(model => models.some(choice => choice.id === model.model_id)) : []
  usesCursorPicker.current = cursorChoices.length > 1
  const cursorPicker = cursorChoices.length > 1 && <Chip label={label} ariaLabel={`Model: ${label}`} disabled={saving} open={cursorOpen} onOpenChange={setCursorOpen} wide>
    {/* The menu's type-to-jump would take the search box's keys. */}
    <div onKeyDown={event => event.stopPropagation()}>
      <CursorModelPicker models={cursorChoices} selectedModelId={currentModel} onSelect={modelId => { setCursorOpen(false); pickModel(modelId) }} />
    </div>
    {footer}
  </Chip>

  if (phone && cursorPicker) return <>{cursorPicker}{showEffort && effortLabel && <Chip label={effortLabel} ariaLabel={`Reasoning: ${effortLabel}`} disabled={saving}><ChoiceItems choices={levels} value={currentEffort} onPick={pickEffort} /></Chip>}</>
  if (phone) {
    if (!showModel && !showEffort && !sessionId) return <>{fallback}</>
    const combined = effortLabel ? `${label} · ${effortLabel}` : label
    return <Chip label={combined} ariaLabel={`Model: ${combined}`} disabled={saving} open={menuOpen} onOpenChange={setMenuOpen}>
      {showModel && <ChoiceItems title="Model" choices={models} value={currentModel} onPick={pickModel} />}
      {showModel && showEffort && <Menu.Separator className="my-1 h-px bg-border" />}
      {showEffort && <ChoiceItems title="Reasoning" choices={levels} value={currentEffort} onPick={pickEffort} />}
      {footer}
    </Chip>
  }
  return <>
    {cursorPicker || (showModel || sessionId
      ? <Chip label={label} ariaLabel={`Model: ${label}`} disabled={saving} open={menuOpen} onOpenChange={setMenuOpen}>{showModel && <ChoiceItems choices={models} value={currentModel} onPick={pickModel} />}{footer}</Chip>
      : fallback)}
    {showEffort && effortLabel && <Chip label={effortLabel} ariaLabel={`Reasoning: ${effortLabel}`} disabled={saving}><ChoiceItems choices={levels} value={currentEffort} onPick={pickEffort} /></Chip>}
  </>
}
