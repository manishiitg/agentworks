import { useCallback, useEffect, useState } from 'react'
import type { AccountLimitsView } from '../../services/llm-config-api'
import type { CostOverview } from '../../services/api-types'
import { getApiBaseUrl, getAuthToken } from '../../services/api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

const PROGRAMS: Record<string, string> = {
  'claude-code': 'Claude Code', 'codex-cli': 'Codex', 'cursor-cli': 'Cursor',
  'pi-cli': 'Pi', 'muse-cli': 'Muse', 'agy-cli': 'Antigravity',
}
const programName = (id: string) => PROGRAMS[id] || id

/** GET from one place (this server, or one connection through the Mac). The place's server decides what the caller sees. */
export async function placeGet<T>(base: string, path: string): Promise<T> {
  const response = await fetch(`${base}${path}`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: AbortSignal.timeout(20_000) })
  if (!response.ok) throw Object.assign(new Error('unavailable'), { status: response.status })
  return response.json() as Promise<T>
}

type PersonUsage = { id: string; name: string; today: number; week: number }
const day = (offset: number) => { const d = new Date(); d.setUTCDate(d.getUTCDate() - offset); return d.toISOString().slice(0, 10) }
const money = (n: number) => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(n)

/** Usage by person: the same cost answer as the place's own usage page. A person who may not see it gets nothing. */
async function loadPeople(base: string): Promise<PersonUsage[]> {
  const path = (from: string) => `/api/cost/overview?from=${from}&to=${day(0)}`
  const [today, week] = await Promise.all([placeGet<CostOverview>(base, path(day(0))), placeGet<CostOverview>(base, path(day(6)))])
  const people = new Map<string, PersonUsage>()
  for (const user of week.by_user ?? []) people.set(user.id, { id: user.id, name: user.name || user.id, today: 0, week: user.total_cost_usd })
  for (const user of today.by_user ?? []) {
    const row = people.get(user.id) ?? { id: user.id, name: user.name || user.id, today: 0, week: 0 }
    row.today = user.total_cost_usd
    people.set(user.id, row)
  }
  return [...people.values()].sort((a, b) => b.week - a.week)
}

/**
 * Accounts and usage for one place. base is that place's API address (default: the current server);
 * macOnly keeps only the Mac's own logins; signInHint replaces the Sign in again button when the place is not the active one.
 */
export default function AccountLimits({ base, placeName, macOnly, signInHint, onOpenProvider }: {
  base?: string; placeName?: string; macOnly?: boolean; signInHint?: string; onOpenProvider?: (provider: string) => void
} = {}) {
  const [accounts, setAccounts] = useState<AccountLimitsView[]>([])
  const [people, setPeople] = useState<PersonUsage[] | null>(null)
  const [error, setError] = useState(false)
  const [loading, setLoading] = useState(true)
  const refresh = useCallback(() => {
    const from = base ?? getApiBaseUrl()
    void placeGet<{ accounts: AccountLimitsView[] }>(from, '/api/provider-accounts/limits').then(data => {
      setAccounts((data.accounts ?? []).filter(account => !macOnly || !account.from_server))
      setError(false)
    }).catch(() => setError(true)).finally(() => setLoading(false))
    void loadPeople(from).then(setPeople).catch(() => setPeople(null))
  }, [base, macOnly])
  useEffect(refresh, [refresh])
  useLiveRefetch(refresh, { kinds: ['account_limits'], fallbackMs: 60_000, minIntervalMs: 1000, refreshOnFocus: true })
  const programs = [...new Set(accounts.map(account => account.provider))]
  const where = placeName ? ` on ${placeName}` : ''
  return <section className="space-y-4 text-foreground">
    <div>
      <h2 className="text-xl font-semibold">Accounts and usage{where}</h2>
      <p className="text-sm text-muted-foreground">Which AI accounts work, which need a new sign-in, which models they allow, and plan usage they report (separate from chat token totals). Reset times use your local time.</p>
    </div>
    {error && <p role="alert">{placeName ? `${placeName} isn't answering right now.` : 'Could not refresh limits.'} <button type="button" onClick={refresh} className="underline">Try again</button></p>}
    {loading && <p role="status">Loading accounts…</p>}
    {!loading && !error && accounts.length === 0 && <p>No accounts here.</p>}
    {programs.map(program => <div key={program} className="space-y-3">
      <h3 className="text-sm font-semibold">{programName(program)}</h3>
      {accounts.filter(account => account.provider === program).map(account => <article key={`${account.provider}:${account.account_id}`} className="rounded-xl border border-border p-4">
        <div className="flex items-start justify-between gap-3">
          <h4 className="font-medium">{account.name || programName(account.provider)}</h4>
          <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${account.sign_in?.needed ? 'bg-destructive/10 text-destructive' : account.status === 'Ready' ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-muted text-muted-foreground'}`}>{account.status}</span>
        </div>
        <p className="text-sm text-muted-foreground">Login: {account.owner}</p>
        <p className="mt-1 text-xs text-muted-foreground">Models: {account.allowed_models?.length ? account.allowed_models.join(', ') : 'all models'}</p>
        {account.sign_in?.needed && <div role="alert" className="mt-3 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
          <p>{account.sign_in.reason}{account.sign_in.since && ` Turns on this account have failed since ${new Date(account.sign_in.since).toLocaleString()}.`}</p>
          {signInHint ? <p className="mt-2">{signInHint}</p>
            : onOpenProvider && <button type="button" onClick={() => onOpenProvider(account.provider)} className="mt-2 font-medium underline">Sign in again</button>}
        </div>}
        {!account.snapshot?.windows.length && <p className="mt-3 text-sm">Usage not reported. This does not mean zero usage.</p>}
        {account.snapshot?.windows.map((window, index) => <div key={`${window.label}:${index}`} className="mt-3">
          <div className="flex justify-between gap-4 text-sm"><span>{window.label}</span><span>{Math.round(window.used_percent)}% used</span></div>
          <progress aria-label={window.label} value={window.used_percent} max={100} className="h-2 w-full accent-violet-500" />
          <p className="text-xs text-muted-foreground">{window.resets_at ? `Resets ${new Date(window.resets_at).toLocaleString()}` : 'Reset time not reported'}</p>
        </div>)}
        {account.snapshot && <p className="mt-3 text-xs text-muted-foreground">Last checked {new Date(account.snapshot.checked_at).toLocaleString()}</p>}
      </article>)}
    </div>)}
    {!macOnly && people && people.length > 0 && <div className="space-y-2">
      <h3 className="text-sm font-semibold">Usage by person{where}</h3>
      <div className="overflow-x-auto rounded-xl border border-border">
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-muted-foreground"><tr><th className="px-3 py-2 font-medium">Person</th><th className="px-3 py-2 font-medium">Today</th><th className="px-3 py-2 font-medium">Last 7 days</th></tr></thead>
          <tbody>{people.map(person => <tr key={person.id} className="border-t border-border"><td className="px-3 py-2">{person.name}</td><td className="px-3 py-2">{money(person.today)}</td><td className="px-3 py-2">{money(person.week)}</td></tr>)}</tbody>
        </table>
      </div>
    </div>}
  </section>
}
