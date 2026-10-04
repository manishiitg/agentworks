import { useEffect, useState } from 'react'
import { Check, CheckCircle2, CircleHelp } from 'lucide-react'
import type { CodingAgentQuestionPrompt, CodingAgentChoiceAnswer } from '../utils/codingAgentQuestions'

export function CodingAgentQuestionCard({ prompt, onAnswer }: {
  prompt: CodingAgentQuestionPrompt
  onAnswer?: (provider: string, promptId: string, answers: CodingAgentChoiceAnswer[], auto?: boolean) => Promise<void>
}) {
  const [selected, setSelected] = useState<Record<string, string[]>>({})
  const [useOther, setUseOther] = useState<Record<string, boolean>>({})
  const [otherText, setOtherText] = useState<Record<string, string>>({})
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => { setSelected({}); setUseOther({}); setOtherText({}); setSubmitting(false); setError('') }, [prompt.provider, prompt.promptId])
  const pending = prompt.state === 'pending'
  const bounds = (question: CodingAgentQuestionPrompt['questions'][number]) => question.multiSelect
    ? { min: Math.max(1, question.minSelections || 1), max: question.maxSelections || question.options.length + (question.allowOther ? 1 : 0) }
    : { min: 1, max: 1 }
  const ready = prompt.questions.length > 0 && prompt.questions.every((question) => {
    const count = (selected[question.id]?.length || 0) + (useOther[question.id] ? 1 : 0)
    const { min, max } = bounds(question)
    return count >= min && count <= max && (!useOther[question.id] || !!otherText[question.id]?.trim())
  })
  // The user can explicitly submit the first options for every question.
  const submit = async (auto = false) => {
    if (!pending || (!auto && !ready) || !onAnswer || submitting) return
    setSubmitting(true)
    setError('')
    try {
      await onAnswer(prompt.provider, prompt.promptId, auto ? [] : prompt.questions.map((question) => ({
        id: question.id,
        selectedLabels: question.options.filter((option) => (selected[question.id] || []).includes(option.label)).map((option) => option.label),
        ...(useOther[question.id] ? { otherText: otherText[question.id].trim() } : {}),
      })), auto)
      // The settled event is the acknowledgement; keep controls
      // disabled until that durable event reaches the conversation.
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not submit this choice. Refresh and try again.')
      setSubmitting(false)
    }
  }
  return <section className="w-full rounded-xl border border-border bg-card p-4 text-card-foreground" data-testid="coding-agent-question-card">
    <div className="mb-3 flex items-center gap-2 text-xs font-medium text-muted-foreground">
      {prompt.state === 'answered' ? <CheckCircle2 className="h-3.5 w-3.5" aria-hidden="true" /> : <CircleHelp className="h-3.5 w-3.5" aria-hidden="true" />}
      {pending ? 'Clarification needed' : prompt.state === 'answered' ? 'Clarification answered' : 'Clarification closed'}
    </div>
    {prompt.questions.map((question) => <fieldset key={question.id} className="mb-4 space-y-2" disabled={!pending || submitting || !onAnswer}>
      <legend className="mb-2 text-sm font-medium">{question.question}</legend>
      {question.multiSelect && <p className="mb-2 text-xs text-muted-foreground">{question.maxSelections || (question.minSelections || 0) > 1 ? selectionHint(bounds(question)) : 'Select all that apply.'}</p>}
      {question.options.map((option) => {
        const checked = pending ? (selected[question.id] || []).includes(option.label) : prompt.answers.some((answer) => answer.id === question.id && answer.selectedLabels.includes(option.label))
        const full = question.multiSelect && !checked && (selected[question.id]?.length || 0) + (useOther[question.id] ? 1 : 0) >= bounds(question).max
        const disabled = !pending || submitting || !onAnswer || full
        return <label key={option.label} className={`flex gap-2.5 rounded-lg border px-3 py-2.5 text-sm transition-colors focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2 focus-within:ring-offset-card ${checked ? 'border-primary/40 bg-primary/10' : 'border-border bg-background/30'} ${disabled ? 'cursor-default' : 'cursor-pointer hover:border-primary/40 hover:bg-muted/40'} ${full ? 'opacity-50' : ''}`}>
          <input className="peer sr-only" type={question.multiSelect ? 'checkbox' : 'radio'} name={`${prompt.provider}:${prompt.promptId}:${question.id}`} value={option.label} checked={checked} disabled={disabled} onChange={() => { if (!question.multiSelect) setUseOther(current => ({ ...current, [question.id]: false })); setSelected((current) => {
            const prior = current[question.id] || []
            const next = question.multiSelect ? (prior.includes(option.label) ? prior.filter((value) => value !== option.label) : [...prior, option.label]) : [option.label]
            return { ...current, [question.id]: next }
          }) }} />
          <span aria-hidden="true" className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center border ${question.multiSelect ? 'rounded' : 'rounded-full'} ${checked ? 'border-primary bg-primary text-primary-foreground' : 'border-muted-foreground/50'}`}>
            {checked && (question.multiSelect ? <Check className="h-3 w-3" /> : <span className="h-1.5 w-1.5 rounded-full bg-primary-foreground" />)}
          </span>
          <span><span className="block font-medium">{option.label}</span>{option.description && <span className="mt-0.5 block text-xs text-muted-foreground">{option.description}</span>}</span>
        </label>
      })}
      {question.allowOther && (() => {
        const submitted = prompt.answers.find(answer => answer.id === question.id)?.otherText || ''
        const checked = pending ? !!useOther[question.id] : !!submitted
        const full = question.multiSelect && !checked && (selected[question.id]?.length || 0) >= bounds(question).max
        const disabled = !pending || submitting || !onAnswer || full
        return <div className={`rounded-lg border px-3 py-2.5 focus-within:ring-2 focus-within:ring-ring focus-within:ring-offset-2 focus-within:ring-offset-card ${checked ? 'border-primary/40 bg-primary/10' : 'border-border bg-background/30'}`}>
          <label className={`flex items-center gap-2.5 text-sm ${disabled ? 'cursor-default' : 'cursor-pointer'}`}>
            <input className="sr-only" type={question.multiSelect ? 'checkbox' : 'radio'} name={`${prompt.provider}:${prompt.promptId}:${question.id}`} checked={checked} disabled={disabled} onChange={() => {
              setUseOther(current => ({ ...current, [question.id]: !checked }))
              if (!question.multiSelect) setSelected(current => ({ ...current, [question.id]: [] }))
            }} />
            <span aria-hidden="true" className={`flex h-4 w-4 shrink-0 items-center justify-center border ${question.multiSelect ? 'rounded' : 'rounded-full'} ${checked ? 'border-primary bg-primary text-primary-foreground' : 'border-muted-foreground/50'}`}>
              {checked && (question.multiSelect ? <Check className="h-3 w-3" /> : <span className="h-1.5 w-1.5 rounded-full bg-primary-foreground" />)}
            </span>
            <span className="font-medium">Other</span>
          </label>
          {checked && (pending ? <input aria-label={`Other answer for ${question.question}`} maxLength={2000} value={otherText[question.id] || ''} disabled={disabled} onChange={event => setOtherText(current => ({ ...current, [question.id]: event.target.value }))} placeholder="Type your answer" className="mt-2 w-full rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring" /> : <p className="ml-7 mt-1 text-sm">{submitted}</p>)}
        </div>
      })()}
    </fieldset>)}
    {pending ? <div className="flex flex-wrap items-center gap-2">
      <button type="button" onClick={() => void submit()} disabled={!ready || submitting || !onAnswer} className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-card disabled:cursor-not-allowed disabled:opacity-50">{submitting ? 'Submitting…' : 'Send choice'}</button>
      <button type="button" onClick={() => void submit(true)} disabled={submitting || !onAnswer} className="rounded-lg px-3 py-2 text-sm font-medium text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50">Use first options</button>
    </div>
      : <p className="text-xs text-muted-foreground">{prompt.state === 'answered' ? 'Choice submitted' : 'Question interrupted'}</p>}
    {pending && !onAnswer && <p className="mt-2 text-xs text-muted-foreground">This conversation is read-only.</p>}
    {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
  </section>
}

function selectionHint({ min, max }: { min: number; max: number }) {
  if (min === max) return `Choose ${min}.`
  if (min <= 1) return `Choose up to ${max}.`
  return `Choose ${min} to ${max}.`
}
