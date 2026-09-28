import { Check, MessageCircle } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'

// Two-click confirm: the first click arms, the second sends. A single click
// used to send straight to chat, so a misclick (this button sits next to
// Refresh in WorkspaceViewActions) burned a chat turn. A modal per click
// would punish every intentional use instead; arming keeps the confirm
// inline. CONFIRM_FLOOR_MS rejects the second half of a double-click: a
// deliberate confirm comes after reading the armed state, never within
// half a second of arming. After a successful send the button briefly shows
// Sent! so the hop to chat reads as acknowledged.
const ARM_TIMEOUT_MS = 4000
const CONFIRM_FLOOR_MS = 600
const SENT_TIMEOUT_MS = 1800

function hoverCapable(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return true
  return window.matchMedia('(hover: hover)').matches
}

/**
 * Every settings/config panel in the workflow builder (MCP, skills, secrets,
 * LLM, bots, browser, schedules, notify, backup, publish...) only ever shows
 * what's already set up on this deployment or in this workflow — none of
 * them can tell the user that chat itself can search the web, install
 * something new, or explain what's possible beyond that fixed list. This is
 * the fix for that, meant to be dropped into any such panel's header or
 * walkthrough popup.
 *
 * Reuses sendWorkspacePaneMessageToChat, the single delivery path for every
 * right-pane action
 * uses for a Needs-your-decision card (utils/reportHumanInputChat.ts): it
 * finds or opens the right interactive tab for this workflow, queues
 * correctly behind a running turn, and switches focus to chat. A prefilled
 * textarea does none of that. Defaults to the formatted (not terminal) view,
 * since these are ordinary conversational asks, not something to watch a
 * CLI execute live.
 */
export function AskAIButton({
  workspacePath,
  message,
  onAsk,
  label = 'Ask AI',
  className,
  iconOnly = false,
}: {
  workspacePath: string | null
  message: string
  /** Product surfaces can deliver the message to their own chat lane. */
  onAsk?: (message: string) => void | Promise<void>
  label?: string
  className?: string
  /** Renders just the icon (for a tight icon-toolbar spot) instead of icon+label. */
  iconOnly?: boolean
}) {
  const [armed, setArmed] = useState(false)
  const [sent, setSent] = useState(false)
  // Icon-only buttons expand on hover/focus to reveal the label, and a click
  // only counts once expanded: hovering is what tells a fast-moving user
  // which icon they are about to hit. Touch taps emulate hover before the
  // click, so touch needs no extra tap; keyboard focus expands as well.
  const [expanded, setExpanded] = useState(false)
  const armedAtRef = useRef(0)
  const disarmTimerRef = useRef<number | null>(null)
  const sentTimerRef = useRef<number | null>(null)

  useEffect(() => () => {
    if (disarmTimerRef.current !== null) window.clearTimeout(disarmTimerRef.current)
    if (sentTimerRef.current !== null) window.clearTimeout(sentTimerRef.current)
  }, [])

  const disarm = () => {
    if (disarmTimerRef.current !== null) {
      window.clearTimeout(disarmTimerRef.current)
      disarmTimerRef.current = null
    }
    setArmed(false)
    setExpanded(false)
  }

  const deliver = async () => {
    if (onAsk) {
      await onAsk(message)
      return
    }
    if (!workspacePath) return
    await sendWorkspacePaneMessageToChat({ workspacePath, message })
  }

  const buttonClassName = className ?? (iconOnly
    ? 'inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary'
    : 'flex shrink-0 items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary')
  const showExpanded = iconOnly && (expanded || sent)
  const handleClick = () => {
    if (sent) return
    if (iconOnly && hoverCapable() && !expanded) {
      setExpanded(true)
      return
    }
    if (!armed) {
      setArmed(true)
      armedAtRef.current = Date.now()
      if (disarmTimerRef.current !== null) window.clearTimeout(disarmTimerRef.current)
      disarmTimerRef.current = window.setTimeout(() => { setArmed(false); setExpanded(false) }, ARM_TIMEOUT_MS)
      return
    }
    if (Date.now() - armedAtRef.current < CONFIRM_FLOOR_MS) return
    if (disarmTimerRef.current !== null) {
      window.clearTimeout(disarmTimerRef.current)
      disarmTimerRef.current = null
    }
    setArmed(false)
    void deliver().then(
      () => {
        setSent(true)
        if (sentTimerRef.current !== null) window.clearTimeout(sentTimerRef.current)
        sentTimerRef.current = window.setTimeout(() => { setSent(false); setExpanded(false) }, SENT_TIMEOUT_MS)
      },
      err => {
        setExpanded(false)
        useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to open chat.', 'error')
      },
    )
  }

  const stateClassName = sent
    ? ' border-emerald-500/50 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
    : armed
      ? ' border-primary/60 bg-primary/5 text-primary'
      : ''
  const stateLabel = sent ? 'Sent!' : armed ? 'Sure?' : label

  return (
    // A native `title` attribute is unreliable here: this button is disabled
    // when there's no workspacePath, and several browsers suppress the title
    // tooltip on disabled elements entirely. The app-wide Tooltip component
    // (backed by the TooltipProvider in App.tsx) doesn't have that problem.
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={handleClick}
          onMouseEnter={() => { if (iconOnly) setExpanded(true) }}
          onMouseLeave={() => { if (iconOnly) setExpanded(false) }}
          onFocus={() => { if (iconOnly) setExpanded(true) }}
          onBlur={() => { if (iconOnly) setExpanded(false) }}
          onKeyDown={event => { if (event.key === 'Escape') disarm() }}
          aria-label={iconOnly ? (sent ? `${label} (sent to chat)` : armed ? `${label} (click again to send)` : label) : undefined}
          className={`${buttonClassName}${showExpanded ? ' gap-1.5' : ''}${stateClassName}`}
          style={showExpanded ? { width: 'auto', paddingLeft: '0.625rem', paddingRight: '0.625rem' } : undefined}
          disabled={!workspacePath && !onAsk}
        >
          {sent ? <Check className="h-3.5 w-3.5 shrink-0" /> : <MessageCircle className="h-3.5 w-3.5 shrink-0" />}
          {iconOnly ? (
            <span aria-hidden="true" className={`${showExpanded ? 'w-auto opacity-100' : 'w-0 opacity-0'} overflow-hidden whitespace-nowrap text-xs font-medium transition-opacity motion-reduce:transition-none`}>
              {stateLabel}
            </span>
          ) : (
            stateLabel
          )}
        </button>
      </TooltipTrigger>
      {(!showExpanded || armed || sent) && (
        <TooltipContent side="bottom">{sent ? 'Sent to chat' : armed ? 'Click again to send to chat' : label}</TooltipContent>
      )}
    </Tooltip>
  )
}
