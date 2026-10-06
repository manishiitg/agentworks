import type { ReportHumanInput } from '../services/api-types'
import type { WorkspacePaneChatResult } from './workspacePaneChat'
import { openWorkspacePaneChatWithDraft, sendWorkspacePaneMessageToChat } from './workspacePaneChat'

function sourceName(source: string): string {
  if (['technical_review', 'engineering_review', 'ops_review'].includes(source)) return 'Technical Review'
  if (['strategic_review', 'strategy_auditor', 'goal_advisor'].includes(source)) return 'Strategic Review'
  if (source === 'user_suggestion') return 'User suggestion'
  if (source === 'architecture_review') return 'Architecture Review'
  if (source === 'plan_drift_review') return 'Workflow Review'
  return 'Pulse'
}

export function buildReportHumanInputChatMessage(
  input: ReportHumanInput,
  workspacePath: string,
  userQuestion: string,
): string {
  const lines = [
    `I want to discuss a pending ${sourceName(input.source)} decision. Do not submit, dismiss, or mark the decision handled yet; answer my question first.`,
    'If I later explicitly choose an option or give a final answer, call answer_human_input_request with the exact IDs below. Record it as answered only; do not mark it consumed.',
    'This is a Needs your decision card in the Human actions view. Read its current status with get_human_input_request. After my final answer, save it in that turn without waiting for a separate request to mark it; the card refreshes automatically after a successful save.',
    '',
    `Automation: ${workspacePath}`,
    `Decision ID: ${input.id}`,
    '',
    'Decision:',
    input.question.trim(),
  ]

  if (input.context?.trim()) {
    lines.push('', 'Context:', input.context.trim())
  }
  if (input.options.length > 0) {
    lines.push('', 'Available options:')
    input.options.forEach((option, index) => {
      lines.push(`${index + 1}. ${option.title} [option_id=${option.id}]${option.description ? ` — ${option.description}` : ''}`)
    })
  }
  if (input.evidence?.trim()) {
    lines.push('', `Evidence: ${input.evidence.trim()}`)
  }

  lines.push('', userQuestion.trim() ? `My question:\n${userQuestion.trim()}` : 'My question: ')
  return lines.join('\n')
}

/**
 * The user can explicitly delegate one pending decision to the workflow chat.
 * This is intentionally a distinct instruction from "Ask in chat": the latter
 * is advisory and must never answer the decision, while this one authorizes the
 * agent to choose a listed option and carry out the resulting safe workflow
 * work after it has considered the durable evidence.
 */
export function buildReportHumanInputDelegatedActionMessage(
  input: ReportHumanInput,
  workspacePath: string,
): string {
  const lines = [
    `I delegate this pending ${sourceName(input.source)} decision to you. Analyze the current evidence, workflow goal, constraints, and the available options; choose the best supported option and take the resulting safe workflow action.`,
    'Do not ask me to choose between the listed options. Use current evidence and tools to resolve uncertainty where practical. If no option is defensible, do not invent one or take an unsafe action: explain the blocker and leave the decision pending.',
    'After choosing, call answer_human_input_request with the exact decision and option IDs below, then apply the answer as the tool result describes and report the decision, evidence, action, and remaining risk in plain words.',
    '',
    `Automation: ${workspacePath}`,
    `Decision ID: ${input.id}`,
    '',
    'Decision:',
    input.question.trim(),
  ]

  if (input.context?.trim()) {
    lines.push('', 'Context:', input.context.trim())
  }
  if (input.options.length > 0) {
    lines.push('', 'Available options:')
    input.options.forEach((option, index) => {
      lines.push(`${index + 1}. ${option.title} [option_id=${option.id}]${option.description ? ` — ${option.description}` : ''}`)
    })
  }
  if (input.evidence?.trim()) {
    lines.push('', `Evidence: ${input.evidence.trim()}`)
  }

  return lines.join('\n')
}

export async function sendReportHumanInputQuestionToChat({
  input,
  workspacePath,
  userQuestion,
}: {
  input: ReportHumanInput
  workspacePath: string
  userQuestion: string
}): Promise<WorkspacePaneChatResult> {
  const question = userQuestion.trim()
  if (!question) throw new Error('Write a question before opening chat.')
  return sendWorkspacePaneMessageToChat({
    workspacePath,
    message: buildReportHumanInputChatMessage(input, workspacePath, question),
  })
}

export async function delegateReportHumanInputActionToChat({
  input,
  workspacePath,
}: {
  input: ReportHumanInput
  workspacePath: string
}): Promise<WorkspacePaneChatResult> {
  return sendWorkspacePaneMessageToChat({
    workspacePath,
    message: buildReportHumanInputDelegatedActionMessage(input, workspacePath),
  })
}

/**
 * Opens the automation chat with this decision placed in the composer, ending
 * in "My question: ", so the user reads it, types their question and sends.
 */
export async function openReportHumanInputQuestionInChat({
  input,
  workspacePath,
}: {
  input: ReportHumanInput
  workspacePath: string
}): Promise<{ tabId: string }> {
  return openWorkspacePaneChatWithDraft({
    workspacePath,
    message: buildReportHumanInputChatMessage(input, workspacePath, ''),
  })
}

/**
 * The composer text for answering a decision in chat: the choice (or, for a
 * written-answer question, "My answer: "), and the instruction to record it
 * with answer_human_input_request and apply it in the same turn. The caret
 * lands after "Note (optional): " (or "My answer: ") for the user's words.
 */
export function buildReportHumanInputAnswerMessage(
  input: ReportHumanInput,
  workspacePath: string,
  option?: { id: string; title: string },
): string {
  const record = option
    ? `Record it with answer_human_input_request(workspace_path="${workspacePath}", input_id="${input.id}", selected_option_id="${option.id}", note=<my note, if any>), then apply it now as the tool result describes and tell me in one or two plain sentences what changed.`
    : `Record it with answer_human_input_request(workspace_path="${workspacePath}", input_id="${input.id}", note=<my answer>), then apply it now as the tool result describes and tell me in one or two plain sentences what changed.`
  const lines = [
    option
      ? `My answer to this ${sourceName(input.source)} decision: "${option.title}".`
      : `My answer to this ${sourceName(input.source)} question is below.`,
    record,
    '',
    `Decision: ${input.question.trim()}`,
    '',
    option ? 'Note (optional): ' : 'My answer: ',
  ]
  return lines.join('\n')
}

/** Opens the automation chat with the answer pre-filled for the user to send. */
/**
 * Sends the chosen option as the answer: clicking an option is already an
 * explicit choice, so it goes straight to the automation chat (queued behind
 * a running turn), like "Take best action". Use "Ask in chat" to add a note.
 */
export async function sendReportHumanInputAnswerToChat({
  input,
  workspacePath,
  option,
}: {
  input: ReportHumanInput
  workspacePath: string
  option: { id: string; title: string }
}): Promise<WorkspacePaneChatResult> {
  return sendWorkspacePaneMessageToChat({
    workspacePath,
    message: buildReportHumanInputAnswerMessage(input, workspacePath, option),
  })
}

export async function openReportHumanInputAnswerInChat({
  input,
  workspacePath,
  option,
}: {
  input: ReportHumanInput
  workspacePath: string
  option?: { id: string; title: string }
}): Promise<{ tabId: string }> {
  return openWorkspacePaneChatWithDraft({
    workspacePath,
    message: buildReportHumanInputAnswerMessage(input, workspacePath, option),
  })
}
