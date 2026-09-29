import { useEffect, useState } from 'react'
import { ChevronRight, CircleAlert, Loader2 } from 'lucide-react'
import type { CostAggregate, ProviderAccountCost, ProviderCostGroup } from '../../services/api-types'
import { llmConfigService } from '../../services/llm-config-api'
import { formatTokens } from '../workflow/costs/helpers'
import { costAgentLabel } from '../workflow/costs/CostsModelSection'

const currency = (amount: number) => {
  if (amount > 0 && amount < 0.0001) return '<$0.0001'
  return new Intl.NumberFormat('en-US', {
    style: 'currency', currency: 'USD',
    minimumFractionDigits: amount > 0 && amount < 0.01 ? 4 : 2,
    maximumFractionDigits: amount > 0 && amount < 0.01 ? 4 : 2,
  }).format(amount)
}

const costText = (usage: CostAggregate) =>
  usage.total_cost_usd === 0 && (usage.unpriced_call_count ?? 0) > 0 ? 'Not priced' : currency(usage.total_cost_usd ?? 0)

const tokenText = (usage: CostAggregate) =>
  formatTokens((usage.prompt_tokens ?? 0) + (usage.completion_tokens ?? 0) + (usage.cache_read_tokens ?? 0) + (usage.cache_write_tokens ?? 0))

const WORK_KIND: Record<string, string> = { workflow: 'Workflow', crew: 'Crew', code: 'Code', product: 'Project', chat: 'Chat', other: 'Other' }

const accountKindText = (account: ProviderAccountCost) =>
  account.kind === 'server' ? 'Server account' : account.kind === 'unrecorded' ? 'Account not recorded' : account.owner_name ? `Owner: ${account.owner_name}` : 'User account'

function AccountRow({ account }: { account: ProviderAccountCost }) {
  const [open, setOpen] = useState(false)
  const split = account.split ?? []
  return (
    <li className="py-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <button type="button" disabled={split.length === 0} aria-expanded={open} aria-label={`Show where ${account.name} was used`} onClick={() => setOpen(value => !value)} className="inline-flex min-w-0 flex-1 items-center gap-1.5 text-left disabled:cursor-default">
          <ChevronRight className={`h-3.5 w-3.5 shrink-0 text-gray-400 transition-transform ${open ? 'rotate-90' : ''} ${split.length === 0 ? 'invisible' : ''}`} />
          <span className="min-w-0 break-words font-medium text-gray-900 dark:text-gray-100">{account.name}</span>
          <span className="text-xs text-gray-500 dark:text-gray-400">{accountKindText(account)}</span>
          {!account.full_split && <span className="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600 dark:bg-gray-800 dark:text-gray-300">Your share only</span>}
        </button>
        <span className="w-20 text-right text-xs tabular-nums text-gray-500 dark:text-gray-400">{tokenText(account.total)} tokens</span>
        <span className="w-20 text-right tabular-nums text-gray-900 dark:text-gray-100">{costText(account.total)}</span>
      </div>
      {open && split.length > 0 && (
        <table className="mt-2 w-full text-xs">
          <thead><tr className="text-left text-gray-500 dark:text-gray-400"><th className="py-1 pl-5 font-medium">Where</th><th className="py-1 font-medium">Person</th><th className="py-1 text-right font-medium">Tokens</th><th className="py-1 text-right font-medium">Cost</th></tr></thead>
          <tbody>
            {split.map(row => (
              <tr key={`${row.work_id}/${row.user_id}`} className="border-t border-gray-100 text-gray-700 dark:border-gray-800 dark:text-gray-300">
                <td className="py-1 pl-5">{row.work_name || row.work_id || 'Unattributed'}<span className="text-gray-400"> · {WORK_KIND[row.work_kind] || row.work_kind || 'Other'}</span></td>
                <td className="py-1">{row.user_name || row.user_id || 'Unknown'}</td>
                <td className="py-1 text-right tabular-nums">{tokenText(row)}</td>
                <td className="py-1 text-right tabular-nums">{costText(row)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </li>
  )
}

/** Cost and tokens per provider and per account, with a split by work and person. */
export function AccountCostList({ providers, showProviderHeading = true }: { providers: ProviderCostGroup[]; showProviderHeading?: boolean }) {
  if (providers.length === 0) return <p className="text-sm text-gray-500 dark:text-gray-400">No recorded usage in this range.</p>
  return (
    <div className="space-y-4">
      {providers.map(group => (
        <section key={group.provider}>
          {showProviderHeading && (
            <div className="mb-1 flex items-center justify-between gap-3 text-sm">
              <h4 className="font-semibold text-gray-900 dark:text-gray-100">{costAgentLabel(group.provider === 'unknown' ? '' : group.provider, '') || group.provider}</h4>
              <span className="tabular-nums font-medium text-gray-900 dark:text-gray-100">{costText(group.total)}</span>
            </div>
          )}
          <ul className="divide-y divide-gray-100 rounded-lg border border-gray-200 px-3 dark:divide-gray-800 dark:border-gray-700">
            {(group.accounts ?? []).map(account => <AccountRow key={account.account_id || account.name} account={account} />)}
          </ul>
        </section>
      ))}
    </div>
  )
}

const isoDay = (date: Date) => date.toISOString().slice(0, 10)
export const lastDaysRange = (days: number, now = new Date()) => {
  const from = new Date(now)
  from.setUTCDate(from.getUTCDate() - (days - 1))
  return { from: isoDay(from), to: isoDay(now) }
}

/** Providers page: cost by account for one provider (or all), over a date range. */
export default function ProviderAccountCostsSection({ provider }: { provider?: string }) {
  const [range, setRange] = useState(() => lastDaysRange(30))
  const [providers, setProviders] = useState<ProviderCostGroup[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    if (!range.from || !range.to) return
    const controller = new AbortController()
    setLoading(true); setError(null)
    llmConfigService.getProviderAccountCosts(range.from, range.to, controller.signal)
      .then(result => setProviders(result.providers.filter(group => !provider || group.provider === provider)))
      .catch(() => { if (!controller.signal.aborted) setError('Could not load costs.') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [range.from, range.to, provider])
  const total = providers?.[0]?.total
  const dateClass = 'rounded-md border border-gray-300 bg-white px-2 py-1 text-xs text-gray-900 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100'
  return (
    <section className="mb-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Cost by account</h3>
          {provider && total && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{costText(total)} and {tokenText(total)} tokens in this range</p>}
        </div>
        <div className="flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
          <label className="inline-flex items-center gap-1">From <input type="date" aria-label="From date" value={range.from} max={range.to} onChange={event => setRange(current => ({ ...current, from: event.target.value }))} className={dateClass} /></label>
          <label className="inline-flex items-center gap-1">To <input type="date" aria-label="To date" value={range.to} min={range.from} onChange={event => setRange(current => ({ ...current, to: event.target.value }))} className={dateClass} /></label>
        </div>
      </div>
      <div className="mt-3">
        {error && <p className="flex items-center gap-2 text-xs text-red-600 dark:text-red-400"><CircleAlert className="h-3.5 w-3.5" />{error}</p>}
        {loading && !providers && <p className="flex items-center gap-2 text-xs text-gray-500"><Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading costs…</p>}
        {providers && <AccountCostList providers={providers} showProviderHeading={!provider} />}
      </div>
    </section>
  )
}
