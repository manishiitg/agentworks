// The composer's Plan switch (#3699). The server owns which programs support plan mode.

export type PlanKey = { key: string; shiftKey: boolean; ctrlKey: boolean; metaKey: boolean; altKey: boolean; isComposing?: boolean }

/** Shift+Tab alone toggles Plan, like Claude Code's own terminal. */
export function isPlanToggleKey(event: PlanKey): boolean {
  return event.key === 'Tab' && event.shiftKey && !event.ctrlKey && !event.metaKey && !event.altKey && !event.isComposing
}

/** Codex and Cursor answer a plan as a reply; Claude asks with its own approval card. */
export function planNeedsStartButton(provider: string | null | undefined): boolean {
  return provider === 'codex-cli' || provider === 'cursor-cli'
}

export const PLAN_START_MESSAGE = 'Go ahead with the plan.'

/** The answer that approves Claude's plan card (its ExitPlanMode question, id "plan"). */
export function approvesPlan(answers: ReadonlyArray<{ id: string; selectedLabels: string[] }>): boolean {
  return answers.some(answer => answer.id === 'plan' && answer.selectedLabels.includes('Approve'))
}
