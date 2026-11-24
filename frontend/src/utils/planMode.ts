// The composer's Plan switch (#3699). The server owns which programs support plan mode.

export type PlanKey = { key: string; shiftKey: boolean; ctrlKey: boolean; metaKey: boolean; altKey: boolean; isComposing?: boolean }

/** Shift+Tab alone toggles Plan, like Claude Code's own terminal. */
export function isPlanToggleKey(event: PlanKey): boolean {
  return event.key === 'Tab' && event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey && !event.isComposing
}

/** Codex and Cursor answer a plan as a reply. Claude asks with its own plan card; when a planning turn ends
 * without one (the card never came, #3711), the same Start step keeps the chat from staying stuck in Plan. */
export function planNeedsStartButton(provider: string | null | undefined): boolean {
  return provider === 'codex-cli' || provider === 'cursor-cli' || provider === 'claude-code'
}

/** Any answer to Claude's plan card (Approve or Reject) means the card came, so no Start step is needed. */
export function answersPlanCard(answers: ReadonlyArray<{ id: string }>): boolean {
  return answers.some(answer => answer.id === 'plan')
}

export const PLAN_START_MESSAGE = 'Go ahead with the plan.'

/** The answer that approves Claude's plan card (its ExitPlanMode question, id "plan"). */
export function approvesPlan(answers: ReadonlyArray<{ id: string; selectedLabels: string[] }>): boolean {
  return answers.some(answer => answer.id === 'plan' && answer.selectedLabels.includes('Approve'))
}
