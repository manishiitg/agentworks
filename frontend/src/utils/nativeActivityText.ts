// Presentation only. Native observations never decide whether a run succeeds.
export function nativeActivityText(record: Record<string, unknown> | undefined): string | null {
  const labels: Record<string, string> = { helper: 'Coding agent helper', background_command: 'Background command', retry: 'Coding agent', error: 'Coding agent', notice: 'Coding agent' }
  const kind = typeof record?.kind === 'string' ? record.kind : ''
  // Existing question cards and compaction events own those UI rows.
  if (!labels[kind]) return null
  const states: Record<string, string> = { started: 'started', proposed: 'queued', scheduled: 'queued', queued: 'queued', finished: 'finished', completed: 'finished', stopped: 'stopped', interrupted: 'stopped', failed: 'failed', requested: 'needs your answer', helpers_stopped: 'helpers stopped', retrying: 'retrying', waiting: 'waiting' }
  const state = typeof record?.state === 'string' ? record.state : ''
  return `${labels[kind]} · ${states[state] || 'updated'}`
}
