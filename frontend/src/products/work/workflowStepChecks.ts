import type { RunPythonEvidence } from '../../services/api-types'

type CheckEvidence = Pick<RunPythonEvidence, 'kind' | 'step_execution_id' | 'ok'>

/** Only the server's actual step identity assigns checks. Old or unbound evidence stays in the full Checks list. */
export function stepChecks(items: CheckEvidence[]): Map<string, string> {
  const grouped = new Map<string, CheckEvidence[]>()
  for (const item of items) {
    if (item.kind !== 'check' || !item.step_execution_id) continue
    grouped.set(item.step_execution_id, [...(grouped.get(item.step_execution_id) ?? []), item])
  }
  return new Map([...grouped].map(([id, checks]) => {
    const failed = checks.filter(item => item.ok === false).length
    const noun = checks.length === 1 ? 'check' : 'checks'
    return [id, failed ? `${failed} of ${checks.length} ${noun} failed` : `${checks.length} ${noun} passed`]
  }))
}
