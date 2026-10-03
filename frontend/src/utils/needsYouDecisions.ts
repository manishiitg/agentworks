import type { ReportHumanInput } from '../services/api-types'

// Decisions that need the person: every unanswered one, plus answered ones that
// are not applied yet (runs never apply decisions; they wait for "Apply in chat").
export function needsYouDecisions(pending: ReportHumanInput[], answered: ReportHumanInput[]): ReportHumanInput[] {
  return [...pending, ...answered.filter(input => input.status === 'answered' && Boolean(input.apply_message))]
}
