import type { RunPythonEvidence } from '../../services/api-types'

type CheckEvidence = Pick<RunPythonEvidence, 'kind' | 'source_call' | 'ok'>

/** A shared helper may represent several steps. Count only unambiguous step-to-check joins. */
export function uniqueStepChecks(items: CheckEvidence[], sources: Array<string | undefined>): Map<string, string> {
  const occurrences = new Map<string, number>()
  for (const source of sources) if (source) occurrences.set(source, (occurrences.get(source) ?? 0) + 1)
  const lines = new Map<string, string>()
  for (const [source, count] of occurrences) {
    if (count !== 1) continue
    const checks = items.filter(item => item.kind === 'check' && item.source_call === source)
    if (!checks.length) continue
    const failed = checks.filter(item => item.ok === false).length
    const noun = checks.length === 1 ? 'check' : 'checks'
    lines.set(source, failed ? `${failed} of ${checks.length} ${noun} failed` : `${checks.length} ${noun} passed`)
  }
  return lines
}
