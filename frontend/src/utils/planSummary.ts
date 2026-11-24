// A coding agent's plan, shortened for the one-line plan card (#3711). The full text opens in the plan pane.
export type PlanSummary = { title: string; steps: number; peek: string; body: string }

const PLAN_QUESTION = /^approve this plan\?\s*\n?/i

/** The plan text inside a plan approval question ("Approve this plan?\n<plan>"). */
export function planFromQuestion(question: string): string {
  return question.replace(PLAN_QUESTION, '').trim()
}

const clean = (line: string) => line.replace(/^#+\s*/, '').replace(/^\*\*(.+)\*\*$/, '$1').replace(/^plan:\s*/i, '').trim()

export function summarizePlan(text: string): PlanSummary {
  const body = text.trim()
  const lines = body.split('\n').map(line => line.trim()).filter(Boolean)
  const heading = lines.find(line => /^#{1,6}\s/.test(line))
  const titleLine = heading || lines[0] || ''
  const title = clean(titleLine) || 'Plan'
  const numbered = lines.filter(line => /^\d+[.)]\s/.test(line)).length
  const bullets = lines.filter(line => /^[-*]\s/.test(line)).length
  const peekLine = lines.find(line => line !== titleLine && !/^#{1,6}\s/.test(line)) || ''
  const peek = peekLine.replace(/^[-*]\s+|^\d+[.)]\s+/, '').replace(/\*\*/g, '').replace(/`/g, '')
  return { title, steps: numbered || bullets, peek, body }
}
