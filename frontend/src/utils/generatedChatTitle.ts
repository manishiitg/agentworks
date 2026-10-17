/** Human-readable display copy for known machine prompts; never changes saved titles. */
export function readableGeneratedChatTitle(title: string, scheduled = false): string | null {
  const text = title.trim()
  if (/^WORKFLOW REVIEW BEFORE A RUN\.\s+workspace_path\s*=/i.test(text)) return 'Scheduled review'
  if (/^FULL.RUN COMPLETION\b/i.test(text)) return 'Run follow-up'
  if (/^(?:\[AGENTWORKS\b|PULSE RUN CONTEXT\b|SCHEDULED (?:RUN|WORKFLOW)\b)/i.test(text))
    return scheduled ? 'Scheduled run' : 'Automatic review'
  return null
}
