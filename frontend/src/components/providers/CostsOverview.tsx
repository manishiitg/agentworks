import { useCallback, useEffect, useMemo, useState } from 'react'
import { CircleAlert, Loader2, RefreshCw } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { CostAggregate, CostOverview, CostOverviewItem } from '../../services/api-types'
import { formatTokens } from '../workflow/costs/helpers'
import { costAgentLabel } from '../workflow/costs/CostsModelSection'
import type { WorkSession } from '../../products/work/workSessions'
import CostExplorer from './CostExplorer'

const RANGES = [
  { days: 7, label: '7 days' },
  { days: 30, label: '30 days' },
  { days: 90, label: '90 days' },
] as const

const overviewCurrency = (amount: number) => {
  if (amount > 0 && amount < 0.0001) return '<$0.0001'
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: amount > 0 && amount < 0.01 ? 4 : 2,
    maximumFractionDigits: amount > 0 && amount < 0.01 ? 4 : 2,
  }).format(amount)
}

const costLabel = (usage: Pick<CostAggregate, 'total_cost_usd' | 'call_count' | 'unpriced_call_count'>) =>
  usage.total_cost_usd === 0 && (usage.unpriced_call_count ?? 0) > 0
    ? 'Unknown'
    : overviewCurrency(usage.total_cost_usd)

const unpricedLabel = (count?: number) =>
  count ? `${count.toLocaleString()} unpriced ${count === 1 ? 'call' : 'calls'}` : ''

const utcDate = (date: Date) => date.toISOString().slice(0, 10)

const costRangeBounds = (days: number, now = new Date()) => {
  const from = new Date(now)
  from.setUTCDate(from.getUTCDate() - (days - 1))
  return { from: utcDate(from), to: utcDate(now) }
}

const tokenCount = (aggregate?: CostAggregate) =>
  (aggregate?.prompt_tokens ?? 0) + (aggregate?.completion_tokens ?? 0) + (aggregate?.cache_read_tokens ?? 0) + (aggregate?.cache_write_tokens ?? 0)

// Crew rows arrive keyed by project folder; the Crew list supplies names.
const crewProjectId = (workspacePath: string) => {
  const marker = 'Chats/Work/projects/'
  const index = workspacePath.indexOf(marker)
  return index >= 0 ? workspacePath.slice(index + marker.length).split('/')[0] : ''
}

export default function CostsOverview() {
  const [days, setDays] = useState<number>(30)
  const [data, setData] = useState<CostOverview | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [crews, setCrews] = useState<WorkSession[]>([])
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    const { from, to } = costRangeBounds(days)
    setLoading(true)
    setError(null)
    agentApi.getCostOverview(from, to, controller.signal)
      .then(result => {
        // An older server without this endpoint answers with the app's HTML
        // fallback rather than a 404, so check the shape before using it.
        if (!result || typeof result !== 'object' || !Array.isArray(result.items)) {
          setData(null)
          setError('The server did not return cost data. Restart the AgentWorks server to enable this view.')
          return
        }
        setData(result)
      })
      .catch(err => {
        if (controller.signal.aborted) return
        setError(err instanceof Error ? err.message : 'Could not load costs')
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [days, reloadKey])

  useEffect(() => {
    let cancelled = false
    // Loaded lazily: a static import of the Crew product module from the
    // app shell forms an import cycle.
    void import('../../products/work/workSessions')
      .then(module => module.loadWorkSessionsIncludingShared())
      .then(list => { if (!cancelled) setCrews(list) })
      .catch(() => { if (!cancelled) setCrews([]) })
    return () => { cancelled = true }
  }, [])

  const crewByProject = useMemo(() => {
    const map = new Map<string, WorkSession>()
    for (const crew of crews) {
      map.set(crewProjectId(crew.workspacePath) || crew.id, crew)
    }
    return map
  }, [crews])

  const itemLabel = useCallback((item: CostOverviewItem) => {
    if (item.kind !== 'crew') return item.name
    const crew = crewByProject.get(item.name)
    return crew?.identity?.name?.trim() || crew?.title || item.name
  }, [crewByProject])

  const providerRows = useMemo(() => Object.entries(data?.by_provider || {})
    .map(([provider, usage]) => ({ provider, label: costAgentLabel(provider === 'unknown' ? '' : provider, ''), usage }))
    .filter(row => row.usage.total_cost_usd > 0 || row.usage.call_count > 0)
    .sort((a, b) => b.usage.total_cost_usd - a.usage.total_cost_usd), [data])

  const total = data?.total
  const maxProviderCost = Math.max(...providerRows.map(row => row.usage.total_cost_usd), 0)
  const unpricedCalls = total?.unpriced_call_count ?? 0
  const costSources = [
    (total?.provider_actual_cost_usd ?? 0) > 0 ? `${overviewCurrency(total?.provider_actual_cost_usd ?? 0)} provider-reported` : '',
    (total?.subscription_shadow_cost_usd ?? 0) > 0 ? `${overviewCurrency(total?.subscription_shadow_cost_usd ?? 0)} subscription-equivalent estimate` : '',
    (total?.token_estimate_cost_usd ?? 0) > 0 ? `${overviewCurrency(total?.token_estimate_cost_usd ?? 0)} token estimate` : '',
  ].filter(Boolean)

  return (
    <div className="mx-auto max-w-6xl">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold text-gray-950 dark:text-white">Costs</h2>
          <p className="mt-1 max-w-2xl text-sm leading-6 text-gray-600 dark:text-gray-300">
            Recorded AI usage by user, workflow, Crew, project, and bot for work you can open{data?.includes_other ? ', plus unattributed activity' : ''}.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <div role="group" aria-label="Date range" className="inline-flex rounded-lg border border-gray-200 p-0.5 dark:border-gray-700">
            {RANGES.map(range => (
              <button
                type="button"
                key={range.days}
                onClick={() => setDays(range.days)}
                aria-pressed={days === range.days}
                className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                  days === range.days
                    ? 'bg-violet-50 text-violet-700 dark:bg-violet-500/20 dark:text-violet-200'
                    : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
                }`}
              >
                {range.label}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => setReloadKey(key => key + 1)}
            disabled={loading}
            aria-label="Refresh costs"
            className="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100"
          >
            <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      {error && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
          <CircleAlert className="mt-0.5 h-4 w-4 shrink-0" />
          <span className="flex-1">{error}</span>
        </div>
      )}

      {loading && !data ? (
        <div className="flex items-center gap-2 py-10 text-sm text-gray-500">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading costs…
        </div>
      ) : data && (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            {[
              { label: 'Tracked cost', value: overviewCurrency(total?.total_cost_usd ?? 0) },
              { label: 'Unpriced LLM calls', value: unpricedCalls.toLocaleString() },
              { label: 'LLM calls', value: (total?.call_count ?? 0).toLocaleString() },
              { label: 'Tokens', value: formatTokens(tokenCount(total)) },
            ].map(card => (
              <div key={card.label} className="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-700 dark:bg-gray-900">
                <div className="text-xs text-gray-500 dark:text-gray-400">{card.label}</div>
                <div className="mt-1 text-lg font-semibold tabular-nums text-gray-950 dark:text-white">{card.value}</div>
              </div>
            ))}
          </div>
          <div className="mt-3 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm leading-5 text-gray-700 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300">
            <span className="font-medium text-gray-900 dark:text-gray-100">How to read this: </span>
            {costSources.length > 0 ? costSources.join(' · ') : (total?.total_cost_usd ?? 0) > 0 ? 'Recorded cost source unavailable' : 'No priced usage recorded'}.
            {unpricedCalls > 0 && ` ${unpricedLabel(unpricedCalls)} have unknown cost and are excluded from the tracked amount.`}
            {(total?.subscription_shadow_cost_usd ?? 0) > 0 && ' Subscription-equivalent estimates are not your subscription bill.'}
          </div>

          <CostExplorer data={data} days={days} itemLabel={itemLabel} />

          {providerRows.length > 0 && (
            <details className="mt-6 rounded-lg border border-gray-200 px-3 py-2 dark:border-gray-700">
              <summary className="cursor-pointer text-sm font-semibold text-gray-900 dark:text-gray-100">Coding agent breakdown</summary>
              <div className="mt-3 space-y-2">
                {providerRows.map(row => (
                  <div key={row.provider} className="grid grid-cols-[8rem_minmax(0,1fr)_5.5rem] items-center gap-3 text-sm">
                    <span className="truncate text-gray-700 dark:text-gray-200">{row.label}</span>
                    <div className="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                      {row.usage.total_cost_usd > 0 && <div
                        className="h-full rounded-full bg-violet-500 dark:bg-violet-400"
                        style={{ width: `${maxProviderCost > 0 ? Math.max(2, (row.usage.total_cost_usd / maxProviderCost) * 100) : 0}%` }}
                      />}
                    </div>
                    <span className="text-right tabular-nums text-gray-900 dark:text-gray-100" title={unpricedLabel(row.usage.unpriced_call_count)}>
                      {costLabel(row.usage)}
                      {(row.usage.unpriced_call_count ?? 0) > 0 && <span className="block text-[11px] text-gray-500 dark:text-gray-400">{unpricedLabel(row.usage.unpriced_call_count)}</span>}
                    </span>
                  </div>
                ))}
              </div>
            </details>
          )}
        </>
      )}
    </div>
  )
}
