import { useEffect, useRef, useState } from 'react'
import { agentApi } from '../services/api'
import type { TerminalSnapshot } from '../services/api-types'
import { useTheme } from '../hooks/useTheme'
import { LiveAttachXtermPane, RAW_XTERM_THEMES, StaticXtermPane } from './TerminalCenter'

type MainAgentTerminalProps = {
  sessionId: string
  readOnly?: boolean
  /** Called when the session has no live pane to show (structured-transport
   * providers such as Cursor never have one); the host returns the tab to
   * the conversation instead of leaving a placeholder on screen. */
  onUnavailable?: () => void
}

// Keep the diagnostics-only terminal readable without allowing it to dictate
// the normal chat/workspace split. At the 13px JetBrains Mono metrics used by
// TerminalCenter this leaves room for roughly 80 columns, including xterm's
// padding. Narrow chat panes scroll the terminal horizontally instead of
// repeatedly shrinking and reflowing the underlying tmux TUI.
export const MAIN_AGENT_TERMINAL_MIN_WIDTH_PX = 680
const MAIN_AGENT_TERMINAL_HISTORY_LINES = 1000
const MAIN_AGENT_TERMINAL_MISSES_BEFORE_DROP = 3

// Product raw view for the one main coding-agent terminal. This intentionally
// reuses the mature xterm renderer/live tmux attach rather than maintaining a
// second preformatted-text terminal. Child terminal rails stay diagnostics-only.
export function MainAgentTerminal({ sessionId, onUnavailable, readOnly = false }: MainAgentTerminalProps) {
  const onUnavailableRef = useRef(onUnavailable)
  useEffect(() => { onUnavailableRef.current = onUnavailable }, [onUnavailable])
  const { theme } = useTheme()
  const [snapshot, setSnapshot] = useState<TerminalSnapshot | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  // No live view yet (the agent has not started one: before the first
  // message, or between runs for a CLI without a retained terminal). Say so
  // and keep checking, instead of silently switching back to the chat.
  const [notStarted, setNotStarted] = useState(false)
  const contentRef = useRef<HTMLDivElement | null>(null)
  const snapshotRef = useRef<TerminalSnapshot | null>(null)
  // Consecutive "no main terminal" answers: a retained pane can be missing for
  // one poll while the server rebinds it, so the view is dropped only when it
  // stays gone.
  const missesRef = useRef(0)

  useEffect(() => {
    // Slow responses from another session cannot replace this terminal.
    let cancelled = false
    let requestInFlight = false
    const refresh = async () => {
      if (cancelled || requestInFlight) return
      requestInFlight = true
      try {
        // The WebSocket owns live output and its first frame already carries the
        // tmux seed. Poll only metadata here: repeatedly downloading a growing
        // full-history body made this 3s health check reach megabytes and could
        // starve the socket/browser until Axios hit its 15s read timeout.
        const metadata = await agentApi.getMainTerminal(sessionId, { content: 'none' })
        if (cancelled) return
        const previous = snapshotRef.current
        if (paneIsLive(metadata)) {
          const next = {
            ...metadata,
            content: previous?.terminal_id === metadata.terminal_id && previous.tmux_session === metadata.tmux_session ? previous.content : '',
            rows: previous?.terminal_id === metadata.terminal_id && previous.tmux_session === metadata.tmux_session ? previous.rows : [],
          }
          snapshotRef.current = next
          setSnapshot(next)
        } else {
          // A settled pane has no stream to retain its output, so fetch its final
          // history once per revision instead of on every poll. A retained agent
          // (e.g. Cursor on tmux) reuses this pane for later turns; a turn that
          // starts and settles between two polls is visible only as a new
          // chunk_index, and without refetching on it the view froze on old output.
          const needsFinalHistory = !previous ||
            previous.terminal_id !== metadata.terminal_id ||
            previous.active ||
            !previous.content ||
            previous.chunk_index !== metadata.chunk_index
          if (needsFinalHistory) {
            const settled = await agentApi.getMainTerminal(sessionId, { content: 'history', lines: MAIN_AGENT_TERMINAL_HISTORY_LINES })
            if (cancelled) return
            snapshotRef.current = settled
            setSnapshot(settled)
          } else {
            const next = { ...previous, ...metadata, content: previous.content, rows: previous.rows }
            snapshotRef.current = next
            setSnapshot(next)
          }
        }
        missesRef.current = 0
        setError(null)
        setNotStarted(false)
      } catch (cause: any) {
        if (cancelled) return
        const hadSnapshot = snapshotRef.current !== null
        if (cause?.response?.status === 403) {
          // The server refuses the terminal for read-only access (a Crew reader, a
          // read-only login, a read-only channel). Typing into the CLI would skip the
          // read-only notice the chat adds to their messages, so the terminal is kept for
          // owners and editors. It can never work for this session, so say so once and go
          // back to the chat instead of showing "not started" and polling forever.
          snapshotRef.current = null
          setSnapshot(null)
          setNotStarted(false)
          setError('The terminal is only available to owners and editors.')
          onUnavailableRef.current?.()
        } else if (cause?.response?.status === 404) {
          missesRef.current += 1
          if (!hadSnapshot || missesRef.current >= MAIN_AGENT_TERMINAL_MISSES_BEFORE_DROP) {
            snapshotRef.current = null
            setSnapshot(null)
            setError(null)
            setNotStarted(true)
          }
        } else if (!hadSnapshot) {
          setError(cause?.message || 'Could not load the live view.')
        }
        // With a terminal already on screen, a failed poll (a slow server while a
        // message is being sent) must never replace it: the next poll retries.
      } finally {
        if (!cancelled) setLoading(false)
        requestInFlight = false
      }
    }
    snapshotRef.current = null
    missesRef.current = 0
    setSnapshot(null)
    setError(null)
    setNotStarted(false)
    setLoading(true)
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 3000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [sessionId])

  // A retained CLI keeps its tmux pane between turns (process_state "live"),
  // so the live view stays mounted across turns. Keying it on the turn
  // (active) swapped it for a static snapshot at every turn end and back to a
  // fresh, blank live view on every send: a ~1s flash (RTS 2026-09-29).
  const isLive = paneIsLive(snapshot)

  return (
    <section
      className="flex min-h-0 min-w-0 flex-1 flex-col overflow-x-auto bg-[#0b0e14] text-[#e7e9e5]"
      data-testid="main-agent-terminal-scroll-container"
    >
      <div
        className="min-h-0 flex-1"
        data-testid="main-agent-terminal-grid"
        style={{ minWidth: MAIN_AGENT_TERMINAL_MIN_WIDTH_PX }}
      >
        {error ? (
          <div className="p-4 text-sm text-red-300">{error}</div>
        ) : !snapshot && notStarted ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-sm text-neutral-400" data-testid="main-agent-terminal-not-started">
            <p>The live view appears once the agent starts working.<br />Send a message and it will show up here.</p>
            {onUnavailable && (
              <button type="button" className="rounded-md border border-neutral-700 px-3 py-1.5 text-xs text-neutral-300 hover:bg-neutral-800" onClick={() => onUnavailableRef.current?.()}>
                Back to chat
              </button>
            )}
          </div>
        ) : !snapshot ? (
          <div className="flex h-full items-center justify-center text-sm text-neutral-500">
            {/* Users are not told about terminals or tmux: while the live session has
                not come up yet this simply reads as the agent starting. */}
            {loading ? 'Starting…' : 'Starting…'}
          </div>
        ) : isLive ? (
          <LiveAttachXtermPane
            key={`${sessionId}:${snapshot.terminal_id}:${snapshot.tmux_session}`}
            terminalId={snapshot.terminal_id}
            tmuxSession={snapshot.tmux_session}
            sessionId={sessionId}
            className="h-full w-full"
            contentRef={contentRef}
            xtermTheme={RAW_XTERM_THEMES[theme]}
            authoritativeContent={snapshot.content}
            authoritativeVersion={`${snapshot.chunk_index}:${snapshot.updated_at}`}
            reconnectOnClose
            interactive={!readOnly}
            streamUrl={(cols, rows) => agentApi.getMainTerminalStreamUrl(sessionId, cols, rows, snapshot.tmux_session)}
            loadSnapshot={() => agentApi.getMainTerminal(sessionId, { content: 'history', lines: MAIN_AGENT_TERMINAL_HISTORY_LINES })}
          />
        ) : (
          <StaticXtermPane
            key={`${snapshot.terminal_id}:${snapshot.chunk_index}`}
            content={snapshot.content}
            className="h-full w-full"
            contentRef={contentRef}
            xtermTheme={RAW_XTERM_THEMES[theme]}
          />
        )}
      </div>
    </section>
  )
}

// A retained CLI keeps its tmux pane between turns (process_state "live"), so
// the live view stays for the pane's whole life: not only while a turn runs.
function paneIsLive(snapshot: { active?: boolean; tmux_session?: string; process_state?: string } | null | undefined): boolean {
  return Boolean(snapshot?.tmux_session && (snapshot.active || snapshot.process_state === 'live'))
}
