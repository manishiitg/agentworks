import { useEffect, useState } from 'react'
import { Terminal } from 'lucide-react'
import { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { Button } from '../../components/ui/Button'

type ConnectedAgent = { kind: string; provider?: string; online?: boolean; shared?: boolean }
const apps = [
  { name: 'Claude Code', providers: ['claude', 'claude-code'] },
  { name: 'Codex', providers: ['codex', 'codex-cli'] },
  { name: 'Cursor', providers: ['cursor', 'cursor-cli'] },
]

/** Connection state comes from the selected server's existing authorized agent directory. */
export function CrewCodingApps({ connection, crewName }: { connection: string; crewName: string }) {
  const [snapshot, setSnapshot] = useState<{ connection: string; agents: ConnectedAgent[] }>()
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    const controller = new AbortController()
    setError('')
    const load = async () => {
      try {
        const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/agent-directory`, {
          headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: controller.signal,
        })
        if (!response.ok) throw new Error('Could not check coding app connections.')
        const data: { agents?: ConnectedAgent[] } = await response.json()
        if (!controller.signal.aborted) {
          setSnapshot({ connection, agents: (data.agents || []).filter(agent => agent.kind === 'connected_agent' && !agent.shared) })
          setError('')
        }
      } catch {
        if (!controller.signal.aborted) setError('Could not check coding app connections. Try again when the server is available.')
      }
    }
    void load()
    const onFocus = () => { void load() }
    window.addEventListener('focus', onFocus)
    return () => { controller.abort(); window.removeEventListener('focus', onFocus) }
  }, [connection])
  const agents = snapshot?.connection === connection ? snapshot.agents : undefined
  const line = `Connect to AgentWorks, then ask my ${JSON.stringify(crewName)} Crew's agent to help me.`
  const copy = async () => {
    try { await navigator.clipboard.writeText(line); setCopied(true) }
    catch { setError('Copy this setup line into your coding app: ' + line) }
  }
  return <section aria-label="Your coding apps" className="space-y-2 rounded-xl border border-border p-3">
    <div className="flex flex-wrap items-center gap-2"><Terminal aria-hidden="true" className="h-4 w-4" /><p className="font-medium">Your coding apps</p><Button className="ml-auto rounded-full" size="sm" variant="outline" onClick={() => void copy()}>{copied ? 'Copied' : 'Copy setup line'}</Button></div>
    <p className="text-xs text-muted-foreground">In Claude Code, Codex or Cursor, say “connect to AgentWorks”, then ask your Crew’s agent for help. Replies come back in that chat.</p>
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs">{apps.map(app => {
      const connected = agents?.some(agent => app.providers.includes(agent.provider || '') && agent.online === true)
      return <span key={app.name}>{app.name} <span className={connected && !error ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}>{error ? '· unknown' : !agents ? '· checking…' : connected ? '· connected' : '· not connected'}</span></span>
    })}</div>
    {error && <p role="status" className="text-xs text-muted-foreground">{error}</p>}
  </section>
}
