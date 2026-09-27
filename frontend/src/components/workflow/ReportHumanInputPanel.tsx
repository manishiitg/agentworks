import { PulseMetricOutcomes } from "./PulseMetricOutcomes"
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { CheckCircle2, ChevronDown, ChevronRight, Clock3, Loader2, MessageSquareText, RefreshCw, Send, Sparkles, X } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { PulseImpactLedger, ReportHumanInput } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import {
  parseReportHumanInputContext,
  reportHumanInputHistory,
  reportHumanInputImpact,
  reportHumanInputStatusLabel,
} from '../../utils/reportHumanInputFormatting'
import { delegateReportHumanInputActionToChat, openReportHumanInputAnswerInChat, openReportHumanInputQuestionInChat, sendReportHumanInputAnswerToChat } from '../../utils/reportHumanInputChat'
import { useContainerSizeTier } from './reportWidgets/tableHelpers'
import { PlainMarkdown } from '../ui/PlainMarkdown'
import { WORKFLOW_DECISIONS_REFRESH_EVENT } from './workflowEvents'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

// Same timing as AskAIButton's two-click confirm.
const OPTION_ARM_TIMEOUT_MS = 4000
const OPTION_CONFIRM_FLOOR_MS = 600

type ReportHumanInputDraft = {
  selectedOptionId: string
  note: string
  submitting?: boolean
  askingInChat?: boolean
  delegating?: boolean
}

function keepPreviousInputsWhenUnchanged(
  previous: ReportHumanInput[],
  next: ReportHumanInput[],
): ReportHumanInput[] {
  return JSON.stringify(previous) === JSON.stringify(next) ? previous : next
}

function sourceLabel(source?: string): string {
  if (source && ['technical_review', 'engineering_review', 'ops_review'].includes(source)) return 'Technical Review'
  if (source && ['strategic_review', 'strategy_auditor', 'goal_advisor'].includes(source)) return 'Strategic Review'
  if (source === 'user_suggestion') return 'User suggestion'
  if (source === 'architecture_review') return 'Architecture Review'
  if (source === 'plan_drift_review') return 'Plan Drift Review'
  return 'Pulse'
}

function inputTime(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

function priorityTone(priority: string): string {
  if (priority === 'high') return 'border-rose-500/40 bg-rose-500/10 text-rose-200'
  if (priority === 'low') return 'border-slate-500/30 bg-slate-500/10 text-slate-300'
  return 'border-amber-500/35 bg-amber-500/10 text-amber-200'
}

function selectedOptionTitle(input: ReportHumanInput): string {
  if (!input.selected_option_id) return ''
  return input.options.find(option => option.id === input.selected_option_id)?.title || input.selected_option_id
}

function consumedActorLabel(input: ReportHumanInput): string {
  const actor = input.consumed_by?.trim()
  if (actor && actor.toLowerCase() !== 'agent') return actor
  return sourceLabel(input.source)
}

function statusTone(input: ReportHumanInput): string {
	if (input.status === 'consumed') return 'text-emerald-300'
	if (input.status === 'answered' || input.status === 'claimed') return 'text-amber-200'
  return 'text-muted-foreground'
}

function HumanInputContext({ value }: { value: string }) {
  const sections = parseReportHumanInputContext(value)
  if (sections.length === 0) return null

  return (
    <div className="mt-3 space-y-3 border-l border-cyan-400/20 pl-3 text-xs leading-5 text-muted-foreground">
      {sections.map((section, index) => (
        <div key={`${section.label || 'context'}-${index}`}>
          {section.label && (
            <div className="mb-0.5 font-semibold text-foreground">{section.label}</div>
          )}
          {section.body && <PlainMarkdown content={section.body} />}
          {section.items.length > 0 && (
            <ol className="mt-1.5 list-decimal space-y-1.5 pl-4 marker:font-semibold marker:text-cyan-300">
              {section.items.map((item, itemIndex) => <li key={itemIndex} className="pl-1">{item}</li>)}
            </ol>
          )}
        </div>
      ))}
    </div>
  )
}

interface ReportHumanInputPanelProps {
	workspacePath: string
	className?: string
	source?: string
	workspaceLabel?: string
	contentMode?: 'all' | 'pending' | 'history'
	historyMode?: 'collapsed' | 'expanded'
	historyLimit?: number
	providedInputs?: ReportHumanInput[]
	providedImpact?: PulseImpactLedger
	providedLoading?: boolean
	providedError?: string | null
	onRequestRefresh?: () => void
	showEmptyState?: boolean
}

export function ReportHumanInputPanel({
  workspacePath,
	className = '',
	source,
	workspaceLabel,
	contentMode = 'all',
	historyMode = 'collapsed',
	historyLimit = 4,
	providedInputs,
	providedImpact,
	providedLoading,
	providedError,
	onRequestRefresh,
	showEmptyState = false,
}: ReportHumanInputPanelProps) {
  const [inputs, setInputs] = useState<ReportHumanInput[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [drafts, setDrafts] = useState<Record<string, ReportHumanInputDraft>>({})
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [historyOpen, setHistoryOpen] = useState(historyMode === 'expanded')
  const [expandedHistoryIds, setExpandedHistoryIds] = useState<Record<string, boolean>>({})
  // Two-click confirm for decision options (see clickOption).
  const [armedOption, setArmedOption] = useState<{ inputId: string; optionId: string; at: number } | null>(null)
  const disarmTimerRef = useRef<number | null>(null)
  useEffect(() => () => {
    if (disarmTimerRef.current !== null) window.clearTimeout(disarmTimerRef.current)
  }, [])
  const [panelRef, sizeTier] = useContainerSizeTier(560, 900)
	const compactOptions = sizeTier === 'phone'
	const externallyManaged = providedInputs !== undefined
	const visibleInputs = providedInputs ?? inputs
	const visibleLoading = externallyManaged ? Boolean(providedLoading) : loading
	const visibleError = externallyManaged ? (providedError || null) : error

  const loadInputs = useCallback(async (cancelled?: () => boolean, showLoading = true) => {
    if (!workspacePath) return
    if (showLoading) setLoading(true)
    if (showLoading) setError(null)
    try {
      const res = await agentApi.listReportHumanInputs(workspacePath, undefined, source)
      if (cancelled?.()) return
      if (!res.success) throw new Error(res.error || 'Failed to load questions.')
      const nextInputs = res.inputs || []
      setInputs(previous => keepPreviousInputsWhenUnchanged(previous, nextInputs))
      if (!showLoading) setError(null)
    } catch (err) {
      if (cancelled?.()) return
      setError(err instanceof Error ? err.message : 'Failed to load questions.')
      // A transient background-poll failure must not blank a decision card the
      // user is reading. Explicit/initial loads still surface an empty result.
      if (showLoading) setInputs([])
    } finally {
      if (showLoading && !cancelled?.()) setLoading(false)
    }
  }, [source, workspacePath])

	useEffect(() => {
		if (externallyManaged) return
		let cancelled = false
    void loadInputs(() => cancelled)
    return () => { cancelled = true }
	}, [externallyManaged, loadInputs, refreshNonce])

	// Server live-feed notices (a decision created, answered or dismissed)
	// keep the previous inputs on screen and never flash the loading state.
	// The chat never refreshes this pane.
	useLiveRefetch(() => { void loadInputs(undefined, false) }, {
		kinds: ['human_inputs'], workflow: workspacePath, fallbackMs: 0, safetyMs: 0, enabled: !externallyManaged,
	})

  useEffect(() => {
    setDrafts({})
    setHistoryOpen(historyMode === 'expanded')
    setExpandedHistoryIds({})
  }, [historyMode, workspacePath])

	const pending = contentMode === 'history' ? [] : visibleInputs.filter(input => input.status === 'pending')
	const history = contentMode === 'pending' ? [] : reportHumanInputHistory(visibleInputs, historyLimit)
	if (!visibleLoading && !visibleError && pending.length === 0 && history.length === 0) {
		return showEmptyState ? <p className={`rounded-lg border bg-muted/20 px-4 py-6 text-center text-sm text-muted-foreground ${className}`}>No human actions are waiting for you.</p> : null
	}

	const requestRefresh = () => {
    window.dispatchEvent(new CustomEvent(WORKFLOW_DECISIONS_REFRESH_EVENT, { detail: { workspacePath } }))
		if (onRequestRefresh) {
			onRequestRefresh()
			return
		}
		setRefreshNonce(prev => prev + 1)
	}

  const updateDraft = (id: string, patch: Partial<ReportHumanInputDraft>) => {
    setDrafts(prev => {
      const current = prev[id] ?? { selectedOptionId: '', note: '' }
      return { ...prev, [id]: { ...current, ...patch } }
    })
  }

  const dismissInput = async (input: ReportHumanInput) => {
    updateDraft(input.id, { submitting: true })
    try {
      await agentApi.dismissReportHumanInput(workspacePath, input.id)
      useChatStore.getState().addToast('Question dismissed.', 'success')
		setHistoryOpen(historyMode === 'expanded')
		requestRefresh()
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to dismiss question.', 'error')
    } finally {
      updateDraft(input.id, { submitting: false })
    }
  }

  // Two-click confirm, as on Ask AI buttons: the first click arms an option,
  // a second click within OPTION_ARM_TIMEOUT_MS sends it. A second click
  // faster than OPTION_CONFIRM_FLOOR_MS is the tail of a double-click and is
  // ignored, so a misclick never sends an answer.
  const clickOption = (input: ReportHumanInput, option: { id: string; title: string }) => {
    const now = Date.now()
    if (armedOption && armedOption.inputId === input.id && armedOption.optionId === option.id) {
      if (now - armedOption.at < OPTION_CONFIRM_FLOOR_MS) return
      if (disarmTimerRef.current !== null) window.clearTimeout(disarmTimerRef.current)
      setArmedOption(null)
      void answerInChat(input, option)
      return
    }
    setArmedOption({ inputId: input.id, optionId: option.id, at: now })
    if (disarmTimerRef.current !== null) window.clearTimeout(disarmTimerRef.current)
    disarmTimerRef.current = window.setTimeout(() => setArmedOption(null), OPTION_ARM_TIMEOUT_MS)
  }

  // Decisions are answered in chat. Clicking an option is an explicit choice,
  // so it is sent straight away and the agent records and applies it in that
  // turn. A free-text answer opens the chat with the decision filled in, for
  // the user to type and send.
  const answerInChat = async (input: ReportHumanInput, option?: { id: string; title: string }) => {
    if (!option) {
      try {
        await openReportHumanInputAnswerInChat({ input, workspacePath })
      } catch (err) {
        useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to open the chat.', 'error')
      }
      return
    }
    updateDraft(input.id, { delegating: true })
    try {
      const result = await sendReportHumanInputAnswerToChat({ input, workspacePath, option })
      useChatStore.getState().addToast(
        result.queuedBehindRunningTurn ? `"${option.title}" sent; it runs after the current chat turn.` : `"${option.title}" sent to chat.`,
        'success',
      )
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to send the answer to chat.', 'error')
    } finally {
      updateDraft(input.id, { delegating: false })
    }
  }

  // Ask in chat opens the automation chat with the decision already in the
  // composer, so the user reads it there, adds their question and sends.
  const askInChat = async (input: ReportHumanInput) => {
    updateDraft(input.id, { askingInChat: true })
    try {
      await openReportHumanInputQuestionInChat({ input, workspacePath })
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to open the chat.', 'error')
    } finally {
      updateDraft(input.id, { askingInChat: false })
    }
  }

  const delegateActionToChat = async (input: ReportHumanInput) => {
    updateDraft(input.id, { delegating: true })
    try {
      const result = await delegateReportHumanInputActionToChat({ input, workspacePath })
      useChatStore.getState().addToast(
        result.queuedBehindRunningTurn
          ? 'Delegated decision queued behind the current chat turn.'
          : result.reused
            ? 'Decision delegated to the existing chat.'
            : 'New chat opened to analyze and act on this decision.',
        'success',
      )
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to delegate the decision to chat.', 'error')
    } finally {
      updateDraft(input.id, { delegating: false })
    }
  }

  const renderHistoryRows = () => (
    <div className="grid min-w-0 grid-cols-1 gap-1.5">
      {history.map(input => {
        const expanded = expandedHistoryIds[input.id] ?? historyMode === 'expanded'
        const answer = selectedOptionTitle(input)
        const impact = reportHumanInputImpact(input, providedImpact)
        const assessment = impact?.latestAssessment
        return (
          <div key={input.id} className="min-w-0 rounded-md bg-background/50 text-xs">
            <button
              type="button"
              onClick={() => setExpandedHistoryIds(prev => ({ ...prev, [input.id]: !expanded }))}
              aria-expanded={expanded}
              className="flex w-full min-w-0 items-start gap-2 px-2 py-2 text-left hover:bg-muted/40"
            >
              {expanded ? <ChevronDown className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" /> : <ChevronRight className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
              {input.status === 'consumed'
                ? <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-300" />
				: input.status === 'answered' || input.status === 'claimed'
                  ? <Clock3 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-200" />
                  : null}
              <span className="min-w-0 flex-1">
                <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
                  <span className={`min-w-0 font-medium ${statusTone(input)}`}>{reportHumanInputStatusLabel(input)}</span>
                  <span className="text-muted-foreground">{inputTime(input.consumed_at || input.answered_at || input.dismissed_at || input.updated_at)}</span>
                </span>
                <span className={`mt-0.5 block min-w-0 ${expanded ? 'whitespace-normal leading-5 text-foreground' : 'truncate text-muted-foreground'}`}>{input.question}</span>
              </span>
            </button>
            {expanded && (
              <div className="min-w-0 space-y-1.5 border-t border-border/50 px-3 py-2 text-muted-foreground">
                {answer && (
                  <div>
                    <span className="font-medium text-foreground">You answered: </span>
                    <span>{answer}</span>
                  </div>
                )}
                {input.note && (
                  <div>
                    <span className="font-medium text-foreground">Note: </span>
                    <span>{input.note}</span>
                  </div>
                )}
			{(input.status === 'answered' || input.status === 'claimed') && (
                  <div className="flex items-center gap-1.5 rounded-md border border-amber-400/20 bg-amber-400/[0.06] px-2 py-1.5 text-amber-100">
                    <Clock3 className="h-3.5 w-3.5 shrink-0" />
				<span>{input.status === 'claimed' ? 'The saved decision is being processed.' : 'Decision saved — being applied in chat, or at the next run.'}</span>
                  </div>
                )}
                {input.outcome_summary && (
                  <div className="rounded-md border border-emerald-400/20 bg-emerald-400/[0.06] px-2 py-1.5 text-emerald-100">
                    <div>
                      <span className="font-medium">Action taken by {consumedActorLabel(input)}: </span>
                      <span>{input.outcome_summary}</span>
                    </div>
                    {input.consumed_at && <div className="mt-1 text-[11px] text-emerald-200/70">Completed {inputTime(input.consumed_at)}</div>}
                  </div>
                )}
                {impact && (
                  <div className="rounded-md border border-violet-400/20 bg-violet-400/[0.06] px-2 py-1.5 text-violet-100">
                    <div className="font-medium">Impact tracking</div>
                    {impact.intervention.effects?.length ? <PulseMetricOutcomes item={impact.intervention} assessments={providedImpact?.assessments || []} /> : assessment ? (
                      <>
                        <div className="mt-0.5">
                          {assessment.verdict === 'improved' ? 'Improved' : assessment.verdict === 'regressed' ? 'Regressed' : assessment.verdict === 'unchanged' ? 'No clear change' : assessment.verdict === 'confounded' ? 'Could not isolate the effect' : 'Not enough evidence yet'}
                          {typeof assessment.before_value === 'number' && typeof assessment.after_value === 'number'
                            ? ` · ${assessment.before_value} → ${assessment.after_value}`
                            : ''}
                        </div>
                        <div className="mt-1 text-[11px] text-violet-200/70">
                          {(assessment.metric || impact.intervention.metric).replaceAll('_', ' ')} · {assessment.confidence || 'unknown'} confidence · measured {inputTime(assessment.assessed_at)}
                        </div>
                        {assessment.next_checkpoint && <div className="mt-1 text-[11px] text-violet-200/70">Next check: {assessment.next_checkpoint}</div>}
                      </>
                    ) : (
                      <>
                        <div className="mt-0.5">Waiting for comparable run evidence.</div>
                        <div className="mt-1 text-[11px] text-violet-200/70">
                          Measuring {impact.intervention.metric.replaceAll('_', ' ')}
                          {impact.intervention.checkpoint ? ` · next check: ${impact.intervention.checkpoint}` : ''}
                          {impact.intervention.minimum_evidence_runs > 0 ? ` · ${impact.intervention.minimum_evidence_runs} run${impact.intervention.minimum_evidence_runs === 1 ? '' : 's'} required` : ''}
                        </div>
                      </>
                    )}
                  </div>
                )}
                {!answer && !input.note && !input.outcome_summary && <div>No saved answer details.</div>}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )

  if (pending.length === 0 && history.length > 0 && !historyOpen) {
    const latest = history[0]
    const latestSummary = latest?.status === 'consumed' && latest.outcome_summary
      ? latest.outcome_summary
      : latest?.question
    return (
      <section ref={panelRef} className={`min-w-0 max-w-full rounded-lg border border-cyan-500/20 bg-cyan-500/[0.045] px-3 py-2 shadow-sm [overflow-wrap:anywhere] ${className}`}>
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
          <button
            type="button"
            onClick={() => setHistoryOpen(true)}
            className="flex min-w-0 flex-1 items-center gap-2 text-left"
            aria-expanded={false}
          >
            <MessageSquareText className="h-4 w-4 shrink-0 text-cyan-200" />
            <span className="min-w-0">
              <span className="block text-xs font-semibold text-foreground">
                Recent decisions{workspaceLabel ? ` · ${workspaceLabel}` : ''}
              </span>
              <span className="block truncate text-[11px] text-muted-foreground">
                {history.length} saved{latest ? ` · ${reportHumanInputStatusLabel(latest)} · ${latestSummary}` : ''}
              </span>
            </span>
          </button>
          <div className="flex shrink-0 items-center gap-1.5">
            <button
              type="button"
				onClick={requestRefresh}
              className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border bg-background text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-label="Refresh questions"
            >
				{visibleLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            </button>
            <button
              type="button"
              onClick={() => setHistoryOpen(true)}
              className="inline-flex h-7 items-center gap-1 rounded-md border border-border bg-background px-2 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-expanded={false}
            >
              Show
              <ChevronDown className="h-3.5 w-3.5" />
            </button>
          </div>
        </div>
      </section>
    )
  }

  return (
    <section ref={panelRef} className={`min-w-0 max-w-full rounded-lg border border-cyan-500/25 bg-cyan-500/[0.06] p-3 shadow-sm [overflow-wrap:anywhere] ${className}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-cyan-400/30 bg-cyan-400/10 text-cyan-200">
            <MessageSquareText className="h-4 w-4" />
          </div>
          <div className="min-w-0">
            <div className="text-sm font-semibold text-foreground">
              {pending.length > 0
                ? `Needs your decision${workspaceLabel ? ` · ${workspaceLabel}` : ''}`
                : `Questions and answers${workspaceLabel ? ` · ${workspaceLabel}` : ''}`}
            </div>
            <div className="text-xs text-muted-foreground">
              {pending.length > 0
				? 'Review the evidence, ask questions in chat, then approve, reject, or defer.'
                : 'Previous questions, your answers, and their outcomes.'}
            </div>
          </div>
        </div>
        <button
          type="button"
			onClick={requestRefresh}
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-2.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
        >
			{visibleLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          Refresh
        </button>
      </div>
		{visibleError && <div className="mt-2 rounded-md border border-destructive/30 bg-destructive/10 px-2 py-1.5 text-xs text-destructive">{visibleError}</div>}
      <div className="mt-3 flex min-w-0 flex-col gap-2">
        {pending.map(input => {
          const draft = drafts[input.id] || { selectedOptionId: '', note: '' }
          const submitting = Boolean(draft.submitting)
          const askingInChat = Boolean(draft.askingInChat)
          const delegating = Boolean(draft.delegating)
          const busy = submitting || askingInChat || delegating
          return (
            <article key={input.id} className="min-w-0 rounded-md border border-border/70 bg-background/75 p-3">
              <div className="flex flex-wrap items-center gap-2 text-[11px]">
                <span className={`rounded-full border px-2 py-0.5 font-semibold uppercase tracking-[0.08em] ${priorityTone(input.priority)}`}>
                  {input.priority || 'medium'}
                </span>
                <span className="rounded-full border border-border bg-muted/40 px-2 py-0.5 text-muted-foreground">{sourceLabel(input.source)}</span>
                <span className="text-muted-foreground">{inputTime(input.created_at)}</span>
              </div>
              <h4 className="mt-2 text-sm font-semibold leading-snug text-foreground">{input.question}</h4>
              {input.context && <HumanInputContext value={input.context} />}
              {input.options.length > 0 && (
                <div className={compactOptions ? 'mt-3 overflow-hidden rounded-md border border-border/70 bg-background/45' : 'mt-3 grid grid-cols-2 gap-2'}>
                  {input.options.map(option => {
                    const isArmed = armedOption?.inputId === input.id && armedOption.optionId === option.id
                    return (
                      <button
                        key={option.id}
                        type="button"
                        title={isArmed ? 'Click again to send this answer' : 'Click to choose, then click again to send'}
                        aria-pressed={isArmed}
                        disabled={busy}
                        onPointerDown={event => event.stopPropagation()}
                        onClick={event => {
                          event.stopPropagation()
                          clickOption(input, option)
                        }}
                        className={compactOptions
                          ? `flex w-full cursor-pointer items-start gap-2 border-b border-border/60 p-2.5 text-left last:border-b-0 transition-colors ${isArmed ? 'bg-cyan-400/15' : 'hover:bg-cyan-400/10'}`
                          : `flex cursor-pointer items-start gap-2 rounded-md border p-2 text-left transition-colors ${isArmed ? 'border-cyan-400 bg-cyan-400/15' : 'border-border bg-card/50 hover:border-cyan-400/50 hover:bg-cyan-400/10'}`
                        }
                      >
                        <span className="min-w-0 flex-1 text-left">
                          <span className="block break-words text-xs font-semibold text-foreground">{option.title}</span>
                          {option.description && <span className="mt-0.5 block break-words text-xs leading-5 text-muted-foreground">{option.description}</span>}
                          {isArmed && <span className="mt-1 block text-xs font-semibold text-cyan-300">Click again to send</span>}
                        </span>
                      </button>
                    )
                  })}
                </div>
              )}
              {input.allow_free_text && input.options.length === 0 && (
                <button
                  type="button"
                  onClick={() => void answerInChat(input)}
                  disabled={busy}
                  className="mt-3 inline-flex h-8 items-center gap-1.5 rounded-md border border-cyan-400/40 bg-cyan-400/15 px-3 text-xs font-semibold text-cyan-100 hover:bg-cyan-400/25 disabled:opacity-50"
                >
                  <Send className="h-3.5 w-3.5" />
                  Answer in chat
                </button>
              )}
              <div className="mt-3 flex flex-wrap justify-end gap-2">
                <button
                  type="button"
                  onClick={() => void dismissInput(input)}
                  disabled={busy}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
                >
                  <X className="h-3.5 w-3.5" />
                  Dismiss
                </button>
                <button
                  type="button"
                  onClick={() => void askInChat(input)}
                  disabled={busy}
                  title="Open the chat with this decision filled in, so you can add your question and send it."
                  className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-xs font-medium text-foreground hover:border-cyan-400/40 hover:bg-cyan-400/[0.06] disabled:opacity-50"
                >
                  <MessageSquareText className="h-3.5 w-3.5" />
                  Ask in chat
                </button>
                <button
                  type="button"
                  onClick={() => void delegateActionToChat(input)}
                  disabled={busy}
                  title="Let the agent analyze the evidence, choose the best option, and take the resulting safe workflow action."
                  className="inline-flex h-8 items-center gap-1.5 rounded-md border border-violet-400/40 bg-violet-400/15 px-3 text-xs font-semibold text-violet-100 hover:bg-violet-400/25 disabled:opacity-50"
                >
                  {delegating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Sparkles className="h-3.5 w-3.5" />}
                  Take best action
                </button>
              </div>
            </article>
          )
        })}
      </div>
      {history.length > 0 && (
        <div className="mt-3 border-t border-border/60 pt-2">
          <button
            type="button"
            onClick={() => setHistoryOpen(prev => !prev)}
            className="mb-1 flex w-full items-center justify-between gap-2 text-left text-[11px] font-semibold uppercase tracking-[0.16em] text-muted-foreground hover:text-foreground"
            aria-expanded={historyOpen}
          >
            <span>Questions and answers</span>
            {historyOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          </button>
          {historyOpen && renderHistoryRows()}
        </div>
      )}
    </section>
  )
}

export type ReportHumanInputScope = {
	workspacePath: string
	workspaceLabel: string
}

export function ReportHumanInputCollection({
	scopes,
	source,
	className = '',
}: {
	scopes: ReportHumanInputScope[]
	source?: string
	className?: string
}) {
	const scopeKey = scopes.map(scope => `${scope.workspacePath}\u0000${scope.workspaceLabel}`).join('\u0001')
	const stableScopes = useMemo(() => scopeKey.split('\u0001').filter(Boolean).map(value => {
		const [workspacePath, workspaceLabel] = value.split('\u0000')
		return { workspacePath, workspaceLabel }
	}), [scopeKey])
	const [inputs, setInputs] = useState<ReportHumanInput[]>([])
	const [loading, setLoading] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [refreshNonce, setRefreshNonce] = useState(0)

	const loadInputs = useCallback(async (cancelled?: () => boolean, showLoading = true) => {
		const paths = stableScopes.map(scope => scope.workspacePath).filter(Boolean)
		if (paths.length === 0) {
			setInputs([])
			return
		}
		if (showLoading) setLoading(true)
		if (showLoading) setError(null)
		try {
			const result = await agentApi.listReportHumanInputsAggregate(paths, undefined, source)
			if (cancelled?.()) return
			if (!result.success) throw new Error(result.error || 'Failed to load questions.')
			const nextInputs = result.inputs || []
			setInputs(previous => keepPreviousInputsWhenUnchanged(previous, nextInputs))
			if (!showLoading) setError(null)
		} catch (err) {
			if (cancelled?.()) return
			setError(err instanceof Error ? err.message : 'Failed to load questions.')
			if (showLoading) setInputs([])
		} finally {
			if (showLoading && !cancelled?.()) setLoading(false)
		}
	}, [source, stableScopes])

	useEffect(() => {
		let cancelled = false
		void loadInputs(() => cancelled)
		return () => { cancelled = true }
	}, [loadInputs, refreshNonce])

	// Live-feed notices for any workflow; see the single-workflow panel above.
	useLiveRefetch(() => { void loadInputs(undefined, false) }, {
		kinds: ['human_inputs'], fallbackMs: 0, safetyMs: 0,
	})

	if (loading && inputs.length === 0 && !error) return null

	return (
		<div className={className}>
			{stableScopes.map((scope, index) => (
				<ReportHumanInputPanel
					key={scope.workspacePath}
					workspacePath={scope.workspacePath}
					workspaceLabel={scope.workspaceLabel}
					source={source}
					providedInputs={inputs.filter(input => input.workspace_path === scope.workspacePath)}
					providedLoading={loading}
					providedError={index === 0 ? error : null}
					onRequestRefresh={() => setRefreshNonce(value => value + 1)}
				/>
			))}
		</div>
	)
}

export default ReportHumanInputPanel
