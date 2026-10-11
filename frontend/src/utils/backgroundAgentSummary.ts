export function backgroundAgentCompletionSummary(result?: string): string {
  const trimmed = result?.trim()
  if (!trimmed) return ''

  const withoutWrapper = trimmed
    .replace(/^sub-agent\s+.+?\s+completed:\s*/i, '')
    .trim()

  const sequenceMatch = withoutWrapper.match(/^agent\s+.+?\s+completed:\s*(\d+)\s+item(?:\(s\)|s)?\s+completed\.?$/i)
  if (sequenceMatch) {
    const count = Number(sequenceMatch[1])
    return `Finished ${count} task${count === 1 ? '' : 's'}.`
  }

  if (/^status:\s*completed\.?$/i.test(withoutWrapper)) return 'Finished successfully.'
  return withoutWrapper
}
