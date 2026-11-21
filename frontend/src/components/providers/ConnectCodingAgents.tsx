import { useCallback, useEffect, useState } from 'react'
import { CheckCircle2, Loader2 } from 'lucide-react'
import { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { Button } from '../ui/Button'

type CodingApp = { provider: 'claude' | 'codex'; name: string; installed: boolean; hooks: boolean }
type SetupResult = { provider: string; name: string; ok: boolean; lines: string[]; next: string }

// GitHub #2820: the Mac app's own backend runs `agentworks setup <provider> --hooks`; the browser only shows what it says.
async function localSetup<T>(method: 'GET' | 'POST', body?: unknown): Promise<T> {
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/coding-agent-setup`, {
    method, headers: { Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  }).catch(() => { throw new Error('Could not reach the AgentWorks app on this Mac. Check that it is running, then try again.') })
  const data = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(typeof data?.error?.message === 'string' ? data.error.message : typeof data?.error === 'string' ? data.error : 'Could not complete this. Update the AgentWorks app, then try again.')
  return data as T
}

/** Connect Claude Code / Codex on this Mac: the status line, live messages and Team join in their own chats. */
export default function ConnectCodingAgents({ compact = false }: { compact?: boolean }) {
  const [apps, setApps] = useState<CodingApp[]>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [done, setDone] = useState<SetupResult>()
  const inMacApp = hasDesktopConnections()
  const refresh = useCallback(async () => {
    setError('')
    try { setApps((await localSetup<{ apps: CodingApp[] }>('GET')).apps) } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not check Claude Code and Codex.') }
  }, [])
  useEffect(() => { if (inMacApp) void refresh() }, [inMacApp, refresh])
  if (!inMacApp) return null
  const connect = async (provider: string) => {
    setBusy(provider); setError(''); setDone(undefined)
    try { setDone(await localSetup<SetupResult>('POST', { provider })); void refresh() }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Setup did not finish. Try again.') } finally { setBusy('') }
  }
  const installed = apps?.filter(app => app.installed) ?? []
  return <section aria-label="Connect Claude Code / Codex" className={compact ? 'mt-2 rounded-md bg-muted/40 px-2.5 py-2 text-[12px]' : 'space-y-3 rounded-lg border border-border p-4'}>
    <div><h3 className={compact ? 'text-[12px] font-medium' : 'text-sm font-medium'}>Connect Claude Code / Codex</h3>
      <p className={'text-muted-foreground ' + (compact ? 'text-[11px]' : 'text-xs')}>Your own Claude Code or Codex chats then show AgentWorks status, get messages live and can join a Team. Running it again is safe.</p></div>
    {!apps && !error && <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />Checking this Mac…</p>}
    {apps && installed.length === 0 && <p className="text-xs text-muted-foreground">Neither Claude Code nor Codex is installed on this Mac. Install one, open it once, then come back.</p>}
    {installed.length > 0 && <div className="mt-1 flex flex-wrap gap-2">{installed.map(app => <Button key={app.provider} size="xs" variant="outline" disabled={Boolean(busy)} onClick={() => void connect(app.provider)}>
      {busy === app.provider ? <Loader2 className="h-3 w-3 animate-spin" /> : app.hooks && <CheckCircle2 className="h-3 w-3 text-green-600" />}
      {app.hooks ? `${app.name} connected · Connect again` : `Connect ${app.name}`}
    </Button>)}</div>}
    {done && <div role="status" className="mt-1 space-y-1 text-xs">
      <ul className="list-disc space-y-0.5 pl-4 text-muted-foreground">{done.lines.filter(line => !line.startsWith('To undo')).map(line => <li key={line}>{line}</li>)}</ul>
      <p className="font-medium">{done.next}</p>
    </div>}
    {error && <p role="alert" className="mt-1 text-xs text-destructive">{error}</p>}
  </section>
}
