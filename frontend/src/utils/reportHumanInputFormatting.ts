import type {
  PulseImpactAssessment,
  PulseImpactLedger,
  PulseIntervention,
  ReportHumanInput,
} from '../services/api-types'

export type ReportHumanInputContextSection = {
  label: string
  body: string
  items: string[]
}

export function reportHumanInputStatusLabel(input: ReportHumanInput): string {
	if (input.status === 'answered') {
		if (input.selected_option_id === 'approve') return 'Approved · waiting for action'
		if (input.selected_option_id === 'reject') return 'Rejected · waiting for closure'
		if (input.selected_option_id === 'defer' || input.selected_option_id === 'wait') return 'Deferred'
		return 'Decision saved · waiting for action'
	}
	if (input.status === 'claimed') {
		return 'Decision action is running'
	}
  if (input.status === 'consumed') return 'Action completed'
  if (input.status === 'dismissed') return 'Dismissed'
  if (input.status === 'withdrawn') return 'Withdrawn'
  return 'Needs answer'
}

export function reportHumanInputHistory(inputs: ReportHumanInput[], limit = 4): ReportHumanInput[] {
  return inputs
    .filter(input => input.status !== 'pending')
    .slice(0, limit)
}

export type ReportHumanInputImpact = {
  intervention: PulseIntervention
  latestAssessment?: PulseImpactAssessment
}

// Decisions and impact share one durable join key. Keep this projection in the
// UI instead of copying impact state onto report_human_inputs, which would
// create two lifecycle authorities that can disagree.
export function reportHumanInputImpact(
  input: ReportHumanInput,
  ledger?: PulseImpactLedger,
): ReportHumanInputImpact | null {
  if (!ledger) return null
  const intervention = ledger.interventions.find(item => item.human_input_id === input.id)
  if (!intervention) return null
  return {
    intervention,
    // The API returns immutable assessment history newest first.
    latestAssessment: ledger.assessments.find(item => item.intervention_id === intervention.intervention_id),
  }
}

const CONTEXT_MARKER_PATTERN = /(?:^|\s)(Proposal|Strategy Auditor specialization|Goal Advisor specialization|Exact intended edits(?: if approved)?|Rationale|Expected impact|Risk|Evidence):\s*/gi

function displayLabel(value: string): string {
  const normalized = value.trim().toLowerCase()
  if (normalized.startsWith('exact intended edits')) return 'Intended edits'
  if (normalized === 'expected impact') return 'Expected impact'
  return normalized.charAt(0).toUpperCase() + normalized.slice(1)
}

function splitNumberedItems(value: string, forceList: boolean): { body: string; items: string[] } {
  const matches = Array.from(value.matchAll(/(?:^|\s)\((\d+)\)\s*/g))
  if (matches.length === 0 || (!forceList && matches.length < 2)) return { body: value.trim(), items: [] }

  const items = matches.map((match, index) => {
    const start = (match.index || 0) + match[0].length
    const end = index + 1 < matches.length ? matches[index + 1].index : value.length
    return value.slice(start, end).trim()
  }).filter(Boolean)

  return {
    body: value.slice(0, matches[0].index || 0).trim(),
    items,
  }
}

export function parseReportHumanInputContext(value: string): ReportHumanInputContextSection[] {
  const text = value.trim()
  if (!text) return []

  const matches = Array.from(text.matchAll(CONTEXT_MARKER_PATTERN))
  if (matches.length === 0) return [{ label: '', body: text, items: [] }]

  const sections: ReportHumanInputContextSection[] = []
  const prefix = text.slice(0, matches[0].index || 0).trim()
  if (prefix) sections.push({ label: '', body: prefix, items: [] })

  matches.forEach((match, index) => {
    const label = displayLabel(match[1])
    const start = (match.index || 0) + match[0].length
    const end = index + 1 < matches.length ? matches[index + 1].index : text.length
    const content = text.slice(start, end).trim()
    const structured = splitNumberedItems(content, label === 'Intended edits')
    if (structured.body || structured.items.length > 0) {
      sections.push({ label, ...structured })
    }
  })

  return sections.length > 0 ? sections : [{ label: '', body: text, items: [] }]
}
