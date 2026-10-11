import { transcriptReadingKey } from './useTranscriptScroll'
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, AlertTriangle, ArrowDownToLine, ArrowRightToLine, Bot, Braces, Bug, Check, ChevronDown, ChevronRight, ChevronUp, ClipboardCheck, Copy, CornerDownLeft, CornerUpLeft, GitBranch, History, Info, ListRestart, MessageSquare, Network, Power, RefreshCw, SearchCheck, Square, Terminal, Trash2, X } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { Terminal as XTerm, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { agentApi } from '../services/api'
import {
  canToggleTerminalView,
  mergeTerminalSnapshotBody,
  reconcileTerminalSnapshots,
  resolveTerminalFormattedView,
  shouldHydrateMainTerminalEvents,
  shouldLoadTerminalEvents,
  shouldStreamTerminal,
  terminalViewPreferenceKey,
} from '../utils/terminalSnapshotIdentity'
import type { PollingEvent, RuntimeSnapshot, TerminalSnapshot } from '../services/api-types'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { normalizeEventViewMode, useChatStore } from '../stores/useChatStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { TERMINAL_REFRESH_REQUEST_EVENT } from '../utils/terminalRefresh'
import { GEOMETRY_RECONNECT_AFTER_CLOSE, LIVE_ATTACH_INBAND_RESEED_DEBOUNCE_MS, LIVE_ATTACH_INBAND_RESEED_TIMEOUT_MS, LiveAttachReseedTracker, planGeometryChange, planLiveAttachClose, routeLiveAttachFrame, terminalGridChange, terminalReconnectDelayMs, terminalSnapshotCanReconnect, type GeometryChangeStep } from '../utils/terminalReconnect'
import { terminalPayloadHasVisibleContent } from '../utils/terminalVisibleContent'
import { useTheme } from '../hooks/useTheme'
import { useSessionExecutionTree } from '../hooks/useSessionExecutionTree'
import type { Theme } from '../contexts/ThemeContext'
import { normalizeAnsiForEmbeddedXterm } from '../utils/ansiSanitize'
import { installDisplayOnlyXtermGuards } from '../utils/displayOnlyXterm'
import { installInteractiveXtermKeys, sendTerminalInput, sendTerminalPaste } from '../utils/interactiveXterm'
import { TerminalOutputQueue } from '../utils/terminalOutputQueue'
import { preserveTerminalContinuity } from '../utils/terminalContinuity'
import { isMainAgentTerminal, preferredTerminalForContext } from '../utils/terminalIdentity'
import { hasFreshTerminalDetailBody } from '../utils/terminalDetailFreshness'
import {
  canonicalTerminalRailSelection,
  hiddenSelectedTerminalRailGroup,
  organizeTerminalRail,
  terminalRailGroupSearchText,
  terminalRailTitle,
  terminalRailVisualKind,
  type TerminalRailLogicalGroup,
  type TerminalRailSection,
} from '../utils/terminalRailOrganization'
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip'
import { reconcileTerminalRuntimeState, runtimeDisplayStatus, terminalConversationHasPendingWork, terminalTurnIsBusy } from '../utils/runtimeActivity'
import { usePlanData } from './workflow/hooks/usePlanData'
import { TerminalEventTranscript } from './TerminalEventTranscript'
import { selectTerminalEvents, toolErrorContextByEventID } from '../utils/terminalEventTranscript'
import { formatToolCallArguments } from '../utils/toolCallFormatting'
import type { PlanStep } from '../utils/stepConfigMatching'
import { requestWorkflowPlanStepFocus } from '../utils/workflowPlanFocus'
import { mergeNewerTerminalEventPage, mergeTerminalEventPages, terminalEventSequenceBounds } from '../utils/terminalEventPage'
import { projectExecutionTreeTerminals } from '../utils/terminalExecutionProjection'
import { hydrateTabEvents } from '../utils/sessionRestore'
import { hydrateExecutionConversation } from '../utils/executionConversationRestore'
import { isExecutionConversationTab } from '../utils/sessionEventSource'

// hasAnsiCodes returns true when the string contains at least one CSI escape.
// Used to decide whether to take the colored-render path or fall back to the
// existing plain text path.
function hasAnsiCodes(s: string): boolean {
  return s.includes('\x1B[')
}

function hasTerminalRedrawControls(s: string): boolean {
  if (!s) return false
  if (hasAnsiCodes(s)) return true
  if (s.includes('\x1B]') || s.includes('\x07') || s.includes('\x0f')) return true
  // Some persisted tmux pipe snapshots have the OSC introducer stripped but
  // still carry the title-control payload (`]0;...`) and carriage-return redraws.
  if (/\](?:0|1|2);/.test(s)) return true
  // Treat bare carriage returns as terminal redraw control, but not CRLF text.
  return /\r(?!\n)/.test(s)
}

function isTmuxContentSource(source?: string): boolean {
  const normalized = (source || '').trim().toLowerCase()
  return normalized === 'tmux_pipe' || normalized === 'tmux_capture' || normalized === 'tmux_stream'
}

// xterm invokes write callbacks asynchronously. A React pane can unmount and
// dispose its terminal before that callback runs; calling scrollToBottom on the
// disposed instance throws inside xterm and can break the surrounding chat UI.
function scrollCurrentXtermToBottom(term: XTerm, currentTerm: XTerm | null): boolean {
  if (currentTerm !== term) return false
  try {
    term.scrollToBottom()
    return true
  } catch {
    return false
  }
}

interface TerminalCenterProps {
  currentSessionId?: string
  compact?: boolean
  hasPendingTerminalActivity?: boolean
}

const TERMINAL_REFRESH_HISTORY_LINES = 1000
const TERMINAL_ACTIVE_DISPLAY_HISTORY_LINES = 1000
const TERMINAL_ACTIVE_RAIL_MAIN_HISTORY_LINES = 600
const TERMINAL_ACTIVE_RAIL_STEP_HISTORY_LINES = 1200
const TERMINAL_ACTIVE_RAIL_PROBE_SCREEN_LINES = 80
const TERMINAL_STATIC_DETAIL_HISTORY_LINES = 1500
const RAW_XTERM_MIN_FIT_COLS = 40
const RAW_XTERM_MIN_FIT_ROWS = 10
const RAW_XTERM_MIN_FIT_WIDTH_PX = 240
const RAW_XTERM_MIN_FIT_HEIGHT_PX = 120
const TERMINAL_DETAIL_CACHE_LIMIT = 40
const MAX_PRIOR_ARCHIVED_TURNS_TO_INLINE = 3
const EMPTY_TERMINAL_RESPONSE_GRACE_POLLS = 10
const TERMINAL_POLL_INTERVAL_MS = 3000
const TERMINAL_ACTIVE_RAIL_PROBE_LIMIT = 4
const ARCHIVED_TURN_PREFETCH_LIMIT = 1
const TERMINAL_FAST_POLL_INTERVAL_MS = 750
const TERMINAL_FAST_POLL_DURATION_MS = 5000
const TERMINAL_EVENT_PAGE_LIMIT = 300
const TERMINAL_EVENT_LIVE_POLL_INTERVAL_MS = 1000

interface SelectedTerminalEventPage {
  terminalId: string | null
  events: PollingEvent[]
  loaded: boolean
  loading: boolean
  loadingOlder: boolean
  hasOlder: boolean
  oldestSequence?: number
  latestSequence?: number
  error?: string
}

interface MainSessionOlderEventPage {
  sessionId: string | null
  events: PollingEvent[]
  loadingOlder: boolean
  hasOlder?: boolean
  nextOffset: number
  error?: string
}

const EMPTY_SELECTED_TERMINAL_EVENT_PAGE: SelectedTerminalEventPage = {
  terminalId: null,
  events: [],
  loaded: false,
  loading: false,
  loadingOlder: false,
  hasOlder: false,
}
const EMPTY_MAIN_SESSION_OLDER_EVENT_PAGE: MainSessionOlderEventPage = {
  sessionId: null,
  events: [],
  loadingOlder: false,
  nextOffset: 0,
}
const EMPTY_TERMINAL_EVENTS: PollingEvent[] = []
const LIVE_ATTACH_RESEED_DEBOUNCE_MS = 150
const LIVE_ATTACH_RESEED_MIN_INTERVAL_MS = 2500
// Must stay ABOVE the backend's own seed deadline (terminalTmuxActionTimeout,
// 5s). When the two are equal, a backend seed that legitimately runs long under
// load loses the race: the client gives up and reconnects, adding another seed
// to a box that is already slow. The client should only ever time out a seed
// the server has genuinely abandoned.
const LIVE_ATTACH_SEED_TIMEOUT_MS = 8000
const LIVE_ATTACH_SNAPSHOT_LINES = 200
// Mirrors liveAttachSupersededCloseCode in agent_go/cmd/server/terminal_live_attach.go.
// The backend sends this when another window took the terminal over; it is the
// ONLY signal that distinguishes eviction from an ordinary disconnect, and the
// only thing preventing two open tabs from evicting each other in a loop.
const LIVE_ATTACH_SUPERSEDED_CLOSE_CODE = 4001
// Trailing debounce on ResizeObserver before a fit is evaluated.
const LIVE_ATTACH_FIT_DEBOUNCE_MS = 120
const TERMINAL_SETTLED_CAPTURE_INTERVAL_MS = 400
const TERMINAL_SETTLED_CAPTURE_MIN_WINDOW_MS = 1200
const TERMINAL_SETTLED_CAPTURE_STABLE_SAMPLES = 2
const TERMINAL_SETTLED_CAPTURE_MAX_ATTEMPTS = 8

type TerminalColorScheme = 'neon' | 'mono' | 'homebrew' | 'catppuccin' | 'nord' | 'gruvbox' | 'solarized' | 'tokyo'
type TerminalDebugKey = 'enter' | 'esc' | 'ctrl-c' | 'ctrl-o' | 'tab' | 'up' | 'down'
type TerminalRailFilter = 'all' | 'running' | 'attention' | 'non-running'
// Matches the running/attention/completed status colors used throughout this
// file (dotRunning/stateFailed-style emerald/red/sky), so the collapsed
// rail's dots mean the same thing the expanded labels do.
// The collapsed rail previously showed a bare colored dot beside each count.
// Colour alone is not a legend: the only way to learn that emerald meant "live"
// was to hover for the tooltip, so the strip read as two unexplained numbers.
// A glyph says what the colour merely implies, in the same 56px, and keeps
// working for anyone who cannot separate the hues.
const RAIL_FILTER_ICON: Record<TerminalRailFilter, LucideIcon> = {
  all: Terminal,
  running: Activity,
  attention: AlertTriangle,
  'non-running': Check,
}
const RAIL_FILTER_TEXT_COLOR: Record<TerminalRailFilter, string> = {
  all: 'text-neutral-400',
  running: 'text-emerald-400',
  attention: 'text-red-400',
  'non-running': 'text-sky-400',
}
type TerminalDetailOptions = { content?: 'stored' | 'screen' | 'history' | 'tmux' | 'deep'; lines?: number; debug?: boolean; debugSource?: string }

const DEFAULT_TERMINAL_COLOR_SCHEME: TerminalColorScheme = 'homebrew'
const TERMINAL_SCROLL_DEBUG_STORAGE_KEY = 'runloop_terminal_debug'

const TERMINAL_THEMES = {
  neon: {
    selection: 'selection:bg-cyan-500/25',
    contentText: 'text-[12px] leading-5',
    assistantLabelText: 'text-[11px] leading-4',
    assistantBodyText: 'text-[12.5px] leading-6',
    toolText: 'text-[11px] leading-4',
    doneText: 'text-[10.5px]',
    footerText: 'text-[11px]',
    headerText: 'text-xs',
    railText: 'text-xs',
    railMetaText: 'text-[10px]',
    chipText: 'text-[10px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12.5px] !leading-6 !text-neutral-100 [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-6 [&_p]:!text-neutral-100 [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-6 [&_strong]:!text-amber-200 [&_code]:!rounded [&_code]:!bg-neutral-900/80 [&_code]:!px-1 [&_code]:!text-amber-200 [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-neutral-800 [&_pre]:!bg-neutral-950/70 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-cyan-300 [&_blockquote]:!my-1 [&_blockquote]:!border-neutral-700 [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-cyan-300 [&_h2]:!text-cyan-300 [&_h3]:!text-cyan-200',
    prompt: 'text-cyan-300/90',
    userAuto: 'text-cyan-300/80',
    user: 'text-emerald-300/85',
    assistant: 'text-cyan-300/85',
    toolPending: 'text-yellow-300',
    done: 'text-emerald-500/70',
    streaming: 'text-cyan-300/80',
    preValidationPassedText: 'text-emerald-300',
    preValidationPassedChip: 'border-emerald-700/60 bg-emerald-950/30 text-emerald-300',
    dotRunning: 'bg-emerald-400',
    dotCompleted: 'bg-sky-400',
    dotClosing: 'bg-amber-400',
    stateRunning: 'text-emerald-300',
    stateCompleted: 'text-sky-300',
    stateClosing: 'text-amber-300',
    routeRail: 'bg-cyan-950/15 text-cyan-100',
    routeIcon: 'bg-cyan-400/15 text-cyan-300',
    routeClose: 'text-cyan-300/50 hover:bg-cyan-900/45 hover:text-cyan-100',
    routeMeta: 'text-cyan-300/75',
    railSelected: 'border-l-emerald-300 bg-[#222826] text-neutral-50 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]',
    railSpinner: 'text-cyan-300/90',
    selectedRouteChip: 'border-cyan-700/60 bg-cyan-950/25 text-cyan-200',
    warningText: 'text-amber-300',
    warningChip: 'border-amber-800/70 bg-amber-950/30 text-amber-200',
    copiedIcon: 'text-emerald-300',
    debugActive: 'border-cyan-700/80 text-cyan-300',
    inputFocus: 'focus:border-cyan-500/80',
    emptyPulse: 'bg-blue-400',
  },
  mono: {
    selection: 'selection:bg-white/15',
    contentText: 'text-[12px] leading-[1.55]',
    assistantLabelText: 'text-[10.5px] leading-4',
    assistantBodyText: 'text-[12px] leading-5',
    toolText: 'text-[10.5px] leading-4',
    doneText: 'text-[10px]',
    footerText: 'text-[10.5px]',
    headerText: 'text-[11px]',
    railText: 'text-[11px]',
    railMetaText: 'text-[9.5px]',
    chipText: 'text-[9.5px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12.5px] !leading-6 !text-neutral-100 [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-6 [&_p]:!text-neutral-100 [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-6 [&_strong]:!text-neutral-50 [&_code]:!rounded [&_code]:!bg-neutral-900/80 [&_code]:!px-1 [&_code]:!text-neutral-100 [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-neutral-800 [&_pre]:!bg-neutral-950/70 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-neutral-100 [&_blockquote]:!my-1 [&_blockquote]:!border-neutral-700 [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-neutral-50 [&_h2]:!text-neutral-50 [&_h3]:!text-neutral-100',
    prompt: 'text-neutral-100',
    userAuto: 'text-neutral-400',
    user: 'text-neutral-200',
    assistant: 'text-neutral-100',
    toolPending: 'text-neutral-300',
    done: 'text-neutral-500',
    streaming: 'text-neutral-300',
    preValidationPassedText: 'text-neutral-300',
    preValidationPassedChip: 'border-neutral-700/80 bg-neutral-900/70 text-neutral-300',
    dotRunning: 'bg-neutral-100',
    dotCompleted: 'bg-neutral-500',
    dotClosing: 'bg-neutral-400',
    stateRunning: 'text-neutral-100',
    stateCompleted: 'text-neutral-400',
    stateClosing: 'text-neutral-400',
    routeRail: 'bg-neutral-900/45 text-neutral-200',
    routeIcon: 'bg-neutral-800 text-neutral-200',
    routeClose: 'text-neutral-500 hover:bg-neutral-800 hover:text-neutral-100',
    routeMeta: 'text-neutral-500',
    railSelected: 'border-l-neutral-100 bg-[#242424] text-neutral-50 shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]',
    railSpinner: 'text-neutral-100',
    selectedRouteChip: 'border-neutral-700/80 bg-neutral-900/80 text-neutral-200',
    warningText: 'text-neutral-400',
    warningChip: 'border-neutral-700/80 bg-neutral-900/70 text-neutral-300',
    copiedIcon: 'text-neutral-100',
    debugActive: 'border-neutral-500 text-neutral-100',
    inputFocus: 'focus:border-neutral-400',
    emptyPulse: 'bg-neutral-400',
  },
  homebrew: {
    selection: 'selection:bg-lime-300/20',
    contentText: 'text-[12.5px] leading-[1.65]',
    assistantLabelText: 'text-[11px] leading-4',
    assistantBodyText: 'text-[12.5px] leading-6',
    toolText: 'text-[11px] leading-4',
    doneText: 'text-[10.5px]',
    footerText: 'text-[11px]',
    headerText: 'text-[11px]',
    railText: 'text-[11.5px]',
    railMetaText: 'text-[10px]',
    chipText: 'text-[10px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12.5px] !leading-6 !text-neutral-100 [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-6 [&_p]:!text-neutral-100 [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-6 [&_strong]:!text-neutral-50 [&_code]:!rounded [&_code]:!bg-neutral-900/85 [&_code]:!px-1 [&_code]:!text-neutral-100 [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-neutral-800 [&_pre]:!bg-black/45 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-lime-200 [&_blockquote]:!my-1 [&_blockquote]:!border-neutral-700 [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-neutral-50 [&_h2]:!text-neutral-50 [&_h3]:!text-neutral-100',
    prompt: 'text-lime-200/90',
    userAuto: 'text-neutral-400',
    user: 'text-neutral-100',
    assistant: 'text-neutral-100',
    toolPending: 'text-lime-200/80',
    done: 'text-neutral-500',
    streaming: 'text-lime-200/85',
    preValidationPassedText: 'text-lime-200/80',
    preValidationPassedChip: 'border-lime-900/50 bg-lime-950/20 text-lime-200/80',
    dotRunning: 'bg-lime-300',
    dotCompleted: 'bg-neutral-500',
    dotClosing: 'bg-neutral-400',
    stateRunning: 'text-lime-200',
    stateCompleted: 'text-neutral-400',
    stateClosing: 'text-neutral-400',
    routeRail: 'bg-lime-950/10 text-neutral-200',
    routeIcon: 'bg-lime-950/45 text-lime-200/80',
    routeClose: 'text-neutral-500 hover:bg-neutral-800 hover:text-neutral-100',
    routeMeta: 'text-neutral-500',
    railSelected: 'border-l-lime-300 bg-[#20231d] text-neutral-50 shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]',
    railSpinner: 'text-lime-200/90',
    selectedRouteChip: 'border-lime-900/55 bg-lime-950/20 text-lime-100/85',
    warningText: 'text-neutral-400',
    warningChip: 'border-neutral-700/80 bg-neutral-900/70 text-neutral-300',
    copiedIcon: 'text-lime-200',
    debugActive: 'border-lime-700/70 text-lime-200',
    inputFocus: 'focus:border-lime-700/80',
    emptyPulse: 'bg-lime-300',
  },
  catppuccin: {
    selection: 'selection:bg-pink-300/20',
    contentText: 'text-[12.5px] leading-[1.62]',
    assistantLabelText: 'text-[11px] leading-4',
    assistantBodyText: 'text-[12.5px] leading-6',
    toolText: 'text-[11px] leading-4',
    doneText: 'text-[10.5px]',
    footerText: 'text-[11px]',
    headerText: 'text-[11px]',
    railText: 'text-[11.5px]',
    railMetaText: 'text-[10px]',
    chipText: 'text-[10px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12.5px] !leading-6 !text-[#cdd6f4] [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-6 [&_p]:!text-[#cdd6f4] [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-6 [&_strong]:!text-[#f5e0dc] [&_code]:!rounded [&_code]:!bg-[#11111b]/85 [&_code]:!px-1 [&_code]:!text-[#f5c2e7] [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-[#45475a] [&_pre]:!bg-[#11111b]/75 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-[#89b4fa] [&_blockquote]:!my-1 [&_blockquote]:!border-[#585b70] [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-[#f5c2e7] [&_h2]:!text-[#f5c2e7] [&_h3]:!text-[#cba6f7]',
    prompt: 'text-[#89b4fa]',
    userAuto: 'text-[#a6adc8]',
    user: 'text-[#cdd6f4]',
    assistant: 'text-[#f5c2e7]',
    toolPending: 'text-[#f9e2af]',
    done: 'text-[#a6adc8]',
    streaming: 'text-[#89b4fa]',
    preValidationPassedText: 'text-[#a6e3a1]',
    preValidationPassedChip: 'border-[#a6e3a1]/40 bg-[#1e1e2e] text-[#a6e3a1]',
    dotRunning: 'bg-[#89b4fa]',
    dotCompleted: 'bg-[#a6adc8]',
    dotClosing: 'bg-[#f9e2af]',
    stateRunning: 'text-[#89b4fa]',
    stateCompleted: 'text-[#a6adc8]',
    stateClosing: 'text-[#f9e2af]',
    routeRail: 'bg-[#1e1e2e]/55 text-[#cdd6f4]',
    routeIcon: 'bg-[#313244] text-[#cba6f7]',
    routeClose: 'text-[#a6adc8] hover:bg-[#313244] hover:text-[#f5e0dc]',
    routeMeta: 'text-[#a6adc8]',
    railSelected: 'border-l-[#89b4fa] bg-[#1e1e2e] text-[#cdd6f4] shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]',
    railSpinner: 'text-[#89b4fa]',
    selectedRouteChip: 'border-[#cba6f7]/50 bg-[#1e1e2e] text-[#cba6f7]',
    warningText: 'text-[#f9e2af]',
    warningChip: 'border-[#f9e2af]/45 bg-[#1e1e2e] text-[#f9e2af]',
    copiedIcon: 'text-[#a6e3a1]',
    debugActive: 'border-[#89b4fa]/70 text-[#89b4fa]',
    inputFocus: 'focus:border-[#89b4fa]',
    emptyPulse: 'bg-[#89b4fa]',
  },
  nord: {
    selection: 'selection:bg-sky-300/20',
    contentText: 'text-[12px] leading-[1.6]',
    assistantLabelText: 'text-[10.5px] leading-4',
    assistantBodyText: 'text-[12px] leading-5',
    toolText: 'text-[10.5px] leading-4',
    doneText: 'text-[10px]',
    footerText: 'text-[10.5px]',
    headerText: 'text-[11px]',
    railText: 'text-[11px]',
    railMetaText: 'text-[9.5px]',
    chipText: 'text-[9.5px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12px] !leading-5 !text-[#d8dee9] [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-5 [&_p]:!text-[#d8dee9] [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-5 [&_strong]:!text-[#eceff4] [&_code]:!rounded [&_code]:!bg-[#2e3440]/85 [&_code]:!px-1 [&_code]:!text-[#88c0d0] [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-[#4c566a] [&_pre]:!bg-[#2e3440]/70 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-[#88c0d0] [&_blockquote]:!my-1 [&_blockquote]:!border-[#4c566a] [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-[#88c0d0] [&_h2]:!text-[#88c0d0] [&_h3]:!text-[#81a1c1]',
    prompt: 'text-[#88c0d0]',
    userAuto: 'text-[#8fbcbb]',
    user: 'text-[#eceff4]',
    assistant: 'text-[#d8dee9]',
    toolPending: 'text-[#ebcb8b]',
    done: 'text-[#8fbcbb]/75',
    streaming: 'text-[#88c0d0]',
    preValidationPassedText: 'text-[#a3be8c]',
    preValidationPassedChip: 'border-[#a3be8c]/45 bg-[#2e3440]/70 text-[#a3be8c]',
    dotRunning: 'bg-[#88c0d0]',
    dotCompleted: 'bg-[#81a1c1]',
    dotClosing: 'bg-[#ebcb8b]',
    stateRunning: 'text-[#88c0d0]',
    stateCompleted: 'text-[#81a1c1]',
    stateClosing: 'text-[#ebcb8b]',
    routeRail: 'bg-[#2e3440]/45 text-[#d8dee9]',
    routeIcon: 'bg-[#3b4252] text-[#88c0d0]',
    routeClose: 'text-[#4c566a] hover:bg-[#3b4252] hover:text-[#eceff4]',
    routeMeta: 'text-[#81a1c1]',
    railSelected: 'border-l-[#88c0d0] bg-[#242933] text-[#eceff4] shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]',
    railSpinner: 'text-[#88c0d0]',
    selectedRouteChip: 'border-[#88c0d0]/50 bg-[#2e3440]/70 text-[#88c0d0]',
    warningText: 'text-[#ebcb8b]',
    warningChip: 'border-[#ebcb8b]/45 bg-[#2e3440]/70 text-[#ebcb8b]',
    copiedIcon: 'text-[#a3be8c]',
    debugActive: 'border-[#88c0d0]/70 text-[#88c0d0]',
    inputFocus: 'focus:border-[#88c0d0]',
    emptyPulse: 'bg-[#88c0d0]',
  },
  gruvbox: {
    selection: 'selection:bg-yellow-300/20',
    contentText: 'text-[12.5px] leading-[1.62]',
    assistantLabelText: 'text-[11px] leading-4',
    assistantBodyText: 'text-[12.5px] leading-6',
    toolText: 'text-[11px] leading-4',
    doneText: 'text-[10.5px]',
    footerText: 'text-[11px]',
    headerText: 'text-[11px]',
    railText: 'text-[11.5px]',
    railMetaText: 'text-[10px]',
    chipText: 'text-[10px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12.5px] !leading-6 !text-[#ebdbb2] [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-6 [&_p]:!text-[#ebdbb2] [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-6 [&_strong]:!text-[#fbf1c7] [&_code]:!rounded [&_code]:!bg-[#1d2021]/85 [&_code]:!px-1 [&_code]:!text-[#fabd2f] [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-[#504945] [&_pre]:!bg-[#1d2021]/75 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-[#83a598] [&_blockquote]:!my-1 [&_blockquote]:!border-[#665c54] [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-[#fabd2f] [&_h2]:!text-[#fabd2f] [&_h3]:!text-[#d3869b]',
    prompt: 'text-[#b8bb26]',
    userAuto: 'text-[#a89984]',
    user: 'text-[#ebdbb2]',
    assistant: 'text-[#fbf1c7]',
    toolPending: 'text-[#fabd2f]',
    done: 'text-[#a89984]',
    streaming: 'text-[#b8bb26]',
    preValidationPassedText: 'text-[#b8bb26]',
    preValidationPassedChip: 'border-[#b8bb26]/45 bg-[#282828]/70 text-[#b8bb26]',
    dotRunning: 'bg-[#b8bb26]',
    dotCompleted: 'bg-[#a89984]',
    dotClosing: 'bg-[#fabd2f]',
    stateRunning: 'text-[#b8bb26]',
    stateCompleted: 'text-[#a89984]',
    stateClosing: 'text-[#fabd2f]',
    routeRail: 'bg-[#282828]/55 text-[#ebdbb2]',
    routeIcon: 'bg-[#3c3836] text-[#fabd2f]',
    routeClose: 'text-[#928374] hover:bg-[#3c3836] hover:text-[#fbf1c7]',
    routeMeta: 'text-[#a89984]',
    railSelected: 'border-l-[#fabd2f] bg-[#2d2926] text-[#fbf1c7] shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]',
    railSpinner: 'text-[#b8bb26]',
    selectedRouteChip: 'border-[#fabd2f]/50 bg-[#282828]/70 text-[#fabd2f]',
    warningText: 'text-[#fabd2f]',
    warningChip: 'border-[#fabd2f]/45 bg-[#282828]/70 text-[#fabd2f]',
    copiedIcon: 'text-[#b8bb26]',
    debugActive: 'border-[#fabd2f]/70 text-[#fabd2f]',
    inputFocus: 'focus:border-[#fabd2f]',
    emptyPulse: 'bg-[#b8bb26]',
  },
  solarized: {
    selection: 'selection:bg-cyan-300/20',
    contentText: 'text-[12px] leading-[1.6]',
    assistantLabelText: 'text-[10.5px] leading-4',
    assistantBodyText: 'text-[12px] leading-5',
    toolText: 'text-[10.5px] leading-4',
    doneText: 'text-[10px]',
    footerText: 'text-[10.5px]',
    headerText: 'text-[11px]',
    railText: 'text-[11px]',
    railMetaText: 'text-[9.5px]',
    chipText: 'text-[9.5px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12px] !leading-5 !text-[#93a1a1] [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-5 [&_p]:!text-[#93a1a1] [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-5 [&_strong]:!text-[#eee8d5] [&_code]:!rounded [&_code]:!bg-[#002b36]/85 [&_code]:!px-1 [&_code]:!text-[#2aa198] [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-[#586e75] [&_pre]:!bg-[#002b36]/75 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-[#268bd2] [&_blockquote]:!my-1 [&_blockquote]:!border-[#586e75] [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-[#b58900] [&_h2]:!text-[#b58900] [&_h3]:!text-[#2aa198]',
    prompt: 'text-[#2aa198]',
    userAuto: 'text-[#839496]',
    user: 'text-[#eee8d5]',
    assistant: 'text-[#93a1a1]',
    toolPending: 'text-[#b58900]',
    done: 'text-[#839496]',
    streaming: 'text-[#2aa198]',
    preValidationPassedText: 'text-[#859900]',
    preValidationPassedChip: 'border-[#859900]/45 bg-[#073642]/70 text-[#859900]',
    dotRunning: 'bg-[#2aa198]',
    dotCompleted: 'bg-[#268bd2]',
    dotClosing: 'bg-[#b58900]',
    stateRunning: 'text-[#2aa198]',
    stateCompleted: 'text-[#268bd2]',
    stateClosing: 'text-[#b58900]',
    routeRail: 'bg-[#073642]/55 text-[#93a1a1]',
    routeIcon: 'bg-[#002b36] text-[#2aa198]',
    routeClose: 'text-[#586e75] hover:bg-[#073642] hover:text-[#eee8d5]',
    routeMeta: 'text-[#839496]',
    railSelected: 'border-l-[#2aa198] bg-[#073642] text-[#eee8d5] shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]',
    railSpinner: 'text-[#2aa198]',
    selectedRouteChip: 'border-[#2aa198]/50 bg-[#073642]/70 text-[#2aa198]',
    warningText: 'text-[#b58900]',
    warningChip: 'border-[#b58900]/45 bg-[#073642]/70 text-[#b58900]',
    copiedIcon: 'text-[#859900]',
    debugActive: 'border-[#2aa198]/70 text-[#2aa198]',
    inputFocus: 'focus:border-[#2aa198]',
    emptyPulse: 'bg-[#2aa198]',
  },
  tokyo: {
    selection: 'selection:bg-indigo-300/20',
    contentText: 'text-[12px] leading-[1.58]',
    assistantLabelText: 'text-[10.5px] leading-4',
    assistantBodyText: 'text-[12px] leading-5',
    toolText: 'text-[10.5px] leading-4',
    doneText: 'text-[10px]',
    footerText: 'text-[10.5px]',
    headerText: 'text-[11px]',
    railText: 'text-[11px]',
    railMetaText: 'text-[9.5px]',
    chipText: 'text-[9.5px]',
    microText: 'text-[9px]',
    markdown:
      '!font-mono !text-[12px] !leading-5 !text-[#c0caf5] [&_*]:!font-mono [&_p]:!my-1 [&_p]:!leading-5 [&_p]:!text-[#c0caf5] [&_ul]:!my-1 [&_ol]:!my-1 [&_li]:!my-0.5 [&_li]:!leading-5 [&_strong]:!text-[#d5d6db] [&_code]:!rounded [&_code]:!bg-[#1a1b26]/85 [&_code]:!px-1 [&_code]:!text-[#bb9af7] [&_pre]:!my-1.5 [&_pre]:!rounded [&_pre]:!border [&_pre]:!border-[#414868] [&_pre]:!bg-[#1a1b26]/75 [&_pre]:!p-2 [&_pre]:!text-[11.5px] [&_a]:!text-[#7aa2f7] [&_blockquote]:!my-1 [&_blockquote]:!border-[#414868] [&_h1]:!text-[13px] [&_h2]:!text-[13px] [&_h3]:!text-[12.5px] [&_h1]:!text-[#7dcfff] [&_h2]:!text-[#7dcfff] [&_h3]:!text-[#bb9af7]',
    prompt: 'text-[#7dcfff]',
    userAuto: 'text-[#9aa5ce]',
    user: 'text-[#c0caf5]',
    assistant: 'text-[#c0caf5]',
    toolPending: 'text-[#e0af68]',
    done: 'text-[#9aa5ce]',
    streaming: 'text-[#7dcfff]',
    preValidationPassedText: 'text-[#9ece6a]',
    preValidationPassedChip: 'border-[#9ece6a]/45 bg-[#1a1b26]/70 text-[#9ece6a]',
    dotRunning: 'bg-[#7dcfff]',
    dotCompleted: 'bg-[#7aa2f7]',
    dotClosing: 'bg-[#e0af68]',
    stateRunning: 'text-[#7dcfff]',
    stateCompleted: 'text-[#7aa2f7]',
    stateClosing: 'text-[#e0af68]',
    routeRail: 'bg-[#1a1b26]/55 text-[#c0caf5]',
    routeIcon: 'bg-[#24283b] text-[#7dcfff]',
    routeClose: 'text-[#565f89] hover:bg-[#24283b] hover:text-[#c0caf5]',
    routeMeta: 'text-[#9aa5ce]',
    railSelected: 'border-l-[#7dcfff] bg-[#1f2335] text-[#c0caf5] shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]',
    railSpinner: 'text-[#7dcfff]',
    selectedRouteChip: 'border-[#7dcfff]/50 bg-[#1a1b26]/70 text-[#7dcfff]',
    warningText: 'text-[#e0af68]',
    warningChip: 'border-[#e0af68]/45 bg-[#1a1b26]/70 text-[#e0af68]',
    copiedIcon: 'text-[#9ece6a]',
    debugActive: 'border-[#7dcfff]/70 text-[#7dcfff]',
    inputFocus: 'focus:border-[#7dcfff]',
    emptyPulse: 'bg-[#7dcfff]',
  },
} as const

type TerminalTheme = (typeof TERMINAL_THEMES)[TerminalColorScheme]

export const RAW_XTERM_FONT_FAMILY = '"JetBrains Mono", "SFMono-Regular", "SF Mono", Menlo, Monaco, "Cascadia Mono", "Fira Code", Consolas, "Liberation Mono", monospace'
export const RAW_XTERM_FONT_SIZE = 13
const RAW_XTERM_SCROLLBAR_WIDTH = 7
const RAW_XTERM_CSS_LINE_HEIGHT = 'normal'
export const RAW_XTERM_THEMES: Record<Theme, ITheme> = {
  dark: {
    background: '#0b0e14',
    // xterm always draws the ruler's left edge, independently of the top and
    // bottom border options. Its default is white even in a dark theme.
    overviewRulerBorder: '#00000000',
    scrollbarSliderBackground: '#94a3b838',
    scrollbarSliderHoverBackground: '#94a3b866',
    scrollbarSliderActiveBackground: '#94a3b88c',
  },
  light: {
    background: '#ffffff',
    overviewRulerBorder: '#00000000',
    scrollbarSliderBackground: '#64748b38',
    scrollbarSliderHoverBackground: '#64748b66',
    scrollbarSliderActiveBackground: '#64748b8c',
  },
}

function applyRawXtermTheme(term: XTerm, theme: ITheme) {
  term.options.theme = theme
  const viewport = term.element?.querySelector('.xterm-viewport') as HTMLElement | null
  if (viewport && theme.background) {
    viewport.style.backgroundColor = theme.background
  }
}

function clearXtermSelection(term: XTerm) {
  try {
    if (term.hasSelection()) {
      term.clearSelection()
    }
  } catch {
    // Selection APIs can throw while xterm is mounting/unmounting.
  }
}

function normalizeVisibleScreenSeed(content: string): string {
  return normalizeAnsiForEmbeddedXterm(content).replace(/\r?\n/g, '\r\n')
}

function buildVisibleScreenReseed(content: string): string {
  return `\x1b[0m\x1b[H\x1b[2J${normalizeVisibleScreenSeed(content)}\x1b[0m\x1b[J`
}

// Reseed for a SETTLED pane, whose content prop is the full aggregated capture
// rather than just the visible screen.
//
// ED(2) erases the viewport in place without pushing to scrollback, so it does
// not clear what is already there. When the content is taller than the
// viewport, painting it scrolls the excess into scrollback — and a settled pane
// that keeps getting new content stacks a near-duplicate copy on every reseed.
// ED(3) drops the scrollback first, which is lossless HERE precisely because
// the content being painted already carries the history. Do NOT use this for
// the disconnected-snapshot path: that content is visible-screen-only, so
// clearing scrollback would destroy history the user can still scroll back to.
function buildSettledScreenReseed(content: string): string {
  return `\x1b[0m\x1b[H\x1b[2J\x1b[3J${normalizeVisibleScreenSeed(content)}\x1b[0m\x1b[J`
}

function measureRawXtermCellMetrics(el: HTMLElement): { charWidth: number; lineHeight: number } | null {
  const ruler = document.createElement('span')
  ruler.textContent = '0'.repeat(100)
  ruler.setAttribute('aria-hidden', 'true')
  ruler.style.cssText = 'position:absolute;visibility:hidden;white-space:pre;top:0;left:0;pointer-events:none;'
  ruler.style.fontFamily = RAW_XTERM_FONT_FAMILY
  ruler.style.fontSize = `${RAW_XTERM_FONT_SIZE}px`
  ruler.style.lineHeight = RAW_XTERM_CSS_LINE_HEIGHT
  el.appendChild(ruler)
  const rect = ruler.getBoundingClientRect()
  ruler.remove()
  const charWidth = rect.width / 100
  const lineHeight = rect.height || RAW_XTERM_FONT_SIZE
  if (!Number.isFinite(charWidth) || !Number.isFinite(lineHeight) || charWidth <= 0 || lineHeight <= 0) return null
  return { charWidth, lineHeight }
}

function measureRawXtermElementSize(el: HTMLElement | null): { cols: number; rows: number } | null {
  if (!el || !hasUsableTerminalFitBox(el)) return null
  const metrics = measureRawXtermCellMetrics(el)
  if (!metrics) return null
  const style = window.getComputedStyle(el)
  const padX = (parseFloat(style.paddingLeft) || 0) + (parseFloat(style.paddingRight) || 0)
  const padY = (parseFloat(style.paddingTop) || 0) + (parseFloat(style.paddingBottom) || 0)
  const cols = Math.floor((el.clientWidth - padX) / metrics.charWidth) - 1
  const rows = Math.floor((el.clientHeight - padY) / metrics.lineHeight)
  if (cols < RAW_XTERM_MIN_FIT_COLS || rows < RAW_XTERM_MIN_FIT_ROWS) return null
  return { cols, rows }
}

// FitAddon is the single sizing authority for embedded xterm panes. Do NOT
// re-measure with DOM rulers and term.resize() on top of fit(): the two
// metrics always disagree slightly, so every layout tick resizes the grid
// twice, each firing a resize-window -> full CLI repaint (stacked spinners,
// mis-wrapped frames). The PoC demo used bare fit() and rendered flawlessly.
function fitRawXtermToVisibleGrid(fit: FitAddon): void {
  fit.fit()
}

function scrollXtermFromWheel(
  term: XTerm,
  event: WheelEvent,
  onViewportStickChange?: (isNearBottom: boolean) => void,
) {
  const buffer = term.buffer.active
  const wantsUp = event.deltaY < 0
  const wantsDown = event.deltaY > 0
  const canScrollUp = buffer.viewportY > 0
  const canScrollDown = buffer.viewportY < buffer.baseY
  if ((wantsUp && !canScrollUp) || (wantsDown && !canScrollDown) || (!wantsUp && !wantsDown)) {
    onViewportStickChange?.(buffer.baseY - buffer.viewportY <= 1)
    return
  }
  const row = term.element?.querySelector('.xterm-rows > div') as HTMLElement | null
  const lineHeightPx = row?.getBoundingClientRect().height || RAW_XTERM_FONT_SIZE
  const rawLines = event.deltaMode === 1
    ? event.deltaY
    : event.deltaMode === 2
      ? event.deltaY * term.rows
      : event.deltaY / Math.max(1, lineHeightPx)
  const direction = rawLines < 0 ? -1 : 1
  const lines = Math.max(1, Math.min(12, Math.ceil(Math.abs(rawLines)))) * direction
  event.preventDefault()
  event.stopPropagation()
  term.scrollLines(lines)
  const distanceFromBottom = Math.max(0, term.buffer.active.baseY - term.buffer.active.viewportY)
  onViewportStickChange?.(distanceFromBottom <= 1)
}

function hasUsableTerminalFitBox(el: HTMLElement): boolean {
  if (!el.isConnected || el.getClientRects().length === 0) return false
  const style = window.getComputedStyle(el)
  if (style.display === 'none' || style.visibility === 'hidden') return false
  const rect = el.getBoundingClientRect()
  return rect.width >= RAW_XTERM_MIN_FIT_WIDTH_PX && rect.height >= RAW_XTERM_MIN_FIT_HEIGHT_PX
}

interface RoutingRouteSummary {
  route_id?: string
  route_name?: string
  condition?: string
  next_step_id?: string
  next_step_type?: string
}

interface RoutingDecision {
  id: string
  stepId?: string
  stepTitle?: string
  selectedRouteId: string
  selectedRouteName?: string
  nextStepId?: string
  nextStepType?: string
  routeCount: number
  reasoning?: string
  timestamp?: string
}

function humanizeIdentifier(value?: string): string {
  if (!value) return ''
  const cleaned = value
    .replace(/^exec[-_:]/i, '')
    .replace(/^step[-_:]\d+[-_:]/i, '')
    .replace(/^main[-_:]/i, '')
    .replace(/[-_]+/g, ' ')
    .trim()
  if (!cleaned) return ''
  return cleaned.charAt(0).toUpperCase() + cleaned.slice(1)
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function stringField(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function routingPayload(event: PollingEvent): Record<string, unknown> | undefined {
  const data = asRecord(event.data)
  const nested = asRecord(data?.data)
  return nested || data
}

function routingRoutes(value: unknown): RoutingRouteSummary[] {
  if (!Array.isArray(value)) return []
  const routes: RoutingRouteSummary[] = []
  for (const item of value) {
    const route = asRecord(item)
    if (!route) continue
    routes.push({
      route_id: stringField(route.route_id) || undefined,
      route_name: stringField(route.route_name) || undefined,
      condition: stringField(route.condition) || undefined,
      next_step_id: stringField(route.next_step_id) || undefined,
      next_step_type: stringField(route.next_step_type) || undefined,
    })
  }
  return routes
}

function routingDecisionFromEvent(event: PollingEvent): RoutingDecision | null {
  if (event.type !== 'routing_evaluated') return null
  const payload = routingPayload(event)
  if (!payload) return null
  const response = asRecord(payload.routing_response)
  const selectedRouteId = stringField(response?.selected_route_id)
  if (!selectedRouteId) return null
  const routes = routingRoutes(payload.routes)
  const selectedRoute = routes.find(route => route.route_id === selectedRouteId)
  const stepId = stringField(payload.step_id)
  return {
    id: event.id || `${stepId || 'routing'}:${selectedRouteId}:${event.timestamp || ''}`,
    stepId: stepId || undefined,
    stepTitle: stringField(payload.step_title) || undefined,
    selectedRouteId,
    selectedRouteName: selectedRoute?.route_name,
    nextStepId: selectedRoute?.next_step_id,
    nextStepType: selectedRoute?.next_step_type,
    routeCount: routes.length,
    reasoning: stringField(response?.reasoning) || undefined,
    timestamp: event.timestamp,
  }
}

function routeDecisionLabel(decision: RoutingDecision): string {
  return decision.selectedRouteName || humanizeIdentifier(decision.selectedRouteId) || decision.selectedRouteId
}

function routeDecisionTitle(decision: RoutingDecision): string {
  const label = routeDecisionLabel(decision)
  const nextStepType = decision.nextStepType ? ` (${stepTypeLabel(decision.nextStepType)})` : ''
  return `Routing: ${label}${decision.nextStepId ? ` -> ${decision.nextStepId}${nextStepType}` : ''}`
}

function routeDecisionDedupeKey(decision: RoutingDecision): string {
  return [
    decision.stepId || '',
    decision.selectedRouteId || '',
    decision.nextStepId || '',
  ].join('|')
}

function routingDecisionTime(decision: RoutingDecision): number {
  return decision.timestamp ? new Date(decision.timestamp).getTime() || 0 : 0
}

function formatExecutionKind(kind?: string): string {
  switch (kind) {
    case 'main_agent':
      return 'Main agent'
    case 'workflow_step':
    case 'execution_only':
    case 'step':
      return 'Automation step'
    case 'background_agent':
      return 'Background agent'
    case 'todo_task':
    case 'sub_agent':
    case 'delegation':
      return 'Sub-agent'
    default:
      return humanizeIdentifier(kind)
  }
}

function formatTerminalKindLabel(terminal: TerminalSnapshot): string {
  const kind = terminal.execution_kind || terminal.scope
  if ((kind === 'workflow_step' || kind === 'execution_only' || kind === 'step') && terminal.step_type) {
    return `${humanizeIdentifier(terminal.step_type)} step`
  }
  return formatExecutionKind(terminal.execution_kind)
}

// Matches the backend's internal "agent-<stepID>" agent_name for the
// reused execution_only agent behind a agent step's turns (see
// controller_agent.go). That id is not a display name, so it must
// never win over terminalRailTitle's agent-aware humanization
// regardless of which branch below resolves the title — this terminal is
// frequently tagged execution_kind sub_agent/background_agent (a separate,
// still-open classification gap), which would otherwise return it verbatim.
const AGENT_AGENT_NAME_PATTERN = /^message[-_ ]sequence(?:[-_ ].*)?$/i

function resolvedAgentName(terminal: TerminalSnapshot): string {
  const raw = terminal.agent_name || ''
  if (raw && AGENT_AGENT_NAME_PATTERN.test(raw)) {
    return terminalRailTitle(terminal)
  }
  return raw
}

function formatTerminalTitle(terminal: TerminalSnapshot): string {
  // Prefer a human title (step title, or the agent's own name for step-less
  // maintenance agents like learning/organize) over the raw step_id. The ID —
  // e.g. "_global" for the global-learnings skill — is a folder/lookup key, not
  // a display name, so it's the last-resort fallback. Everything else — parent,
  // chip, workflow name, kind — lives in the meta row so the title stays minimal.
  const kind = (terminal.execution_kind || terminal.scope || '').toLowerCase()
  if (isMainAgentTerminal(terminal)) {
    return terminal.agent_name || terminal.step_name || 'Main agent'
  }
  if (kind === 'background_agent' || kind === 'background' || kind === 'delegation' || kind === 'todo_task' || kind === 'sub_agent') {
    return resolvedAgentName(terminal) || terminal.step_name || terminal.display_title || visibleStepID(terminal) || formatTerminalKindLabel(terminal) || 'Terminal'
  }
  // Delegate to the rail's title logic rather than re-deriving it: it already
  // humanizes the backend's internal "agent-<stepID>" agent_name
  // (via parent_step_id) instead of printing that raw slug when step_name is
  // empty — the divergence between this function and the rail is exactly what
  // let that raw id leak into the panel header while the rail showed the
  // correct name for the same terminal.
  return terminal.step_name || terminalRailTitle(terminal) || visibleStepID(terminal) || formatTerminalKindLabel(terminal) || 'Terminal'
}

function visibleStepID(terminal: TerminalSnapshot): string {
  const value = terminal.step_id || ''
  if (!value) return ''
  if (isMainAgentTerminal(terminal) && value.startsWith('main_agent:')) return ''
  return value
}

// The renderer is selected automatically: structured streams use the clean
// transcript and tmux-backed streams use the raw terminal.
function formatTransportChip(terminal: TerminalSnapshot): string {
  const provider = terminal.status?.provider_label || ''
  // The clean rail is task navigation, not provider telemetry. Some retained
  // steps have provider metadata and others do not, so showing it produces an
  // arbitrary mix of "Claude", blank, and legacy labels for sibling steps.
  // Model/provider details remain available in the selected pane footer.
  if (isSyntheticTerminal(terminal)) return ''
  return provider ? `${provider} · Terminal` : 'Terminal'
}

function formatRailTransportChip(terminal: TerminalSnapshot): string {
  return formatTransportChip(terminal)
    .replace(/^Claude Code\b/, 'Claude')
    .replace(/^Codex CLI\b/, 'Codex')
}

// formatCost matches the Go-side formatUSD scale: cheap calls keep
// six decimals so a $0.000089 haiku call doesn't render as "$0.0000".
function formatCost(cost: number): string {
  if (cost >= 1) return cost.toFixed(2)
  if (cost >= 0.01) return cost.toFixed(4)
  if (cost > 0) return cost.toFixed(6)
  return '0'
}

function stepTypeLabel(stepType?: string): string {
  const type = (stepType || '').trim()
  return type ? `${humanizeIdentifier(type)} step` : ''
}

function terminalStepTypeLabel(terminal: TerminalSnapshot): string {
  const labels: Record<ReturnType<typeof terminalRailVisualKind>, string> = {
    terminal: stepTypeLabel(terminal.step_type),
    orchestrator: 'Orchestrator',
    'sub-agent': 'Sub-agent',
    'agent': 'Agent',
    routing: 'Routing step',
    scripted: 'Scripted step',
    evaluation: 'Evaluation',
    reviewer: 'Reviewer',
  }
  return labels[terminalRailVisualKind(terminal)]
}

function formatSelectedTerminalMeta(terminal: TerminalSnapshot): string {
  // The selected pane already exposes provider/model/transport and execution
  // details in its status area. Keep this header to the minimum orientation
  // context: the title is rendered separately, followed by type and freshness.
  return [
    terminal.execution_tree_placeholder ? terminal.display_meta : '',
    terminalStepTypeLabel(terminal),
    formatStartedAt(terminal),
    formatUpdatedAge(terminal),
  ].filter(Boolean).join(' · ')
}

function findPlanStepByID(steps: PlanStep[] | undefined, stepID: string): PlanStep | null {
  if (!steps?.length || !stepID) return null

  const pending = [...steps]
  const visited = new Set<PlanStep>()
  while (pending.length > 0) {
    const step = pending.shift()
    if (!step || visited.has(step)) continue
    visited.add(step)

    if (step.id === stepID) return step
    if (step.type !== 'agent' && step.type !== 'todo_task' && step.type !== 'orchestrator') continue

    if ('todo_task_step' in step && step.todo_task_step) pending.push(step.todo_task_step)
    for (const route of step.predefined_routes || []) {
      if (route.route_id === stepID && route.sub_agent_step) return route.sub_agent_step
      if (route.sub_agent_step) pending.push(route.sub_agent_step)
    }
  }

  return null
}

function terminalPreValidationSummary(terminal: TerminalSnapshot): string {
  return terminal.status?.pre_validation_summary || ''
}

function terminalPreValidationClass(terminal: TerminalSnapshot, theme: TerminalTheme): string {
  switch ((terminal.status?.pre_validation_status || '').toLowerCase()) {
    case 'passed':
      return theme.preValidationPassedText
    case 'failed':
      return 'text-red-300'
    default:
      return 'text-neutral-400'
  }
}

function terminalPreValidationChip(terminal: TerminalSnapshot, theme: TerminalTheme): { label: string; className: string; title: string } | null {
  const summary = terminalPreValidationSummary(terminal)
  if (!summary) return null

  const passed = terminal.status?.pre_validation_passed_checks || 0
  const failed = terminal.status?.pre_validation_failed_checks || 0
  const total = terminal.status?.pre_validation_total_checks || 0
  const countLabel = total > 0 ? `${passed}/${total}` : ''
  switch ((terminal.status?.pre_validation_status || '').toLowerCase()) {
    case 'passed':
      return {
        label: countLabel ? `✓ ${countLabel}` : '✓',
        className: theme.preValidationPassedChip,
        title: summary,
      }
    case 'failed':
      return {
        label: failed > 0 ? `✕ ${failed}` : '✕',
        className: 'border-red-700/60 bg-red-950/30 text-red-300',
        title: summary,
      }
    default:
      return {
        label: countLabel ? `• ${countLabel}` : '•',
        className: 'border-neutral-700 bg-neutral-900 text-neutral-400',
        title: summary,
      }
  }
}

function terminalClosesAt(terminal: TerminalSnapshot): Date | null {
  if (!terminal.closes_at) return null
  const date = new Date(terminal.closes_at)
  if (Number.isNaN(date.getTime())) return null
  return date
}

function terminalSecondsUntilClose(terminal: TerminalSnapshot): number {
  const closesAt = terminalClosesAt(terminal)
  if (!closesAt) return 0
  return Math.max(0, Math.ceil((closesAt.getTime() - Date.now()) / 1000))
}

function formatCloseCountdown(seconds: number): string {
  if (seconds <= 0) return 'closing'
  if (seconds >= 60) return `${Math.ceil(seconds / 60)}m`
  return `${seconds}s`
}

function terminalState(terminal: TerminalSnapshot): string {
  if (isArchivedTurnTerminal(terminal)) return 'completed'
  if (terminal.snapshot_kind === 'archived' && terminal.process_state === 'closed') {
    return terminal.state === 'failed' ? 'failed' : 'archived'
  }
  if (!terminal.active && terminalSecondsUntilClose(terminal) > 0) return 'closing'
  if (terminal.state === 'closing' && terminalSecondsUntilClose(terminal) <= 0) return 'completed'
  if (terminal.active && terminal.state === 'idle') return 'running'
  if (terminal.state) return terminal.state
  return terminal.active ? 'running' : 'completed'
}

function terminalStateLabel(terminal: TerminalSnapshot): string {
  switch (terminalState(terminal)) {
    case 'running':
      return 'active'
    case 'completed':
      return 'completed'
    case 'failed':
      return 'failed'
    case 'stale':
      return 'stale'
    case 'archived':
      return 'archived'
    case 'closing':
      // The tmux process is already gone (killed 30s after task end);
      // what this countdown measures is when the read-only snapshot
      // expires from the rail. "closes" reads like a live process is
      // shutting down — say "kept" so the user knows the work is done.
      return `kept ${formatCloseCountdown(terminalSecondsUntilClose(terminal))}`
    default:
      return terminal.active ? 'active' : 'idle'
  }
}

function terminalStateDescription(terminal: TerminalSnapshot): string {
  switch (terminalState(terminal)) {
    case 'running':
      return 'Active: the coding agent is still running and this terminal is updating.'
    case 'completed':
      return 'Completed: the coding agent finished; this is the retained terminal snapshot.'
    case 'failed':
      return 'Failed: the coding agent or automation step ended with an error.'
    case 'stale':
      return 'Stale: no terminal updates were received for a long time; this pane may have lost its lifecycle event.'
    case 'archived':
      if (isSyntheticTerminal(terminal)) {
        return 'Completed: the background task finished and this saved result is read-only.'
      }
      return terminal.close_reason
        ? `Archived terminal capture: ${terminal.close_reason}. The tmux process is closed.`
        : 'Archived terminal capture: the tmux process is closed and this view is read-only.'
    case 'closing':
      return `Snapshot: the agent finished and this read-only view will be removed in ${formatCloseCountdown(terminalSecondsUntilClose(terminal))}.`
    default:
      return terminal.active ? 'Active terminal' : 'Inactive terminal snapshot'
  }
}

function terminalDotClass(terminal: TerminalSnapshot, theme: TerminalTheme): string {
  switch (terminalState(terminal)) {
    case 'running':
      return theme.dotRunning
    case 'completed':
      return theme.dotCompleted
    case 'failed':
      return 'bg-red-400'
    case 'stale':
      return 'bg-zinc-400'
    case 'archived':
      return theme.dotCompleted
    case 'closing':
      return theme.dotClosing
    default:
      return 'bg-neutral-500'
  }
}

function canDismissTerminal(terminal: TerminalSnapshot): boolean {
  if (isMainAgentTerminal(terminal)) return false
  const state = terminalState(terminal)
  return state === 'completed' || state === 'closing' || state === 'failed' || state === 'stale'
}

function canForceCompleteTerminal(terminal: TerminalSnapshot): boolean {
  const state = terminalState(terminal)
  return state === 'running' || state === 'stale'
}

function canSendTerminalDebugInput(terminal: TerminalSnapshot): boolean {
  // Allow key input whenever a tmux pane exists — not only while the terminal
  // reports "running". A pane can be alive and waiting at a prompt even when the
  // terminal state is idle/completed (e.g. a turn mis-detected as "completed"
  // while actually stalled on an MCP-tool approval prompt), and sending
  // Tab/Enter/Esc is exactly what unblocks it. If the pane is truly gone the
  // backend send-keys fails gracefully and surfaces an error.
  return Boolean(terminal.tmux_session)
}

function hasTerminalDebugActions(terminal: TerminalSnapshot): boolean {
  return Boolean(terminal.tmux_session)
}

function isCursorTerminal(terminal: TerminalSnapshot): boolean {
  const haystack = [
    terminal.status?.provider_label,
    terminal.label,
    terminal.display_title,
    terminal.execution_kind,
    terminal.tmux_session,
  ].filter(Boolean).join(' ').toLowerCase()
  return haystack.includes('cursor')
}

function ctrlODebugLabel(terminal: TerminalSnapshot): string {
  return isCursorTerminal(terminal) ? 'Send Ctrl+O (raw key)' : 'Send Ctrl+O (expand)'
}

function ctrlODebugTitle(terminal: TerminalSnapshot): string {
  return isCursorTerminal(terminal)
    ? 'Sends raw Ctrl+O to the Cursor tmux pane. Cursor CLI may ignore this shortcut depending on its current prompt state.'
    : 'Sends Ctrl+O to the tmux pane. Some coding CLIs use this to expand a collapsed view.'
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

function terminalDebugText(terminal: TerminalSnapshot): string {
  return [
    `terminal_id=${terminal.terminal_id}`,
    terminal.tmux_session ? `tmux_session=${terminal.tmux_session}` : '',
    `session_id=${terminal.session_id}`,
    terminal.owner_id ? `owner_id=${terminal.owner_id}` : '',
    terminal.execution_id ? `execution_id=${terminal.execution_id}` : '',
    terminal.execution_kind ? `execution_kind=${terminal.execution_kind}` : '',
    terminal.step_type ? `step_type=${terminal.step_type}` : '',
    terminal.state ? `state=${terminal.state}` : '',
    terminal.process_state ? `process_state=${terminal.process_state}` : '',
    terminal.snapshot_kind ? `snapshot_kind=${terminal.snapshot_kind}` : '',
    terminal.close_reason ? `close_reason=${terminal.close_reason}` : '',
    terminal.closes_at ? `closes_at=${terminal.closes_at}` : '',
    terminal.retention_seconds ? `retention_seconds=${terminal.retention_seconds}` : '',
    `title=${formatTerminalTitle(terminal)}`,
  ].filter(Boolean).join('\n')
}

function trimTerminalDisplayContent(content: string): string {
  // Tmux screen captures often include the pane's empty rows after the last
  // prompt. Keep the raw snapshot in state, but do not render those trailing
  // blank rows or first-open auto-scroll lands on empty space.
  return content.replace(/(?:[ \t\r]*\n)+[ \t\r]*$/g, '')
}

function terminalUpdatedTime(terminal: TerminalSnapshot): number {
  const value = new Date(terminal.updated_at || terminal.created_at).getTime()
  return Number.isNaN(value) ? 0 : value
}

function terminalCreatedTime(terminal: TerminalSnapshot): number {
  const value = new Date(terminal.created_at || terminal.updated_at).getTime()
  return Number.isNaN(value) ? 0 : value
}

function formatUpdatedAge(terminal: TerminalSnapshot): string {
  const updatedAt = terminalUpdatedTime(terminal)
  if (!updatedAt) return ''
  const seconds = Math.max(0, Math.floor((Date.now() - updatedAt) / 1000))
  if (seconds < 5) return 'updated now'
  if (seconds < 60) return `updated ${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `updated ${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  return `updated ${hours}h ago`
}

function formatStartedAt(terminal: TerminalSnapshot): string {
  const startedAt = terminalCreatedTime(terminal)
  if (!startedAt) return ''

  const date = new Date(startedAt)
  const isToday = date.toDateString() === new Date().toDateString()
  const time = date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  return isToday
    ? `started ${time}`
    : `started ${date.toLocaleDateString([], { month: 'short', day: 'numeric' })}, ${time}`
}

function formatRailAge(terminal: TerminalSnapshot): { label: string; title: string } | null {
  const startedAt = terminalCreatedTime(terminal)
  if (!startedAt) return null
  const date = new Date(startedAt)
  const seconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000))
  const title = `Started ${date.toLocaleString()}`
  if (seconds < 10) return { label: 'now', title }
  if (seconds < 60) return { label: `${seconds}s ago`, title }
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return { label: `${minutes}m ago`, title }
  const hours = Math.floor(minutes / 60)
  return { label: `${hours}h ago`, title }
}

function isArchivedTurnTerminal(terminal: TerminalSnapshot): boolean {
  return terminal.terminal_id.includes(':turn-')
}

function terminalHasDisplayBody(terminal: TerminalSnapshot): boolean {
  return !!(terminal.content || '').trim() || (terminal.rows?.length || 0) > 0
}

function isRailVisibleTerminal(terminal: TerminalSnapshot): boolean {
  return !(isArchivedTurnTerminal(terminal) && isMainAgentTerminal(terminal))
}

// turnIndexFromTerminalID parses ":turn-N" out of an archived-turn terminal_id.
// Returns 0 for terminals that don't carry a turn marker so the caller can
// safely sort mixed lists.
function turnIndexFromTerminalID(terminalID: string): number {
  const m = terminalID.match(/:turn-(\d+)/)
  return m ? parseInt(m[1], 10) : 0
}

// findPriorArchivedTurns returns the `:turn-N` archived terminals for the
// same session as `current`, sorted in chronological order. Used both to
// drive the lazy content fetch and to stitch the aggregated scrollback.
function findPriorArchivedTurns(current: TerminalSnapshot, allTerminals: TerminalSnapshot[]): TerminalSnapshot[] {
  const sessionID = (current.session_id || '').trim()
  const ownerID = (current.owner_id || '').trim()
  if (!sessionID || isArchivedTurnTerminal(current) || !isSyntheticTerminal(current)) {
    return []
  }
  // Metadata-only selected terminals cannot yet be classified reliably. Fetch
  // the selected terminal body first; once it is known to be structured text,
  // archived turns can be stitched in without blocking the active pane.
  if (!terminalHasDisplayBody(current)) {
    return []
  }
  const matchingTurns = allTerminals
    .filter(t =>
      t.terminal_id !== current.terminal_id &&
      (t.session_id || '').trim() === sessionID &&
      (t.owner_id || '').trim() === ownerID &&
      isArchivedTurnTerminal(t) &&
      isSyntheticTerminal(t),
    )
    .sort((a, b) => turnIndexFromTerminalID(a.terminal_id) - turnIndexFromTerminalID(b.terminal_id))
  return matchingTurns.slice(-MAX_PRIOR_ARCHIVED_TURNS_TO_INLINE)
}

// aggregatePriorTurnContent stitches archived :turn- snapshot bodies (read
// from `contentByID` — the rail poll fetches metadata only, so per-archived
// content has to be loaded on demand and cached) in front of the current
// live terminal's content. Result reads like a tmux pane scrollback.
function aggregatePriorTurnContent(
  current: TerminalSnapshot,
  priorTurns: TerminalSnapshot[],
  contentByID: Record<string, string>,
): string {
  const currentContent = current.content || ''
  if (priorTurns.length === 0) return currentContent
  const parts: string[] = []
  for (const t of priorTurns) {
    const cached = contentByID[t.terminal_id]
    const c = (cached ?? t.content ?? '').trim()
    if (c) parts.push(c)
  }
  if (currentContent.trim()) parts.push(currentContent)
  return parts.join('\n\n')
}

function sortTerminalsNewestFirst(terminals: TerminalSnapshot[]): TerminalSnapshot[] {
  return [...terminals].sort((a, b) => {
    const mainDelta = (isMainAgentTerminal(b) && !isArchivedTurnTerminal(b) ? 1 : 0) - (isMainAgentTerminal(a) && !isArchivedTurnTerminal(a) ? 1 : 0)
    if (mainDelta !== 0) return mainDelta
    const archivedDelta = (isArchivedTurnTerminal(a) ? 1 : 0) - (isArchivedTurnTerminal(b) ? 1 : 0)
    if (archivedDelta !== 0) return archivedDelta
    return terminalUpdatedTime(b) - terminalUpdatedTime(a)
  })
}

function sortTerminalsForRail(terminals: TerminalSnapshot[]): TerminalSnapshot[] {
  return [...terminals].sort((a, b) => {
    // Rail order must not depend on state or updated_at. A pane moving
    // from running -> completed, or receiving a fresh tmux scrape,
    // should only change its dot/content, not jump around the list.
    const currentMainDelta = (isMainAgentTerminal(b) && !isArchivedTurnTerminal(b) ? 1 : 0) - (isMainAgentTerminal(a) && !isArchivedTurnTerminal(a) ? 1 : 0)
    if (currentMainDelta !== 0) return currentMainDelta
    const archivedDelta = (isArchivedTurnTerminal(a) ? 1 : 0) - (isArchivedTurnTerminal(b) ? 1 : 0)
    if (archivedDelta !== 0) return archivedDelta
    const mainDelta = (isMainAgentTerminal(b) ? 1 : 0) - (isMainAgentTerminal(a) ? 1 : 0)
    if (mainDelta !== 0) return mainDelta
    const createdDelta = terminalCreatedTime(a) - terminalCreatedTime(b)
    if (createdDelta !== 0) return createdDelta
    return terminalPaneKey(a).localeCompare(terminalPaneKey(b))
  })
}

function terminalPaneKey(terminal: TerminalSnapshot): string {
  return terminal.terminal_id
}

function terminalRailPadding(depth: number): number {
  return 8 + Math.min(Math.max(depth, 0), 10) * 6
}

function TerminalRailBranchMarker({ depth }: { depth: number }) {
  if (depth <= 0) return null
  return (
    <span className="relative h-4 w-2.5 shrink-0" aria-hidden>
      <span className="absolute left-1 top-0 h-2.5 border-l border-neutral-700/70" />
      <span className="absolute left-1 top-2.5 w-1.5 border-t border-neutral-700/70" />
    </span>
  )
}

function terminalPaneIconDetails(terminal: TerminalSnapshot) {
  const visualKind = terminalRailVisualKind(terminal)
  return {
    terminal: { label: isSyntheticTerminal(terminal) ? 'Automation step' : 'Terminal', Icon: Terminal },
    orchestrator: { label: 'Orchestrator', Icon: Network },
    'sub-agent': { label: 'Sub-agent', Icon: Bot },
    'agent': { label: 'Agent', Icon: ListRestart },
    routing: { label: 'Routing decision', Icon: GitBranch },
    scripted: { label: 'Scripted step', Icon: Braces },
    evaluation: { label: 'Evaluation', Icon: ClipboardCheck },
    reviewer: { label: 'Reviewer', Icon: SearchCheck },
  }[visualKind]
}

function TerminalTypeGlyph({ terminal, className = 'h-2.5 w-2.5' }: { terminal: TerminalSnapshot; className?: string }) {
  const { label, Icon } = terminalPaneIconDetails(terminal)
  return <Icon className={className} aria-label={label} />
}

function TerminalPaneIcon({ terminal }: { terminal: TerminalSnapshot }) {
  const { label } = terminalPaneIconDetails(terminal)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border border-neutral-700/70 bg-neutral-900/80 text-neutral-400"
          aria-label={label}
        >
          <TerminalTypeGlyph terminal={terminal} />
        </span>
      </TooltipTrigger>
      <TooltipContent side="right" className="text-xs">
        {label}
      </TooltipContent>
    </Tooltip>
  )
}

function TerminalArchivedTurnIcon() {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border border-amber-700/60 bg-amber-950/20 text-amber-300/80"
          aria-label="Archived previous turn"
        >
          <History className="h-2.5 w-2.5" />
        </span>
      </TooltipTrigger>
      <TooltipContent side="right" className="text-xs">
        Archived previous turn
      </TooltipContent>
    </Tooltip>
  )
}

function terminalDetailCacheKey(terminal: TerminalSnapshot): string {
  return `${terminal.terminal_id}:${terminal.chunk_index}:${terminal.updated_at || terminal.created_at || ''}`
}

function latestCachedTerminalDetail(
  terminal: TerminalSnapshot,
  cache: Record<string, TerminalSnapshot>,
): TerminalSnapshot | undefined {
  let latest: TerminalSnapshot | undefined
  let latestTime = -1
  for (const detail of Object.values(cache)) {
    if (detail.terminal_id !== terminal.terminal_id) continue
    const detailTime = terminalUpdatedTime(detail)
    if (!latest || detailTime >= latestTime) {
      latest = detail
      latestTime = detailTime
    }
  }
  return latest
}

function dedupeTerminalsByID(terminals: TerminalSnapshot[]): TerminalSnapshot[] {
  const byID = new Map<string, TerminalSnapshot>()
  for (const terminal of terminals) {
    const existing = byID.get(terminal.terminal_id)
    const terminalIsRunning = terminalState(terminal) === 'running'
    const existingIsRunning = existing ? terminalState(existing) === 'running' : false
    if (
      !existing ||
      (terminalIsRunning && !existingIsRunning) ||
      (
        terminalIsRunning === existingIsRunning &&
        terminalUpdatedTime(terminal) >= terminalUpdatedTime(existing)
      )
    ) {
      byID.set(terminal.terminal_id, terminal)
    }
  }
  return Array.from(byID.values())
}

const SPINNER_FRAMES = ['◐', '◓', '◑', '◒']

function useSpinnerFrame(active: boolean): string {
  const [frame, setFrame] = useState(0)
  useEffect(() => {
    if (!active) return
    const id = window.setInterval(() => {
      setFrame(f => (f + 1) % SPINNER_FRAMES.length)
    }, 110)
    return () => window.clearInterval(id)
  }, [active])
  return SPINNER_FRAMES[frame]
}

// LiveAttachXtermPane is the live-attach transport (see
// docs/refactor/terminal_live_attach_transport.md). It renders the SELECTED live
// tmux terminal directly from the /api/terminals/{id}/stream WebSocket instead of
// the snapshot/replay polling path: the backend's first frame is a seed captured
// IN-BAND on the tmux control channel (reset + scrollback history + current
// screen, atomic with the stream — no overlap, no gap), then the live
// control-mode %output byte stream. We write those bytes straight into xterm —
// no content-prop replay.
//
// It is rendered for every selected non-synthetic terminal with a tmux session;
// the rail/other terminals are untouched. It is mounted with a key that includes
// the logical terminal id and tmux session, so a relaunched session fully
// remounts (fresh buffer + a fresh WS), making cross-session overlap impossible
// by construction.
//
// Main-agent panes preserve native input modes and send keyboard bytes to tmux.
// Width changes reseed on the same socket; the parser drains before fitting the
// new grid. Diagnostic panes retain their display-only guards.
const LiveAttachXtermPaneInner: React.FC<{
  terminalId: string
  tmuxSession?: string
  sessionId?: string
  className?: string
  contentRef: React.RefObject<HTMLDivElement | null>
  xtermTheme: ITheme
  authoritativeContent?: string
  authoritativeVersion?: string
  reconnectOnClose: boolean
  onViewportStickChange?: (isNearBottom: boolean) => void
  onScrollToBottomReady?: (handler: (() => void) | null) => void
  onOutputText?: (text: string) => void
  streamUrl?: (cols: number, rows: number) => string
  loadSnapshot?: () => Promise<TerminalSnapshot>
  interactive?: boolean
}> = ({ terminalId, tmuxSession, sessionId, className, contentRef, xtermTheme, authoritativeContent, authoritativeVersion, reconnectOnClose, onViewportStickChange, onScrollToBottomReady, onOutputText, streamUrl, loadSnapshot, interactive = false }) => {
  const [connectionState, setConnectionState] = useState<'connecting' | 'connected' | 'reconnecting' | 'snapshot' | 'settled' | 'superseded' | 'revoked' | 'exited'>('connecting')
  // Set inside the socket effect so the superseded badge's "Take over" button
  // can re-attach this pane without remounting it.
  const takeOverRef = useRef<(() => void) | null>(null)
  const mountRef = useRef<HTMLDivElement | null>(null)
  const terminalRef = useRef<XTerm | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const outputQueueRef = useRef<TerminalOutputQueue | null>(null)
  const inputReadyRef = useRef(false)
  const [inputError, setInputError] = useState<string | null>(null)
  const reconnectOnCloseRef = useRef(reconnectOnClose)
  const onViewportStickChangeRef = useRef(onViewportStickChange)
  const onOutputTextRef = useRef(onOutputText)
  const streamUrlRef = useRef(streamUrl)
  const loadSnapshotRef = useRef(loadSnapshot)
  const lastVisibleReseedRef = useRef<{ content: string; at: number }>({ content: '', at: 0 })
  const receivedVisibleOutputRef = useRef(false)
  const receivedLiveSeedRef = useRef(false)
  const wroteConnectingSnapshotRef = useRef(false)

  useEffect(() => {
    reconnectOnCloseRef.current = reconnectOnClose
  }, [reconnectOnClose])

  useEffect(() => {
    onViewportStickChangeRef.current = onViewportStickChange
  }, [onViewportStickChange])

  useEffect(() => {
    onOutputTextRef.current = onOutputText
  }, [onOutputText])

  useEffect(() => { streamUrlRef.current = streamUrl }, [streamUrl])
  useEffect(() => { loadSnapshotRef.current = loadSnapshot }, [loadSnapshot])

  useEffect(() => {
    const mount = mountRef.current
    if (!mount) return
    receivedVisibleOutputRef.current = false
    receivedLiveSeedRef.current = false
    wroteConnectingSnapshotRef.current = false
    lastVisibleReseedRef.current = { content: '', at: 0 }

    // convertEol stays FALSE: the WS carries the raw terminal byte stream (live
    // %output + the CR-normalized current-screen backfill), so xterm must honor the
    // bytes verbatim — unlike the snapshot path which post-processes content.
    const term = new XTerm({
      allowProposedApi: false,
      convertEol: false,
      cursorBlink: true,
      disableStdin: true,
      // Pure passthrough: no density-profile CSS layer. Only fixed xterm metrics
      // and app light/dark colors are configured; bytes still render as raw tmux.
      fontFamily: RAW_XTERM_FONT_FAMILY,
      fontSize: RAW_XTERM_FONT_SIZE,
      fontWeight: 400,
      fontWeightBold: 600,
      scrollback: 20000,
      // xterm 6 uses the ruler width for its scrollbar; FitAddon reserves the
      // same width so the thinner track keeps the terminal grid accurate.
      overviewRuler: { width: RAW_XTERM_SCROLLBAR_WIDTH, showTopBorder: false, showBottomBorder: false },
      theme: xtermTheme,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(mount)
    applyRawXtermTheme(term, xtermTheme)
    const displayGuards = interactive ? installInteractiveXtermKeys(term) : installDisplayOnlyXtermGuards(term)
    terminalRef.current = term
    const outputQueue = new TerminalOutputQueue(term)
    outputQueueRef.current = outputQueue
    const sendInput = (data: string, binary = false) => {
      if (!interactive) return
      if (!sendTerminalInput(wsRef.current, inputReadyRef.current, data, binary)) {
        inputReadyRef.current = false
        setInputError('Terminal disconnected. Reconnect before typing again.')
        term.options.disableStdin = true
      }
    }
    const inputDisposable = term.onData(data => sendInput(data))
    const binaryInputDisposable = term.onBinary(data => sendInput(data, true))
    const onPaste = (event: ClipboardEvent) => {
      if (!interactive) return
      const text = event.clipboardData?.getData('text/plain')
      if (!text) return
      event.preventDefault()
      event.stopImmediatePropagation()
      if (!sendTerminalPaste(wsRef.current, inputReadyRef.current, text)) {
        inputReadyRef.current = false
        setInputError('Terminal disconnected. Reconnect before pasting again.')
        term.options.disableStdin = true
      }
    }
    mount.addEventListener('paste', onPaste, true)
    onScrollToBottomReady?.(() => {
      if (!scrollCurrentXtermToBottom(term, terminalRef.current)) return
      onViewportStickChangeRef.current?.(true)
    })

    const scrollDisposable = term.onScroll(viewportY => {
      const distanceFromBottom = Math.max(0, term.buffer.active.baseY - viewportY)
      onViewportStickChangeRef.current?.(distanceFromBottom <= 1)
    })

    const hasUsableGrid = () => (
      term.cols >= RAW_XTERM_MIN_FIT_COLS &&
      term.rows >= RAW_XTERM_MIN_FIT_ROWS
    )

    // The app's pane is a React/flex container that sizes after mount and on later
    // layout changes, so a ResizeObserver (fires post-layout with the real size) is
    // required to fit the xterm correctly — unlike the static-page PoC whose
    // container is sized immediately by CSS. Debounced so only the settled size
    // starts a geometry reconnect.
    // WebSocket lifecycle with reconnect for live sessions. The backend sends a
    // reset + current-screen backfill on every (re)connect, so a dropped socket
    // recovers without any client-side replay/seed state.
    let closed = false
    let reconnectTimer: number | undefined
    let hasConnected = false
    let failedAttempts = 0
    let connectionEpoch = 0
    let seedTimer: number | undefined
    let resizeReconnectPending = false
    let superseded = false
    // Set by a final close code (access revoked / CLI exited): nothing
    // reconnects afterwards.
    let finalState = false
    // Set while connect() waits for a usable (visible) fit box.
    let awaitingFitBox = false
    const reseed = new LiveAttachReseedTracker()
    let reseedTimer: number | undefined
    let reseedDebounceTimer: number | undefined
    const clearReseedTimers = () => {
      if (reseedTimer !== undefined) window.clearTimeout(reseedTimer)
      if (reseedDebounceTimer !== undefined) window.clearTimeout(reseedDebounceTimer)
      reseedTimer = undefined
      reseedDebounceTimer = undefined
    }

    const clearSeedTimer = () => {
      if (seedTimer !== undefined) {
        window.clearTimeout(seedTimer)
        seedTimer = undefined
      }
    }

    const isCurrentRecovery = (epoch: number) => !closed && !superseded && !finalState && connectionEpoch === epoch && !wsRef.current
    const showDisconnectedSnapshot = async (epoch: number): Promise<boolean> => {
      if (!isCurrentRecovery(epoch)) return false
      try {
        const snapshot = loadSnapshotRef.current
          ? await loadSnapshotRef.current()
          : await agentApi.getTerminal(terminalId, {
              content: 'screen',
              lines: LIVE_ATTACH_SNAPSHOT_LINES,
              debugSource: 'live-attach-reconnect',
            })
        if (!isCurrentRecovery(epoch)) return false

        const canReconnect = terminalSnapshotCanReconnect(snapshot)
        const snapshotContent = snapshot.content || ''
        if (snapshotContent.trim()) {
          clearXtermSelection(term)
          outputQueue.reseed(buildVisibleScreenReseed(snapshotContent), () => {
            if (!scrollCurrentXtermToBottom(term, terminalRef.current)) return
            onViewportStickChangeRef.current?.(true)
          })
          onOutputTextRef.current?.(snapshotContent)
          setConnectionState(canReconnect ? 'snapshot' : 'settled')
        } else {
          setConnectionState(canReconnect ? 'reconnecting' : 'settled')
        }
        return canReconnect
      } catch {
        if (!isCurrentRecovery(epoch)) return false
        setConnectionState('reconnecting')
        return reconnectOnCloseRef.current
      }
    }

    const scheduleReconnect = () => {
      if (closed || superseded || finalState || !reconnectOnCloseRef.current) {
        if (!closed) setConnectionState('settled')
        return
      }
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      const delay = terminalReconnectDelayMs(failedAttempts)
      failedAttempts += 1
      reconnectTimer = window.setTimeout(connect, delay)
    }

    const recoverAfterDisconnect = async () => {
      if (closed || !reconnectOnCloseRef.current) return
      const epoch = connectionEpoch
      void outputQueue.invalidate()
      setConnectionState('reconnecting')
      const canReconnect = await showDisconnectedSnapshot(epoch)
      if (canReconnect && isCurrentRecovery(epoch)) scheduleReconnect()
    }

    const connect = () => {
      if (closed || superseded || finalState || resizeReconnectPending) return
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      reconnectTimer = undefined
      if (!reconnectOnCloseRef.current && hasConnected) {
        setConnectionState('settled')
        return
      }
      if (!hasUsableTerminalFitBox(contentRef.current || mount)) {
        // A hidden/collapsed pane still holds its previous grid; connecting now
        // would seed tmux at that stale width. Wait for the ResizeObserver to
        // report a usable box (fitTerminal resumes here), with the slow retry
        // kept as a backstop for visibility changes that do not resize the box.
        awaitingFitBox = true
        reconnectTimer = window.setTimeout(connect, terminalReconnectDelayMs(failedAttempts))
        return
      }
      awaitingFitBox = false
      try {
        fitRawXtermToVisibleGrid(fit)
      } catch {
        // Fall through to the grid check below.
      }
      if (!hasUsableGrid()) {
        // The pane can be transiently hidden/collapsed when a reconnect fires.
        // Retry instead of bailing: returning here used to strand the stream
        // permanently (hasConnected stayed true, so nothing called connect again).
        reconnectTimer = window.setTimeout(connect, terminalReconnectDelayMs(failedAttempts))
        return
      }
      const epoch = ++connectionEpoch
      clearSeedTimer()
      inputReadyRef.current = false
      term.options.disableStdin = true
      const oldSocket = wsRef.current
      wsRef.current = null
      oldSocket?.close()
      void outputQueue.invalidate()
      hasConnected = true
      const url = streamUrlRef.current
        ? streamUrlRef.current(term.cols, term.rows)
        : agentApi.getTerminalStreamUrl(terminalId, term.cols, term.rows, tmuxSession, sessionId)
      let ws: WebSocket
      try {
        ws = new WebSocket(url)
      } catch {
        void recoverAfterDisconnect()
        return
      }
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws
      // Per-connection decoder: a partial multibyte sequence from a dropped
      // socket must not leak into the next connection's first chunk.
      let decoder = new TextDecoder()
      // The first frame of every (re)connect is the backend's in-band seed
      // (reset + scrollback history + current screen). It gets the same
      // embedded-xterm ANSI normalization as the static snapshot path (drops
      // Claude Code's neutral canvas fill, which renders as gray panels/bars).
      // Live %output after it is written verbatim, like a real terminal, and
      // never clears the user's selection.
      let seedPending = true
      let outputBlocked = false
      let inputFailed = false
      let renderEpoch = 0
      const writeOutput = (data: string | Uint8Array, callback?: () => void, reseed = false) => {
        const accepted = reseed && typeof data === 'string' ? outputQueue.reseed(data, callback) : outputQueue.write(data, callback)
        if (accepted) return
        outputBlocked = true
        inputReadyRef.current = false
        term.options.disableStdin = true
        setInputError('Terminal output exceeded the render buffer. Reconnecting…')
        void outputQueue.invalidate()
        ws.close(4002, 'terminal render buffer full')
      }
      ws.onopen = () => {
        if (closed || wsRef.current !== ws) return
        clearSeedTimer()
        seedTimer = window.setTimeout(() => {
          if (wsRef.current === ws && seedPending) ws.close()
        }, LIVE_ATTACH_SEED_TIMEOUT_MS)
      }
      ws.onmessage = ev => {
        // Once a new grid is requested, discard bytes from the old-width socket.
        // Its replacement starts with an authoritative reset + seed.
        if (closed || outputBlocked || resizeReconnectPending || wsRef.current !== ws) return
        const data = ev.data
        if (typeof data === 'string') {
          try {
            const control = JSON.parse(data)
            if (control.type === 'input_error') {
              inputFailed = true
              setInputError(control.message || 'Terminal input could not be delivered.')
              inputReadyRef.current = false
              term.options.disableStdin = true
              return
            }
          } catch { /* Legacy servers may send terminal text frames. */ }
        }
        const route = routeLiveAttachFrame(reseed, data)
        if (route.kind === 'drop' || route.kind === 'ignore-marker') {
          // While a reseed is pending, bytes may be wrapped for the old width
          // and the seed after the marker already contains their effect. A
          // marker for a superseded request is ignored; the newer one follows.
          return
        }
        if (route.kind === 'reseed') {
          if (reseedTimer !== undefined) {
            window.clearTimeout(reseedTimer)
            reseedTimer = undefined
          }
          // Only now is the old screen discarded: the next binary frame is
          // the authoritative seed captured at the new grid.
          const render = ++renderEpoch
          outputQueue.barrier(() => {
            if (closed || wsRef.current !== ws || renderEpoch !== render) return
            resetRawXtermForGeometryChange()
            try { term.resize(route.grid.cols, route.grid.rows) } catch { /* The next fit retries. */ }
          })
          decoder = new TextDecoder()
          seedPending = true
          // The layout may have moved again while the reseed was in flight.
          scheduleFit(true)
          return
        }
        if (seedPending) {
          seedPending = false
          receivedLiveSeedRef.current = true
          clearSeedTimer()
          failedAttempts = 0
          const text = data instanceof ArrayBuffer
            ? decoder.decode(new Uint8Array(data))
            : String(data)
          const hasVisibleContent = terminalPayloadHasVisibleContent(text)
          receivedVisibleOutputRef.current = hasVisibleContent
          clearXtermSelection(term)
          const parsedSeedEpoch = renderEpoch
          writeOutput(normalizeAnsiForEmbeddedXterm(text), () => {
            // Input modes (application arrows, mouse tracking) must be parsed
            // before enabling the keyboard. An old seed callback cannot enable
            // input on a replacement socket or during a geometry transition.
            if (closed || connectionEpoch !== epoch || wsRef.current !== ws || resizeReconnectPending || outputBlocked || inputFailed || reseed.shouldDropBinary() || parsedSeedEpoch !== renderEpoch) return
            setConnectionState(hasVisibleContent ? 'connected' : 'connecting')
            inputReadyRef.current = interactive
            term.options.disableStdin = !interactive
            setInputError(null)
            if (interactive) term.focus()
          }, true)
          onOutputTextRef.current?.(text)
          return
        }
        if (data instanceof ArrayBuffer) {
          const bytes = new Uint8Array(data)
          writeOutput(bytes)
          const text = decoder.decode(bytes, { stream: true })
          if (!receivedVisibleOutputRef.current && terminalPayloadHasVisibleContent(text)) {
            receivedVisibleOutputRef.current = true
            setConnectionState('connected')
          }
          onOutputTextRef.current?.(text)
        } else if (typeof data === 'string') {
          writeOutput(data)
          if (!receivedVisibleOutputRef.current && terminalPayloadHasVisibleContent(data)) {
            receivedVisibleOutputRef.current = true
            setConnectionState('connected')
          }
          onOutputTextRef.current?.(data)
        }
      }
      ws.onclose = event => {
        if (wsRef.current === ws) {
          inputReadyRef.current = false
          term.options.disableStdin = true
        }
        const wasCurrentSocket = wsRef.current === ws
        if (wasCurrentSocket) {
          clearSeedTimer()
          wsRef.current = null
          // The replacement socket starts with a full seed of its own.
          reseed.cancel()
          clearReseedTimers()
        }
        const action = planLiveAttachClose({
          code: event.code,
          paneClosed: closed,
          wasCurrentSocket,
          resizeReconnectPending,
          reconnectOnClose: reconnectOnCloseRef.current,
          supersededCloseCode: LIVE_ATTACH_SUPERSEDED_CLOSE_CODE,
        })
        if (action === 'ignore') return
        if (action === 'superseded') {
          // The backend evicted this viewer: the terminal was opened in another
          // window. Reconnecting would evict whoever replaced us and start a
          // ping-pong that never converges, so this is TERMINAL. Keep the
          // already-rendered local frame: a fresh capture uses the other
          // viewer's tmux geometry and reflowing it into this pane corrupts
          // wrapping. An explicit take-over reconnects and seeds at this pane's
          // own grid.
          resizeReconnectPending = false
          superseded = true
          setConnectionState('superseded')
          return
        }
        if (action === 'access-revoked' || action === 'cli-exited') {
          // Final: reconnecting cannot bring the stream back. Keep the last
          // rendered frame and say why.
          resizeReconnectPending = false
          finalState = true
          setConnectionState(action === 'access-revoked' ? 'revoked' : 'exited')
          return
        }
        if (action === 'geometry-reconnect') {
          const epoch = ++connectionEpoch
          void outputQueue.invalidate().then(() => {
            if (closed || superseded || connectionEpoch !== epoch || wsRef.current) return
            resizeReconnectPending = false
            try {
              if (!hasUsableTerminalFitBox(contentRef.current || mount)) {
                scheduleReconnect()
                return
              }
              setConnectionState('reconnecting')
              resetRawXtermForGeometryChange()
              for (const step of GEOMETRY_RECONNECT_AFTER_CLOSE) {
                if (!runGeometryStep(step)) {
                  scheduleReconnect()
                  return
                }
              }
            } catch {
              scheduleReconnect()
            }
          })
          return
        }
        void recoverAfterDisconnect()
      }
      ws.onerror = () => {
        try {
          ws.close()
        } catch {
          // ignore; onclose will schedule the reconnect
        }
      }
    }

    // Explicit user re-attach after being superseded. Resets the backoff and
    // re-fits first, so the take-over seeds at this pane's real grid (and in
    // turn supersedes whoever holds the terminal now).
    takeOverRef.current = () => {
      if (closed) return
      superseded = false
      resizeReconnectPending = true
      failedAttempts = 0
      setConnectionState('reconnecting')
      const epoch = ++connectionEpoch
      clearSeedTimer()
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      reconnectTimer = undefined
      inputReadyRef.current = false
      term.options.disableStdin = true
      const oldSocket = wsRef.current
      wsRef.current = null
      oldSocket?.close()
      void outputQueue.invalidate().then(() => {
        if (closed || connectionEpoch !== epoch) return
        resizeReconnectPending = false
        try {
          if (hasUsableTerminalFitBox(contentRef.current || mount)) {
            fitRawXtermToVisibleGrid(fit)
          }
        } catch {
          // Fall through: connect() re-checks the grid and retries if unusable.
        }
        connect()
      })
    }

    // Executes one planned geometry step. The ORDER comes from
    // planGeometryChange (pure, unit-tested); this only performs the effects.
    // Clearing the buffer is only half the job: the reseed effect skips writes
    // when the incoming content matches what it last wrote, so without also
    // forgetting that record an unchanged snapshot would leave the pane blank
    // until the next distinct frame.
    const resetRawXtermForGeometryChange = () => {
      const term = terminalRef.current
      if (!term) return
      try {
        term.reset()
      } catch {
        // reset can throw while the pane is detached; the fresh seed still lands.
      }
      lastVisibleReseedRef.current = { content: '', at: 0 }
      wroteConnectingSnapshotRef.current = false
    }

    const runGeometryStep = (step: GeometryChangeStep): boolean => {
      switch (step) {
        case 'suspend-output':
          // Stop feeding xterm from the old-width socket BEFORE anything
          // resizes; its replacement starts with an authoritative reset + seed.
          resizeReconnectPending = true
          inputReadyRef.current = false
          term.options.disableStdin = true
          setConnectionState('reconnecting')
          // Drop history drawn at the OLD grid. tmux emits lines that are
          // already hard-wrapped at its own width, so xterm has no soft-wrap
          // markers to reflow against: scrollback keeps whatever width it was
          // painted at forever. After a resize the viewport repaints correctly
          // while that history stays behind, re-wrapped mid-word ("the
          // strategy i" / "s") and interleaved with the new seed, which is why
          // resizing the window never repaired what was already on screen.
          // Safe to discard: the replacement stream opens with an authoritative
          // reset + full seed, so this is re-painted at the new grid.
          void outputQueue.invalidate()
          return true
        case 'close-socket': {
          const ws = wsRef.current
          if (!ws) return false
          try {
            ws.close(1000, 'terminal geometry changed')
          } catch {
            resizeReconnectPending = false
            void recoverAfterDisconnect()
            return false
          }
          return true
        }
        case 'fit':
          fitRawXtermToVisibleGrid(fit)
          return true
        case 'open-socket':
          if (!hasUsableGrid()) return false
          connect()
          return true
      }
    }

    const minimumGrid = { cols: RAW_XTERM_MIN_FIT_COLS, rows: RAW_XTERM_MIN_FIT_ROWS }

    // Old-server / failed-reseed path: close this socket and reconnect at the
    // new grid (the reconnect seeds from scratch).
    const reconnectForGeometry = () => {
      const steps = planGeometryChange({
        hasSocket: Boolean(wsRef.current),
        alreadyPending: resizeReconnectPending,
        needsReconnect: true,
        superseded: superseded || finalState,
      })
      for (const step of steps) {
        if (!runGeometryStep(step)) break
      }
    }

    // Sends one in-band reseed request for the settled grid. The old screen
    // stays up (no reset, no badge) until the matching marker arrives.
    const requestReseed = () => {
      reseedDebounceTimer = undefined
      if (closed || superseded || finalState || resizeReconnectPending) return
      const ws = wsRef.current
      if (!ws || ws.readyState !== WebSocket.OPEN) return
      // Never reseed a hidden/collapsed pane at a stale width; the
      // ResizeObserver fires again once the box is usable.
      if (!hasUsableTerminalFitBox(contentRef.current || mount)) return
      const proposed = fit.proposeDimensions()
      const current = reseed.pendingGrid ?? { cols: term.cols, rows: term.rows }
      if (!proposed || terminalGridChange(current, proposed, minimumGrid) === 'none') return
      const request = reseed.begin({ cols: proposed.cols, rows: proposed.rows })
      inputReadyRef.current = false
      term.options.disableStdin = true
      void outputQueue.invalidate()
      try {
        ws.send(JSON.stringify(request))
      } catch {
        reseed.cancel()
        reconnectForGeometry()
        return
      }
      if (reseedTimer !== undefined) window.clearTimeout(reseedTimer)
      reseedTimer = window.setTimeout(() => {
        reseedTimer = undefined
        if (reseed.timeout(request.epoch)) reconnectForGeometry()
      }, LIVE_ATTACH_INBAND_RESEED_TIMEOUT_MS)
    }

    const fitTerminal = () => {
      try {
        if (superseded || finalState || resizeReconnectPending) return
        if (!hasUsableTerminalFitBox(contentRef.current || mount)) return
        if (!hasConnected) {
          fitRawXtermToVisibleGrid(fit)
          if (hasUsableGrid()) connect()
          return
        }
        if (awaitingFitBox && !wsRef.current && !closed) {
          // A reconnect was parked on a hidden pane; the box is usable again.
          if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
          reconnectTimer = undefined
          connect()
          return
        }

        // Compare against the grid a pending reseed is moving to, not the
        // xterm grid it has not been resized to yet.
        const currentGrid = reseed.pendingGrid ?? { cols: term.cols, rows: term.rows }
        const proposedGrid = fit.proposeDimensions()
        const gridChange = terminalGridChange(currentGrid, proposedGrid, minimumGrid)
        if (gridChange === 'none') return
        const openSocket = wsRef.current?.readyState === WebSocket.OPEN ? wsRef.current : null
        const socketOpen = openSocket !== null

        // Vertical layout changes do not alter line wrapping. Resize xterm and
        // tmux over the existing socket so the browser's accumulated history is
        // retained. Reconnecting here used to send a RIS seed, clearing all
        // xterm scrollback; Claude's alternate-screen TUI commonly has
        // tmux history_size=0, so there was nothing with which to restore it.
        if (gridChange === 'rows-only' && socketOpen && !reseed.shouldDropBinary() && !resizeReconnectPending) {
          outputQueue.barrier(() => {
            if (closed || finalState || wsRef.current !== openSocket || resizeReconnectPending || reseed.shouldDropBinary()) return
            fitRawXtermToVisibleGrid(fit)
            openSocket.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
          })
          return
        }

        // A width change (or any change while a reseed is in flight) re-seeds
        // in-band on this socket once the layout has settled.
        if (socketOpen && reseed.supported && !resizeReconnectPending && !superseded && !finalState) {
          if (reseedDebounceTimer !== undefined) window.clearTimeout(reseedDebounceTimer)
          reseedDebounceTimer = window.setTimeout(
            requestReseed,
            Math.max(0, LIVE_ATTACH_INBAND_RESEED_DEBOUNCE_MS - LIVE_ATTACH_FIT_DEBOUNCE_MS),
          )
          return
        }

        const needsReconnect = gridChange === 'columns'
        if (needsReconnect && !wsRef.current) {
          // A disconnected parser can still be finishing its last frame. A
          // layout change must wait for that frame too, and invalidate any
          // snapshot request/timer started before the geometry changed.
          resizeReconnectPending = true
          const epoch = ++connectionEpoch
          if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
          reconnectTimer = undefined
          void outputQueue.invalidate().then(() => {
            if (closed || superseded || connectionEpoch !== epoch) return
            resizeReconnectPending = false
            try {
              resetRawXtermForGeometryChange()
              fitRawXtermToVisibleGrid(fit)
              connect()
            } catch {
              scheduleReconnect()
            }
          })
          return
        }
        const steps = planGeometryChange({
          hasSocket: Boolean(wsRef.current),
          alreadyPending: resizeReconnectPending,
          needsReconnect: gridChange === 'columns',
          superseded: superseded || finalState,
        })
        for (const step of steps) {
          if (!runGeometryStep(step)) break
        }
      } catch {
        // Fit can fail during unmount or while the pane is display:none.
      }
    }
    let fitTimer: number | undefined
    let lastObservedFitBox: { width: number; height: number } | null = null
    const scheduleFit = (force = false) => {
      const fitBox = (contentRef.current || mount).getBoundingClientRect()
      const sameFitBox = lastObservedFitBox !== null
        && Math.abs(lastObservedFitBox.width - fitBox.width) < 0.5
        && Math.abs(lastObservedFitBox.height - fitBox.height) < 0.5
      if (!force && sameFitBox) return
      lastObservedFitBox = { width: fitBox.width, height: fitBox.height }
      if (fitTimer !== undefined) window.clearTimeout(fitTimer)
      // The layout is still moving: a queued reseed would capture a width
      // that is about to change. fitTerminal re-queues it once settled.
      if (reseedDebounceTimer !== undefined) {
        window.clearTimeout(reseedDebounceTimer)
        reseedDebounceTimer = undefined
      }
      fitTimer = window.setTimeout(fitTerminal, LIVE_ATTACH_FIT_DEBOUNCE_MS)
    }
    // Wait for one settled layout pass before opening the stream. In the app the
    // terminal pane is flex-sized after mount; connecting immediately can seed
    // tmux/backfill at a stale grid and then receive live cursor-relative updates
    // at the final grid, which shows up as duplicated wrapping/spinner stacking.
    scheduleFit(true)
    const resizeObserver = new ResizeObserver(() => scheduleFit())
    resizeObserver.observe(contentRef.current || mount)
    // Font loading can change the cell width without changing the pane's box,
    // so ResizeObserver alone will not notice that the fitted grid is stale.
    const fonts = document.fonts
    const onFontsLoaded = () => { if (!closed) scheduleFit(true) }
    fonts?.addEventListener('loadingdone', onFontsLoaded)
    void fonts?.ready.then(onFontsLoaded)

    return () => {
      closed = true
      connectionEpoch += 1
      outputQueue.dispose()
      outputQueueRef.current = null
      inputReadyRef.current = false
      inputDisposable.dispose()
      binaryInputDisposable.dispose()
      mount.removeEventListener('paste', onPaste, true)
      scrollDisposable.dispose()
      displayGuards.dispose()
      resizeObserver.disconnect()
      fonts?.removeEventListener('loadingdone', onFontsLoaded)
      if (fitTimer !== undefined) window.clearTimeout(fitTimer)
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      clearSeedTimer()
      clearReseedTimers()
      const ws = wsRef.current
      wsRef.current = null
      if (ws) {
        try {
          ws.onclose = null
          ws.close()
        } catch {
          // ignore
        }
      }
      takeOverRef.current = null
      onScrollToBottomReady?.(null)
      terminalRef.current = null
      term.dispose()
    }
    // terminalId is stable for a mounted instance (key includes tmux_session).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [terminalId, tmuxSession, sessionId, interactive])

  useEffect(() => {
    const term = terminalRef.current
    if (!term) return
    applyRawXtermTheme(term, xtermTheme)
  }, [xtermTheme])

  useEffect(() => {
    const content = authoritativeContent || ''
    const term = terminalRef.current
    // While the tmux WebSocket is live, do not mix in capture-pane snapshots.
    // The stream already starts with a backend backfill and then sends raw
    // control-mode bytes; reseeding from snapshots here duplicates in-place TUI
    // redraws like spinners and status separators.
    // One exception is the initial connection window: showing the already-known
    // snapshot avoids a blank pane while the backend resizes tmux and captures
    // its authoritative seed. The first live seed starts with a terminal reset,
    // so it replaces this temporary frame without mixing snapshot and live bytes.
    if (reconnectOnCloseRef.current) {
      if (!term || !content.trim() || receivedLiveSeedRef.current || wroteConnectingSnapshotRef.current) return
      wroteConnectingSnapshotRef.current = true
      lastVisibleReseedRef.current = { content, at: Date.now() }
      setConnectionState('snapshot')
      clearXtermSelection(term)
      outputQueueRef.current?.reseed(buildVisibleScreenReseed(content), () => {
        if (!scrollCurrentXtermToBottom(term, terminalRef.current)) return
        onViewportStickChangeRef.current?.(true)
      })
      return
    }
    if (!term || !content.trim()) return
    if (content === lastVisibleReseedRef.current.content) return

    const now = Date.now()
    const waitMs = Math.max(
      LIVE_ATTACH_RESEED_DEBOUNCE_MS,
      LIVE_ATTACH_RESEED_MIN_INTERVAL_MS - (now - lastVisibleReseedRef.current.at),
    )
    const timer = window.setTimeout(() => {
      const currentTerm = terminalRef.current
      if (!currentTerm) return
      if (content === lastVisibleReseedRef.current.content) return

      const distanceFromBottom = Math.max(0, currentTerm.buffer.active.baseY - currentTerm.buffer.active.viewportY)
      if (distanceFromBottom > 1) return

      lastVisibleReseedRef.current = { content, at: Date.now() }
      clearXtermSelection(currentTerm)
      outputQueueRef.current?.reseed(buildSettledScreenReseed(content), () => {
        if (!scrollCurrentXtermToBottom(currentTerm, terminalRef.current)) return
        onViewportStickChangeRef.current?.(true)
      })
    }, waitMs)

    return () => window.clearTimeout(timer)
  }, [authoritativeContent, authoritativeVersion, terminalId, reconnectOnClose])

  const handleWheel = useCallback((event: WheelEvent) => {
    const term = terminalRef.current
    if (!term) return
    scrollXtermFromWheel(term, event, onViewportStickChangeRef.current)
  }, [])

  useEffect(() => {
    const node = contentRef.current
    if (!node) return
    const listenerOptions: AddEventListenerOptions = { passive: false, capture: true }
    if (interactive) return // xterm routes wheel/mouse input using the CLI's modes.
    node.addEventListener('wheel', handleWheel, listenerOptions)
    return () => {
      node.removeEventListener('wheel', handleWheel, listenerOptions)
    }
  }, [contentRef, handleWheel, interactive])

  return (
    <div
      ref={contentRef}
      className={`relative ${className || ''}`}
      style={{ backgroundColor: xtermTheme.background }}
    >
      {inputError && <div role="alert" className="absolute bottom-2 left-2 right-2 z-10 flex items-center gap-2 rounded bg-red-950/95 px-3 py-2 text-xs text-red-200">
        <span className="flex-1">{inputError}</span>
        <button type="button" className="shrink-0 underline" onClick={() => takeOverRef.current?.()}>Reconnect</button>
      </div>}
      {connectionState !== 'connected' && (
        <div className={`absolute right-2 top-2 z-10 inline-flex items-center gap-1.5 rounded border border-neutral-700/80 bg-neutral-950/90 px-2 py-1 font-mono text-[10px] text-neutral-300 shadow-sm ${connectionState === 'superseded' ? '' : 'pointer-events-none'}`}>
          {(connectionState === 'connecting' || connectionState === 'reconnecting') && <RefreshCw className="h-3 w-3 animate-spin" />}
          <span>
            {connectionState === 'connecting' && 'Connecting terminal...'}
            {connectionState === 'reconnecting' && 'Reconnecting terminal...'}
            {connectionState === 'snapshot' && 'Showing latest snapshot · connecting'}
            {connectionState === 'settled' && 'Terminal session ended'}
            {connectionState === 'superseded' && 'Open in another window · showing snapshot'}
            {connectionState === 'revoked' && 'Terminal access revoked'}
            {connectionState === 'exited' && 'The CLI exited'}
          </span>
          {connectionState === 'superseded' && (
            // Deliberately explicit: taking over evicts the other window, so it
            // must be a user action rather than an automatic reconnect.
            <button
              type="button"
              onClick={() => takeOverRef.current?.()}
              className="rounded border border-neutral-600/80 px-1.5 py-0.5 text-neutral-200 hover:border-neutral-400 hover:text-neutral-50"
            >
              Take over
            </button>
          )}
        </div>
      )}
      <div
        ref={mountRef}
        className="runloop-raw-xterm h-full w-full p-1.5 [&_.xterm]:h-full"
        style={{
          fontFamily: RAW_XTERM_FONT_FAMILY,
          fontSize: RAW_XTERM_FONT_SIZE,
        }}
      />
    </div>
  )
}

export const LiveAttachXtermPane = memo(LiveAttachXtermPaneInner)

const TerminalWaitingPane: React.FC<{
  className?: string
  contentRef?: React.RefObject<HTMLDivElement | null>
  xtermTheme: ITheme
  title?: string
  message?: string
}> = ({
  className,
  contentRef,
  xtermTheme,
  title = 'Waiting for terminal',
  message = 'The terminal is idle or restoring after inactivity. Output will appear here when the agent produces activity.',
}) => (
  <div
    ref={contentRef}
    className={`${className || ''} flex items-center justify-center px-6 py-10`}
    style={{ backgroundColor: xtermTheme.background }}
  >
    <div className="flex max-w-sm flex-col items-center gap-3 text-center font-mono">
      <div className="relative">
        <Terminal
          className="h-9 w-9"
          strokeWidth={1.35}
          style={{ color: xtermTheme.foreground, opacity: 0.35 }}
          aria-hidden
        />
        <span
          className="absolute -right-1 top-0 h-2 w-2 animate-pulse rounded-full"
          style={{ backgroundColor: xtermTheme.cursor || xtermTheme.foreground }}
        />
      </div>
      <div className="text-sm font-semibold" style={{ color: xtermTheme.foreground }}>
        {title}
      </div>
      <div className="text-xs leading-5" style={{ color: xtermTheme.foreground, opacity: 0.58 }}>
        {message}
      </div>
    </div>
  </div>
)

const StaticXtermPaneInner: React.FC<{
  content: string
  className?: string
  contentRef: React.RefObject<HTMLDivElement | null>
  xtermTheme: ITheme
  onViewportStickChange?: (isNearBottom: boolean) => void
  onScrollToBottomReady?: (handler: (() => void) | null) => void
}> = ({ content, className, contentRef, xtermTheme, onViewportStickChange, onScrollToBottomReady }) => {
  const mountRef = useRef<HTMLDivElement | null>(null)
  const terminalRef = useRef<XTerm | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const outputQueueRef = useRef<TerminalOutputQueue | null>(null)
  const onViewportStickChangeRef = useRef(onViewportStickChange)

  useEffect(() => {
    onViewportStickChangeRef.current = onViewportStickChange
  }, [onViewportStickChange])

  useEffect(() => {
    const mount = mountRef.current
    if (!mount) return

    const term = new XTerm({
      allowProposedApi: false,
      convertEol: true,
      cursorBlink: true,
      disableStdin: true,
      fontFamily: RAW_XTERM_FONT_FAMILY,
      fontSize: RAW_XTERM_FONT_SIZE,
      fontWeight: 400,
      fontWeightBold: 600,
      scrollback: 20000,
      // xterm 6 uses the ruler width for its scrollbar; FitAddon reserves the
      // same width so the thinner track keeps the terminal grid accurate.
      overviewRuler: { width: RAW_XTERM_SCROLLBAR_WIDTH, showTopBorder: false, showBottomBorder: false },
      theme: xtermTheme,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(mount)
    applyRawXtermTheme(term, xtermTheme)
    const displayGuards = installDisplayOnlyXtermGuards(term)
    terminalRef.current = term
    fitRef.current = fit
    const outputQueue = new TerminalOutputQueue(term)
    outputQueueRef.current = outputQueue
    onScrollToBottomReady?.(() => {
      if (!scrollCurrentXtermToBottom(term, terminalRef.current)) return
      onViewportStickChangeRef.current?.(true)
    })

    const scrollDisposable = term.onScroll(viewportY => {
      const distanceFromBottom = Math.max(0, term.buffer.active.baseY - viewportY)
      onViewportStickChangeRef.current?.(distanceFromBottom <= 1)
    })

    const fitTerminal = () => {
      void outputQueue.whenIdle().then(() => {
        if (terminalRef.current !== term) return
        try {
          fitRawXtermToVisibleGrid(fit)
        } catch {
          // Fit can fail during unmount or while the pane is display:none.
        }
      })
    }
    let fitTimer: number | undefined
    const scheduleFit = () => {
      if (fitTimer !== undefined) window.clearTimeout(fitTimer)
      fitTimer = window.setTimeout(fitTerminal, 120)
    }
    scheduleFit()
    const resizeObserver = new ResizeObserver(scheduleFit)
    resizeObserver.observe(contentRef.current || mount)
    const fonts = document.fonts
    const onFontsLoaded = () => { if (terminalRef.current === term) scheduleFit() }
    fonts?.addEventListener('loadingdone', onFontsLoaded)
    void fonts?.ready.then(onFontsLoaded)

    return () => {
      outputQueue.dispose()
      outputQueueRef.current = null
      scrollDisposable.dispose()
      displayGuards.dispose()
      resizeObserver.disconnect()
      fonts?.removeEventListener('loadingdone', onFontsLoaded)
      if (fitTimer !== undefined) window.clearTimeout(fitTimer)
      onScrollToBottomReady?.(null)
      terminalRef.current = null
      fitRef.current = null
      term.dispose()
    }
    // Static pane identity is controlled by the React key; content/theme update in
    // separate effects below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const term = terminalRef.current
    if (!term) return
    applyRawXtermTheme(term, xtermTheme)
  }, [xtermTheme])

  useEffect(() => {
    const term = terminalRef.current
    const outputQueue = outputQueueRef.current
    if (!term || !outputQueue) return
    let cancelled = false
    // A refreshed snapshot must not yank a user who scrolled up back to the
    // bottom; restore their line instead (the snapshot is append-mostly).
    const previousViewportY = term.buffer.active.viewportY
    const followBottom = term.buffer.active.baseY - previousViewportY <= 1
    // reset() does not clear xterm's pending write buffer. Wait for the old
    // parse before resetting; a rapid refresh can then replace it atomically.
    void outputQueue.invalidate().then(() => {
      if (cancelled || terminalRef.current !== term) return
      try {
        const fit = fitRef.current
        if (fit && mountRef.current) fitRawXtermToVisibleGrid(fit)
      } catch {
        // Fit can fail while the pane is briefly hidden during tab/layout changes.
      }
      term.reset()
      if (!content) {
        onViewportStickChangeRef.current?.(true)
        return
      }
      outputQueue.reseed('\x1bc' + normalizeAnsiForEmbeddedXterm(content), () => {
        if (cancelled || terminalRef.current !== term) return
        try {
          const fit = fitRef.current
          if (fit && mountRef.current) fitRawXtermToVisibleGrid(fit)
        } catch {
          // Fit can fail during a layout transition.
        }
        if (!followBottom) {
          try { term.scrollToLine(Math.min(previousViewportY, term.buffer.active.baseY)) } catch { /* disposed pane */ }
          return
        }
        if (!scrollCurrentXtermToBottom(term, terminalRef.current)) return
        onViewportStickChangeRef.current?.(true)
      })
    })
    return () => { cancelled = true }
  }, [content])

  const handleWheel = useCallback((event: WheelEvent) => {
    const term = terminalRef.current
    if (!term) return
    scrollXtermFromWheel(term, event, onViewportStickChangeRef.current)
  }, [])

  useEffect(() => {
    const node = contentRef.current
    if (!node) return
    const listenerOptions: AddEventListenerOptions = { passive: false, capture: true }
    node.addEventListener('wheel', handleWheel, listenerOptions)
    return () => {
      node.removeEventListener('wheel', handleWheel, listenerOptions)
    }
  }, [contentRef, handleWheel])

  return (
    <div
      ref={contentRef}
      className={`relative ${className || ''}`}
      style={{ backgroundColor: xtermTheme.background }}
    >
      <div
        ref={mountRef}
        className="runloop-raw-xterm h-full w-full p-1.5 [&_.xterm]:h-full"
        style={{
          fontFamily: RAW_XTERM_FONT_FAMILY,
          fontSize: RAW_XTERM_FONT_SIZE,
        }}
      />
    </div>
  )
}

export const StaticXtermPane = memo(StaticXtermPaneInner)

function normalizeTerminalWorkflowPath(path?: string | null): string {
  return (path || '')
    .replace(/^\/data\/docs\//, '')
    .replace(/\/+$/, '')
}

// workflowMatchKey reduces a path to its workflow identity — the segment after
// "Workflow/" (lowercased), or the last path segment as a fallback. Matching on
// this is robust to the many forms the same workflow's path takes (relative vs
// absolute, "/data/docs/..." vs "...workspace-docs/...", trailing slashes, case),
// which the previous full-path endsWith comparison was not — that fragility hid
// a resumed session's terminals when the active preset path and the terminal's
// workflow_path described the same workflow in different forms.
function workflowMatchKey(path?: string | null): string {
  const parts = normalizeTerminalWorkflowPath(path).replace(/\\/g, '/').split('/').filter(Boolean)
  const wfIdx = parts.findIndex(p => p.toLowerCase() === 'workflow')
  const name = wfIdx >= 0 && parts[wfIdx + 1] ? parts[wfIdx + 1] : parts[parts.length - 1]
  return (name || '').toLowerCase()
}

function terminalMatchesWorkflow(terminal: TerminalSnapshot, workflowPath?: string | null): boolean {
  const target = workflowMatchKey(workflowPath)
  if (!target) return true
  const terminalKey = workflowMatchKey(terminal.workflow_path)
  if (!terminalKey) return false
  return terminalKey === target
}

function formatTokens(n?: number): string {
  if (n === undefined || n === null) return '–'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

function formatStatusFooterCost(usd?: number): string {
  if (usd === undefined || usd === null || usd === 0) return ''
  return `$${formatCost(usd)}`
}

function isSyntheticTerminal(terminal: TerminalSnapshot): boolean {
  const transport = (terminal.step_transport || '').toLowerCase()
  // Workflow-step CLI transport is authoritative once set: every CLI provider
  // used in workflow steps now speaks structured JSON, not raw TUI bytes (see
  // the coding-agent transport unification), so a "structured" label can be
  // trusted outright instead of re-sniffing content for redraw sequences.
  // Sniffing ahead of the label was what randomly dropped structured,
  // event-backed step terminals into the legacy text-parsing renderer,
  // producing a different look (message bubbles, tool-call cards) from the
  // same step's sibling terminals for no reason visible to the user.
  if (transport === 'tmux') return false
  if (transport === 'api' || transport === 'structured' || transport === 'structured_cli' || transport === 'non_tmux') return true
  // No transport label at all (older sessions, or the main/builder agent,
  // which intentionally stays tmux): fall back to sniffing content, since
  // that is the only signal available.
  if (hasTerminalRedrawControls(terminal.content || '')) return false
  if (terminal.tmux_session) return false
  if (isTmuxContentSource(terminal.content_source)) return false
  return true
}

function terminalTmuxDetailOptions(terminal: TerminalSnapshot, displayDetail = false): TerminalDetailOptions | undefined {
  const transport = (terminal.step_transport || '').toLowerCase()
  if (!terminal.tmux_session && transport !== 'tmux') return undefined
  const state = terminalState(terminal)
  if (terminal.active && state !== 'stale' && state !== 'failed') {
    const lines = displayDetail
      ? TERMINAL_ACTIVE_DISPLAY_HISTORY_LINES
      : isMainAgentTerminal(terminal)
        ? TERMINAL_ACTIVE_RAIL_MAIN_HISTORY_LINES
        : TERMINAL_ACTIVE_RAIL_STEP_HISTORY_LINES
    // Active/streaming: request `history`, which the backend serves for an ACTIVE
    // pane as the incremental pipe recording (content_source `tmux_pipe`), not a
    // `capture-pane` snapshot. The previous `screen` mode returned only the visible
    // pane (`capture-pane -p`, no `-S`) so xterm had no scrollback (baseY≈0) and the
    // user couldn't scroll up during streaming. The raw pipe byte stream lets xterm
    // emulate the terminal: in-place redraws render via ANSI (no duplicate litter)
    // and real scrollback accumulates in xterm's native buffer (scroll works).
    return {
      content: 'history',
      lines,
    }
  }
  // Idle: `history` → backend serves a static full-buffer `capture-pane -S` snapshot.
  return { content: 'history', lines: TERMINAL_STATIC_DETAIL_HISTORY_LINES }
}

function terminalScrollDebugEnabled(): boolean {
  if (typeof window === 'undefined') return false
  try {
    const stored = window.localStorage.getItem(TERMINAL_SCROLL_DEBUG_STORAGE_KEY)
    if (stored && ['1', 'true', 'yes', 'on'].includes(stored.toLowerCase())) return true
    const query = new URLSearchParams(window.location.search)
    return query.get('terminal_debug') === '1'
  } catch {
    return false
  }
}

function withTerminalScrollDebug(options: TerminalDetailOptions | undefined, debugSource: string): TerminalDetailOptions | undefined {
  if (!terminalScrollDebugEnabled()) return options
  return {
    ...(options || {}),
    debug: true,
    debugSource,
  }
}

function terminalRequestOptions(terminal: TerminalSnapshot, displayDetail: boolean, debugSource: string): TerminalDetailOptions | undefined {
  return withTerminalScrollDebug(terminalTmuxDetailOptions(terminal, displayDetail), debugSource)
}

function terminalStoredRequestOptions(debugSource: string): TerminalDetailOptions {
  return withTerminalScrollDebug({ content: 'stored' }, debugSource) || { content: 'stored' }
}

function terminalTextLineCount(content?: string): number {
  if (!content) return 0
  return content.split(/\n/).length
}

function logTerminalScrollDebug(
  debugSource: string,
  requestTerminal: TerminalSnapshot | undefined,
  options: TerminalDetailOptions | undefined,
  detail?: TerminalSnapshot,
): void {
  if (!terminalScrollDebugEnabled()) return
  const terminal = detail || requestTerminal
  const payload = {
    source: debugSource,
    terminal_id: terminal?.terminal_id,
    tmux_session: terminal?.tmux_session,
    step_transport: terminal?.step_transport,
    content_source: terminal?.content_source,
    active: terminal?.active,
    state: terminal ? terminalState(terminal) : undefined,
    chunk_index: terminal?.chunk_index,
    request_tmux_session: requestTerminal?.tmux_session,
    request_step_transport: requestTerminal?.step_transport,
    request_content_source: requestTerminal?.content_source,
    request_active: requestTerminal?.active,
    request_state: requestTerminal ? terminalState(requestTerminal) : undefined,
    request_chunk_index: requestTerminal?.chunk_index,
    detail_tmux_session: detail?.tmux_session,
    detail_step_transport: detail?.step_transport,
    detail_content_source: detail?.content_source,
    detail_active: detail?.active,
    detail_state: detail ? terminalState(detail) : undefined,
    detail_chunk_index: detail?.chunk_index,
    requested_content: options?.content || 'stored',
    requested_lines: options?.lines,
    content_lines: terminalTextLineCount(detail?.content),
    content_bytes: detail?.content?.length || 0,
    row_count: Array.isArray(detail?.rows) ? detail.rows.length : undefined,
  }
  console.info('[TERMINAL_DEBUG] frontend detail', payload)
}

// Error event types that should surface above the selected pane. They would
// otherwise be invisible when the rail only contains terminal snapshots.
const TERMINAL_ERROR_EVENT_TYPES = new Set<string>([
  'orchestrator_agent_error',
  'conversation_error',
  'workflow_error',
  'agent_error',
  'tool_call_error',
  'llm_generation_error',
])

interface TerminalErrorBannerEntry {
  id: string
  type: string
  message: string
  timestamp?: string
  terminalID?: string
  toolName?: string
  toolServer?: string
  toolArguments?: string
}

const TERMINAL_ERROR_MESSAGE_LIMIT = 220

function compactTerminalErrorMessage(message: string): string {
  const singleLine = message.replace(/\s+/g, ' ').trim()
  if (singleLine.length <= TERMINAL_ERROR_MESSAGE_LIMIT) return singleLine
  return `${singleLine.slice(0, TERMINAL_ERROR_MESSAGE_LIMIT)}...`
}

function extractErrorMessage(event: unknown): string {
  const e = event as { type?: string; data?: unknown }
  const data = e?.data as { data?: Record<string, unknown>; message?: string; error?: string } | undefined
  const payload = (data?.data && typeof data.data === 'object') ? data.data : (data as Record<string, unknown> | undefined)
  if (!payload) return ''
  for (const key of ['error', 'message', 'detail', 'reason']) {
    const v = payload[key]
    if (typeof v === 'string' && v.trim()) return v
  }
  return ''
}

function TerminalErrorExpandedDetails({ entry, maxHeightClass }: {
  entry: TerminalErrorBannerEntry
  maxHeightClass: string
}) {
  return (
    <div className={`mt-1 space-y-2 overflow-y-auto rounded border border-red-900/45 bg-red-950/25 p-2 font-mono text-[11px] leading-4 text-red-200 ${maxHeightClass}`}>
      {(entry.toolName || entry.toolServer) && (
        <div>
          <div className="font-sans text-[10px] font-semibold uppercase tracking-wide text-red-300/70">Tool</div>
          <div className="break-all">{entry.toolName || 'tool'}{entry.toolServer ? ` · ${entry.toolServer}` : ''}</div>
        </div>
      )}
      {entry.toolArguments && (
        <div>
          <div className="font-sans text-[10px] font-semibold uppercase tracking-wide text-red-300/70">Arguments</div>
          <pre className="whitespace-pre-wrap break-words">{entry.toolArguments}</pre>
        </div>
      )}
      <div>
        <div className="font-sans text-[10px] font-semibold uppercase tracking-wide text-red-300/70">Error</div>
        <div className="whitespace-pre-wrap break-words">{entry.message}</div>
      </div>
    </div>
  )
}

function eventErrorParts(event: PollingEvent): {
  eventRecord: Record<string, unknown>
  data?: Record<string, unknown>
  inner?: Record<string, unknown>
  metadata?: Record<string, unknown>
} {
  const eventRecord = event as unknown as Record<string, unknown>
  const data = asRecord(event.data)
  const inner = asRecord(data?.data)
  const metadata = asRecord(inner?.metadata) || asRecord(data?.metadata) || asRecord(eventRecord.metadata)
  return { eventRecord, data, inner, metadata }
}

function collectStringFields(...values: unknown[]): string[] {
  const out: string[] = []
  for (const value of values) {
    const text = stringField(value)
    if (text) out.push(text)
  }
  return out
}

function normalizeTerminalOwnerCandidate(value: string): string {
  let trimmed = value.trim()
  for (const prefix of ['delegation:', 'workflow:', 'background:', 'agent:', 'batch:']) {
    if (trimmed.startsWith(prefix)) {
      trimmed = trimmed.slice(prefix.length).trim()
      break
    }
  }
  return trimmed
}

function resolveErrorTerminal(event: PollingEvent, terminals: TerminalSnapshot[]): TerminalSnapshot | undefined {
  const { eventRecord, data, inner, metadata } = eventErrorParts(event)
  const exactTerminalIDs = collectStringFields(
    eventRecord.terminal_id,
    data?.terminal_id,
    inner?.terminal_id,
    metadata?.terminal_id,
  )
  for (const terminalID of exactTerminalIDs) {
    const matched = terminals.find(terminal => terminal.terminal_id === terminalID)
    if (matched) return matched
  }

  const tmuxSessions = collectStringFields(
    eventRecord.tmux_session,
    data?.tmux_session,
    inner?.tmux_session,
    metadata?.tmux_session,
    metadata?.tmux_session_name,
    metadata?.claude_code_interactive_session,
    metadata?.codex_interactive_session,
    metadata?.cursor_interactive_session,
  )
  for (const tmuxSession of tmuxSessions) {
    const matched = terminals.find(terminal => terminal.tmux_session === tmuxSession)
    if (matched) return matched
  }

  const ownerCandidates = collectStringFields(
    eventRecord.execution_id,
    eventRecord.parent_execution_id,
    eventRecord.correlation_id,
    data?.execution_id,
    data?.parent_execution_id,
    data?.correlation_id,
    data?.delegation_id,
    data?.background_agent_id,
    data?.agent_id,
    inner?.execution_id,
    inner?.parent_execution_id,
    inner?.correlation_id,
    inner?.delegation_id,
    inner?.background_agent_id,
    inner?.agent_id,
    metadata?.execution_id,
    metadata?.owner_execution_id,
    metadata?.execution_owner_id,
    metadata?.parent_execution_id,
    metadata?.background_agent_id,
    metadata?.delegation_id,
    metadata?.agent_id,
    metadata?.workshop_step_id,
    metadata?.current_step_id,
    metadata?.orchestrator_step_id,
    metadata?.workflow_step_id,
    metadata?.step_id,
  ).map(normalizeTerminalOwnerCandidate)

  for (const candidate of ownerCandidates) {
    const matched = terminals.find(terminal =>
      terminal.terminal_id === candidate ||
      terminal.owner_id === candidate ||
      terminal.execution_id === candidate ||
      terminal.step_id === candidate ||
      `${terminal.session_id}:${candidate}` === terminal.terminal_id ||
      (candidate.startsWith('main:') && isMainAgentTerminal(terminal)),
    )
    if (matched) return matched
  }
  return undefined
}

const DISMISSED_TERMINAL_ERRORS_KEY_PREFIX = 'terminal-dismissed-errors:'

function dismissedTerminalErrorsKey(sessionId?: string): string | null {
  return sessionId ? `${DISMISSED_TERMINAL_ERRORS_KEY_PREFIX}${sessionId}` : null
}

function readDismissedTerminalErrorIDs(sessionId?: string): Set<string> {
  const key = dismissedTerminalErrorsKey(sessionId)
  if (!key) return new Set()
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || '[]')
    return new Set(Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : [])
  } catch {
    return new Set()
  }
}

function writeDismissedTerminalErrorIDs(sessionId: string | undefined, ids: Set<string>) {
  const key = dismissedTerminalErrorsKey(sessionId)
  if (!key) return
  try {
    window.localStorage.setItem(key, JSON.stringify(Array.from(ids).slice(-100)))
  } catch {
    // Best-effort UI preference only.
  }
}

const TerminalCenterInner: React.FC<TerminalCenterProps> = ({ currentSessionId, compact, hasPendingTerminalActivity = false }) => {
  const { theme: appTheme } = useTheme()
  // terminalCenterOpen was the legacy toggle gate (separate sidekick
  // panel); kept here for any callers that still pass the flag but no
  // longer affects rendering — Debug-mode mount is the only gate.
  // Scope terminals to the current chat session. The workflowEventBridge
  // adds every workflow-step event under the chat tab's sessionID, so
  // filtering by currentSessionId surfaces this chat's workflow steps
  // without leaking terminals from other chat tabs / unrelated workflows.
  const viewAll = false
  // A foreground turn can finish before its asynchronous child starts. Keep
  // polling while the surrounding session still expects activity; stopping on
  // a briefly idle tree between dispatch and child registration would miss the
  // later child and recreate the invisibility bug. Once the session is settled,
  // the hook still polls for as long as the tree itself reports live work.
  const { data: sessionExecutionTree } = useSessionExecutionTree(
    currentSessionId,
    !!currentSessionId,
    hasPendingTerminalActivity,
  )
  const [terminals, setTerminals] = useState<TerminalSnapshot[]>([])
  const [runtimeStatesBySession, setRuntimeStatesBySession] = useState<Record<string, RuntimeSnapshot>>({})
  // archivedTurnContents caches `:turn-N` snapshot bodies so we can stitch
  // prior turns into the live synthetic terminal's scrollback without
  // refetching on every render. Archived turns are immutable once written,
  // so the cache is correct without invalidation.
  const [archivedTurnContents, setArchivedTurnContents] = useState<Record<string, string>>({})
  const [terminalDetailCache, setTerminalDetailCache] = useState<Record<string, TerminalSnapshot>>({})
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [userSelectedID, setUserSelectedID] = useState<string | null>(null)
  const [copiedTerminalID, setCopiedTerminalID] = useState<string | null>(null)
  const [expandedTelemetryTerminalID, setExpandedTelemetryTerminalID] = useState<string | null>(null)
  const [dismissedTerminalIDs, setDismissedTerminalIDs] = useState<Set<string>>(() => new Set())
  const [dismissedRouteIDs] = useState<Set<string>>(() => new Set())
  const [dismissedErrorIDs, setDismissedErrorIDs] = useState<Set<string>>(() => readDismissedTerminalErrorIDs(currentSessionId))
  const [expandedErrorIDs, setExpandedErrorIDs] = useState<Set<string>>(() => new Set())
  const terminalColorScheme = DEFAULT_TERMINAL_COLOR_SCHEME
  // The rail is always narrow now — decided there was no case where expanding
  // it to the wide, full-label layout earned back the space it cost. The
  // narrow rail still surfaces Live/Issues/Done via the status-colored dots
  // in renderRailControls; it just never grows a text-label column.
  const railNarrow = true
  const [terminalRailFilter, setTerminalRailFilter] = useState<TerminalRailFilter>('all')
  // The rail's search box was part of the wide-mode UI, now retired along with
  // it (see railNarrow above). Kept as an always-empty constant rather than
  // threading a removal through every filter/empty-state branch that reads it.
  const terminalRailSearch = ''
  const [expandedRailGroupKeys, setExpandedRailGroupKeys] = useState<Set<string>>(() => new Set())
  const [collapsedRailSections, setCollapsedRailSections] = useState<Set<TerminalRailSection>>(() => new Set(['other']))
  const [error, setError] = useState<string | null>(null)
  const [terminalActionBusy, setTerminalActionBusy] = useState<string | null>(null)
  const [debugPanelOpenForID, setDebugPanelOpenForID] = useState<string | null>(null)
  const [errorPanelOpenForID, setErrorPanelOpenForID] = useState<string | null>(null)
  const [debugText, setDebugText] = useState<string>('')
  const debugMenuRef = useRef<HTMLDivElement | null>(null)
  const errorMenuRef = useRef<HTMLDivElement | null>(null)

  const activeWorkflowPath = useGlobalPresetStore(state => {
    const activeWorkflowId = state.activePresetIds.workflow
    if (!activeWorkflowId) return null
    return state.workflowPresets.find(preset => preset.id === activeWorkflowId)?.selectedFolder?.filepath ?? null
  })
  const isWorkflowTerminalContext = useChatStore(state => {
    if (!currentSessionId) return false
    return Object.values(state.chatTabs).some(tab =>
      tab.sessionId === currentSessionId &&
      tab.metadata?.mode === 'workflow'
    )
  })
  const mainTranscriptReadingKey = useChatStore(state => {
    const tab = Object.values(state.chatTabs).find(candidate => candidate.sessionId === currentSessionId)
    return tab ? transcriptReadingKey(tab.tabId, currentSessionId ?? undefined) : undefined
  })
  const activeEventViewMode = useChatStore(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return normalizeEventViewMode(tab?.viewMode ?? state.eventViewModePreference)
  })
  const selectedTabRequiresHistoryHydration = useChatStore(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return Boolean(
      tab?.sessionId === currentSessionId &&
      (tab?.metadata?.isViewOnly || tab?.config?.restoredConversationPath),
    )
  })
  const selectedTabIsExecution = useChatStore(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return tab?.sessionId === currentSessionId && isExecutionConversationTab(tab)
  })
  const selectedTabPrefersFormattedResume = useChatStore(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return Boolean(
      tab?.sessionId === currentSessionId &&
      tab?.config?.restoredConversationPath,
    )
  })
  const terminalWorkflowPathFilter = isWorkflowTerminalContext ? activeWorkflowPath : null
  const { plan: terminalWorkflowPlan } = usePlanData(terminalWorkflowPathFilter)

  const sessionEvents = useChatStore(state => (
    currentSessionId ? state.tabEvents[currentSessionId] : undefined
  ))
  const sessionHasMoreOlderEvents = useChatStore(state => (
    currentSessionId ? (state.tabHasMoreOlderEvents[currentSessionId] ?? false) : false
  ))
  const sessionStreamingText = useChatStore(state => (
    currentSessionId ? state.streamingText[currentSessionId] : undefined
  ))
  const sessionStreamingStatus = useChatStore(state => (
    currentSessionId ? state.streamingStatus[currentSessionId] : undefined
  ))
  const [selectedTerminalEventPage, setSelectedTerminalEventPage] = useState<SelectedTerminalEventPage>(
    EMPTY_SELECTED_TERMINAL_EVENT_PAGE,
  )
  // Main-agent events live in the session stream rather than the per-terminal
  // cursor endpoint. Keep older pages locally: they are only needed while the
  // user has explicitly opened this Conversation, and should not bloat every
  // other consumer of the session-wide store.
  const [mainSessionOlderEventPage, setMainSessionOlderEventPage] = useState<MainSessionOlderEventPage>(
    EMPTY_MAIN_SESSION_OLDER_EVENT_PAGE,
  )
  const selectedTerminalEventPageRef = useRef(selectedTerminalEventPage)
  const terminalEventRequestGenerationRef = useRef(0)
  const terminalEventRefreshInFlightRef = useRef(false)
  useEffect(() => {
    selectedTerminalEventPageRef.current = selectedTerminalEventPage
  }, [selectedTerminalEventPage])
  useEffect(() => {
    setMainSessionOlderEventPage(current => (
      current.sessionId === currentSessionId ? current : EMPTY_MAIN_SESSION_OLDER_EVENT_PAGE
    ))
  }, [currentSessionId])
  useEffect(() => {
    setDismissedErrorIDs(readDismissedTerminalErrorIDs(currentSessionId))
  }, [currentSessionId])

  const dismissTerminalError = useCallback((errorID: string) => {
    setDismissedErrorIDs(prev => {
      const next = new Set(prev)
      next.add(errorID)
      writeDismissedTerminalErrorIDs(currentSessionId, next)
      return next
    })
  }, [currentSessionId])

  const toggleTerminalError = useCallback((errorID: string) => {
    setExpandedErrorIDs(prev => {
      const next = new Set(prev)
      if (next.has(errorID)) {
        next.delete(errorID)
      } else {
        next.add(errorID)
      }
      return next
    })
  }, [])

  const routingDecisions = useMemo(() => {
    const byKey = new Map<string, RoutingDecision>()
    for (const event of sessionEvents || []) {
      const decision = routingDecisionFromEvent(event)
      if (decision && dismissedRouteIDs.has(decision.id)) continue
      if (!decision) continue
      const key = routeDecisionDedupeKey(decision)
      const existing = byKey.get(key)
      if (!existing || routingDecisionTime(decision) >= routingDecisionTime(existing)) {
        byKey.set(key, decision)
      }
    }
    return Array.from(byKey.values()).sort((a, b) => routingDecisionTime(a) - routingDecisionTime(b))
  }, [sessionEvents, dismissedRouteIDs])
  const routingDecisionByNextStepID = useMemo(() => {
    const byStep = new Map<string, RoutingDecision>()
    for (const decision of routingDecisions) {
      if (!decision.nextStepId || decision.nextStepId === 'end') continue
      byStep.set(decision.nextStepId, decision)
    }
    return byStep
  }, [routingDecisions])
  // Surface genuine error events (llm_generation_error, workflow_error, etc.)
  // on the terminal that caused them when the event carries enough
  // identity. Cancellation events are lifecycle state, not error banners.
  // Only unscoped errors stay in the global banner.
  //
  // CAUTION: the zustand selector returns a value compared by reference.
  // Build the list with useMemo over a narrowly-selected events array
  // so a re-derived list with the same content doesn't trigger an
  // infinite render loop (a previous version returned a fresh [] every
  // call, which Zustand saw as "changed" → re-render → repeat).
  const terminalErrorGroups = useMemo<{
    global: TerminalErrorBannerEntry[]
    byTerminalID: Map<string, TerminalErrorBannerEntry[]>
  }>(() => {
    const byTerminalID = new Map<string, TerminalErrorBannerEntry[]>()
    const global: TerminalErrorBannerEntry[] = []
    if (!sessionEvents || sessionEvents.length === 0) return { global, byTerminalID }
    const toolErrorContexts = toolErrorContextByEventID(sessionEvents)
    const seen = new Set<string>()
    for (let i = sessionEvents.length - 1; i >= 0; i--) {
      const evt = sessionEvents[i] as unknown as { id?: string; type?: string; timestamp?: string }
      if (!evt?.type || !TERMINAL_ERROR_EVENT_TYPES.has(evt.type)) continue
      const id = evt.id || `${evt.type}-${i}`
      if (dismissedErrorIDs.has(id)) continue
      const message = extractErrorMessage(evt) || evt.type.replace(/_/g, ' ')
      const toolContext = toolErrorContexts.get(id)
      const terminal = resolveErrorTerminal(sessionEvents[i], terminals)
      const terminalID = terminal ? terminalPaneKey(terminal) : undefined
      const dedupeKey = `${terminalID || 'global'}:${evt.type}:${compactTerminalErrorMessage(message)}`
      if (seen.has(dedupeKey)) continue
      seen.add(dedupeKey)
      const entry: TerminalErrorBannerEntry = {
        id,
        type: evt.type,
        message,
        timestamp: evt.timestamp,
        terminalID,
        toolName: toolContext?.name,
        toolServer: toolContext?.server,
        toolArguments: toolContext?.args
          ? formatToolCallArguments(toolContext.args).text
          : undefined,
      }
      if (terminalID) {
        const items = byTerminalID.get(terminalID) || []
        items.push(entry)
        byTerminalID.set(terminalID, items)
      } else {
        global.push(entry)
      }
    }
    return { global, byTerminalID }
  }, [sessionEvents, dismissedErrorIDs, terminals])
  const sessionErrorBanner = terminalErrorGroups.global
  const terminalErrorsByID = terminalErrorGroups.byTerminalID
  const terminalCenterRef = useRef<HTMLDivElement | null>(null)
  const terminalOutputRef = useRef<HTMLDivElement | null>(null)
  const xtermScrollToBottomRef = useRef<(() => void) | null>(null)
  const terminalAutoScrollRef = useRef(true)
  const terminalManualScrollLockRef = useRef(false)
  const selectedTerminalIDRef = useRef<string | null>(null)
  const fetchInFlightRef = useRef(false)
  const fetchInFlightScopeRef = useRef<string | null>(null)
  const fetchRequestSeqRef = useRef(0)
  const terminalsRef = useRef<TerminalSnapshot[]>([])
  const terminalDetailCacheRef = useRef<Record<string, TerminalSnapshot>>({})
  const selectedDetailFetchKeysRef = useRef<Set<string>>(new Set())
  const selectedLivePreviewFetchKeysRef = useRef<Set<string>>(new Set())
  const selectedSettledScreenProbeKeysRef = useRef<Set<string>>(new Set())
  const emptyResponseCountRef = useRef(0)
  const lastFetchScopeRef = useRef<string | null>(null)
  const fastPollUntilRef = useRef(0)
  const fastPollIntervalRef = useRef<number | null>(null)
  const lastSizeHintSentRef = useRef<{ cols: number; rows: number } | null>(null)
  const terminalTheme = TERMINAL_THEMES[terminalColorScheme]
  const rawXtermTheme = RAW_XTERM_THEMES[appTheme]

  // PLAT-106 repair 3: the session-change reset below is a useEffect, so it runs
  // AFTER the first render for the new session — that render still paints the
  // previous session's terminals and selection. Switching between a Chat and a
  // Schedule tab therefore showed the wrong session's pane for a frame.
  //
  // Adjusting state during render (React's documented "adjust state when a prop
  // changes" idiom) makes the reset synchronous: React discards this render and
  // immediately re-renders with cleared state, so stale terminals are never
  // committed. The effect below still owns the ref/non-state cleanups.
  const [terminalsSessionID, setTerminalsSessionID] = useState<string | null | undefined>(currentSessionId)
  if (terminalsSessionID !== currentSessionId) {
    setTerminalsSessionID(currentSessionId)
    setTerminals([])
    setSelectedID(null)
    setUserSelectedID(null)
  }

  useEffect(() => {
    setTerminals([])
    setRuntimeStatesBySession({})
    setSelectedID(null)
    setUserSelectedID(null)
    selectedTerminalIDRef.current = null
    terminalAutoScrollRef.current = true
    terminalManualScrollLockRef.current = false
    selectedSettledScreenProbeKeysRef.current.clear()
    selectedLivePreviewFetchKeysRef.current.clear()
  }, [currentSessionId])

  useEffect(() => {
    terminalsRef.current = terminals
  }, [terminals])

  useEffect(() => {
    terminalDetailCacheRef.current = terminalDetailCache
  }, [terminalDetailCache])

  // Insert a freshly-fetched terminal detail into the LRU cache. Keyed by
  // terminalDetailCacheKey (id:chunk_index:updated_at), so an identical key is a
  // no-op — the body is already current.
  const cacheTerminalDetail = useCallback((detail: TerminalSnapshot) => {
    setTerminalDetailCache(current => {
      const key = terminalDetailCacheKey(detail)
      if (current[key]) return current
      const next: Record<string, TerminalSnapshot> = { ...current, [key]: detail }
      const entries = Object.entries(next)
      if (entries.length <= TERMINAL_DETAIL_CACHE_LIMIT) return next
      return Object.fromEntries(entries.slice(entries.length - TERMINAL_DETAIL_CACHE_LIMIT)) as Record<string, TerminalSnapshot>
    })
  }, [])

  const copyTerminalDebug = useCallback(async (terminal: TerminalSnapshot) => {
    await navigator.clipboard.writeText(terminalDebugText(terminal))
    setCopiedTerminalID(terminal.terminal_id)
    window.setTimeout(() => setCopiedTerminalID(current => current === terminal.terminal_id ? null : current), 1500)
  }, [])

  const applyTerminalSnapshotUpdate = useCallback((updated: TerminalSnapshot) => {
    setTerminals(current => current.map(item => (
      item.terminal_id === updated.terminal_id ? { ...item, ...updated } : item
    )))
    setTerminalDetailCache(current => {
      const key = terminalDetailCacheKey(updated)
      const next = Object.fromEntries(
        Object.entries(current).filter(([, detail]) => detail.terminal_id !== updated.terminal_id),
      ) as Record<string, TerminalSnapshot>
      next[key] = updated
      return next
    })
  }, [])

  const forceCompleteTerminal = useCallback(async (terminal: TerminalSnapshot) => {
    if (!canForceCompleteTerminal(terminal)) return
    const optimistic: TerminalSnapshot = {
      ...terminal,
      active: false,
      state: 'completed',
      closes_at: undefined,
      retention_seconds: 0,
      updated_at: new Date().toISOString(),
    }
    applyTerminalSnapshotUpdate(optimistic)
    setTerminalActionBusy('complete')
    try {
      const updated = await agentApi.completeTerminal(terminal.terminal_id)
      applyTerminalSnapshotUpdate(updated)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to mark terminal complete')
    } finally {
      setTerminalActionBusy(current => current === 'complete' ? null : current)
    }
  }, [applyTerminalSnapshotUpdate])

  const forceFailTerminal = useCallback(async (terminal: TerminalSnapshot) => {
    const optimistic: TerminalSnapshot = {
      ...terminal,
      active: false,
      state: 'failed',
      closes_at: undefined,
      retention_seconds: 0,
      updated_at: new Date().toISOString(),
    }
    applyTerminalSnapshotUpdate(optimistic)
    setTerminalActionBusy('fail')
    try {
      const updated = await agentApi.failTerminal(terminal.terminal_id)
      applyTerminalSnapshotUpdate(updated)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to mark terminal failed')
    } finally {
      setTerminalActionBusy(current => current === 'fail' ? null : current)
    }
  }, [applyTerminalSnapshotUpdate])

  const refreshTerminalSnapshot = useCallback(async (terminal: TerminalSnapshot) => {
    if (!canSendTerminalDebugInput(terminal)) return
    setTerminalActionBusy('refresh')
    try {
      const updated = await agentApi.refreshTerminal(terminal.terminal_id, { lines: TERMINAL_REFRESH_HISTORY_LINES })
      applyTerminalSnapshotUpdate(updated)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to refresh terminal')
    } finally {
      setTerminalActionBusy(current => current === 'refresh' ? null : current)
    }
  }, [applyTerminalSnapshotUpdate])

  const killTerminalSession = useCallback(async (terminal: TerminalSnapshot) => {
    if (!canSendTerminalDebugInput(terminal)) return
    const confirmed = window.confirm(`Kill tmux session ${terminal.tmux_session}? This stops the underlying coding agent process.`)
    if (!confirmed) return
    setTerminalActionBusy('kill')
    try {
      const updated = await agentApi.killTerminal(terminal.terminal_id)
      applyTerminalSnapshotUpdate(updated)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to kill terminal tmux session')
    } finally {
      setTerminalActionBusy(current => current === 'kill' ? null : current)
    }
  }, [applyTerminalSnapshotUpdate])

  const copyTerminalPaneText = useCallback(async (terminal: TerminalSnapshot) => {
    await navigator.clipboard.writeText(terminal.content || '')
    setCopiedTerminalID(terminal.terminal_id)
    window.setTimeout(() => setCopiedTerminalID(current => current === terminal.terminal_id ? null : current), 1500)
  }, [])

  const copyTmuxAttachCommand = useCallback(async (terminal: TerminalSnapshot) => {
    if (!terminal.tmux_session) return
    await navigator.clipboard.writeText(`tmux attach -t ${shellQuote(terminal.tmux_session)}`)
    setCopiedTerminalID(terminal.terminal_id)
    window.setTimeout(() => setCopiedTerminalID(current => current === terminal.terminal_id ? null : current), 1500)
  }, [])

  const sendTerminalDebugKey = useCallback(async (terminal: TerminalSnapshot, key: TerminalDebugKey) => {
    if (!canSendTerminalDebugInput(terminal)) return
    setTerminalActionBusy(key)
    try {
      await agentApi.sendTerminalKey(terminal.terminal_id, key)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : `Failed to send ${key}`)
    } finally {
      setTerminalActionBusy(current => current === key ? null : current)
    }
  }, [])

  const sendTerminalDebugText = useCallback(async (terminal: TerminalSnapshot, text: string, submit: boolean) => {
    if (!canSendTerminalDebugInput(terminal) || !text.trim()) return
    setTerminalActionBusy('send-text')
    try {
      await agentApi.sendTerminalInput(terminal.terminal_id, text, submit)
      setDebugText('')
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to send text')
    } finally {
      setTerminalActionBusy(current => current === 'send-text' ? null : current)
    }
  }, [])

  const toggleDebugPanel = useCallback((terminal: TerminalSnapshot) => {
    const key = terminalPaneKey(terminal)
    setSelectedID(key)
    setUserSelectedID(key)
    terminalAutoScrollRef.current = true
    terminalManualScrollLockRef.current = false
    setDebugPanelOpenForID(current => current === key ? null : key)
  }, [])

  // Dismiss the debug action menu on outside click / Escape.
  useEffect(() => {
    if (!debugPanelOpenForID) return
    const handleMouseDown = (event: MouseEvent) => {
      if (debugMenuRef.current?.contains(event.target as Node)) return
      setDebugPanelOpenForID(null)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setDebugPanelOpenForID(null)
    }
    document.addEventListener('mousedown', handleMouseDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handleMouseDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [debugPanelOpenForID])

  // Error details use the same compact header-menu interaction as terminal
  // diagnostics. Keeping the menu out of the terminal body avoids covering
  // live output or changing the tmux viewport whenever a noisy run records an
  // expected/recoverable tool failure.
  useEffect(() => {
    if (!errorPanelOpenForID) return
    const handleMouseDown = (event: MouseEvent) => {
      if (errorMenuRef.current?.contains(event.target as Node)) return
      setErrorPanelOpenForID(null)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setErrorPanelOpenForID(null)
    }
    document.addEventListener('mousedown', handleMouseDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handleMouseDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [errorPanelOpenForID])

  const selectTerminalFromRail = useCallback((terminal: TerminalSnapshot) => {
    const key = terminalPaneKey(terminal)
    setSelectedID(key)
    setUserSelectedID(key)
    terminalAutoScrollRef.current = true
    terminalManualScrollLockRef.current = false
  }, [])

  const fetchTerminals = useCallback(async () => {
    const fetchScope = `${viewAll ? 'all' : (currentSessionId || '')}:${terminalWorkflowPathFilter || ''}`
    if (fetchInFlightRef.current && fetchInFlightScopeRef.current === fetchScope) return
    fetchInFlightRef.current = true
    fetchInFlightScopeRef.current = fetchScope
    const requestSeq = fetchRequestSeqRef.current + 1
    fetchRequestSeqRef.current = requestSeq
    if (lastFetchScopeRef.current !== fetchScope) {
      lastFetchScopeRef.current = fetchScope
      emptyResponseCountRef.current = 0
    }

    try {
      const response = await agentApi.listTerminals(viewAll ? undefined : currentSessionId, 'none')
      const runtimeStates = response.runtime_states || {}
      const visibleTerminals = (response.terminals || [])
        .filter(terminal =>
          !dismissedTerminalIDs.has(terminal.terminal_id) &&
          // A session-scoped response already contains only the main agent and
          // its descendants. Structured children can have an empty or
          // child-local workflow_path, so filtering them again here made both
          // live and retained Clean panes disappear from the rail.
          (!viewAll || terminalMatchesWorkflow(terminal, terminalWorkflowPathFilter))
        )
        .map(terminal => reconcileTerminalRuntimeState(terminal, runtimeStates[terminal.session_id]))

      const nextTerminals = dedupeTerminalsByID(visibleTerminals)
      if (fetchRequestSeqRef.current !== requestSeq) return
      setRuntimeStatesBySession(runtimeStates)
      setTerminals(current => {
        const scopedMainTerminal = current.find(terminal => (
          isMainAgentTerminal(terminal) && !isArchivedTurnTerminal(terminal)
        ))
        const currentMatchesScope = (
          viewAll ||
          !currentSessionId ||
          scopedMainTerminal?.session_id === currentSessionId
        )
        const continuity = preserveTerminalContinuity(current, nextTerminals, {
          sameScope: !viewAll && !!currentSessionId && currentMatchesScope,
          hasPendingActivity: hasPendingTerminalActivity,
          emptyPollCount: emptyResponseCountRef.current,
          gracePolls: EMPTY_TERMINAL_RESPONSE_GRACE_POLLS,
        })
        emptyResponseCountRef.current = continuity.emptyPollCount
        return reconcileTerminalSnapshots(current, continuity.terminals)
      })
      setError(null)
    } catch (err) {
      if (fetchRequestSeqRef.current !== requestSeq) return
      setError(err instanceof Error ? err.message : 'Failed to load terminals')
    } finally {
      if (fetchRequestSeqRef.current === requestSeq) {
        fetchInFlightRef.current = false
        fetchInFlightScopeRef.current = null
      }
    }
  }, [currentSessionId, dismissedTerminalIDs, hasPendingTerminalActivity, terminalWorkflowPathFilter, viewAll])

  const dismissTerminal = useCallback(async (terminal: TerminalSnapshot) => {
    if (!canDismissTerminal(terminal)) return
    setDismissedTerminalIDs(current => {
      const next = new Set(current)
      next.add(terminal.terminal_id)
      return next
    })
    setTerminals(current => current.filter(item => item.terminal_id !== terminal.terminal_id))
    setTerminalDetailCache(current => (
      Object.fromEntries(
        Object.entries(current).filter(([, detail]) => detail.terminal_id !== terminal.terminal_id),
      ) as Record<string, TerminalSnapshot>
    ))
    if (selectedID === terminalPaneKey(terminal)) {
      setSelectedID(null)
    }
    if (userSelectedID === terminalPaneKey(terminal)) {
      setUserSelectedID(null)
    }
    if (debugPanelOpenForID === terminalPaneKey(terminal)) {
      setDebugPanelOpenForID(null)
    }
    try {
      await agentApi.dismissTerminal(terminal.terminal_id)
    } catch (err) {
      // Keep the terminal hidden locally even if the running backend has not picked up
      // the DELETE route yet. A backend restart will make dismissal persistent there too.
      console.warn('Failed to dismiss terminal on backend', err)
    }
  }, [debugPanelOpenForID, selectedID, userSelectedID])

  const clearableNonRunningTerminals = useMemo(
    () => dedupeTerminalsByID(terminals)
      .filter(isRailVisibleTerminal)
      .filter(terminal => !isMainAgentTerminal(terminal) && canDismissTerminal(terminal)),
    [terminals],
  )

  const clearNonRunningTerminals = useCallback(() => {
    clearableNonRunningTerminals.forEach(terminal => {
      void dismissTerminal(terminal)
    })
  }, [clearableNonRunningTerminals, dismissTerminal])

  const groupedTerminals = useMemo(() => {
    const projectedTerminals = projectExecutionTreeTerminals(terminals, sessionExecutionTree)
    const uniqueTerminals = dedupeTerminalsByID(projectedTerminals)
    const railTerminals = uniqueTerminals.filter(isRailVisibleTerminal)
    // Selection always sees the complete terminal set. Rail filters only affect
    // navigation, so changing a filter cannot make the active pane jump.
    const allTerminals = sortTerminalsForRail(railTerminals)
    const activeTerminals = allTerminals.filter(terminal => terminalState(terminal) === 'running')
    const finishedTerminals = allTerminals.filter(terminal => terminalState(terminal) !== 'running')
    const currentTerminals = sortTerminalsNewestFirst(railTerminals.filter(terminal => !isArchivedTurnTerminal(terminal)))
    const logicalGroups = organizeTerminalRail(allTerminals, {
      getState: terminalState,
      isMainAgent: isMainAgentTerminal,
    })
    const normalizedSearch = terminalRailSearch.trim().toLowerCase()
    let visibleGroups = logicalGroups.filter(group => {
      const matchesFilter = terminalRailFilter === 'all' ||
        (terminalRailFilter === 'running' && group.section === 'active') ||
        (terminalRailFilter === 'attention' && group.section === 'attention') ||
        (terminalRailFilter === 'non-running' && group.section !== 'active' && group.section !== 'attention')
      return matchesFilter && (!normalizedSearch || terminalRailGroupSearchText(group).includes(normalizedSearch))
    })
    const selectedRailTerminal = allTerminals.find(terminal => terminalPaneKey(terminal) === selectedID)
    const hiddenSelectedGroup = hiddenSelectedTerminalRailGroup(logicalGroups, visibleGroups, selectedRailTerminal)
    if (hiddenSelectedGroup) {
      const visibleKeys = new Set(visibleGroups.map(group => group.key))
      visibleKeys.add(hiddenSelectedGroup.key)
      visibleGroups = logicalGroups.filter(group => visibleKeys.has(group.key))
    }
    const sectionCounts = logicalGroups.reduce<Record<TerminalRailSection, number>>((counts, group) => {
      counts[group.section] += 1
      return counts
    }, { active: 0, attention: 0, workflow: 0, review: 0, other: 0 })
    return {
      activeTerminals,
      finishedTerminals,
      currentTerminals,
      orderedTerminals: allTerminals,
      logicalGroups,
      visibleGroups,
      sectionCounts,
    }
  }, [terminals, sessionExecutionTree, terminalRailFilter, terminalRailSearch, selectedID])
  const terminalFocusActive = activeEventViewMode === 'terminal'
  const currentMainTerminal = useMemo(
    () => groupedTerminals.currentTerminals.find(terminal => isMainAgentTerminal(terminal)) || null,
    [groupedTerminals.currentTerminals],
  )

  useEffect(() => {
    // Component is now only mounted when Debug view is active (it's
    // not a sidekick panel anymore), so polling should always run
    // whenever this component is on screen. The previous
    // terminalCenterOpen flag gated a standalone toggle that no
    // longer exists.
    void fetchTerminals()
    const interval = window.setInterval(fetchTerminals, TERMINAL_POLL_INTERVAL_MS)
    return () => window.clearInterval(interval)
  }, [fetchTerminals])

  useEffect(() => {
    const stopFastPolling = () => {
      if (fastPollIntervalRef.current !== null) {
        window.clearInterval(fastPollIntervalRef.current)
        fastPollIntervalRef.current = null
      }
    }

    const startFastPolling = () => {
      fastPollUntilRef.current = Date.now() + TERMINAL_FAST_POLL_DURATION_MS
      void fetchTerminals()
      if (fastPollIntervalRef.current !== null) return

      fastPollIntervalRef.current = window.setInterval(() => {
        if (Date.now() > fastPollUntilRef.current) {
          stopFastPolling()
          return
        }
        void fetchTerminals()
      }, TERMINAL_FAST_POLL_INTERVAL_MS)
    }

    window.addEventListener(TERMINAL_REFRESH_REQUEST_EVENT, startFastPolling)
    return () => {
      window.removeEventListener(TERMINAL_REFRESH_REQUEST_EVENT, startFastPolling)
      stopFastPolling()
    }
  }, [fetchTerminals])

  useEffect(() => {
    if (groupedTerminals.orderedTerminals.length === 0) {
      setSelectedID(null)
      return
    }
    const selected = groupedTerminals.orderedTerminals.find(terminal => terminalPaneKey(terminal) === selectedID)
    const userSelected = groupedTerminals.orderedTerminals.find(terminal => terminalPaneKey(terminal) === userSelectedID)
    const latestActive = groupedTerminals.activeTerminals[0]
    const preferredTerminal = preferredTerminalForContext(
      currentMainTerminal,
      [latestActive, groupedTerminals.currentTerminals[0], groupedTerminals.orderedTerminals[0]],
      isWorkflowTerminalContext,
    )

    const canonicalSelected = canonicalTerminalRailSelection(groupedTerminals.logicalGroups, selected)
    if (selected && canonicalSelected && canonicalSelected.terminal_id !== selected.terminal_id) {
      const canonicalKey = terminalPaneKey(canonicalSelected)
      setSelectedID(canonicalKey)
      if (userSelectedID === terminalPaneKey(selected)) setUserSelectedID(canonicalKey)
      return
    }

    if (userSelected) {
      const userSelectedKey = terminalPaneKey(userSelected)
      if (selectedID !== userSelectedKey) {
        setSelectedID(userSelectedKey)
      }
      return
    }

    if (userSelectedID && !userSelected) {
      setUserSelectedID(null)
    }

    if (
      selected &&
      preferredTerminal &&
      isArchivedTurnTerminal(selected) &&
      !isArchivedTurnTerminal(preferredTerminal) &&
      terminalPaneKey(selected) !== terminalPaneKey(preferredTerminal)
    ) {
      setSelectedID(terminalPaneKey(preferredTerminal))
      return
    }

    if ((!selectedID || !selected) && preferredTerminal) {
      setSelectedID(terminalPaneKey(preferredTerminal))
    }
  }, [currentMainTerminal, groupedTerminals, isWorkflowTerminalContext, selectedID, userSelectedID])

  const selectedTerminal = useMemo(
    () => {
      if (!selectedID) return null
      return groupedTerminals.orderedTerminals.find(terminal => terminalPaneKey(terminal) === selectedID) || null
    },
    [groupedTerminals, selectedID],
  )
  const selectedTerminalKey = selectedTerminal ? terminalPaneKey(selectedTerminal) : null
  const selectedTerminalDetailCacheKey = selectedTerminal ? terminalDetailCacheKey(selectedTerminal) : null
  const selectedTerminalView = useMemo(() => {
    if (!selectedTerminal) return null
    const cachedDetail = selectedTerminalDetailCacheKey ? terminalDetailCache[selectedTerminalDetailCacheKey] : undefined
    if (cachedDetail && terminalPaneKey(cachedDetail) === selectedTerminalKey) {
      return mergeTerminalSnapshotBody(selectedTerminal, cachedDetail)
    }
    const staleDetail = latestCachedTerminalDetail(selectedTerminal, terminalDetailCache)
    if (staleDetail && terminalPaneKey(staleDetail) === selectedTerminalKey) {
      return mergeTerminalSnapshotBody(selectedTerminal, staleDetail)
    }
    return selectedTerminal
  }, [selectedTerminal, selectedTerminalDetailCacheKey, selectedTerminalKey, terminalDetailCache])
  const selectedPlanStep = useMemo(() => {
    const stepID = selectedTerminalView?.step_id?.trim()
    if (!stepID || !terminalWorkflowPlan) return null
    return findPlanStepByID(
      [...terminalWorkflowPlan.steps, ...(terminalWorkflowPlan.orphan_steps || [])],
      stepID,
    )
  }, [selectedTerminalView?.step_id, terminalWorkflowPlan])
  const planTitleForTerminal = useCallback((terminal: TerminalSnapshot): string => {
    const stepID = terminal.step_id?.trim()
    if (!stepID || !terminalWorkflowPlan) return ''
    return findPlanStepByID(
      [...terminalWorkflowPlan.steps, ...(terminalWorkflowPlan.orphan_steps || [])],
      stepID,
    )?.title?.trim() || ''
  }, [terminalWorkflowPlan])
  const selectedTerminalDisplayTitle = selectedPlanStep?.title?.trim()
    || (selectedTerminalView ? planTitleForTerminal(selectedTerminalView) : '')
    || (selectedTerminalView ? terminalRailTitle(selectedTerminalView) : '')
    || (selectedTerminalView ? formatTerminalTitle(selectedTerminalView) : '')
  const showSelectedPlanStep = useCallback(() => {
    if (!selectedPlanStep) return

    // Match the existing Plan toolbar behavior before asking the mounted canvas
    // to reveal the node and its read-only detail sidebar. openWorkspaceView
    // shows the pane, sets the view, and minimizes the file workspace.
    const workflowStore = useWorkflowStore.getState()
    workflowStore.openWorkspaceView('flow')
    workflowStore.setFocusedPane('preview')
    requestWorkflowPlanStepFocus({
      stepId: selectedPlanStep.id,
      workspacePath: terminalWorkflowPathFilter,
    })
  }, [selectedPlanStep, terminalWorkflowPathFilter])

  const priorArchivedTurns = useMemo(
    () => (selectedTerminalView ? findPriorArchivedTurns(selectedTerminalView, terminals) : []),
    [selectedTerminalView, terminals],
  )

  // Lazily fetch full content for each prior :turn- snapshot so the
  // aggregated scrollback isn't blank. The rail's listTerminals poll uses
  // content='none' for payload size, so archived turn bodies aren't already
  // in state. Archived snapshots are immutable, so a one-shot fetch per id
  // is enough — keep a cache to skip refetches when terminals state churns.
  useEffect(() => {
    if (priorArchivedTurns.length === 0) return
    let cancelled = false
    const missing = priorArchivedTurns
      .filter(t => archivedTurnContents[t.terminal_id] === undefined)
      .slice(-ARCHIVED_TURN_PREFETCH_LIMIT)
    if (missing.length === 0) return
    void (async () => {
      const results: Array<{ id: string; content: string }> = []
      for (const t of missing) {
        if (cancelled) return
        try {
          const detailOptions = terminalStoredRequestOptions('archived-turn')
          const detail = await agentApi.getTerminal(t.terminal_id, detailOptions)
          logTerminalScrollDebug('archived-turn', t, detailOptions, detail)
          results.push({ id: t.terminal_id, content: detail.content || '' })
        } catch (err) {
          console.warn('Failed to load archived turn content', t.terminal_id, err)
          results.push({ id: t.terminal_id, content: '' })
        }
      }
      if (cancelled) return
      setArchivedTurnContents(prev => {
        const next = { ...prev }
        for (const r of results) next[r.id] = r.content
        return next
      })
    })()
    return () => { cancelled = true }
  }, [priorArchivedTurns, archivedTurnContents])

  const selectedTerminalDisplayContent = useMemo(
    () => {
      if (!selectedTerminalView) return ''
      const aggregated = aggregatePriorTurnContent(selectedTerminalView, priorArchivedTurns, archivedTurnContents)
      return trimTerminalDisplayContent(aggregated)
    },
    [selectedTerminalView, priorArchivedTurns, archivedTurnContents],
  )
  // Per-terminal explicit view choices. Raw tmux is the default; a tmux pane shows
  // raw TUI bytes, but the same turn is ALSO emitted as structured events
  // (tool_call_start/end with arguments, llm_generation_end with the answer) --
  // that is what the transcript renders for structured providers. Both views
  // describe the same run, so which one is useful is a reading choice, not a
  // property of the transport. Coding-agent TUIs commonly use tmux's alternate
  // screen (history_size=0), so the durable transcript remains available for
  // both parent and child agents when the user explicitly selects it.
  const [formattedViewPreferences, setFormattedViewPreferences] = useState<Record<string, boolean>>({})
  const selectedTerminalIsSynthetic = selectedTerminalView ? isSyntheticTerminal(selectedTerminalView) : false
  const selectedTerminalIsTmux = Boolean(
    selectedTerminalView &&
    !selectedTerminalIsSynthetic &&
    selectedTerminalView.tmux_session,
  )
  const selectedTerminalID = selectedTerminalView?.terminal_id ?? null
  const selectedTerminalViewPreferenceKey = terminalViewPreferenceKey(selectedTerminalView)
  const selectedTerminalState = (selectedTerminalView?.state || '').trim().toLowerCase()
  const isSelectedTerminalStreaming = shouldStreamTerminal(selectedTerminalView)
  const isSelectedTerminalTurnBusy = Boolean(
    selectedTerminalView && terminalTurnIsBusy(
      selectedTerminalView,
      runtimeStatesBySession[selectedTerminalView.session_id],
    ),
  )
  const selectedTerminalHasPendingConversationWork = Boolean(
    selectedTerminalView && (
      terminalConversationHasPendingWork(
        selectedTerminalView,
        runtimeStatesBySession[selectedTerminalView.session_id],
        terminals,
      ) ||
      // ChatArea owns the submitted-turn lifecycle. It remains true across the
      // gap where the main foreground is idle and a just-dispatched child has
      // not yet appeared in the runtime snapshot/terminal ledger.
      (isMainAgentTerminal(selectedTerminalView) && hasPendingTerminalActivity)
    ),
  )
  const selectedTerminalHasBusyChild = Boolean(
    selectedTerminalView &&
    isMainAgentTerminal(selectedTerminalView) &&
    terminals.some(terminal => (
      terminal.terminal_id !== selectedTerminalView.terminal_id &&
      terminal.session_id === selectedTerminalView.session_id &&
      !isMainAgentTerminal(terminal) &&
      terminalTurnIsBusy(terminal, runtimeStatesBySession[terminal.session_id])
    ))
  )
  const selectedTerminalWaitingForBackground = Boolean(
    selectedTerminalView &&
    isMainAgentTerminal(selectedTerminalView) &&
    !isSelectedTerminalTurnBusy &&
    (runtimeStatesBySession[selectedTerminalView.session_id]?.background_live ||
      selectedTerminalHasBusyChild ||
      hasPendingTerminalActivity)
  )
  const selectedTerminalUsesSessionEvents = Boolean(
    selectedTerminalView && isMainAgentTerminal(selectedTerminalView),
  )
  useEffect(() => {
    if (!selectedTabPrefersFormattedResume || !selectedTerminalViewPreferenceKey) return
    setFormattedViewPreferences(current => (
      Object.prototype.hasOwnProperty.call(current, selectedTerminalViewPreferenceKey)
        ? current
        : { ...current, [selectedTerminalViewPreferenceKey]: true }
    ))
  }, [selectedTabPrefersFormattedResume, selectedTerminalViewPreferenceKey])
  // The toggle describes an available view, not already-loaded data. In
  // particular, restored Schedule tabs intentionally start with no event
  // history; hiding the toggle in that state made Formatted unreachable.
  const canShowFormattedView = canToggleTerminalView(
    selectedTerminalView,
    selectedTerminalIsSynthetic,
    Boolean(selectedTerminalDisplayContent),
    selectedTerminalUsesSessionEvents,
  )
  const showFormattedView = resolveTerminalFormattedView(
    canShowFormattedView,
    selectedTerminalViewPreferenceKey
      ? (formattedViewPreferences[selectedTerminalViewPreferenceKey] ?? (selectedTabPrefersFormattedResume ? true : undefined))
      : undefined,
  )
  const selectedHeaderTitle = showFormattedView && selectedTerminalView && isMainAgentTerminal(selectedTerminalView)
    ? humanizeIdentifier(
        selectedTerminalView.workflow_label
          || selectedTerminalView.workflow_name
          || selectedTerminalDisplayTitle.split('>')[0]
          || 'Conversation',
      )
    : selectedTerminalDisplayTitle
  const selectedTerminalCanLoadEvents = shouldLoadTerminalEvents(
    selectedTerminalView,
    selectedTerminalUsesSessionEvents,
    selectedTerminalIsSynthetic || showFormattedView,
  )

  const loadSelectedTerminalEventPage = useCallback(async () => {
    if (!selectedTerminalID || !selectedTerminalCanLoadEvents) return

    terminalEventRefreshInFlightRef.current = false
    const generation = ++terminalEventRequestGenerationRef.current
    setSelectedTerminalEventPage({
      ...EMPTY_SELECTED_TERMINAL_EVENT_PAGE,
      terminalId: selectedTerminalID,
      loading: true,
    })
    try {
      const response = await agentApi.getTerminalEvents(selectedTerminalID, {
        limit: TERMINAL_EVENT_PAGE_LIMIT,
      })
      if (terminalEventRequestGenerationRef.current !== generation) return
      const events = mergeTerminalEventPages([], response.events || [])
      const bounds = terminalEventSequenceBounds(events)
      setSelectedTerminalEventPage({
        terminalId: selectedTerminalID,
        events,
        loaded: true,
        loading: false,
        loadingOlder: false,
        hasOlder: response.has_older,
        oldestSequence: response.oldest_sequence ?? bounds.oldestSequence,
        latestSequence: response.latest_sequence ?? bounds.latestSequence,
      })
    } catch (loadError) {
      if (terminalEventRequestGenerationRef.current !== generation) return
      setSelectedTerminalEventPage({
        ...EMPTY_SELECTED_TERMINAL_EVENT_PAGE,
        terminalId: selectedTerminalID,
        loaded: true,
        error: loadError instanceof Error ? loadError.message : 'Failed to load conversation events.',
      })
    }
  }, [selectedTerminalCanLoadEvents, selectedTerminalID])

  useEffect(() => {
    terminalEventRequestGenerationRef.current++
    terminalEventRefreshInFlightRef.current = false
    if (!selectedTerminalID || !selectedTerminalCanLoadEvents) {
      // Switching back to Raw must not fetch or discard transcript data. Clear
      // only when selection moved to another terminal.
      if (selectedTerminalEventPageRef.current.terminalId !== selectedTerminalID) {
        setSelectedTerminalEventPage(EMPTY_SELECTED_TERMINAL_EVENT_PAGE)
      }
      return
    }
    const currentPage = selectedTerminalEventPageRef.current
    if (currentPage.terminalId === selectedTerminalID && currentPage.loaded) return
    void loadSelectedTerminalEventPage()
  }, [loadSelectedTerminalEventPage, selectedTerminalCanLoadEvents, selectedTerminalID])

  const [mainEventHydration, setMainEventHydration] = useState<{
    sessionId: string | null
    loading: boolean
    loaded?: boolean
    error?: string
  }>({ sessionId: null, loading: false })

  const loadMainSessionEvents = useCallback(async () => {
    if (!currentSessionId) return
    setMainEventHydration({ sessionId: currentSessionId, loading: true })
    try {
      if (selectedTabIsExecution) {
        await hydrateExecutionConversation(currentSessionId, activeWorkflowPath || undefined)
      } else {
        await hydrateTabEvents(currentSessionId, { workspacePath: activeWorkflowPath || undefined })
      }
      setMainEventHydration({ sessionId: currentSessionId, loading: false, loaded: true })
    } catch (loadError) {
      setMainEventHydration({
        sessionId: currentSessionId,
        loading: false,
        error: loadError instanceof Error ? loadError.message : 'Failed to load conversation events.',
      })
    }
  }, [activeWorkflowPath, currentSessionId, selectedTabIsExecution])

  useEffect(() => {
    if (!shouldHydrateMainTerminalEvents(
      selectedTerminalUsesSessionEvents,
      showFormattedView,
      sessionEvents?.length ?? 0,
      selectedTabRequiresHistoryHydration,
      mainEventHydration.sessionId === currentSessionId && !!mainEventHydration.loaded,
    )) return
    if (mainEventHydration.sessionId === currentSessionId && mainEventHydration.loading) return
    void loadMainSessionEvents()
  }, [
    currentSessionId,
    loadMainSessionEvents,
    mainEventHydration.loaded,
    mainEventHydration.loading,
    mainEventHydration.sessionId,
    selectedTabRequiresHistoryHydration,
    selectedTerminalUsesSessionEvents,
    sessionEvents?.length,
    showFormattedView,
  ])

  const mainSessionHasOlderEvents = mainSessionOlderEventPage.sessionId === currentSessionId &&
    mainSessionOlderEventPage.hasOlder !== undefined
    ? mainSessionOlderEventPage.hasOlder
    : sessionHasMoreOlderEvents

  const loadOlderMainSessionEvents = useCallback(async () => {
    if (!currentSessionId || !mainSessionHasOlderEvents || mainSessionOlderEventPage.loadingOlder) return

    const sessionId = currentSessionId
    const offset = mainSessionOlderEventPage.sessionId === sessionId
      ? mainSessionOlderEventPage.nextOffset
      : 0
    setMainSessionOlderEventPage(current => (
      current.sessionId === sessionId
        ? { ...current, loadingOlder: true, error: undefined }
        : {
            sessionId,
            events: [],
            loadingOlder: true,
            hasOlder: undefined,
            nextOffset: offset,
          }
    ))
    try {
      // Request the full session page. selectTerminalEvents below already
      // removes events owned by sibling terminals; fetching the full page is
      // what makes the offset a stable cursor instead of skipping history that
      // the generic chat working set happened to filter out.
      const response = await agentApi.getSessionEvents(sessionId, undefined, {
        limit: TERMINAL_EVENT_PAGE_LIMIT,
        offset,
        workingSet: 'all',
      })
      setMainSessionOlderEventPage(current => {
        if (current.sessionId !== sessionId) return current
        const pageEvents = response.events || []
        return {
          ...current,
          events: mergeTerminalEventPages(current.events, pageEvents),
          loadingOlder: false,
          hasOlder: response.has_more,
          // Offset advances through the server's full page even when a later
          // terminal-selection filter hides some entries from this view.
          nextOffset: offset + pageEvents.length,
        }
      })
    } catch (loadError) {
      setMainSessionOlderEventPage(current => current.sessionId === sessionId
        ? {
            ...current,
            loadingOlder: false,
            error: loadError instanceof Error ? loadError.message : 'Failed to load older conversation events.',
          }
        : current)
    }
  }, [currentSessionId, mainSessionHasOlderEvents, mainSessionOlderEventPage.loadingOlder, mainSessionOlderEventPage.nextOffset, mainSessionOlderEventPage.sessionId])

  const toggleFormattedView = useCallback((preferenceKey: string, currentlyFormatted: boolean) => {
    const nextFormatted = !currentlyFormatted
    setFormattedViewPreferences(current => ({
      ...current,
      [preferenceKey]: nextFormatted,
    }))
    if (shouldHydrateMainTerminalEvents(
      selectedTerminalUsesSessionEvents,
      nextFormatted,
      sessionEvents?.length ?? 0,
      selectedTabRequiresHistoryHydration,
      mainEventHydration.sessionId === currentSessionId && !!mainEventHydration.loaded,
    )) {
      void loadMainSessionEvents()
    }
  }, [
    currentSessionId,
    loadMainSessionEvents,
    mainEventHydration.loaded,
    mainEventHydration.sessionId,
    selectedTabRequiresHistoryHydration,
    selectedTerminalUsesSessionEvents,
    sessionEvents?.length,
  ])

  const refreshSelectedTerminalEvents = useCallback(async () => {
    const page = selectedTerminalEventPageRef.current
    if (
      !selectedTerminalID ||
      !selectedTerminalCanLoadEvents ||
      page.terminalId !== selectedTerminalID ||
      !page.loaded ||
      page.loading ||
      terminalEventRefreshInFlightRef.current
    ) return

    terminalEventRefreshInFlightRef.current = true
    const generation = terminalEventRequestGenerationRef.current
    try {
      const response = await agentApi.getTerminalEvents(selectedTerminalID, {
        limit: TERMINAL_EVENT_PAGE_LIMIT,
        afterSequence: page.latestSequence,
      })
      if (terminalEventRequestGenerationRef.current !== generation) return
      setSelectedTerminalEventPage(current => {
        if (current.terminalId !== selectedTerminalID) return current
        const merged = mergeNewerTerminalEventPage(current, response)
        return {
          ...current,
          ...merged,
          error: undefined,
        }
      })
    } catch (refreshError) {
      if (terminalEventRequestGenerationRef.current !== generation) return
      setSelectedTerminalEventPage(current => current.terminalId === selectedTerminalID
        ? {
            ...current,
            error: refreshError instanceof Error ? refreshError.message : 'Failed to refresh conversation events.',
          }
        : current)
    } finally {
      if (terminalEventRequestGenerationRef.current === generation) {
        terminalEventRefreshInFlightRef.current = false
      }
    }
  }, [selectedTerminalCanLoadEvents, selectedTerminalID])

  // Detailed child events do not enter the session-wide store. Poll only the
  // selected live transcript, and do one final refresh whenever its terminal
  // snapshot changes or settles.
  useEffect(() => {
    if (!selectedTerminalID || !selectedTerminalCanLoadEvents) return
    void refreshSelectedTerminalEvents()
    if (!isSelectedTerminalStreaming) return
    const interval = window.setInterval(
      () => { void refreshSelectedTerminalEvents() },
      TERMINAL_EVENT_LIVE_POLL_INTERVAL_MS,
    )
    return () => window.clearInterval(interval)
  }, [
    isSelectedTerminalStreaming,
    refreshSelectedTerminalEvents,
    selectedTerminalID,
    selectedTerminalCanLoadEvents,
    selectedTerminalView?.chunk_index,
    selectedTerminalView?.updated_at,
  ])

  const loadOlderSelectedTerminalEvents = useCallback(async () => {
    const page = selectedTerminalEventPageRef.current
    if (
      !selectedTerminalID ||
      !selectedTerminalCanLoadEvents ||
      page.terminalId !== selectedTerminalID ||
      page.loadingOlder ||
      !page.hasOlder ||
      !page.oldestSequence
    ) return

    const generation = terminalEventRequestGenerationRef.current
    setSelectedTerminalEventPage(current => current.terminalId === selectedTerminalID
      ? { ...current, loadingOlder: true, error: undefined }
      : current)
    try {
      const response = await agentApi.getTerminalEvents(selectedTerminalID, {
        limit: TERMINAL_EVENT_PAGE_LIMIT,
        beforeSequence: page.oldestSequence,
      })
      if (terminalEventRequestGenerationRef.current !== generation) return
      setSelectedTerminalEventPage(current => {
        if (current.terminalId !== selectedTerminalID) return current
        const events = mergeTerminalEventPages(current.events, response.events || [])
        const bounds = terminalEventSequenceBounds(events)
        return {
          ...current,
          events,
          loadingOlder: false,
          hasOlder: response.has_older,
          oldestSequence: bounds.oldestSequence ?? current.oldestSequence,
          latestSequence: bounds.latestSequence ?? current.latestSequence,
        }
      })
    } catch (loadError) {
      if (terminalEventRequestGenerationRef.current !== generation) return
      setSelectedTerminalEventPage(current => current.terminalId === selectedTerminalID
        ? {
            ...current,
            loadingOlder: false,
            error: loadError instanceof Error ? loadError.message : 'Failed to load older conversation events.',
          }
        : current)
    }
  }, [selectedTerminalCanLoadEvents, selectedTerminalID])

  const selectedTerminalEventSource = selectedTerminalUsesSessionEvents
    ? mergeTerminalEventPages(
        mainSessionOlderEventPage.sessionId === currentSessionId ? mainSessionOlderEventPage.events : [],
        sessionEvents || [],
      )
    : selectedTerminalEventPage.terminalId === selectedTerminalID
      ? selectedTerminalEventPage.events
      : EMPTY_TERMINAL_EVENTS
  const selectedTerminalEvents = useMemo(
    () => selectTerminalEvents(selectedTerminalEventSource, selectedTerminalView, terminals),
    [selectedTerminalEventSource, selectedTerminalView, terminals],
  )
  const selectedTerminalHasPreValidationEvent = useMemo(
    () => selectedTerminalEvents.some(event => event.type === 'pre_validation_completed'),
    [selectedTerminalEvents],
  )
  // Live-attach is the ONLY transport for the selected live tmux terminal: it
  // renders over the /api/terminals/{id}/stream WebSocket while the selected
  // terminal is active. Completed tmux panes render through StaticXtermPane from
  // a capture snapshot; keeping a websocket open for settled panes creates a
  // remount/reconnect loop as the snapshot chunk metadata changes.
  const useLiveAttachForSelected = !!(
    selectedTerminalView &&
    !selectedTerminalIsSynthetic &&
    selectedTerminalView.tmux_session &&
    isSelectedTerminalStreaming
  )
  const selectedLiveAttachPhase = selectedTerminalView
    ? isSelectedTerminalStreaming
      ? 'live'
      : `settled:${selectedTerminalView.state || 'unknown'}:${selectedTerminalView.chunk_index ?? 'x'}`
    : 'none'
  // Keep the live-attach pane mounted across transient selection nulls. The old
  // polling path momentarily drops the selected terminal from groupedTerminals on
  // each list refresh (and a session-id blip briefly clears the selection), which
  // would otherwise unmount LiveAttachXtermPane, close the WS, and cancel the
  // control-mode attach before it can stream — producing stacked capture-pane
  // snapshots instead of smooth %output. Debounce the unmount so the attach
  // establishes once and streams; a concrete non-live selection drops immediately.
  const liveAttachTerminalId =
    useLiveAttachForSelected && selectedTerminalView ? selectedTerminalView.terminal_id : null
  const liveAttachStreamKey =
    useLiveAttachForSelected && selectedTerminalView
      ? `${selectedTerminalView.terminal_id}:${selectedTerminalView.tmux_session || ''}:${selectedLiveAttachPhase}`
      : null
  const [stableLiveAttach, setStableLiveAttach] = useState<{ terminalId: string; streamKey: string } | null>(null)
  useEffect(() => {
    if (liveAttachTerminalId && liveAttachStreamKey) {
      setStableLiveAttach(prev => (
        prev?.terminalId === liveAttachTerminalId && prev?.streamKey === liveAttachStreamKey
          ? prev
          : { terminalId: liveAttachTerminalId, streamKey: liveAttachStreamKey }
      ))
      return
    }
    if (selectedTerminalView) {
      // A concrete non-live / synthetic selection — switch away immediately.
      setStableLiveAttach(null)
      return
    }
    // selectedTerminalView is transiently null (list/session flicker) — debounce.
    const timer = window.setTimeout(() => setStableLiveAttach(null), 800)
    return () => window.clearTimeout(timer)
  }, [liveAttachTerminalId, liveAttachStreamKey, selectedTerminalView])
  const stableLiveAttachId = stableLiveAttach?.terminalId ?? null
  const stableLiveAttachKey = stableLiveAttach?.streamKey ?? null

  useEffect(() => {
    if (!selectedTerminalView || !useLiveAttachForSelected || !selectedTerminalView.tmux_session) return
    if (selectedTerminalView.content?.trim()) return

    const previewKey = `${selectedTerminalView.terminal_id}:${selectedTerminalView.tmux_session}`
    if (selectedLivePreviewFetchKeysRef.current.has(previewKey)) return
    selectedLivePreviewFetchKeysRef.current.add(previewKey)

    void agentApi.getTerminal(selectedTerminalView.terminal_id, {
      content: 'screen',
      lines: LIVE_ATTACH_SNAPSHOT_LINES,
      debugSource: 'selected-live-preview',
    })
      .then(detail => {
        if (!detail.content?.trim()) return
        cacheTerminalDetail(detail)
      })
      .catch(err => {
        selectedLivePreviewFetchKeysRef.current.delete(previewKey)
        console.warn('Failed to load live terminal preview', selectedTerminalView.terminal_id, err)
      })
  }, [
    selectedTerminalView,
    useLiveAttachForSelected,
    cacheTerminalDetail,
  ])

  useEffect(() => {
    if (!selectedTerminalView || useLiveAttachForSelected) return
    if (selectedTerminalView.execution_tree_placeholder) return
    const detailKey = terminalDetailCacheKey(selectedTerminalView)
    const cached = terminalDetailCacheRef.current[detailKey]
    // selectedTerminalView may contain a deliberately stale cached body to
    // avoid flicker. Only the raw latest list snapshot or the cache entry for
    // its exact revision can prove that the latest turn has been fetched.
    if (!selectedTerminal || hasFreshTerminalDetailBody(selectedTerminal, cached)) return
    if (selectedDetailFetchKeysRef.current.has(detailKey)) return
    selectedDetailFetchKeysRef.current.add(detailKey)

    let cancelled = false
    const detailOptions = terminalRequestOptions(selectedTerminalView, true, 'selected-detail') || terminalStoredRequestOptions('selected-detail')
    void agentApi.getTerminal(selectedTerminalView.terminal_id, detailOptions)
      .then(detail => {
        if (cancelled) return
        logTerminalScrollDebug('selected-detail', selectedTerminalView, detailOptions, detail)
        cacheTerminalDetail(detail)
      })
      .catch(err => {
        console.warn('Failed to load selected terminal detail', selectedTerminalView.terminal_id, err)
      })
    return () => {
      cancelled = true
    }
  }, [
    selectedTerminalView,
    selectedTerminal,
    useLiveAttachForSelected,
    cacheTerminalDetail,
  ])

  const selectedTerminalIDForSettledProbe = selectedTerminalView?.terminal_id
  const selectedTerminalTmuxForSettledProbe = selectedTerminalView?.tmux_session

  useEffect(() => {
    if (!selectedTerminalIDForSettledProbe || selectedTerminalIsSynthetic || !selectedTerminalTmuxForSettledProbe) return
    // The selected live tmux pane is rendered from the WebSocket byte stream.
    // Polling capture-pane in parallel reseeds xterm and makes in-place TUI
    // redraws look like the whole screen is refreshing. Once the pane is no
    // longer streaming, take one final history snapshot for the settled view so
    // Claude/Codex transcript text above the final screen remains scrollable.
    const probePrefix = `${selectedTerminalIDForSettledProbe}:${selectedTerminalTmuxForSettledProbe}:`
    if (isSelectedTerminalStreaming) {
      for (const key of Array.from(selectedSettledScreenProbeKeysRef.current)) {
        if (key.startsWith(probePrefix)) selectedSettledScreenProbeKeysRef.current.delete(key)
      }
      return
    }
    const probeKey = `${probePrefix}${selectedTerminalState || 'unknown'}`
    if (selectedSettledScreenProbeKeysRef.current.has(probeKey)) return
    selectedSettledScreenProbeKeysRef.current.add(probeKey)

    let cancelled = false
    let inFlight = false
    let timer: number | undefined
    let attempts = 0
    let stableSamples = 0
    let previousContent: string | null = null
    const startedAt = Date.now()
    const terminalId = selectedTerminalIDForSettledProbe

    const probeSelectedSettledHistory = () => {
      if (cancelled || inFlight) return
      inFlight = true
      attempts += 1
      const detailOptions = withTerminalScrollDebug({
        content: 'history',
        lines: TERMINAL_ACTIVE_DISPLAY_HISTORY_LINES,
      }, 'selected-settled-history')
      void agentApi.getTerminal(terminalId, detailOptions)
        .then(detail => {
          if (cancelled) return
          logTerminalScrollDebug('selected-settled-history', detail, detailOptions, detail)
          cacheTerminalDetail(detail)

          const content = detail.content || ''
          stableSamples = previousContent !== null && content === previousContent
            ? stableSamples + 1
            : 0
          previousContent = content
        })
        .catch(err => {
          if (!cancelled) {
            console.warn('Failed to refresh selected settled terminal history', terminalId, err)
          }
        })
        .finally(() => {
          inFlight = false
          if (cancelled) return
          const observedLongEnough = Date.now() - startedAt >= TERMINAL_SETTLED_CAPTURE_MIN_WINDOW_MS
          const contentIsStable = stableSamples >= TERMINAL_SETTLED_CAPTURE_STABLE_SAMPLES
          if (attempts >= TERMINAL_SETTLED_CAPTURE_MAX_ATTEMPTS || (observedLongEnough && contentIsStable)) {
            return
          }
          timer = window.setTimeout(probeSelectedSettledHistory, TERMINAL_SETTLED_CAPTURE_INTERVAL_MS)
        })
    }

    probeSelectedSettledHistory()
    return () => {
      cancelled = true
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [
    isSelectedTerminalStreaming,
    selectedTerminalIDForSettledProbe,
    selectedTerminalTmuxForSettledProbe,
    selectedTerminalState,
    selectedTerminalIsSynthetic,
    cacheTerminalDetail,
  ])

  const selectedRouteDecision = selectedTerminalView?.step_id
    ? routingDecisionByNextStepID.get(selectedTerminalView.step_id)
    : undefined
  const selectedTerminalErrors = selectedTerminalView
    ? terminalErrorsByID.get(terminalPaneKey(selectedTerminalView)) || []
    : []
  const selectedTerminalErrorEntries = selectedTerminalView
    ? [...selectedTerminalErrors, ...sessionErrorBanner]
    : []
  const selectedTerminalErrorPanelKey = selectedTerminalView
    ? terminalPaneKey(selectedTerminalView)
    : null
  const hasBusyTerminal = groupedTerminals.orderedTerminals.some(terminal => (
    terminalTurnIsBusy(terminal, runtimeStatesBySession[terminal.session_id])
  ))
  const railSpinner = useSpinnerFrame(hasBusyTerminal)
  const selectedTerminalSpinner = useSpinnerFrame(selectedTerminalHasPendingConversationWork)

  const activeRailTmuxProbeTargets = useMemo(
    () => groupedTerminals.orderedTerminals
      .filter(terminal =>
        terminalState(terminal) === 'running' &&
        !!terminal.tmux_session &&
        terminal.terminal_id !== selectedTerminalView?.terminal_id &&
        !isMainAgentTerminal(terminal)
      )
      .slice(0, TERMINAL_ACTIVE_RAIL_PROBE_LIMIT),
    [groupedTerminals.orderedTerminals, selectedTerminalView?.terminal_id],
  )

  // The rail/list poll is metadata-only. Without a screen probe, a workflow-step
  // tmux pane that finished without a fresh streaming_end can keep showing the
  // spinner until the user clicks it. Probe visible active step panes in the
  // background so their state can settle to completed in the tree.
  useEffect(() => {
    if (activeRailTmuxProbeTargets.length === 0) return
    let cancelled = false
    let inFlight = false

    const probeActiveRailTerminals = () => {
      if (cancelled || inFlight) return
      inFlight = true
      void Promise.all(activeRailTmuxProbeTargets.map(async terminal => {
        // Skip the terminal rendering over live-attach: its control-mode client
        // owns that session, and a parallel capture-pane probe interferes with the
        // %output stream (makes pi's in-place redraws stack).
        if (terminal.terminal_id === stableLiveAttachId) return null
        const detailOptions = withTerminalScrollDebug({
          content: 'screen',
          lines: TERMINAL_ACTIVE_RAIL_PROBE_SCREEN_LINES,
        }, 'rail-probe')
        try {
          const detail = await agentApi.getTerminal(terminal.terminal_id, detailOptions)
          logTerminalScrollDebug('rail-probe', terminal, detailOptions, detail)
          return detail
        } catch {
          return null
        }
      })).then(details => {
        if (cancelled) return
        let applied = false
        for (const detail of details) {
          if (!detail) continue
          const current = terminalsRef.current.find(terminal => terminal.terminal_id === detail.terminal_id)
          const changed = !current ||
            current.active !== detail.active ||
            current.state !== detail.state ||
            current.chunk_index !== detail.chunk_index ||
            current.updated_at !== detail.updated_at
          if (changed) {
            cacheTerminalDetail(detail)
            applyTerminalSnapshotUpdate(detail)
            applied = true
          }
        }
        if (applied) void fetchTerminals()
      }).finally(() => {
        inFlight = false
      })
    }

    const interval = window.setInterval(probeActiveRailTerminals, TERMINAL_POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [activeRailTmuxProbeTargets, stableLiveAttachId, applyTerminalSnapshotUpdate, cacheTerminalDetail, fetchTerminals])

  const handleXtermViewportStickChange = useCallback((isNearBottom: boolean) => {
    terminalAutoScrollRef.current = isNearBottom
    terminalManualScrollLockRef.current = !isNearBottom
  }, [])

  const registerXtermScrollToBottom = useCallback((handler: (() => void) | null) => {
    xtermScrollToBottomRef.current = handler
  }, [])

  const scrollSelectedTerminalToBottom = useCallback(() => {
    terminalAutoScrollRef.current = true
    terminalManualScrollLockRef.current = false
    xtermScrollToBottomRef.current?.()
    const scroll = () => {
      const el = terminalOutputRef.current
      if (!el) return
      el.scrollTop = Math.max(0, el.scrollHeight - el.clientHeight)
    }
    scroll()
    window.requestAnimationFrame(scroll)
  }, [])

  useEffect(() => {
    const el = terminalOutputRef.current
    if (!el || !selectedTerminalDisplayContent) return

    const terminalChanged = selectedTerminalIDRef.current !== selectedTerminalKey
    if (terminalChanged) {
      const isFirstSelection = selectedTerminalIDRef.current === null
      selectedTerminalIDRef.current = selectedTerminalKey
      if (isFirstSelection || !terminalManualScrollLockRef.current) {
        terminalAutoScrollRef.current = true
        terminalManualScrollLockRef.current = false
      }
    }

    if (!terminalAutoScrollRef.current || terminalManualScrollLockRef.current) return

    const frame = window.requestAnimationFrame(() => {
      const maxScrollTop = Math.max(0, el.scrollHeight - el.clientHeight)
      el.scrollTop = maxScrollTop
    })
    return () => window.cancelAnimationFrame(frame)
  }, [selectedTerminalKey, selectedTerminalDisplayContent])

  const measureTerminalElementSize = useCallback((el: HTMLElement | null) => {
    return measureRawXtermElementSize(el)
  }, [])

  const sendTerminalSizeHint = useCallback((cols: number, rows: number) => {
    const nextCols = Math.floor(cols)
    const nextRows = Math.floor(rows)
    if (nextCols < RAW_XTERM_MIN_FIT_COLS || nextRows < RAW_XTERM_MIN_FIT_ROWS) return
    const last = lastSizeHintSentRef.current
    if (last && last.cols === nextCols && last.rows === nextRows) return
    lastSizeHintSentRef.current = { cols: nextCols, rows: nextRows }
    void agentApi.reportTerminalSizeHint(nextCols, nextRows, currentSessionId).catch(() => {
      lastSizeHintSentRef.current = null
    })
  }, [currentSessionId])

  // Startup/idle size hint: keep the backend's preferred tmux launch size in
  // sync with the visible TerminalCenter container, even before any terminal
  // exists. In multiagent chat the terminal DOM often appears after the first
  // render, so a one-shot mount effect misses the real width.
  useEffect(() => {
    const el = terminalCenterRef.current
    if (!el) return

    let timer: number | undefined
    const measureAndSend = () => {
      const measured = measureTerminalElementSize(el)
      if (measured) sendTerminalSizeHint(measured.cols, measured.rows)
    }
    const schedule = () => {
      if (timer !== undefined) window.clearTimeout(timer)
      timer = window.setTimeout(measureAndSend, 150)
    }

    schedule()
    const observer = new ResizeObserver(schedule)
    observer.observe(el)
    window.addEventListener('resize', schedule)

    return () => {
      observer.disconnect()
      window.removeEventListener('resize', schedule)
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [measureTerminalElementSize, sendTerminalSizeHint])

  const renderRailControls = () => {
    const completedCount = groupedTerminals.sectionCounts.workflow + groupedTerminals.sectionCounts.review + groupedTerminals.sectionCounts.other
    const filters: Array<{ key: TerminalRailFilter; label: string; narrowLabel: string; count: number }> = [
      { key: 'all', label: 'All', narrowLabel: 'A', count: groupedTerminals.logicalGroups.length },
      { key: 'running', label: 'Live', narrowLabel: 'L', count: groupedTerminals.sectionCounts.active },
      { key: 'attention', label: 'Issues', narrowLabel: '!', count: groupedTerminals.sectionCounts.attention },
      { key: 'non-running', label: 'Done', narrowLabel: 'D', count: completedCount },
    ]

    return (
      <div key="terminal-rail-controls" data-testid="terminal-rail-controls" className="border-y border-neutral-800/80 bg-[#0b0d0c] p-2">
        <div className="flex min-w-0 items-center gap-1">
          <button
            type="button"
            onClick={clearNonRunningTerminals}
            data-testid="terminal-rail-clear-completed"
            disabled={clearableNonRunningTerminals.length === 0}
            className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-neutral-500 transition-colors hover:bg-neutral-900 hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-35 disabled:hover:text-neutral-500"
            title="Clear completed agents"
            aria-label="Clear completed agents"
          >
            <Trash2 className="h-3 w-3" />
          </button>
        </div>
        {/* Collapsed rail still has 56px to work with. A bare letter+number per
            filter (first attempt) read as cryptic noise, especially at zero.
            A status-colored dot carries the same meaning the expanded labels
            do (emerald=live, amber=attention, sky=done) without needing text,
            and rows with nothing to report just don't render — four rows of
            "0" told the reader nothing four times over. */}
        {filters.some(filter => filter.key !== 'all' && filter.count > 0) && (
          <div className="mt-1 flex flex-col gap-0.5">
            {filters.filter(filter => filter.key !== 'all' && filter.count > 0).map(filter => (
              <button
                key={filter.key}
                type="button"
                onClick={() => setTerminalRailFilter(filter.key)}
                data-testid={`terminal-rail-filter-narrow-${filter.key}`}
                className={`flex items-center justify-center gap-1.5 rounded px-1 py-1 text-[10px] font-medium leading-none tabular-nums transition-colors ${
                  terminalRailFilter === filter.key
                    ? 'bg-neutral-800 text-neutral-100 shadow-sm'
                    : 'text-neutral-400 hover:bg-neutral-900 hover:text-neutral-200'
                }`}
                title={`${filter.count} ${filter.label.toLowerCase()} — click to filter`}
                aria-label={`Show ${filter.count} ${filter.label.toLowerCase()} agents`}
                aria-pressed={terminalRailFilter === filter.key}
              >
                {(() => {
                  const FilterIcon = RAIL_FILTER_ICON[filter.key]
                  return <FilterIcon className={`h-3 w-3 shrink-0 ${RAIL_FILTER_TEXT_COLOR[filter.key]}`} aria-hidden />
                })()}
                <span>{filter.count}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    )
  }

  const renderRailItem = (
    terminal: TerminalSnapshot,
    depth: number = 0,
    options?: {
      title?: string
      selected?: boolean
      attemptCount?: number
      expanded?: boolean
      onToggleAttempts?: (event: React.MouseEvent<HTMLButtonElement>) => void
    },
  ) => (
    (() => {
      const preValidationChip = terminalPreValidationChip(terminal, terminalTheme)
      const state = terminalState(terminal)
      const sessionLifecycle = runtimeDisplayStatus(runtimeStatesBySession[terminal.session_id])
      const isRunning = terminalTurnIsBusy(terminal, runtimeStatesBySession[terminal.session_id])
      const railAge = formatRailAge(terminal)
      const railTransport = formatRailTransportChip(terminal)
      const terminalErrors = terminalErrorsByID.get(terminalPaneKey(terminal)) || []
      const latestTerminalError = terminalErrors[0]
      const selected = options?.selected ?? terminalPaneKey(terminal) === selectedTerminalKey
      const title = options?.title || formatTerminalTitle(terminal)
      const viewLabel = isSyntheticTerminal(terminal) ? 'Clean view' : 'Terminal view'
      if (railNarrow) {
        return (
          <button
            key={terminalPaneKey(terminal)}
            type="button"
            data-testid={`terminal-rail-item-${terminalPaneKey(terminal)}`}
            onClick={() => selectTerminalFromRail(terminal)}
            className={`group relative flex h-9 w-full items-center justify-center border-l-2 transition-colors ${
              selected
                ? terminalTheme.railSelected
                : 'border-l-transparent text-neutral-400 hover:bg-[#1b1f1d] hover:text-neutral-100'
            }`}
            // The narrow rail is ordered by CREATION time and deliberately never
            // reorders (see sortTerminalsForRail), so a stack of finished agents
            // gives no clue which one finished last. Nothing in the icon or the
            // tooltip carried a time at all. Surface the age so recency is
            // readable without destabilising the order.
            title={`${title} · ${terminal.display_meta ? `${terminal.display_meta} · ` : ''}${viewLabel} · ${terminalStateDescription(terminal)} · ${formatUpdatedAge(terminal)}`}
            aria-label={`Open ${title}${terminal.display_meta ? `, ${terminal.display_meta}` : ''} in ${viewLabel}, ${terminalStateDescription(terminal)}, ${formatUpdatedAge(terminal)}`}
          >
            <span className="relative inline-flex h-5 w-5 items-center justify-center rounded border border-neutral-700/80 bg-neutral-900/90">
              <TerminalTypeGlyph terminal={terminal} className="h-3 w-3" />
              <span
                className={`absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full ring-2 ring-[#141615] ${
                  isRunning ? terminalTheme.dotRunning : terminalDotClass(terminal, terminalTheme)
                }`}
                aria-hidden
              />
              {latestTerminalError && (
                <AlertTriangle
                  className="absolute -bottom-1 -right-1 h-3 w-3 rounded-full bg-[#141615] text-red-300"
                  aria-label="Terminal error"
                />
              )}
            </span>
            {(options?.attemptCount || 0) > 1 && (
              <span className="absolute bottom-0.5 right-1 rounded bg-neutral-800 px-1 text-[8px] tabular-nums text-neutral-300">
                {options?.attemptCount}
              </span>
            )}
          </button>
        )
      }
      return (
        <div
          key={terminalPaneKey(terminal)}
          data-testid={`terminal-rail-item-${terminalPaneKey(terminal)}`}
          role="button"
          tabIndex={0}
          onClick={() => {
            selectTerminalFromRail(terminal)
          }}
          onKeyDown={event => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault()
              selectTerminalFromRail(terminal)
            }
          }}
          className={`group block w-full cursor-pointer border-l-2 py-1.5 pl-2.5 pr-2.5 text-left transition-colors ${terminalTheme.railText} ${
            selected
              ? terminalTheme.railSelected
              : 'border-l-transparent text-neutral-400 hover:bg-[#1b1f1d] hover:text-neutral-200'
          }`}
          style={{ paddingLeft: terminalRailPadding(depth) }}
        >
          <div className="flex items-center gap-1.5">
            <TerminalRailBranchMarker depth={depth} />
            {isRunning ? (
              <span
                className={`w-2 shrink-0 text-center font-mono text-[10px] leading-none ${terminalTheme.railSpinner}`}
                title={terminalStateDescription(terminal)}
                aria-label={terminalStateDescription(terminal)}
              >
                {railSpinner}
              </span>
            ) : (
              <span
                className={`h-2 w-2 shrink-0 rounded-full ${terminalDotClass(terminal, terminalTheme)}`}
                title={terminalStateDescription(terminal)}
                aria-label={terminalStateDescription(terminal)}
              />
            )}
            <TerminalPaneIcon terminal={terminal} />
            {isArchivedTurnTerminal(terminal) && (
              <TerminalArchivedTurnIcon />
            )}
            <span className="min-w-0 flex-1 truncate font-medium" title={title}>
              {title}
            </span>
            {latestTerminalError && (
              <AlertTriangle
                className="h-3.5 w-3.5 shrink-0 text-red-300"
                aria-label="Terminal error"
              />
            )}
            {(options?.attemptCount || 0) > 1 && options?.onToggleAttempts && (
              <button
                type="button"
                onClick={options.onToggleAttempts}
                className="inline-flex shrink-0 items-center gap-0.5 rounded px-1 py-0.5 text-[9px] text-neutral-500 hover:bg-neutral-800 hover:text-neutral-200"
                title={options.expanded ? 'Hide earlier attempts' : `Show ${options.attemptCount} attempts`}
                aria-label={options.expanded ? 'Hide earlier attempts' : `Show ${options.attemptCount} attempts`}
                aria-expanded={options.expanded}
              >
                {options.expanded ? <ChevronDown className="h-2.5 w-2.5" /> : <ChevronRight className="h-2.5 w-2.5" />}
                {!railNarrow && <span>{options.attemptCount}</span>}
              </button>
            )}
            {canDismissTerminal(terminal) && (options?.attemptCount || 0) <= 1 && (
              <button
                type="button"
                onClick={event => {
                  event.stopPropagation()
                  void dismissTerminal(terminal)
                }}
                className="shrink-0 rounded p-0.5 text-neutral-500 opacity-0 hover:bg-neutral-700/80 hover:text-neutral-100 group-hover:opacity-100"
                title="Remove terminal from UI"
                aria-label="Remove terminal from UI"
              >
                <X className="h-3 w-3" />
              </button>
            )}
          </div>
          {/* The provider/start-time meta row is the bulk of the card height;
              hide it when the chat is the narrow report/plan rail to slim each
              card to one line (info stays on hover via title attrs / when the
              chat is given more room). */}
          {!railNarrow && (
          <div className={`mt-0.5 flex items-center gap-1.5 opacity-70 ${terminalTheme.railMetaText}`}>
            {railTransport && (
              <span className="min-w-0 truncate text-neutral-500" title={formatTransportChip(terminal)}>
                {railTransport}
              </span>
            )}
            {railAge && (
              <span className="shrink-0 text-neutral-500" title={railAge.title}>
                {railAge.label}
              </span>
            )}
            {preValidationChip && (
              <span
                className={`shrink-0 rounded border px-1 py-0.5 font-semibold leading-none ${terminalTheme.microText} ${preValidationChip.className}`}
                title={preValidationChip.title}
              >
                {preValidationChip.label}
              </span>
            )}
            {state === 'closing' && (
              <span className={`shrink-0 ${terminalTheme.warningText}`}>· {terminalStateLabel(terminal)}</span>
            )}
            {isMainAgentTerminal(terminal) && sessionLifecycle && (
              <span className="shrink-0 text-neutral-500">· session {sessionLifecycle}</span>
            )}
          </div>
          )}
          {latestTerminalError && (
            <div
              className="mt-1 flex min-w-0 items-center gap-1.5 text-[10px] leading-4 text-red-300"
              title={latestTerminalError.message}
            >
              <AlertTriangle className="h-3 w-3 shrink-0" aria-hidden />
              <span className="min-w-0 truncate">{compactTerminalErrorMessage(latestTerminalError.message)}</span>
            </div>
          )}
        </div>
      )
    })()
  )

  const toggleRailGroup = (key: string) => {
    setExpandedRailGroupKeys(current => {
      const next = new Set(current)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const toggleRailSection = (section: TerminalRailSection) => {
    setCollapsedRailSections(current => {
      const next = new Set(current)
      if (next.has(section)) next.delete(section)
      else next.add(section)
      return next
    })
  }

  const renderRailGroup = (group: TerminalRailLogicalGroup) => {
    const expanded = expandedRailGroupKeys.has(group.key)
    const groupSelected = group.members.some(terminal => terminalPaneKey(terminal) === selectedTerminalKey)
    const isSequence = terminalRailVisualKind(group.representative) === 'agent'
    const earlierTerminals = group.terminals.filter(
      terminal => terminalPaneKey(terminal) !== terminalPaneKey(group.representative),
    )
    return (
      <div key={group.key}>
        {renderRailItem(group.representative, 0, {
          title: planTitleForTerminal(group.representative) || group.title,
          selected: groupSelected,
          attemptCount: group.terminals.length,
          expanded,
          onToggleAttempts: event => {
            event.stopPropagation()
            toggleRailGroup(group.key)
          },
        })}
        {expanded && earlierTerminals.map((terminal, index) => renderRailItem(terminal, 1, {
          title: isSequence
            ? `Earlier turn ${earlierTerminals.length - index}`
            : `Attempt ${terminal.step_attempt || earlierTerminals.length - index}`,
        }))}
      </div>
    )
  }

  const sectionLabels: Record<TerminalRailSection, string> = {
    active: 'Active',
    attention: 'Needs attention',
    workflow: isWorkflowTerminalContext ? 'Workflow steps' : 'Background tasks',
    review: 'Pulse reviewers',
    other: 'Other agents',
  }

  const sectionIcon = (section: TerminalRailSection) => {
    const iconClass = 'h-3.5 w-3.5 shrink-0'
    switch (section) {
      case 'active':
        return <Power className={iconClass} aria-hidden />
      case 'attention':
        return <AlertTriangle className={iconClass} aria-hidden />
      case 'workflow':
        return <Terminal className={iconClass} aria-hidden />
      case 'review':
        return <Activity className={iconClass} aria-hidden />
      default:
        return <Braces className={iconClass} aria-hidden />
    }
  }

  const renderRailSection = (section: TerminalRailSection) => {
    const groups = groupedTerminals.visibleGroups.filter(group => group.section === section)
    if (groups.length === 0) return null
    const collapsed = terminalRailSearch ? false : collapsedRailSections.has(section)
    const sectionDescription = `${sectionLabels[section]}: ${groups.length} ${groups.length === 1 ? 'agent' : 'agents'}`
    return (
      <section key={section} className="border-b border-neutral-800/60">
        <button
          type="button"
          onClick={() => toggleRailSection(section)}
          className={`flex h-8 w-full items-center text-left text-[10px] font-semibold uppercase text-neutral-500 hover:bg-neutral-900/70 hover:text-neutral-300 ${railNarrow ? 'justify-center gap-2 px-1' : 'gap-1.5 px-2'}`}
          aria-expanded={!collapsed}
          aria-label={`${collapsed ? 'Expand' : 'Collapse'} ${sectionDescription}`}
          title={railNarrow ? sectionDescription : undefined}
        >
          {collapsed ? <ChevronRight className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
          {sectionIcon(section)}
          {!railNarrow && <span className="min-w-0 flex-1 truncate">{sectionLabels[section]}</span>}
          {!railNarrow && <span className="ml-auto rounded bg-neutral-900 px-1.5 py-0.5 text-[9px] font-medium text-neutral-500">{groups.length}</span>}
        </button>
        {!collapsed && groups.map(renderRailGroup)}
      </section>
    )
  }

  return (
    <div ref={terminalCenterRef} className={`flex min-h-0 min-w-0 flex-col bg-[#191a18] text-neutral-100 ${compact ? '' : 'flex-1 overflow-hidden'}`}>
      <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
        {(!hasPendingTerminalActivity && groupedTerminals.orderedTerminals.length === 0) ? (
          <TerminalWaitingPane
            className="min-h-0 flex-1"
            xtermTheme={rawXtermTheme}
            message="No terminal is currently attached. Send a message, resume a chat, or wait for scheduled activity to produce output here."
          />
        ) : (
          <>
        {error && (
          <div className="rounded border border-red-800/50 bg-red-950/20 px-3 py-2 text-xs text-red-200">
            {error}
          </div>
        )}

        {!error && terminals.length === 0 && routingDecisions.length === 0 && (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-12 text-center">
            <Terminal className="h-10 w-10 text-neutral-700" strokeWidth={1.25} />
            <div className="text-sm font-medium text-neutral-300">
              {hasPendingTerminalActivity ? 'Starting terminal...' : 'No terminals yet'}
            </div>
            <div className="max-w-md text-xs leading-relaxed text-neutral-500">
              {hasPendingTerminalActivity
                ? 'Your message was sent. The coding agent is attaching its terminal; output will appear here as soon as the backend registers the pane.'
                : 'Run an automation step, send a message to the main agent, or kick off a coding-agent task to see its activity stream here. Each call becomes its own pane — the rail on the left lists them all, the right pane shows live output, tool calls, and cost.'}
            </div>
            <div className="mt-1 flex items-center gap-1.5 text-[11px] text-neutral-600">
              <span className={`inline-block h-1.5 w-1.5 animate-pulse rounded-full ${terminalTheme.emptyPulse}`} />
              <span>{hasPendingTerminalActivity ? 'Waiting for terminal...' : 'Watching for activity...'}</span>
            </div>
          </div>
        )}

        {groupedTerminals.orderedTerminals.length > 0 && (
          <div className={`relative flex min-h-0 min-w-0 flex-1 gap-0 overflow-hidden bg-[#101211] ${
            terminalFocusActive ? 'border-y border-neutral-800/70' : 'border border-neutral-700/70'
          }`}>
            {/* The main agent and controls stay pinned. Logical task sections
                scroll independently, with retries hidden under each task. */}
            <div data-testid="terminal-rail" className={`hidden shrink-0 flex-col overflow-hidden border-r border-neutral-700/70 bg-[#141615] sm:flex ${railNarrow ? 'w-14' : 'w-56'}`}>
              {currentMainTerminal && renderRailItem(currentMainTerminal, 0, { title: 'Main agent' })}
              {renderRailControls()}
              <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden">
                {(['active', 'attention', 'workflow', 'review', 'other'] as TerminalRailSection[]).map(renderRailSection)}
                {groupedTerminals.visibleGroups.length === 0 && (
                  <div className={`px-3 py-5 text-center text-[11px] leading-4 text-neutral-600 ${railNarrow ? 'hidden' : ''}`}>
                    {terminalRailSearch
                      ? 'No agents match this search.'
                      : terminalRailFilter === 'running'
                        ? 'No other agents are running.'
                        : terminalRailFilter === 'attention'
                          ? 'No agents need attention.'
                          : terminalRailFilter === 'non-running'
                            ? 'No completed agents.'
                            : 'No agents yet.'}
                  </div>
                )}
              </div>
            </div>

            {/* Right pane — the selected terminal's content. Header
                bar at top (chip + meta + actions), content below. */}
            <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
              {selectedTerminalView ? (
                <>
                  <div className={`flex items-center justify-between gap-3 border-b border-neutral-700/70 bg-[#171a18] text-neutral-400 ${terminalTheme.headerText} ${
                    terminalFocusActive ? 'px-2 py-1' : 'px-3 py-2'
                  }`}>
                    <div className="flex min-w-0 flex-1 items-center gap-2">
                      {selectedRouteDecision && (
                        <span
                          className={`inline-flex max-w-[45%] shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 font-medium ${terminalTheme.chipText} ${terminalTheme.selectedRouteChip}`}
                          title={routeDecisionTitle(selectedRouteDecision)}
                        >
                          <GitBranch className="h-3 w-3 shrink-0" />
                          <span className="truncate">{routeDecisionLabel(selectedRouteDecision)}</span>
                        </span>
                      )}
                      {isArchivedTurnTerminal(selectedTerminalView) && (
                        <TerminalArchivedTurnIcon />
                      )}
                      <span
                        className="max-w-[38%] shrink-0 truncate font-medium text-neutral-200"
                        title={selectedHeaderTitle}
                      >
                        {selectedHeaderTitle}
                      </span>
                      {selectedPlanStep && (
                        <button
                          type="button"
                          onClick={showSelectedPlanStep}
                          className="inline-flex shrink-0 items-center justify-center rounded p-0.5 text-neutral-500 hover:bg-neutral-800/80 hover:text-neutral-100"
                          title="Show step in plan"
                          aria-label={`Show ${selectedTerminalDisplayTitle} in plan`}
                        >
                          <Info className="h-3.5 w-3.5" />
                        </button>
                      )}
                      {!showFormattedView && (
                        <>
                          <span className="shrink-0 opacity-35" aria-hidden="true">·</span>
                          <span className="min-w-0 flex-1 truncate opacity-80">
                            {formatSelectedTerminalMeta(selectedTerminalView)}
                          </span>
                        </>
                      )}
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      {terminalState(selectedTerminalView) === 'closing' && (
                        <span
                          className={terminalTheme.warningText}
                          title={terminalStateDescription(selectedTerminalView)}
                        >
                          {terminalStateLabel(selectedTerminalView)}
                        </span>
                      )}
                      {canShowFormattedView && selectedTerminalViewPreferenceKey && (
                        <button
                          type="button"
                          onClick={() => toggleFormattedView(selectedTerminalViewPreferenceKey, showFormattedView)}
                          aria-pressed={showFormattedView}
                          className={`inline-flex items-center gap-1 rounded px-1.5 py-1 text-[10px] font-medium transition-colors ${
                            showFormattedView
                              ? 'bg-neutral-800 text-neutral-100'
                              : 'text-neutral-500 hover:bg-neutral-800/80 hover:text-neutral-100'
                          }`}
                          title={showFormattedView
                            ? 'Showing the readable conversation. Click to inspect the technical terminal.'
                            : 'Showing the technical terminal. Click to return to the readable conversation.'}
                        >
                          {showFormattedView ? <Terminal className="h-3.5 w-3.5" /> : <MessageSquare className="h-3.5 w-3.5" />}
                          <span>{showFormattedView ? 'Terminal' : 'Conversation'}</span>
                        </button>
                      )}
                      {selectedTerminalIsTmux && !showFormattedView && (
                        <>
                          <button
                            type="button"
                            onClick={scrollSelectedTerminalToBottom}
                            className="inline-flex items-center justify-center rounded p-1 text-neutral-500 hover:bg-neutral-800/80 hover:text-neutral-100"
                            title="Scroll terminal to bottom"
                            aria-label="Scroll terminal to bottom"
                          >
                            <ArrowDownToLine className="h-3.5 w-3.5" />
                          </button>
                          <button
                            type="button"
                            onClick={() => void copyTerminalDebug(selectedTerminalView)}
                            className="inline-flex items-center justify-center rounded p-1 text-neutral-500 hover:bg-neutral-800/80 hover:text-neutral-100"
                            title="Copy terminal debug IDs"
                            aria-label="Copy terminal debug IDs"
                          >
                            {copiedTerminalID === selectedTerminalView.terminal_id ? (
                              <Check className={`h-3.5 w-3.5 ${terminalTheme.copiedIcon}`} />
                            ) : (
                              <Info className="h-3.5 w-3.5" />
                            )}
                          </button>
                        </>
                      )}
                      {selectedTerminalErrorEntries.length > 0 && selectedTerminalErrorPanelKey && (
                        <div ref={errorMenuRef} className="relative inline-flex">
                          <button
                            type="button"
                            onMouseDown={event => event.preventDefault()}
                            onClick={() => setErrorPanelOpenForID(current => (
                              current === selectedTerminalErrorPanelKey ? null : selectedTerminalErrorPanelKey
                            ))}
                            className={`relative inline-flex items-center justify-center rounded border p-1 transition-colors hover:bg-red-950/40 hover:text-red-100 ${
                              errorPanelOpenForID === selectedTerminalErrorPanelKey
                                ? 'border-red-700/80 bg-red-950/45 text-red-200'
                                : 'border-red-900/70 text-red-300'
                            }`}
                            title={`${selectedTerminalErrorEntries.length} captured error${selectedTerminalErrorEntries.length === 1 ? '' : 's'}`}
                            aria-label={`Show ${selectedTerminalErrorEntries.length} captured error${selectedTerminalErrorEntries.length === 1 ? '' : 's'}`}
                            aria-haspopup="menu"
                            aria-expanded={errorPanelOpenForID === selectedTerminalErrorPanelKey}
                            data-testid="terminal-error-indicator"
                          >
                            <AlertTriangle className="h-3.5 w-3.5" />
                          </button>
                          {errorPanelOpenForID === selectedTerminalErrorPanelKey && (
                            <div
                              role="menu"
                              className="absolute right-0 top-full z-[80] mt-1 flex max-h-[min(55vh,28rem)] w-[min(34rem,calc(100vw-5rem))] flex-col overflow-hidden rounded-lg border border-red-900/70 bg-[#171313] text-xs text-neutral-200 shadow-2xl"
                              data-testid="terminal-error-menu"
                            >
                              <div className="flex items-center justify-between border-b border-red-950 px-3 py-2">
                                <div className="flex items-center gap-2 font-medium text-red-200">
                                  <AlertTriangle className="h-3.5 w-3.5" />
                                  <span>Captured errors</span>
                                  <span className="rounded bg-red-950 px-1.5 py-0.5 text-[10px] tabular-nums text-red-300">
                                    {selectedTerminalErrorEntries.length}
                                  </span>
                                </div>
                                <button
                                  type="button"
                                  onClick={() => setErrorPanelOpenForID(null)}
                                  className="rounded p-1 text-neutral-500 hover:bg-neutral-800 hover:text-neutral-100"
                                  aria-label="Close captured errors"
                                >
                                  <X className="h-3.5 w-3.5" />
                                </button>
                              </div>
                              <div className="flex min-h-0 flex-col gap-1 overflow-y-auto p-2">
                                {selectedTerminalErrorEntries.map(entry => {
                                  const isOpen = expandedErrorIDs.has(entry.id)
                                  return (
                                    <div key={entry.id} className="rounded border border-red-950/90 bg-red-950/20 p-2 text-red-200">
                                      <div className="flex min-w-0 items-center gap-2">
                                        <span className="shrink-0 rounded bg-red-900/35 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wide text-red-200">
                                          {entry.type.replace(/_/g, ' ')}
                                        </span>
                                        {entry.toolName && (
                                          <span className="max-w-40 shrink truncate font-mono text-[10px] text-red-200/75" title={entry.toolName}>
                                            {entry.toolName}
                                          </span>
                                        )}
                                        <span className="min-w-0 flex-1 truncate leading-5" title={entry.message}>
                                          {compactTerminalErrorMessage(entry.message)}
                                        </span>
                                        <button
                                          type="button"
                                          onClick={() => toggleTerminalError(entry.id)}
                                          className="shrink-0 rounded border border-red-800/60 px-2 py-0.5 text-[10px] font-medium text-red-200 hover:bg-red-900/35"
                                          aria-expanded={isOpen}
                                        >
                                          {isOpen ? 'Less' : 'Details'}
                                        </button>
                                        <button
                                          type="button"
                                          onClick={() => {
                                            dismissTerminalError(entry.id)
                                            if (selectedTerminalErrorEntries.length === 1) setErrorPanelOpenForID(null)
                                          }}
                                          className="shrink-0 rounded p-0.5 text-red-400/60 hover:bg-red-900/35 hover:text-red-200"
                                          title="Dismiss"
                                          aria-label="Dismiss terminal error"
                                        >
                                          <X className="h-3 w-3" />
                                        </button>
                                      </div>
                                      {isOpen && (
                                        <TerminalErrorExpandedDetails entry={entry} maxHeightClass="max-h-48" />
                                      )}
                                    </div>
                                  )
                                })}
                              </div>
                            </div>
                          )}
                        </div>
                      )}
                      {hasTerminalDebugActions(selectedTerminalView) && (
                        <div ref={debugMenuRef} className="relative inline-flex">
                          <button
                            type="button"
                            onMouseDown={event => event.preventDefault()}
                            onClick={() => toggleDebugPanel(selectedTerminalView)}
                            className={`inline-flex items-center justify-center rounded border p-1 hover:bg-neutral-800/80 hover:text-neutral-100 ${
                              debugPanelOpenForID === terminalPaneKey(selectedTerminalView)
                                ? terminalTheme.debugActive
                                : 'border-neutral-700/90 text-neutral-300'
                            }`}
                            title="Debug terminal actions"
                            aria-label="Debug terminal actions"
                            aria-haspopup="menu"
                            aria-expanded={debugPanelOpenForID === terminalPaneKey(selectedTerminalView)}
                          >
                            <Bug className="h-3.5 w-3.5" />
                          </button>
                          {debugPanelOpenForID === terminalPaneKey(selectedTerminalView) && (
                            <div
                              role="menu"
                              className="absolute right-0 top-full z-[60] mt-1 min-w-[200px] rounded-md border border-neutral-700/90 bg-[#151716] p-1 text-xs text-neutral-200 shadow-lg"
                            >
                              <button
                                type="button"
                                role="menuitem"
                                onMouseDown={event => event.preventDefault()}
                                onClick={() => { setDebugPanelOpenForID(null); void copyTerminalPaneText(selectedTerminalView) }}
                                disabled={!selectedTerminalView.content}
                                className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-not-allowed disabled:opacity-40"
                              >
                                <Copy className="h-3.5 w-3.5 shrink-0" />
                                <span>Copy pane text</span>
                              </button>
                              {selectedTerminalView.tmux_session && (
                                <button
                                  type="button"
                                  role="menuitem"
                                  onMouseDown={event => event.preventDefault()}
                                  onClick={() => { setDebugPanelOpenForID(null); void copyTmuxAttachCommand(selectedTerminalView) }}
                                  className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80"
                                >
                                  <Terminal className="h-3.5 w-3.5 shrink-0" />
                                  <span>Copy tmux attach</span>
                                </button>
                              )}
                              {canSendTerminalDebugInput(selectedTerminalView) && (
                                <button
                                  type="button"
                                  role="menuitem"
                                  onMouseDown={event => event.preventDefault()}
                                  onClick={() => { setDebugPanelOpenForID(null); void refreshTerminalSnapshot(selectedTerminalView) }}
                                  disabled={terminalActionBusy === 'refresh'}
                                  className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                >
                                  <RefreshCw className="h-3.5 w-3.5 shrink-0" />
                                  <span>Capture deeper history</span>
                                </button>
                              )}
                              {canForceCompleteTerminal(selectedTerminalView) && (
                                <button
                                  type="button"
                                  role="menuitem"
                                  onMouseDown={event => event.preventDefault()}
                                  onClick={() => { setDebugPanelOpenForID(null); void forceCompleteTerminal(selectedTerminalView) }}
                                  disabled={terminalActionBusy === 'complete'}
                                  className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                >
                                  <Check className="h-3.5 w-3.5 shrink-0" />
                                  <span>Mark complete</span>
                                </button>
                              )}
                              <button
                                type="button"
                                role="menuitem"
                                onMouseDown={event => event.preventDefault()}
                                onClick={() => { setDebugPanelOpenForID(null); void forceFailTerminal(selectedTerminalView) }}
                                disabled={terminalActionBusy === 'fail'}
                                className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                              >
                                <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
                                <span>Mark failed</span>
                              </button>
                              {canSendTerminalDebugInput(selectedTerminalView) && (
                                <>
                                  <div className="my-1 h-px bg-neutral-800/80" role="none" />
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void sendTerminalDebugKey(selectedTerminalView, 'enter') }}
                                    disabled={terminalActionBusy === 'enter'}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <CornerDownLeft className="h-3.5 w-3.5 shrink-0" />
                                    <span>Send Enter</span>
                                  </button>
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void sendTerminalDebugKey(selectedTerminalView, 'esc') }}
                                    disabled={terminalActionBusy === 'esc'}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <CornerUpLeft className="h-3.5 w-3.5 shrink-0" />
                                    <span>Send Esc</span>
                                  </button>
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void sendTerminalDebugKey(selectedTerminalView, 'ctrl-c') }}
                                    disabled={terminalActionBusy === 'ctrl-c'}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <Square className="h-3.5 w-3.5 shrink-0" />
                                    <span>Send Ctrl+C (interrupt)</span>
                                  </button>
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void sendTerminalDebugKey(selectedTerminalView, 'ctrl-o') }}
                                    disabled={terminalActionBusy === 'ctrl-o'}
                                    title={ctrlODebugTitle(selectedTerminalView)}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <Braces className="h-3.5 w-3.5 shrink-0" />
                                    <span>{ctrlODebugLabel(selectedTerminalView)}</span>
                                  </button>
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void sendTerminalDebugKey(selectedTerminalView, 'tab') }}
                                    disabled={terminalActionBusy === 'tab'}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <ArrowRightToLine className="h-3.5 w-3.5 shrink-0" />
                                    <span>Send Tab (allowlist / select)</span>
                                  </button>
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void sendTerminalDebugKey(selectedTerminalView, 'up') }}
                                    disabled={terminalActionBusy === 'up'}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <ChevronUp className="h-3.5 w-3.5 shrink-0" />
                                    <span>Send Up arrow</span>
                                  </button>
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void sendTerminalDebugKey(selectedTerminalView, 'down') }}
                                    disabled={terminalActionBusy === 'down'}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-neutral-800/80 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <ChevronDown className="h-3.5 w-3.5 shrink-0" />
                                    <span>Send Down arrow</span>
                                  </button>
                                  <div className="my-1 h-px bg-neutral-800/80" role="none" />
                                  <div className="px-2 py-1.5">
                                    <div className="flex items-center gap-1.5">
                                      <input
                                        type="text"
                                        value={debugText}
                                        onChange={e => setDebugText(e.target.value)}
                                        onKeyDown={e => {
                                          if (e.key === 'Enter' && !e.shiftKey) {
                                            e.preventDefault()
                                            void sendTerminalDebugText(selectedTerminalView, debugText, true)
                                          }
                                        }}
                                        onMouseDown={e => e.stopPropagation()}
                                        placeholder="Send text…"
                                        className="h-6 flex-1 rounded border border-neutral-700/80 bg-neutral-900 px-2 text-xs text-neutral-200 placeholder-neutral-600 focus:border-neutral-500 focus:outline-none"
                                      />
                                      <button
                                        type="button"
                                        onMouseDown={e => e.preventDefault()}
                                        onClick={() => { void sendTerminalDebugText(selectedTerminalView, debugText, false) }}
                                        disabled={terminalActionBusy === 'send-text' || !debugText.trim()}
                                        title="Send without Enter"
                                        className="flex h-6 items-center rounded border border-neutral-700/80 bg-neutral-900 px-1.5 text-neutral-400 hover:bg-neutral-800 hover:text-neutral-100 disabled:cursor-wait disabled:opacity-40"
                                      >
                                        <Terminal className="h-3 w-3" />
                                      </button>
                                      <button
                                        type="button"
                                        onMouseDown={e => e.preventDefault()}
                                        onClick={() => { void sendTerminalDebugText(selectedTerminalView, debugText, true) }}
                                        disabled={terminalActionBusy === 'send-text' || !debugText.trim()}
                                        title="Send + Enter"
                                        className="flex h-6 items-center rounded border border-neutral-700/80 bg-neutral-900 px-1.5 text-neutral-400 hover:bg-neutral-800 hover:text-neutral-100 disabled:cursor-wait disabled:opacity-40"
                                      >
                                        <CornerDownLeft className="h-3 w-3" />
                                      </button>
                                    </div>
                                  </div>
                                  <div className="my-1 h-px bg-neutral-800/80" role="none" />
                                  <button
                                    type="button"
                                    role="menuitem"
                                    onMouseDown={event => event.preventDefault()}
                                    onClick={() => { setDebugPanelOpenForID(null); void killTerminalSession(selectedTerminalView) }}
                                    disabled={terminalActionBusy === 'kill'}
                                    className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-red-300 hover:bg-red-950/35 hover:text-red-100 disabled:cursor-wait disabled:opacity-50"
                                  >
                                    <Power className="h-3.5 w-3.5 shrink-0" />
                                    <span>Kill tmux session</span>
                                  </button>
                                </>
                              )}
                            </div>
                          )}
                        </div>
                      )}
                      {canDismissTerminal(selectedTerminalView) && (
                        <button
                          type="button"
                          onClick={() => void dismissTerminal(selectedTerminalView)}
                          className="inline-flex items-center justify-center rounded border border-neutral-700/90 p-1 text-neutral-300 hover:bg-neutral-800/80 hover:text-neutral-100"
                          title="Remove terminal from UI"
                          aria-label="Remove terminal from UI"
                        >
                          <X className="h-3.5 w-3.5" />
                        </button>
                      )}
                    </div>
                  </div>
                  {terminalPreValidationSummary(selectedTerminalView) &&
                    (!selectedTerminalIsSynthetic || !selectedTerminalHasPreValidationEvent) && (
                    <div className={`border-b border-neutral-700/70 bg-[#151716] px-3 py-1.5 ${terminalTheme.headerText} ${terminalPreValidationClass(selectedTerminalView, terminalTheme)}`}>
                      {terminalPreValidationSummary(selectedTerminalView)}
                    </div>
                  )}
                  <div className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
                    {selectedTerminalView?.execution_tree_placeholder ? (
                      <TerminalWaitingPane
                        className="min-w-0 flex-1 overflow-hidden overscroll-contain"
                        contentRef={terminalOutputRef as React.RefObject<HTMLDivElement | null>}
                        xtermTheme={rawXtermTheme}
                        message="This asynchronous agent is running. Its detailed terminal will appear here as soon as the runtime publishes it."
                      />
                    ) : selectedTerminalIsSynthetic || showFormattedView ? (
                    // Clean view always renders the real event stream. Never
                    // substitute the legacy parsed-row card: it is not the
                    // conversation UI and can carry stale sibling metadata.
                    //
                    // A tmux terminal reaches this by toggle: the pane and the
                    // transcript are two renderings of one run, and the events
                    // carry what the pane cannot show well -- tool arguments,
                    // results, and an unwrapped final answer.
                    <TerminalEventTranscript
                      scrollKey={selectedTerminalUsesSessionEvents ? mainTranscriptReadingKey : undefined}
                      events={selectedTerminalEventSource}
                      terminal={selectedTerminalView}
                      siblingTerminals={terminals}
                      // The diagnostic terminal transcript is intentionally a
                      // live inspection surface, unlike the product chats.
                      loading={selectedTerminalUsesSessionEvents
                        ? mainEventHydration.sessionId === currentSessionId && mainEventHydration.loading
                        : selectedTerminalEventPage.loading}
                      loadingOlder={selectedTerminalUsesSessionEvents
                        ? mainSessionOlderEventPage.loadingOlder
                        : selectedTerminalEventPage.loadingOlder}
                      hasOlder={selectedTerminalUsesSessionEvents
                        ? mainSessionHasOlderEvents
                        : selectedTerminalEventPage.hasOlder}
                      error={selectedTerminalUsesSessionEvents
                        ? (mainSessionOlderEventPage.error || (mainEventHydration.sessionId === currentSessionId ? mainEventHydration.error : undefined))
                        : selectedTerminalEventPage.error}
                      onLoadOlder={selectedTerminalUsesSessionEvents ? loadOlderMainSessionEvents : loadOlderSelectedTerminalEvents}
                      onRetry={selectedTerminalUsesSessionEvents
                        ? (mainSessionOlderEventPage.error ? loadOlderMainSessionEvents : loadMainSessionEvents)
                        : loadSelectedTerminalEventPage}
                      // Tmux providers stream terminal snapshots/progress, not
                      // reliable assistant-token deltas. Rendering those in the
                      // formatted transcript creates a growing raw-output blog
                      // immediately before the authoritative sidecar-backed
                      // completion answer. Keep the friendly live status, but
                      // reserve prose for the final structured answer. API
                      // providers (no tmux session) still stream their answer
                      // naturally as it is generated.
                      streamingText={selectedTerminalUsesSessionEvents && !selectedTerminalView?.tmux_session
                        ? sessionStreamingText
                        : undefined}
                      // Only display a live status that the event stream itself
                      // supplied. Terminal lifecycle metadata can lag behind a
                      // completed tmux turn, so deriving "Working…" from it made
                      // an already-finished conversation look permanently busy.
                      streamingStatus={selectedTerminalHasPendingConversationWork
                        ? (selectedTerminalUsesSessionEvents ? sessionStreamingStatus : undefined) ||
                          (selectedTerminalWaitingForBackground ? 'Waiting for background work…' : 'Working…')
                        : undefined}
                    />
                  ) : stableLiveAttachId && stableLiveAttachKey ? (
                    <LiveAttachXtermPane
                      interactive={selectedTerminalUsesSessionEvents}
                      // Keyed on the debounced terminal+tmux identity so transient
                      // selection flicker does not remount, but a relaunched session
                      // with the same logical terminal id reconnects to the new stream.
                      key={stableLiveAttachKey}
                      terminalId={stableLiveAttachId}
                      tmuxSession={selectedTerminalView?.tmux_session}
                      sessionId={selectedTerminalView?.session_id}
                      contentRef={terminalOutputRef as React.RefObject<HTMLDivElement | null>}
                      xtermTheme={rawXtermTheme}
                      authoritativeContent={selectedTerminalView?.terminal_id === stableLiveAttachId ? selectedTerminalDisplayContent : ''}
                      authoritativeVersion={selectedTerminalView?.terminal_id === stableLiveAttachId
                        ? `${selectedTerminalView.terminal_id}:${selectedTerminalView.chunk_index ?? 'x'}:${selectedTerminalView.updated_at || ''}:${selectedTerminalDisplayContent.length}`
                        : stableLiveAttachKey}
                      reconnectOnClose={isSelectedTerminalStreaming}
                      onViewportStickChange={handleXtermViewportStickChange}
                      onScrollToBottomReady={registerXtermScrollToBottom}
                      className="min-w-0 flex-1 overflow-hidden overscroll-contain"
                    />
                  ) : useLiveAttachForSelected ? (
                    <TerminalWaitingPane
                      className="min-w-0 flex-1 overflow-hidden overscroll-contain"
                      contentRef={terminalOutputRef as React.RefObject<HTMLDivElement | null>}
                      xtermTheme={rawXtermTheme}
                      message="Attaching to the live tmux session. If the agent is idle after inactivity, output will appear here when it resumes."
                    />
                  ) : selectedTerminalView && selectedTerminalDisplayContent ? (
                    <StaticXtermPane
                      // Inactive/restored tmux snapshots often no longer have a
                      // live tmux_session. They still carry captured ANSI content,
                      // so render them through xterm instead of the blank live
                      // placeholder. Live sessions with tmux_session stay on the
                      // WebSocket transport above.
                      key={`${selectedTerminalKey || selectedTerminalView.terminal_id}:static`}
                      content={selectedTerminalDisplayContent}
                      contentRef={terminalOutputRef as React.RefObject<HTMLDivElement | null>}
                      xtermTheme={rawXtermTheme}
                      onViewportStickChange={handleXtermViewportStickChange}
                      onScrollToBottomReady={registerXtermScrollToBottom}
                      className="min-w-0 flex-1 overflow-hidden overscroll-contain"
                    />
                  ) : (
                    <TerminalWaitingPane
                      className="min-w-0 flex-1 overflow-hidden overscroll-contain"
                      contentRef={terminalOutputRef as React.RefObject<HTMLDivElement | null>}
                      xtermTheme={rawXtermTheme}
                      message="Terminal output is being restored. If this session was released after inactivity, new output will attach here automatically."
                    />
                    )}
                  </div>
                  {selectedTerminalView && (() => {
                    const st = selectedTerminalView.status || {}
                    const tokensIn = formatTokens(st.total_input_tokens || st.input_tokens)
                    const tokensOut = formatTokens(st.total_output_tokens || st.output_tokens)
                    const cost = formatStatusFooterCost(st.cost_usd)
                    // Surface cache read/write tokens when the provider reports
                    // them, so this real-time telemetry isn't silently dropped.
                    const cacheParts: string[] = []
                    if (st.cache_read_input_tokens) cacheParts.push(`${formatTokens(st.cache_read_input_tokens)} cache`)
                    if (st.cache_creation_input_tokens) cacheParts.push(`${formatTokens(st.cache_creation_input_tokens)} cache-w`)
                    const cacheSeg = cacheParts.join(' · ')
                    const dur = typeof st.duration_ms === 'number' && st.duration_ms > 0
                      ? `${(st.duration_ms / 1000).toFixed(st.duration_ms < 10_000 ? 1 : 0)}s`
                      : ''
                    const toolCount = typeof st.tool_count === 'number' ? st.tool_count : 0
                    const tools = toolCount > 0 ? `${toolCount} ${toolCount === 1 ? 'tool' : 'tools'}` : ''
                    // Provider-agnostic statusline extras (e.g. plan rate-limit usage).
                    // Each CLI adapter normalizes its own schema into these display-ready
                    // segments upstream (status_meta.status_extras); render them verbatim
                    // with no per-provider knowledge here.
                    const rawExtras = (st.status_meta as Record<string, unknown> | undefined)?.status_extras
                    const extraSegs = Array.isArray(rawExtras)
                      ? rawExtras.filter((x): x is string => typeof x === 'string')
                      : []
                    const provider = st.provider_label || selectedTerminalView.label || selectedTerminalView.execution_kind || 'pane'
                    const context = extraSegs.find(segment => /^ctx\b/i.test(segment)) || ''
                    const usageLimitSegments = extraSegs.filter(segment => (
                      segment !== context && /\b\d+(?:\.\d+)?%\b/.test(segment)
                    ))
                    const compactSegments = [provider, cost, ...usageLimitSegments, context].filter(Boolean)
                    const detailSegments = [
                      tools,
                      tokensIn !== '–' || tokensOut !== '–' ? `${tokensIn} in · ${tokensOut} out` : '',
                      cacheSeg,
                      dur,
                      ...extraSegs.filter(segment => segment !== context && !usageLimitSegments.includes(segment)),
                    ].filter(Boolean)
                    // Clean workflow steps already expose their live state in
                    // the rail and lifecycle card. When no telemetry has
                    // arrived yet, the footer would contain only the backend's
                    // internal terminal label (for example "Step engagement
                    // connect"), which is both redundant and user-hostile.
                    if (selectedTerminalIsSynthetic &&
                      !cost &&
                      usageLimitSegments.length === 0 &&
                      !context &&
                      detailSegments.length === 0) {
                      return null
                    }
                    const paneID = terminalPaneKey(selectedTerminalView)
                    const detailsExpanded = expandedTelemetryTerminalID === paneID
                    return (
                      <div className={`border-t border-neutral-700/70 bg-[#101211] font-mono text-neutral-500 ${terminalTheme.footerText} ${
                        terminalFocusActive ? 'px-2 py-0.5' : 'px-3 py-1'
                      }`}>
                        <div className="flex min-w-0 items-center gap-2">
                          <span className={selectedTerminalHasPendingConversationWork ? terminalTheme.streaming : 'text-neutral-600'}>
                            {selectedTerminalHasPendingConversationWork ? selectedTerminalSpinner : '·'}
                          </span>
                          <span
                            className="min-w-0 flex-1 truncate"
                            title={[...compactSegments, ...detailSegments].join(' · ')}
                          >
                            {compactSegments.join('  ·  ')}
                          </span>
                          {detailSegments.length > 0 && (
                            <button
                              type="button"
                              onClick={() => setExpandedTelemetryTerminalID(detailsExpanded ? null : paneID)}
                              className="inline-flex shrink-0 items-center justify-center rounded p-0.5 text-neutral-600 hover:bg-neutral-800/80 hover:text-neutral-300"
                              title={detailsExpanded ? 'Hide usage details' : 'Show usage details'}
                              aria-label={detailsExpanded ? 'Hide terminal usage details' : 'Show terminal usage details'}
                              aria-expanded={detailsExpanded}
                            >
                              {detailsExpanded ? <ChevronDown className="h-3 w-3" /> : <ChevronUp className="h-3 w-3" />}
                            </button>
                          )}
                        </div>
                        {detailsExpanded && detailSegments.length > 0 && (
                          <div className="mt-0.5 truncate pl-4 text-neutral-600" title={detailSegments.join(' · ')}>
                            usage · {detailSegments.join('  ·  ')}
                          </div>
                        )}
                      </div>
                    )
                  })()}
                </>
              ) : (
                <div className="flex flex-1 items-center justify-center text-xs text-neutral-500">
                  Select a terminal from the rail to view its content.
                </div>
              )}
            </div>
          </div>
        )}
          </>
        )}
      </div>
    </div>
  )
}

// FIX A: Memoized so typing in the chat input — which re-renders ChatArea (the
// parent) — does NOT re-render the terminal subtree and disturb the live xterm
// panes. Props (currentSessionId, compact, hasPendingTerminalActivity) are all
// primitives and stable while typing, so the shallow-prop comparison short-
// circuits the re-render.
export const TerminalCenter = memo(TerminalCenterInner)
