import { useEffect, useState } from 'react'
import { Check, CheckCircle2, CircleHelp } from 'lucide-react'
import { SKIPPED_QUESTION_ANSWER, takesTypedAnswer, typedAnswers, type CodingAgentQuestionPrompt, type CodingAgentChoiceAnswer } from '../utils/codingAgentQuestions'
import { PlanLine } from './chat/PlanModeControls'
import { planFromQuestion } from '../utils/planSummary'

export function CodingAgentQuestionCard({ prompt, onAnswer, docked }: {
  prompt: CodingAgentQuestionPrompt
  onAnswer?: (provider: string, promptId: string, answers: CodingAgentChoiceAnswer[], auto?: boolean) => Promise<void>
  // The pending prompt is answered in the panel above the message box (ComposerPendingPrompt).
  docked?: boolean
}) {
  const [selected, setSelected] = useState<Record<string, string[]>>({})
  const [useOther, setUseOther] = useState<Record<string, boolean>>({})
  const [otherText, setOtherText] = useState<Record<string, string>>({})
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [questionIndex, setQuestionIndex] = useState(0)
  useEffect(() => { setSelected({}); setUseOther({}); setOtherText({}); setSubmitting(false); setError(''); setQuestionIndex(0) }, [prompt.provider, prompt.promptId])
  const pending = prompt.state === 'pending'
  const bounds = (question: CodingAgentQuestionPrompt['questions'][number]) => question.multiSelect
    ? { min: Math.max(1, question.minSelections || 1), max: question.maxSelections || question.options.length + (question.allowOther ? 1 : 0) }
    : { min: 1, max: 1 }
  const questionReady = (question: CodingAgentQuestionPrompt['questions'][number]) => {
    const count = (selected[question.id]?.length || 0) + ((useOther[question.id] || question.options.length === 0) ? 1 : 0)
    const { min, max } = bounds(question)
    return count >= min && count <= max && (!(useOther[question.id] || question.options.length === 0) || !!otherText[question.id]?.trim())
  }
  const ready = prompt.questions.length > 0 && prompt.questions.every(questionReady)
  const activeIndex = Math.min(questionIndex, Math.max(0, prompt.questions.length - 1))
  const activeQuestion = prompt.questions[activeIndex]
  const chooseOption = (question: CodingAgentQuestionPrompt['questions'][number], label: string) => {
    if (!pending || submitting || !onAnswer) return
    const prior = selected[question.id] || []
    const count = prior.length + (useOther[question.id] ? 1 : 0)
    if (question.multiSelect && !prior.includes(label) && count >= bounds(question).max) return
    if (!question.multiSelect) setUseOther(current => ({ ...current, [question.id]: false }))
    setSelected(current => ({ ...current, [question.id]: question.multiSelect
      ? (prior.includes(label) ? prior.filter(value => value !== label) : [...prior, label]) : [label] }))
  }
  const canUseFirstOptions = prompt.questions.length > 0 && prompt.questions.every(question => question.options.length > 0)
  // The user can explicitly submit the first options for every question.
  const submit = async (auto = false) => {
    if (!pending || (auto && !canUseFirstOptions) || (!auto && !ready) || !onAnswer || submitting) return
    setSubmitting(true)
    setError('')
    try {
      await onAnswer(prompt.provider, prompt.promptId, auto ? [] : prompt.questions.map((question) => ({
        id: question.id,
        selectedLabels: question.options.filter((option) => (selected[question.id] || []).includes(option.label)).map((option) => option.label),
        ...((useOther[question.id] || question.options.length === 0) ? { otherText: otherText[question.id].trim() } : {}),
      })), auto)
      // The settled event is the acknowledgement; keep controls
      // disabled until that durable event reaches the conversation.
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not submit this choice. Refresh and try again.')
      setSubmitting(false)
    }
  }
  // Skip answers every question with a plain "no answer" so the agent carries on instead of waiting.
  const canSkip = takesTypedAnswer(prompt)
  const skip = async () => {
    if (!pending || !canSkip || !onAnswer || submitting) return
    setSubmitting(true)
    setError('')
    try {
      await onAnswer(prompt.provider, prompt.promptId, typedAnswers(prompt, SKIPPED_QUESTION_ANSWER))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not skip this question. Refresh and try again.')
      setSubmitting(false)
    }
  }
  if (pending && docked) {
    return <p className="flex items-center gap-2 text-xs text-muted-foreground" data-testid="coding-agent-question-docked">
      <CircleHelp className="h-3.5 w-3.5" aria-hidden="true" /> Waiting for your answer just above the message box.
    </p>
  }
  // A plan stays one slim line in the conversation; the full plan opens in the plan pane (#3711).
  if (prompt.requestKind === 'plan_approval' && prompt.questions.length === 1) {
    const question = prompt.questions[0]
    const given = prompt.answers.find(answer => answer.id === question.id)
    const chosen = given?.selectedLabels[0]
    const plan = planFromQuestion(question.question)
    if (!pending) return <PlanLine plan={plan} outcome={chosen === 'Approve' ? 'approved' : given?.otherText ? 'changed' : chosen ? 'rejected' : 'closed'} />
    const answer = (label: string) => () => {
      if (submitting || !onAnswer) return
      setSubmitting(true)
      setError('')
      onAnswer(prompt.provider, prompt.promptId, [{ id: question.id, selectedLabels: [label] }]).catch(cause => {
        setError(cause instanceof Error ? cause.message : 'Could not send your answer. Try again.')
        setSubmitting(false)
      })
    }
    return <div className="w-full">
      <PlanLine plan={plan} answer={onAnswer ? { busy: submitting, approve: answer('Approve'), reject: answer('Reject') } : undefined} />
      {error && <p role="alert" className="mt-1 text-xs text-destructive">{error}</p>}
    </div>
  }
  return <section className={`w-full rounded-xl border border-border bg-card p-3 text-card-foreground ${pending ? 'flex max-h-[min(320px,40dvh)] flex-col' : ''}`} data-testid="coding-agent-question-card" tabIndex={pending ? 0 : undefined} onKeyDown={event => {
    // Shortcuts belong to this focused card, never the chat's typing box.
    if (!pending || !activeQuestion || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey
      || (event.target instanceof HTMLElement && event.target.closest('input, textarea, select, [contenteditable="true"]'))) return
    const index = Number(event.key) - 1
    const option = /^[1-9]$/.test(event.key) ? activeQuestion.options[index] : undefined
    if (option) { event.preventDefault(); chooseOption(activeQuestion, option.label) }
  }}>
    <div className="mb-2 flex shrink-0 items-center gap-2 text-xs font-medium text-muted-foreground">
      {prompt.state === 'answered' ? <CheckCircle2 className="h-3.5 w-3.5" aria-hidden="true" /> : <CircleHelp className="h-3.5 w-3.5" aria-hidden="true" />}
      {pending ? 'Clarification needed' : prompt.state === 'answered' ? 'Clarification answered' : prompt.state === 'unanswered' ? 'Question closed without an answer' : 'Clarification closed'}
      {pending && prompt.questions.length > 1 && <span aria-live="polite" className="ml-auto">Question {activeIndex + 1} of {prompt.questions.length}</span>}
    </div>
    {pending && prompt.statusMessage && <p className="mb-2 text-xs text-muted-foreground">{prompt.statusMessage}</p>}
    <div className={pending ? 'min-h-0 overflow-y-auto overscroll-contain pr-1' : undefined}>
    {(pending ? (activeQuestion ? [activeQuestion] : []) : prompt.questions).map((question) => <fieldset key={question.id} className="mb-3 space-y-1" disabled={!pending || submitting || !onAnswer}>
      <legend className="mb-1.5 whitespace-pre-wrap text-[13px] font-medium">{question.question}</legend>
      {question.multiSelect && <p className="mb-2 text-xs text-muted-foreground">{question.maxSelections || (question.minSelections || 0) > 1 ? selectionHint(bounds(question)) : 'Select all that apply.'}</p>}
      {question.options.map((option, optionIndex) => {
        const checked = pending ? (selected[question.id] || []).includes(option.label) : prompt.answers.some((answer) => answer.id === question.id && answer.selectedLabels.includes(option.label))
        const full = question.multiSelect && !checked && (selected[question.id]?.length || 0) + ((useOther[question.id] || question.options.length === 0) ? 1 : 0) >= bounds(question).max
        const disabled = !pending || submitting || !onAnswer || full
        return <label key={option.label} className={`flex min-h-11 items-center gap-2 rounded-md border px-3 py-2.5 text-sm transition-colors sm:min-h-0 sm:items-start sm:px-2.5 sm:py-1.5 sm:text-xs focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2 focus-within:ring-offset-card ${checked ? 'border-primary/40 bg-primary/10' : 'border-border bg-background/30'} ${disabled ? 'cursor-default' : 'cursor-pointer hover:border-primary/40 hover:bg-muted/40'} ${full ? 'opacity-50' : ''}`}>
          <input className="peer sr-only" type={question.multiSelect ? 'checkbox' : 'radio'} name={`${prompt.provider}:${prompt.promptId}:${question.id}`} value={option.label} checked={checked} disabled={disabled} onChange={() => chooseOption(question, option.label)} />
          <span aria-hidden="true" className={`mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center border ${question.multiSelect ? 'rounded' : 'rounded-full'} ${checked ? 'border-primary bg-primary text-primary-foreground' : 'border-muted-foreground/50'}`}>
            {checked && (question.multiSelect ? <Check className="h-3 w-3" /> : <span className="h-1.5 w-1.5 rounded-full bg-primary-foreground" />)}
          </span>
          {pending && optionIndex < 9 && <kbd aria-hidden="true" className="text-xs text-muted-foreground">{optionIndex + 1}</kbd>}
          <span><span className="block font-medium">{option.label}</span>{option.description && <span className="mt-0.5 block text-[11px] leading-snug text-muted-foreground">{option.description}</span>}</span>
        </label>
      })}
      {question.allowOther && (() => {
        const submitted = prompt.answers.find(answer => answer.id === question.id)?.otherText || ''
        const checked = pending ? (question.options.length === 0 || !!useOther[question.id]) : !!submitted
        const full = question.multiSelect && !checked && (selected[question.id]?.length || 0) >= bounds(question).max
        const disabled = !pending || submitting || !onAnswer || full
        return <div className={`rounded-md border px-3 py-2.5 sm:px-2.5 sm:py-1.5 focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2 focus-within:ring-offset-card ${checked ? 'border-primary/40 bg-primary/10' : 'border-border bg-background/30'}`}>
          <label className={`flex min-h-6 items-center gap-2 text-sm sm:min-h-0 sm:text-xs ${disabled ? 'cursor-default' : 'cursor-pointer'}`}>
            {question.options.length > 0 && <>
            <input className="sr-only" type={question.multiSelect ? 'checkbox' : 'radio'} name={`${prompt.provider}:${prompt.promptId}:${question.id}`} checked={checked} disabled={disabled} onChange={() => {
              setUseOther(current => ({ ...current, [question.id]: !checked }))
              if (!question.multiSelect) setSelected(current => ({ ...current, [question.id]: [] }))
            }} />
            <span aria-hidden="true" className={`flex h-3.5 w-3.5 shrink-0 items-center justify-center border ${question.multiSelect ? 'rounded' : 'rounded-full'} ${checked ? 'border-primary bg-primary text-primary-foreground' : 'border-muted-foreground/50'}`}>
              {checked && (question.multiSelect ? <Check className="h-3 w-3" /> : <span className="h-1.5 w-1.5 rounded-full bg-primary-foreground" />)}
            </span>
            </>}
            <span className="font-medium">{question.options.length ? 'Other' : 'Your answer'}</span>
          </label>
          {checked && (pending ? <textarea aria-label={`${question.options.length ? 'Other answer' : 'Answer'} for ${question.question}`} maxLength={2000} value={otherText[question.id] || ''} disabled={disabled} onChange={event => setOtherText(current => ({ ...current, [question.id]: event.target.value }))} placeholder="Type your answer" className="mt-2 w-full rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /> : <p className="ml-7 mt-1 text-sm">{submitted}</p>)}
        </div>
      })()}
    </fieldset>)}
    </div>
    {pending ? <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-border pt-2">
      {prompt.questions.length > 1 && <>
        <button type="button" disabled={activeIndex === 0 || submitting} onClick={() => setQuestionIndex(index => Math.max(0, index - 1))} className="min-h-11 rounded-md px-3 text-sm text-muted-foreground hover:bg-muted disabled:opacity-50 sm:min-h-0 sm:py-1.5 sm:text-xs">Back</button>
        {activeIndex < prompt.questions.length - 1 && <button type="button" disabled={!activeQuestion || !questionReady(activeQuestion) || submitting || !onAnswer} onClick={() => setQuestionIndex(index => Math.min(prompt.questions.length - 1, index + 1))} className="min-h-11 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground disabled:opacity-50 sm:min-h-0 sm:py-1.5 sm:text-xs">Next</button>}
      </>}
      {(prompt.questions.length === 1 || activeIndex === prompt.questions.length - 1) && <button type="button" onClick={() => void submit()} disabled={!ready || submitting || !onAnswer} className="min-h-11 rounded-md bg-primary px-4 text-sm font-medium sm:min-h-0 sm:px-3 sm:py-1.5 sm:text-xs text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-card disabled:cursor-not-allowed disabled:opacity-50">{submitting ? 'Submitting…' : 'Send answer'}</button>}
      {canUseFirstOptions && <button type="button" onClick={() => void submit(true)} disabled={submitting || !onAnswer} className="min-h-11 rounded-md px-3 text-sm font-medium text-muted-foreground sm:min-h-0 sm:px-2.5 sm:py-1.5 sm:text-xs hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50">Use first options</button>}
      {canSkip && <button type="button" onClick={() => void skip()} disabled={submitting || !onAnswer} data-testid="coding-agent-question-skip" title="Skip: the agent continues without your answer" className="ml-auto min-h-11 rounded-md px-3 text-sm text-muted-foreground sm:min-h-0 sm:px-2.5 sm:py-1.5 sm:text-xs hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50">Skip</button>}
    </div>
      : <p className="text-xs text-muted-foreground">{prompt.statusMessage || (prompt.state === 'answered' ? 'Choice submitted' : prompt.state === 'unanswered' ? 'This question closed without an answer.' : 'Question interrupted')}</p>}
    {pending && !onAnswer && <p className="mt-2 text-xs text-muted-foreground">This conversation cannot be changed.</p>}
    {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
  </section>
}

function selectionHint({ min, max }: { min: number; max: number }) {
  if (min === max) return `Choose ${min}.`
  if (min <= 1) return `Choose up to ${max}.`
  return `Choose ${min} to ${max}.`
}
