export type PlaybookSetupProgress = {
  completed: string[]
  remaining: string[]
}

export function parsePlaybookSetupProgress(content: string, playbookId: string, version: string, expectedChecks: readonly string[]): PlaybookSetupProgress | null {
  try {
    const raw = JSON.parse(content) as {
      schema_version?: unknown
      playbook_id?: unknown
      playbook_version?: unknown
      checks?: Array<{ id?: unknown }>
      completed_steps?: unknown
      evidence?: Record<string, unknown>
    }
    if (raw.schema_version !== 1 || raw.playbook_id !== playbookId || raw.playbook_version !== version) return null
    if (!Array.isArray(raw.checks) || !Array.isArray(raw.completed_steps) || !raw.evidence || typeof raw.evidence !== 'object') return null
    const expected = new Set(expectedChecks)
    const available = new Set(raw.checks.map(check => check.id).filter((id): id is string => typeof id === 'string'))
    if (available.size !== expected.size || [...expected].some(id => !available.has(id))) return null
    const completed = [...new Set(raw.completed_steps)].filter((id): id is string =>
      typeof id === 'string' && expected.has(id) && typeof raw.evidence?.[id] === 'string' && Boolean((raw.evidence[id] as string).trim()))
    return { completed, remaining: expectedChecks.filter(id => !completed.includes(id)) }
  } catch {
    return null
  }
}
