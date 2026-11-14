import { useCallback, useEffect, useState } from 'react'
import { llmConfigService, type AccountLimitsView } from '../../services/llm-config-api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

export default function AccountLimits() {
  const [accounts, setAccounts] = useState<AccountLimitsView[]>([])
  const [error, setError] = useState(false)
  const [loading, setLoading] = useState(true)
  const refresh = useCallback(() => {
    void llmConfigService.getAccountLimits().then(rows => {
      setAccounts(rows)
      setError(false)
    }).catch(() => setError(true)).finally(() => setLoading(false))
  }, [])
  useEffect(refresh, [refresh])
  useLiveRefetch(refresh, { kinds: ['account_limits'], fallbackMs: 60_000, minIntervalMs: 1000, refreshOnFocus: true })
  return <section className="space-y-4 text-gray-950 dark:text-gray-100">
    <h2 className="text-xl font-semibold">Subscription limits</h2>
    <p className="text-sm text-gray-500">Plan usage reported by your AI accounts, separate from chat token totals. Reset times use your local time.</p>
    {error && <p role="alert">Could not refresh limits. <button type="button" onClick={refresh} className="underline">Try again</button></p>}
    {loading && <p role="status">Loading account limits…</p>}
    {!loading && !error && accounts.length === 0 && <p>No accounts available.</p>}
    {accounts.map(account => <article key={`${account.provider}:${account.account_id}`} className="rounded-xl border border-gray-200 p-4 dark:border-gray-700">
      <h3 className="font-medium">{account.name || account.provider}</h3>
      <p className="text-sm text-gray-500">{account.provider} · {account.owner}</p>
      {!account.snapshot?.windows.length && <p className="mt-3 text-sm">Usage not reported. This does not mean zero usage.</p>}
      {account.snapshot?.windows.map((window, index) => <div key={`${window.label}:${index}`} className="mt-3">
        <div className="flex justify-between gap-4 text-sm"><span>{window.label}</span><span>{Math.round(window.used_percent)}% used</span></div>
        <progress aria-label={window.label} value={window.used_percent} max={100} className="h-2 w-full accent-violet-500" />
        <p className="text-xs text-gray-500">{window.resets_at ? `Resets ${new Date(window.resets_at).toLocaleString()}` : 'Reset time not reported'}</p>
      </div>)}
      {account.snapshot && <p className="mt-3 text-xs text-gray-500">Last checked {new Date(account.snapshot.checked_at).toLocaleString()}</p>}
    </article>)}
  </section>
}
