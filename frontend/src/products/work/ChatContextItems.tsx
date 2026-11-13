import { useChatContextView } from './useChatContextView'
import { useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { Check, Info, Layers, RotateCcw } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { useChatPreferences } from '../../hooks/useChatPreferences'
import { formatTokens } from '../../utils/formatTokens'
import { ContextMeter } from '../../components/chat/ContextMeter'

export const menuRowClass = 'flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
const pairButtonClass = 'flex flex-1 cursor-pointer items-center justify-center gap-1.5 rounded-md border border-border px-2 py-1.5 text-xs outline-none data-[highlighted]:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
export const menuLabelClass = 'px-2.5 pb-1 pt-1.5 text-[11px] font-medium text-muted-foreground'

function resetDate(iso: string | undefined, compact = false): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, compact
    ? { month: 'short', day: 'numeric' }
    : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

/** Each provider window keeps its own server-reported reset time. */
export function chatLimitsText(limits: ChatContextView['limits']): string {
  return (limits ?? []).map(limit => {
    const when = resetDate(limit.resets_at)
    return `${limit.label} ${Math.round(limit.used_percent)}%${when ? ` (resets ${when})` : ''}`
  }).join(' · ')
}

/** Keep the weekly reset date visible beside the 7d plan use. */
export function chatLimitsShort(limits: ChatContextView['limits']): string {
  return (limits ?? []).map(limit => {
    const when = limit.label === '7d' ? resetDate(limit.resets_at, true) : ''
    return `${limit.label} ${Math.round(limit.used_percent)}%${when ? ` (resets ${when})` : ''}`
  }).join(' · ')
}

/** Plan use without reset times ("5h 42% · 7d 56%"); the times are in its hover text. */
export function chatLimitsPercent(limits: ChatContextView['limits']): string {
  return (limits ?? []).map(limit => `${limit.label} ${Math.round(limit.used_percent)}%`).join(' · ')
}

/** One-line setting: its explanation is the hover text of the row and its ⓘ. */
function SettingLabel({ label, hint }: { label: string; hint?: string }) {
  return <span className="flex min-w-0 flex-1 items-center gap-1.5" title={hint}>
    <span className="truncate">{label}</span>
    {hint && <Info className="h-3 w-3 shrink-0 text-muted-foreground" aria-label={hint} />}
  </span>
}

/** Sent when the menu changes a chat's context, so the chip in the chat box shows the server's new view. */
export const chatContextChangedEvent = 'agentworks:chat-context-changed'
/** Asks the chat box of one chat to send a message as if the person typed it ({ sessionId, text }). */
export const chatSendMessageEvent = 'agentworks:chat-send-message'
/** Sent by the chip in the chat box to open that chat's model menu. */
export const openChatContextMenuEvent = 'agentworks:open-chat-context-menu'

type ChatContextChange = { idle_compaction?: boolean; compact_early?: boolean; claude_terminal?: boolean; action?: 'compact' | 'fresh' }

/**
 * Sends one change to the server and shows its answer everywhere: the menu, the chip and the terminal icon
 * all follow chatContextChangedEvent. Returns the server's new view, or null when it refused (a toast says why).
 */
export async function changeChatContext(sessionId: string, body: ChatContextChange): Promise<ChatContextView | null> {
  try {
    const result = await agentApi.setChatContext({ session_id: sessionId, ...body })
    window.dispatchEvent(new CustomEvent(chatContextChangedEvent, { detail: result.context }))
    if (body.claude_terminal !== undefined) {
      // Show the chat the way it now runs; the terminal opens with the next message's launch.
      const tab = Object.values(useChatStore.getState().chatTabs).find(candidate => candidate.sessionId === sessionId)
      if (tab) useChatStore.getState().setTabViewMode(tab.tabId, body.claude_terminal ? 'terminal' : 'formatted')
    }
    if (result.message) useChatStore.getState().addToast(result.message, 'success')
    // The terminal had nothing to start from: send the server's short starter message in this chat for the
    // person, so Claude opens in the terminal now instead of waiting for them to type something.
    if (result.starter_message) {
      window.dispatchEvent(new CustomEvent(chatSendMessageEvent, { detail: { sessionId, text: result.starter_message } }))
    }
    return result.context
  } catch (cause) {
    const reason = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
    useChatStore.getState().addToast(reason || 'That did not work. Try again in a moment.', 'error')
    return null
  }
}

/**
 * The "Context" group of a chat's model menu (PLAT-872-71): how full this chat is, its account's plan limits,
 * and the switches and actions the server offers for it. Everything comes from the server; a choice is sent
 * back and the server's answer is shown. Mounted only while the menu is open, so it reads fresh numbers.
 */
export default function ChatContextItems({ sessionId, modelLabel, showDisplay = true, showTerminal = true }: { sessionId: string; modelLabel?: string; showDisplay?: boolean; showTerminal?: boolean }) {
  const view = useChatContextView(sessionId)
  const [busy, setBusy] = useState(false)

  if (!view) return null
  const used = view.used_tokens ?? 0
  const windowTokens = view.window_tokens ?? 0
  const limits = chatLimitsText(view.limits)
  const offersSomething = Boolean(view.idle_compaction || view.compact_early || view.claude_terminal || view.can_compact || view.can_fresh)
  if (!(used > 0) && !limits && !offersSomething) return null

  const send = async (body: ChatContextChange) => {
    if (busy) return
    setBusy(true)
    try {
      await changeChatContext(sessionId, body)
      // The shared hook receives the server response through chatContextChangedEvent.
    } finally { setBusy(false) }
  }
  // Keep the menu open after a switch, so the person sees it change.
  const keepOpen = (event: Event) => event.preventDefault()

  return <>
    <Menu.Separator className="my-1 h-px bg-border first:hidden" />
    <Menu.Label className={menuLabelClass}>Context</Menu.Label>
    {limits && <div className="flex items-center gap-1.5 px-2.5 pb-1 text-[11px] text-muted-foreground" title={`Your account's plan use, as the provider reports it: ${limits}`}>
      <span className="truncate">{modelLabel ? `${modelLabel} · ` : ''}{chatLimitsPercent(view.limits)}</span>
      <Info className="h-3 w-3 shrink-0" aria-label={limits} />
    </div>}
    {used > 0 && <div className="px-2.5 pb-1.5 pt-0.5">
      <div className="text-xs text-muted-foreground">{formatTokens(used)}{windowTokens > 0 ? ` of ${formatTokens(windowTokens)}` : ''} used</div>
      {windowTokens > 0 && <ContextMeter className="mt-1" used={used} windowTokens={windowTokens} />}
    </div>}
    {view.idle_compaction && <Menu.CheckboxItem className={menuRowClass} checked={view.idle_compaction.on} disabled={busy} onSelect={keepOpen}
      onCheckedChange={checked => void send({ idle_compaction: checked === true })}>
      <SettingLabel label="Auto-compact when idle" hint={view.idle_compaction_hint} />
      <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
    </Menu.CheckboxItem>}
    {view.compact_early && <Menu.CheckboxItem className={menuRowClass} checked={view.compact_early.on} disabled={busy} onSelect={keepOpen}
      onCheckedChange={checked => void send({ compact_early: checked === true })}>
      <SettingLabel label="Compact at 300K" hint={view.compact_early_hint} />
      <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
    </Menu.CheckboxItem>}
    {showTerminal && view.claude_terminal && <Menu.CheckboxItem className={menuRowClass} checked={view.claude_terminal.on} disabled={busy} onSelect={keepOpen}
      onCheckedChange={checked => void send({ claude_terminal: checked === true })}>
      <SettingLabel label="Native Claude Code terminal" hint="Claude Code as you know it, slash commands included. From the next message; the conversation continues." />
      <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
    </Menu.CheckboxItem>}
    {(view.can_compact || view.can_fresh) && <div className="flex gap-1.5 px-1 py-1">
      {view.can_compact && <Menu.Item className={pairButtonClass} disabled={busy} onSelect={() => void send({ action: 'compact' })}
        title="Summarise this chat now, so each next step reads less.">
        <Layers className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Compact now
      </Menu.Item>}
      {view.can_fresh && <Menu.Item className={pairButtonClass} disabled={busy} onSelect={() => void send({ action: 'fresh' })}
        title="Your next message starts a fresh conversation with a short summary of this one. This chat's history stays here.">
        <RotateCcw className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />Fresh chat
      </Menu.Item>}
    </div>}
    {showDisplay && <ContextDisplayMenuItem />}
  </>
}


// Same server-saved display preference, also used in the compact composer footer.
export function ContextDisplayMenuItem({ compact = false, className = menuRowClass }: { compact?: boolean; className?: string }) {
  const preferences = useChatPreferences()
  return <Menu.CheckboxItem className={className} checked={preferences.showContextIndicator} disabled={!preferences.ready || preferences.save.isPending}
    title="Show context use in the chat box" onSelect={event => event.preventDefault()}
    onCheckedChange={checked => preferences.save.mutate({ show_context_indicator: checked === true }, { onError: () => useChatStore.getState().addToast('Could not save that. Try again in a moment.', 'error') })}>
    <span className="min-w-0 flex-1">{compact ? 'Context' : 'Show context use in the chat box'}</span>
    <span className={compact ? 'flex h-3.5 w-3.5 items-center justify-center rounded-sm border border-muted-foreground/50' : undefined}>
      <Menu.ItemIndicator><Check className="h-3 w-3" aria-hidden="true" /></Menu.ItemIndicator>
    </span>
  </Menu.CheckboxItem>
}
