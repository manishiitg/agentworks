import type { ReportDataApi } from '../components/workflow/reportWidgets/reportEmbedContext'

// Record semantic data results, never credentials or signed media/file URLs.
// The same captured result is reused throughout frame seeking.
export function captureReportData(api: ReportDataApi, enabled: boolean) {
  let frozen = false
  const results = new Map<string, { method: string; args: unknown[]; value?: unknown; pending: Promise<unknown> }>()
  const recorded = { ...api }
  if (enabled) {
    for (const method of ['query', 'get', 'getText', 'getHtml', 'getRelayRuns', 'getCosts', 'run'] as const) {
      const original = api[method] as (...args: unknown[]) => Promise<unknown>
      Object.assign(recorded, { [method]: (...args: unknown[]) => {
        const key = JSON.stringify([method, args])
        const prior = results.get(key)
        if (prior) return prior.pending
        if (frozen) return Promise.reject(new Error('The export uses saved data. Load this data before creating the animation.'))
        const entry = { method, args, value: undefined as unknown, pending: Promise.resolve().then(() => original(...args)) }
        entry.pending = entry.pending.then(value => { entry.value = value; return value })
        results.set(key, entry)
        return entry.pending
      } })
    }
  }
  return {
    api: recorded,
    async freeze() {
      await Promise.all([...results.values()].map(entry => entry.pending))
      frozen = true
      return [...results.values()].map(({ method, args, value }) => ({ method, args, value }))
    },
  }
}
