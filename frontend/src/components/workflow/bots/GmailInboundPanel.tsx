import { useCallback, useEffect, useRef, useState } from 'react'
import { agentApi } from '../../../services/api'
import type { GmailConnection, GmailInboundState } from '../../../services/api-types'
import { Button } from '../../ui/Button'
import { FormSection } from '../../ui/FormSection'

function errorMessage(error: unknown): string {
  const response = (error as { response?: { data?: unknown } })?.response?.data
  return typeof response === 'string' ? response : 'Could not load incoming email settings.'
}

// Configuration belongs to Builder tools. Both Email and Triggers show this
// same persisted route without granting mutation authority to the pane.
export function GmailInboundPanel({ workspacePath, connections = [], refreshToken = 0, onCounts }: { workspacePath: string; connections?: GmailConnection[]; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void }) {
  const [state, setState] = useState<GmailInboundState | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const generation = useRef(0)

  const refresh = useCallback(async () => {
    const current = ++generation.current
    setBusy(true); setError('')
    try {
      const result = await agentApi.getGmailInboundRoute(workspacePath)
      if (current === generation.current) setState(result)
    } catch (e) {
      if (current === generation.current) setError(errorMessage(e))
    } finally {
      if (current === generation.current) setBusy(false)
    }
  }, [workspacePath])

  useEffect(() => {
    setState(null); setError(''); setCopied(false)
    void refresh()
    return () => { generation.current++ }
  }, [refresh, refreshToken])

  useEffect(() => {
    onCounts?.({ active: state?.route?.enabled ? 1 : 0, paused: state?.route && !state.route.enabled ? 1 : 0 })
  }, [state, onCounts])

  useEffect(() => {
    if (!state?.route?.enabled || state.watch_ready || state.error) return
    const timer = setTimeout(() => { void refresh() }, 5000)
    return () => clearTimeout(timer)
  }, [state, refresh])

  const route = state?.route
  const account = connections.find(connection => connection.id === route?.connection_id)
  return <FormSection title="Incoming email" description="Ask Builder to connect Gmail, choose the workflow route, or disable this trigger. This panel shows the saved configuration.">
    <div className="space-y-3 text-sm">
      {error && <p role="alert" className="text-destructive">{error}</p>}
      {state && !state.configured && <p className="text-muted-foreground">An administrator needs to enable Gmail incoming email for this deployment.</p>}
      {state?.configured && !route && <p className="text-muted-foreground">No Gmail trigger configured. Ask Builder to link a connected account.</p>}
      {route && <div className="space-y-2 rounded-md border p-3">
        <p className="font-medium">{route.name || 'Gmail trigger'} · {route.enabled ? 'Enabled' : 'Disabled'}</p>
        <p className="text-muted-foreground">Receiving account: {account?.email || account?.display_name || route.connection_id}</p>
        <div className="break-all font-mono text-xs">{route.address}</div>
        <Button size="sm" variant="outline" onClick={async () => { try { await navigator.clipboard.writeText(route.address); setCopied(true) } catch { setError('Could not copy the address. Select and copy it manually.') } }}>{copied ? 'Copied' : 'Copy email address'}</Button>
        <p className="text-muted-foreground">Starts: {route.workflow_trigger ? route.step_id ? `Step ${route.step_id}` : Object.keys(route.route_selections || {}).length ? Object.entries(route.route_selections!).map(([step, branch]) => `${step} → ${branch}`).join(', ') : 'Full workflow' : 'A project chat; replies continue the same chat'}</p>
        {!!route.group_names?.length && <p className="text-muted-foreground">Groups: {route.group_names.join(', ')}</p>}
        <p className="text-muted-foreground">Email final response: {route.reply ? 'On' : 'Off'} · Owner email only</p>
        <p className="text-muted-foreground">{!route.enabled ? 'Incoming email is disabled.' : state?.error ? state.error : state?.watch_ready ? 'Ready to receive email.' : 'Registering your mailbox. This usually takes a few seconds.'}</p>
        {!!state?.deliveries.length && <details><summary>Recent email activity</summary><ul className="mt-2 space-y-1">{state.deliveries.map(d => <li key={d.id}>{d.status === 'staged' ? 'Waiting for mailbox sync' : d.status.replaceAll('_', ' ')}{d.error ? ` — ${d.error}` : ''}</li>)}</ul></details>}
      </div>}
      <Button size="sm" variant="outline" disabled={busy} onClick={() => void refresh()}>Refresh email activity</Button>
    </div>
  </FormSection>
}
