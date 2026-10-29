import { useState } from 'react'
import { CircleHelp, ShieldQuestion } from 'lucide-react'
import { CodingAgentQuestionCard } from '../CodingAgentQuestionCard'
import type { CodingAgentQuestionAnswerHandler, CodingAgentQuestionPrompt } from '../../utils/codingAgentQuestions'

// The backend's command approval asks "Deny" / "Allow" (allow this command once). The buttons say
// Decline / Approve and send the backend's own label, so the answer means exactly what it did before.
const COMMAND_APPROVAL_ID = 'command_permission'
const BUTTON_TEXT: Record<string, string> = { Allow: 'Approve', Deny: 'Decline' }

// A pending approval or question, docked just above the message box until it is answered, so it
// cannot scroll away. The answered record stays in the conversation as before.
export function ComposerPendingPrompt({ prompt, onAnswer }: {
  prompt: CodingAgentQuestionPrompt
  onAnswer: CodingAgentQuestionAnswerHandler
}) {
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const question = prompt.questions[0]
  // One plain choice gets one-click buttons; anything richer keeps the full card.
  const oneClick = prompt.questions.length === 1 && !question.multiSelect && !question.allowOther
    && question.options.length > 0 && question.options.length <= 4
  if (!oneClick) {
    return <div className="mx-auto mb-2 w-full max-w-4xl px-3" data-testid="composer-pending-prompt">
      <CodingAgentQuestionCard prompt={prompt} onAnswer={onAnswer} />
    </div>
  }
  const approval = question.id === COMMAND_APPROVAL_ID
  const answer = async (label: string) => {
    if (submitting) return
    setSubmitting(true)
    setError('')
    try {
      await onAnswer(prompt.provider, prompt.promptId, [{ id: question.id, selectedLabels: [label] }])
      // The settled event closes this panel; keep the buttons disabled until it arrives.
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not send your answer. Try again.')
      setSubmitting(false)
    }
  }
  // Approve is the main action, so it comes last (next to where people expect "send").
  const options = approval ? [...question.options].sort((a, b) => Number(a.label === 'Allow') - Number(b.label === 'Allow')) : question.options
  return <section className="mx-auto mb-2 w-full max-w-4xl px-3" data-testid="composer-pending-prompt" aria-live="polite">
    <div className="rounded-xl border border-amber-300/70 bg-amber-50/80 p-3 text-card-foreground shadow-sm dark:border-amber-500/40 dark:bg-amber-950/30">
      <div className="mb-1.5 flex items-center gap-2 text-xs font-medium text-amber-800 dark:text-amber-300">
        {approval ? <ShieldQuestion className="h-3.5 w-3.5" aria-hidden="true" /> : <CircleHelp className="h-3.5 w-3.5" aria-hidden="true" />}
        {approval ? 'Approval needed' : (question.header || 'Your agent is asking')}
      </div>
      <p className={`max-h-40 overflow-y-auto whitespace-pre-wrap break-words text-[13px] ${approval ? 'font-mono' : 'font-medium'}`}>{question.question}</p>
      <div className="mt-2.5 flex flex-wrap items-center justify-end gap-2">
        {options.map((option) => {
          const primary = approval ? option.label === 'Allow' : false
          return <button key={option.label} type="button" title={option.description || undefined} disabled={submitting} onClick={() => void answer(option.label)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 ${primary ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'border border-border bg-background hover:bg-muted'}`}>
            {approval ? (BUTTON_TEXT[option.label] || option.label) : option.label}
          </button>
        })}
      </div>
      {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
    </div>
  </section>
}
