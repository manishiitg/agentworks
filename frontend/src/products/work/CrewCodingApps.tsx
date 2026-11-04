import { useEffect, useState, type ReactNode } from 'react'
import { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { Button } from '../../components/ui/Button'

type ConnectedAgent = { kind: string; provider?: string; online?: boolean; shared?: boolean }
const apps = [
  { name: 'Claude Code', providers: ['claude', 'claude-code'] },
  { name: 'Codex', providers: ['codex', 'codex-cli'] },
  { name: 'Cursor', providers: ['cursor', 'cursor-cli'] },
]

/** Connection state comes from the selected server's existing authorized agent directory. */
export function CrewCodingApps({ connection, crewName, otherCrews }: { connection?: string; crewName: string; otherCrews?: ReactNode }) {
  const [snapshot, setSnapshot] = useState<{ connection: string; agents: ConnectedAgent[] }>()
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    const controller = new AbortController()
    setError('')
    if (!connection) return () => controller.abort()
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
  const agents = snapshot && snapshot.connection === connection ? snapshot.agents : undefined
  const line = `Connect to AgentWorks, then ask my ${JSON.stringify(crewName)} Crew's agent to help me.`
  const copy = async () => {
    try { await navigator.clipboard.writeText(line); setCopied(true) }
    catch { setError('Copy this setup line into your coding app: ' + line) }
  }
  return <div aria-label="Your coding apps" className="flex flex-wrap items-center gap-x-2.5 gap-y-2 border-t border-border py-2.5 pl-3 text-sm">
    <span className="inline-flex flex-wrap items-center gap-1 text-[12.5px] text-muted-foreground"><span title="In your coding app, say ‘connect to AgentWorks’, then ask your Crew’s agent for help. Replies come back in that chat.">Your agents can always message them:</span>{apps.map((app, index) => {
      const connected = agents?.some(agent => app.providers.includes(agent.provider || '') && agent.online === true)
      const status = error ? 'connection unknown' : !connection ? 'not connected' : !agents ? 'checking connection' : connected ? 'connected' : 'not connected'
      return <span key={app.name} title={`${app.name}: ${status}`} aria-label={`${app.name}: ${status}`}>{index > 0 && <span aria-hidden="true" className="mr-1">·</span>}{app.name}{connected && !error && <span aria-hidden="true" className="ml-1 text-emerald-500">●</span>}</span>
    })}{otherCrews}</span>
    <Button className="ml-auto h-7 rounded-full px-3" size="sm" variant="outline" disabled={!connection} onClick={() => void copy()}>{copied ? 'Copied' : 'Copy setup line'}</Button>
    {error && <p role="status" className="basis-full text-xs text-muted-foreground">{error}</p>}
  </div>
}
