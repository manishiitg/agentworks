import { accountConfigured, accountOptionLabel, accountRelation, accountUsable, NO_LONGER_AVAILABLE } from '../../components/providers/ProviderAccounts'
import { ComposerActionItems, type ComposerActions } from '../../components/chat/ComposerActionsMenu'
import { ComposerSendKeys } from '../../components/chat/ChatSendModeButton'
import ChatStatusBar from './ChatStatusBar'
import { formatTokens } from '../../utils/formatTokens'
import { ThinkingDisplayMenuItem } from '../../components/chat/ThinkingDisplayMenuItem'
import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ArrowRight, BarChart3, Check, ChevronDown, ChevronRight, Laptop, Loader2, Server, Settings2 } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { workChatModelsQueryKey } from './useWorkChatModels'
import type { AgentProfileProviderOption } from '../../utils/agentProfileCapabilities'
import { CursorModelPicker } from '../../components/ui/CursorModelPicker'
import { SharedTokenUsageItems } from '../../components/providers/SharedTokenUsageNotice'
import { sharedAccountProvider } from '../../utils/tokenLimits'
import { ownDefaultConnectionId, useWorkModelChoices, useWorkModelSources } from './useWorkModelChoices'
import ClaudeTerminalToggle from './ClaudeTerminalToggle'
import ChatContextItems, { changeChatContext, menuLabelClass, menuRowClass, openChatContextMenuEvent } from './ChatContextItems'

const chipClass = 'inline-flex max-w-40 items-center gap-0.5 rounded px-1 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 data-[state=open]:bg-muted'
const rowClass = menuRowClass
const labelClass = menuLabelClass
const contentClass = 'z-[100] max-h-[min(360px,var(--radix-dropdown-menu-content-available-height))] w-[min(260px,calc(100vw-24px))] overflow-y-auto overscroll-contain rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg'

export type Choice = { id: string; label: string }

export function ChoiceItems({ title, choices, value, onPick, keepOpenOnPick = false }: { title?: string; choices: Choice[]; value?: string; onPick: (id: string) => void; keepOpenOnPick?: boolean }) {
  return <>
    {title && <Menu.Label className={labelClass}>{title}</Menu.Label>}
    <Menu.RadioGroup value={value} onValueChange={onPick}>
      {choices.map(choice => <Menu.RadioItem key={choice.id} value={choice.id} className={rowClass} onSelect={keepOpenOnPick ? event => event.preventDefault() : undefined}>
        <span className="min-w-0 flex-1 truncate">{choice.label}</span>
        <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
      </Menu.RadioItem>)}
    </Menu.RadioGroup>
  </>
}

export function Chip({ label, ariaLabel, disabled, children, open, onOpenChange, wide, columns, tag, triggerExtra, onCloseAutoFocus }: { label: string; ariaLabel: string; disabled: boolean; children: ReactNode; open?: boolean; onOpenChange?: (open: boolean) => void; wide?: boolean; columns?: boolean; tag?: 'mac' | 'server'; triggerExtra?: ReactNode; onCloseAutoFocus?: (event: Event) => void }) {
  const width = columns ? ' w-[min(560px,calc(100vw-24px))] max-h-[min(480px,var(--radix-dropdown-menu-content-available-height))]' : wide ? ' w-[min(340px,calc(100vw-24px))]' : ''
  return <Menu.Root modal={false} open={open} onOpenChange={onOpenChange}>
    <Menu.Trigger type="button" disabled={disabled} aria-label={ariaLabel} title={ariaLabel} className={triggerExtra ? chipClass.replace('max-w-40', 'max-w-full') : chipClass}>
      {tag === 'mac' && <Laptop className="mr-1 h-3 w-3 shrink-0" aria-hidden="true" />}
      {tag === 'server' && <Server className="mr-1 h-3 w-3 shrink-0" aria-hidden="true" />}
      <span className="min-w-0 max-w-40 truncate">{label}</span>
      {triggerExtra}
      <ChevronDown className="h-3 w-3 shrink-0" aria-hidden="true" />
    </Menu.Trigger>
    <Menu.Portal><Menu.Content onCloseAutoFocus={onCloseAutoFocus} side="top" align="start" sideOffset={8} collisionPadding={12} className={`${contentClass}${width}`}>{children}</Menu.Content></Menu.Portal>
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
export default function ComposerModelChips({ tabId, workspacePath, label, fallback, actions, showContext, busy = false }: {
  tabId: string
  workspacePath: string
  label: string
  fallback: ReactNode
  actions?: ComposerActions
  showContext?: boolean
  busy?: boolean
}) {
  const afterClose = useRef<(() => void) | null>(null)
  const queryClient = useQueryClient()
  const meta = useChatStore(state => state.chatTabs[tabId]?.metadata)
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId) || ''
  const [saving, setSaving] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  // This chat's switch note, read when the menu opens, and a model picked in a long chat waiting for the choice.
  const [switchNote, setSwitchNote] = useState('')
  const [contextTokens, setContextTokens] = useState(0)
  const [pendingSwitch, setPendingSwitch] = useState<{ option: AgentProfileProviderOption; modelId?: string; connectionId?: string } | null>(null)
  useEffect(() => {
    if (!menuOpen || !sessionId) return
    let active = true
    agentApi.getChatContext(sessionId).then(view => { if (active) { setSwitchNote(view?.switch_note || ''); setContextTokens(view?.used_tokens || 0) } }).catch(() => undefined)
    return () => { active = false }
  }, [menuOpen, sessionId])
  const confirmSwitchRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (pendingSwitch) confirmSwitchRef.current?.focus()
  }, [pendingSwitch])
  useOpenFromStatusBar(sessionId, () => setMenuOpen(true))
  const profileId = meta?.agentProfileId || ''
  const projectId = meta?.agentProfileProjectId || ''
  const key = meta?.agentProfileConversationKey
  const conversationKey = key && (key === projectId || key.startsWith(`${projectId}:chat:`)) ? key : meta?.agentProfileIsMainChat ? projectId : ''
  const enabled = Boolean(meta?.agentProfileCanOpenModels && !meta.isViewOnly && projectId && conversationKey && meta.agentProfileEngine)
  const sources = useWorkModelSources({ profileId, profileVersion: meta?.agentProfileVersion, accountProduct: profileId, workspacePath, enabled })
  const selectedOption = enabled ? sources.options.find(option => option.id === meta?.agentProfileEngine) : undefined
  const connectionId = meta?.agentProfileConnectionID || undefined
  // A company account from a connected server counts on that server: its limits come from there.
  const companyServer = sources.accounts?.find(account => account.id === (connectionId || meta?.agentProfileChatConnectionID))?.via_connection
  const { workProviderIds, selectableModels, switchAccountFor, cursorModels, cursorSelectionDisabled, defaultForOption, levelsForModel, effortForModel, effortForPick } = useWorkModelChoices(sources, {
    selectedOption, selectedConnectionId: connectionId,
    inUseConnectionId: connectionId || ownDefaultConnectionId(sources.accounts, selectedOption?.provider),
    workspacePath, accountProduct: profileId,
  })

  const onMenuOpen = (open: boolean) => { setMenuOpen(open); if (!open) setPendingSwitch(null); actions?.onOpenChange?.(open) }
  const onMenuClose = (event: Event) => { const action = afterClose.current; afterClose.current = null; if (action) { event.preventDefault(); action() } }
  const triggerExtra = <>
    {showContext && sessionId && <span className="ml-2 flex min-w-0"><ChatStatusBar sessionId={sessionId} busy={busy} inline /></span>}
    {actions?.uploading && <><Loader2 className="ml-1 h-3 w-3 shrink-0 animate-spin" aria-hidden="true" /><span className="sr-only">Uploading files</span></>}
    {Boolean(actions?.sendOptions?.backgroundWork?.items.length) && <span className="ml-1 h-1.5 w-1.5 shrink-0 rounded-full bg-blue-500" aria-label="Background tasks running" role="status" />}
  </>
  // Attach and commands stay available while model choices are loading.
  const fallbackMenu = actions ? <Chip label={label} ariaLabel="Chat menu" disabled={false} open={menuOpen} onOpenChange={onMenuOpen} onCloseAutoFocus={onMenuClose} triggerExtra={triggerExtra}>
    <ComposerActionItems {...actions} deferAction={action => { afterClose.current = action }} />
    <ComposerSendKeys busy={busy} />
  </Chip> : fallback
  if (!selectedOption) return <>{fallbackMenu}</>
  // Where this chat's model runs, shown only when there is a choice: this program has both an account on
  // this Mac and a company account from a connected server.
  const programAccounts = (sources.accounts || []).filter(account => account.provider === selectedOption.provider)
  const inUseAccount = programAccounts.find(account => account.id === (connectionId || ownDefaultConnectionId(sources.accounts, selectedOption.provider)))
  const sourceTag = inUseAccount && programAccounts.some(account => account.via_connection) && programAccounts.some(account => !account.via_connection)
    ? inUseAccount.via_connection ? (inUseAccount.display_name.split(' · ')[0]?.trim() || 'Server') : 'Mac'
    : undefined
  const models: Choice[] = cursorSelectionDisabled ? [] : selectableModels.map(model => ({ id: model.model_id, label: model.model_name || model.model_id }))
  const currentModel = meta?.agentProfileModelID || defaultForOption(selectedOption, connectionId).modelId
  const levels = levelsForModel(currentModel)
  const currentEffort = effortForModel(currentModel, meta?.agentProfileReasoningEffort)
  const effortLabel = levels.find(level => level.id === currentEffort)?.label
  const modelLabel = models.find(model => model.id === currentModel)?.label || label
  const currentAccountId = connectionId || meta?.agentProfileChatConnectionID || ownDefaultConnectionId(sources.accounts, selectedOption.provider) || `global:${selectedOption.provider}`
  const groups = [{ option: selectedOption, current: true, models }]
  const cliGroups = sources.options.filter(option => option.id === selectedOption.id || workProviderIds.includes(option.provider || option.id)).map(option => ({
    option,
    accounts: (sources.accounts || []).filter(account => account.provider === option.provider &&
      ((accountUsable(account) && accountConfigured(account)) || (option.id === selectedOption.id && account.id === currentAccountId))),
  })).filter(group => group.accounts.length > 0 || group.option.id === selectedOption.id)
  const onThisMac = window.electronAPI?.getDesktopConnection?.() === 'local'

  const save = async (option: AgentProfileProviderOption, modelId: string, reasoningEffort: string | undefined, targetConnectionId?: string) => {
    const current = option.id === selectedOption.id
    if (saving || (current && modelId === currentModel && reasoningEffort === currentEffort && (!targetConnectionId || targetConnectionId === currentAccountId))) return
    setSaving(true)
    try {
      await agentApi.setAgentProfileChatModel(profileId, projectId, {
        conversation_key: conversationKey, engine: option.id, model_id: modelId, reasoning_effort: reasoningEffort, connection_id: targetConnectionId || (current ? connectionId : switchAccountFor(option)),
      })
      await queryClient.invalidateQueries({ queryKey: workChatModelsQueryKey(profileId, projectId) })
    } catch (cause) {
      const reason = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
      useChatStore.getState().addToast(`Could not change the model${reason ? `: ${reason}` : ''}. This chat keeps its current model.`, 'error')
    } finally { setSaving(false) }
  }
  const switchTo = (option: AgentProfileProviderOption, modelId?: string, targetConnectionId?: string) => {
    if (option.id === selectedOption.id && (!targetConnectionId || targetConnectionId === currentAccountId)) return void save(option, modelId || currentModel, effortForPick(modelId || currentModel, currentEffort))
    const defaults = defaultForOption(option, targetConnectionId || switchAccountFor(option))
    const model = modelId || defaults.modelId
    void save(option, model, effortForPick(model, defaults.reasoningEffort, option), targetConnectionId)
  }
  // The server offers compaction for a long chat within one CLI. Across CLIs,
  // confirm the handoff without compacting the old CLI: recent context and archive access carry over.
  const pickModel = (option: AgentProfileProviderOption, modelId?: string, targetConnectionId?: string) => {
    const changes = option.id !== selectedOption.id || (modelId !== undefined && modelId !== currentModel) || Boolean(targetConnectionId && targetConnectionId !== currentAccountId)
    if (!changes || (option.id === selectedOption.id && !switchNote)) { switchTo(option, modelId, targetConnectionId); return false }
    setPendingSwitch({ option, modelId, connectionId: targetConnectionId })
    return true
  }
  const finishSwitch = async (compactFirst: boolean) => {
    const pending = pendingSwitch
    if (!pending) return
    setPendingSwitch(null)
    setMenuOpen(false)
    if (compactFirst && !(await changeChatContext(sessionId, { action: 'compact' }))) return
    switchTo(pending.option, pending.modelId, pending.connectionId)
  }
  const pickEffort = (effort: string) => void save(selectedOption, currentModel, effort)
  // Optional setup and defaults on the Models page and this project's usage.
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

  if (workProviderIds.length < 2 && groups.length < 2 && models.length < 2 && levels.length < 2 && !sessionId) return <>{fallbackMenu}</>
  const chipLabel = effortLabel ? `${modelLabel} · ${effortLabel}` : modelLabel
  // A chat's menu has two columns: model and links on the left, this chat's Context on the right.
  const sourceText = sourceTag ? (sourceTag === 'Mac' ? 'Runs on this Mac' : `Runs on ${sourceTag} (company account)`) : ''
  const changesCLI = Boolean(pendingSwitch && pendingSwitch.option.id !== selectedOption.id)
  const pendingCLI = pendingSwitch?.option.label || pendingSwitch?.option.provider || ''
  const currentCLI = selectedOption.label || selectedOption.provider || ''
  const pendingKeepsAccount = !pendingSwitch?.connectionId || pendingSwitch.connectionId === currentAccountId
  const pendingModelID = pendingSwitch ? pendingSwitch.modelId || (!changesCLI && pendingKeepsAccount
    ? currentModel : defaultForOption(pendingSwitch.option, pendingSwitch.connectionId || switchAccountFor(pendingSwitch.option)).modelId) : ''
  const pendingModelLabel = models.find(model => model.id === pendingModelID)?.label
    || sources.providerManifest.find(provider => provider.id === pendingSwitch?.option.provider)?.models?.find(model => model.model_id === pendingModelID)?.model_name
    || pendingModelID || 'Default model'
  const pendingAccount = sources.accounts?.find(account => account.id === (pendingSwitch?.connectionId || currentAccountId))
  const pendingAccountLabel = pendingAccount ? pendingAccount.via_connection ? pendingAccount.display_name
    : onThisMac && accountRelation(pendingAccount) === 'server' ? 'This Mac' : accountOptionLabel(pendingAccount) : ''
  const switchActionClass = 'flex cursor-pointer items-center rounded-md border px-3 py-2.5 text-left text-[13px] font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <>{sessionId && <ClaudeTerminalToggle sessionId={sessionId} modelKey={label} />}<Chip label={chipLabel} ariaLabel={`Model: ${chipLabel}${sourceText ? `. ${sourceText}` : ''}`} disabled={saving} open={menuOpen} onOpenChange={onMenuOpen} onCloseAutoFocus={onMenuClose}
    triggerExtra={triggerExtra} wide={Boolean(pendingSwitch)} columns={Boolean(sessionId) && !pendingSwitch} tag={sourceTag ? (sourceTag === 'Mac' ? 'mac' : 'server') : undefined}>
    {pendingSwitch ? <div className="min-w-0 p-2.5" aria-label="Finish changing the model">
      <Menu.Label className="mb-1 text-base font-semibold leading-snug">Switch to {changesCLI ? pendingCLI : pendingModelLabel}?</Menu.Label>
      <p className="mb-4 text-xs text-muted-foreground">{changesCLI ? 'Continue in this tab' : currentCLI}{pendingAccountLabel ? ` · ${pendingAccountLabel}` : ''}</p>
      <div className="mb-3.5 flex items-center gap-2.5 rounded-md border border-border bg-muted/40 px-3 py-2.5 text-xs">
        <div className="min-w-0 flex-1"><span className="block text-[10px] text-muted-foreground">Current</span><span>{changesCLI ? `${currentCLI} · ` : ''}{modelLabel}</span></div>
        <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <div className="min-w-0 flex-1"><span className="block text-[10px] text-muted-foreground">Next reply</span><span>{changesCLI ? `${pendingCLI} · ` : ''}{pendingModelLabel}</span></div>
      </div>
      <p className="mb-4 text-xs leading-relaxed text-muted-foreground">{changesCLI
        ? `${pendingCLI} starts with recent messages and access to the full saved conversation. It can search older history when needed—not load it all upfront.`
        : `${contextTokens > 0 ? `This chat has ${formatTokens(contextTokens)} tokens. ` : ''}Continue with the current context, or summarize it first to make it smaller.`}</p>
      <Menu.Item ref={confirmSwitchRef} className={`${switchActionClass} mb-2 border-foreground/80 bg-foreground text-background data-[highlighted]:opacity-90`}
        onSelect={() => void finishSwitch(!changesCLI)}>{changesCLI ? `Switch to ${pendingCLI}` : 'Summarize & switch'}</Menu.Item>
      {!changesCLI && <Menu.Item className={`${switchActionClass} border-border data-[highlighted]:bg-muted`} onSelect={() => void finishSwitch(false)}>Keep full history & switch</Menu.Item>}
      <p className="mt-2 text-[11px] text-muted-foreground">{changesCLI ? 'Your chat stays here. No automatic summary.' : 'A summary keeps the key context, not every detail.'}</p>
      <Menu.Item className="ml-auto mt-3 w-fit cursor-pointer rounded px-1 py-0.5 text-xs text-muted-foreground outline-none data-[highlighted]:bg-muted data-[highlighted]:text-foreground" onSelect={event => { event.preventDefault(); setPendingSwitch(null) }}>Cancel</Menu.Item>
    </div> : <div className={sessionId ? 'grid gap-x-3 sm:grid-cols-2' : undefined}><div className="min-w-0">
    {sourceText && <div className="flex items-center gap-1.5 px-2.5 pb-1 pt-0.5 text-[11px] text-muted-foreground">
      {sourceTag === 'Mac' ? <Laptop className="h-3 w-3 shrink-0" aria-hidden="true" /> : <Server className="h-3 w-3 shrink-0" aria-hidden="true" />}
      <span className="min-w-0 truncate" title={sourceText}>{sourceText}</span>
    </div>}
    {actions && <><ComposerActionItems {...actions} deferAction={action => { afterClose.current = action }} /><Menu.Separator className="my-1.5 h-px bg-border" /></>}
    <Menu.Sub>
      {subRow('CLI', selectedOption.label || selectedOption.provider || selectedOption.id)}
      <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} className={contentClass}>
        {cliGroups.map(({ option, accounts }) => <Menu.Sub key={option.id}>
          <Menu.SubTrigger className={rowClass}>
            <span className="min-w-0 flex-1 truncate">{option.label || option.provider || option.id}</span>
            {option.id === selectedOption.id && <Check className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
          </Menu.SubTrigger>
          <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} className={contentClass}>
            <Menu.Label className={labelClass}>Account</Menu.Label>
            {accounts.map(account => {
              const available = accountUsable(account) && accountConfigured(account)
              const current = option.id === selectedOption.id && account.id === currentAccountId
              return <Menu.Item key={account.id} className={rowClass} disabled={saving || !available} onSelect={event => { if (pickModel(option, undefined, account.id)) event.preventDefault() }}>
                <span className="min-w-0 flex-1 truncate">{account.via_connection ? account.display_name : onThisMac && accountRelation(account) === 'server' ? 'This Mac' : accountOptionLabel(account)}{!available ? ` · ${NO_LONGER_AVAILABLE}` : ''}</span>
                {current && <Check className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
              </Menu.Item>
            })}
            {accounts.length === 0 && <Menu.Item disabled className={rowClass}>{sources.accounts === null ? 'Loading accounts…' : NO_LONGER_AVAILABLE}</Menu.Item>}
          </Menu.SubContent></Menu.Portal>
        </Menu.Sub>)}
      </Menu.SubContent></Menu.Portal>
    </Menu.Sub>
    <Menu.Sub>
      {subRow('Model', modelLabel)}
      <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} className={subContentClass}>
        {groups.map((group, index) => <Fragment key={group.option.id}>
          {index > 0 && separator}
          <Menu.Label className={labelClass}>{group.option.label || group.option.provider || group.option.id}</Menu.Label>
          {group.current && cursorChoices.length > 1
            // The menu's type-to-jump would take the search box's keys.
            ? <div onKeyDown={event => event.stopPropagation()}>
              <CursorModelPicker models={cursorChoices} selectedModelId={currentModel} onSelect={modelId => { if (!pickModel(selectedOption, modelId)) setMenuOpen(false) }} />
            </div>
            : group.models.length > 0
              ? <ChoiceItems choices={group.models} value={group.current ? currentModel : ''} onPick={modelId => { pickModel(group.option, modelId) }} keepOpenOnPick={Boolean(switchNote)} />
              : <Menu.Item className={rowClass} disabled={group.current} onSelect={event => { if (pickModel(group.option)) event.preventDefault() }}>
                <span className="min-w-0 flex-1 truncate">{group.current ? modelLabel : `Use ${group.option.label || group.option.provider}`}</span>
                {group.current && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
              </Menu.Item>}
        </Fragment>)}
      </Menu.SubContent></Menu.Portal>
    </Menu.Sub>
    {levels.length > 1 && effortLabel && <Menu.Sub>
      {subRow('Effort', effortLabel)}
      <Menu.Portal><Menu.SubContent sideOffset={6} collisionPadding={12} className={contentClass}>
        <ChoiceItems choices={levels} value={currentEffort} onPick={pickEffort} />
      </Menu.SubContent></Menu.Portal>
    </Menu.Sub>}
    <ThinkingDisplayMenuItem className={rowClass} />
    {companyServer
      ? <SharedTokenUsageItems accountProvider={selectedOption.provider} serverConnection={companyServer} />
      : <SharedTokenUsageItems accountProvider={sharedAccountProvider(connectionId
        || (meta?.agentProfileChatProvider === selectedOption.provider ? meta?.agentProfileChatConnectionID : undefined), selectedOption.provider)} />}
    {separator}
    <Menu.Item className={rowClass} onSelect={openUsage}><BarChart3 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Usage</Menu.Item>
    <Menu.Item className={rowClass} onSelect={openModels}><Settings2 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Models</Menu.Item>
    </div>
    {/* This chat's Context group (PLAT-872-71): meter, plan limits, auto-compact, compact now, fresh chat. */}
    {sessionId && <div className="min-w-0 sm:border-l sm:border-border sm:pl-3"><ChatContextItems sessionId={sessionId} modelLabel={label} /></div>}
    </div>}
    {/* The message-box keys span both columns at the very bottom (owner-approved mockup, 11 Nov). */}
    {actions && !pendingSwitch && <ComposerSendKeys busy={busy} />}
  </Chip></>
}
