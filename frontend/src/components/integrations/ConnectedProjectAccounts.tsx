import { useEffect, useState } from 'react'
import { ChevronRight, Grid2X2, Slack } from 'lucide-react'
import api, { agentApi, getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'

type Account = { id: string; label: string; status: string; target: 'gmail' | 'slack' }
type Snapshot = { scope: string; rows: Account[]; errors: string[] }

/** Read-only account summary. Every manage action opens the original scoped panel. */
export function ConnectedProjectAccounts({ workspacePath, options, onOpen }: {
  workspacePath: string
  options: readonly { value: string }[]
  onOpen: (target: string) => void
}) {
  const account = useAuthStore(state => state.user?.id)
  const google = options.some(item => item.value === 'gmail')
  const slack = options.some(item => item.value === 'slack')
  const scope = JSON.stringify([getApiBaseUrl(), account, workspacePath, google, slack])
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()
    void Promise.allSettled([
      google ? agentApi.listGmailConnections(workspacePath) : Promise.resolve(null),
      slack ? api.get<{ accounts: { id: string; owner: string; label: string; selected: boolean; connected: boolean }[] }>('/api/integrations/slack/accounts', { params: { workspace_path: workspacePath }, signal: controller.signal }) : Promise.resolve(null),
    ]).then(([gmail, messages]) => {
      if (cancelled) return
      const rows: Account[] = []
      const errors: string[] = []
      if (gmail.status === 'fulfilled') rows.push(...(gmail.value?.connections ?? []).map(item => ({ id: `google:${item.id}`, label: item.display_name || item.email || 'Google account', status: !item.enabled ? 'Paused' : item.auth?.checking ? 'Checking sign-in…' : item.ready ? 'Connected' : 'Needs sign-in', target: 'gmail' as const })))
      else errors.push('Google apps')
      if (messages.status === 'fulfilled') rows.push(...(messages.value?.data.accounts ?? []).filter(item => item.selected).map(item => ({ id: `slack:${item.owner}:${item.id}`, label: item.label || 'Slack account', status: item.connected ? 'Connected' : 'Needs sign-in', target: 'slack' as const })))
      else errors.push('Slack')
      setSnapshot({ scope, rows, errors })
    })
    return () => { cancelled = true; controller.abort() }
  }, [scope, workspacePath, google, slack, attempt])
  useEffect(() => {
    const refresh = () => setAttempt(value => value + 1)
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [])
  if (!google && !slack) return null
  const data = snapshot?.scope === scope ? snapshot : null
  return <section aria-label="Connected accounts" className="mb-4 space-y-2 border-b border-border pb-4 text-xs">
    <h3 className="font-medium">Accounts used here</h3>
    {!data ? <p className="text-muted-foreground">Loading accounts…</p> : <>
      {data.rows.map(item => {
        const Icon = item.target === 'gmail' ? Grid2X2 : Slack
        return <button key={item.id} type="button" onClick={() => onOpen(item.target)} className="flex w-full items-center gap-2 rounded-md border border-border p-3 text-left hover:bg-muted/40">
          <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" /><span className="min-w-0 flex-1"><span className="block truncate font-medium">{item.label}</span><span className="text-[11px] text-muted-foreground">{item.target === 'gmail' ? 'Google apps' : 'Slack'} · {item.status}</span></span><ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        </button>
      })}
      {!data.rows.length && !data.errors.length && <p className="text-muted-foreground">No accounts added here.</p>}
      {!!data.errors.length && <p role="status" className="text-muted-foreground">Could not load {data.errors.join(' and ')}. <button type="button" className="underline" onClick={() => setAttempt(value => value + 1)}>Retry</button></p>}
    </>}
  </section>
}
