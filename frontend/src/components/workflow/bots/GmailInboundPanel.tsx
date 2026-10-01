import { useEffect, useRef, useState } from 'react'
import { agentApi } from '../../../services/api'
import type { GmailConnection, GmailInboundState } from '../../../services/api-types'
import { Button } from '../../ui/Button'
import { FormSection } from '../../ui/FormSection'

function errorMessage(error: unknown): string {
  const response = (error as { response?: { data?: unknown } })?.response?.data
  return typeof response === 'string' ? response : 'Could not load incoming email settings.'
}

export function GmailInboundPanel({ workspacePath, connections }: { workspacePath: string; connections: GmailConnection[] }) {
  const [state, setState] = useState<GmailInboundState | null>(null)
  const [connection, setConnection] = useState('')
  const [reply, setReply] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const generation = useRef(0)

  useEffect(() => {
    const current = ++generation.current
    let stopped = false
    setState(null); setError(''); setBusy(false); setConnection(''); setReply(true); setCopied(false)
    const load = async () => {
      try {
        const result = await agentApi.getGmailInboundRoute(workspacePath)
        if (stopped || current !== generation.current) return
        setState(result)
        setConnection(result.route?.connection_id || '')
        setReply(result.route?.reply ?? true)
      } catch (e) {
        if (!stopped && current === generation.current) setError(errorMessage(e))
      }
    }
    void load()
    return () => { stopped = true; generation.current++ }
  }, [workspacePath])

  const save = async (enabled: boolean) => {
    const current = generation.current
    setBusy(true); setError('')
    try {
      const result = await agentApi.saveGmailInboundRoute({ workspace_path: workspacePath, connection_id: connection, enabled, reply })
      if (current === generation.current) setState(result)
    } catch (e) {
      if (current === generation.current) setError(errorMessage(e))
    } finally {
      if (current === generation.current) setBusy(false)
    }
  }

  const refresh = async () => {
    const current = generation.current
    setBusy(true); setError('')
    try {
      const result = await agentApi.getGmailInboundRoute(workspacePath)
      if (current === generation.current) setState(result)
    } catch (e) {
      if (current === generation.current) setError(errorMessage(e))
    } finally {
      if (current === generation.current) setBusy(false)
    }
  }

  // Poll only active panels while watch registration is pending. Later changes
  // are visible on refresh; an idle UI does not add a mailbox watcher.
  useEffect(() => {
    if (!state?.route?.enabled || state.watch_ready || state.error) return
    const current = generation.current
    const timer = setTimeout(async () => {
      try {
        const result = await agentApi.getGmailInboundRoute(workspacePath)
        if (current === generation.current) setState(result)
      } catch { /* Keep saved settings; the next visit can retry. */ }
    }, 5000)
    return () => clearTimeout(timer)
  }, [state, workspacePath])

  return <FormSection title="Incoming email" description="Email this address to start a new chat. Replies in the same email conversation continue that chat. Only email from your signed-in account is accepted.">
    <div className="space-y-3 text-sm">
      {error && <p role="alert" className="text-destructive">{error}</p>}
      {state && !state.configured && <p className="text-muted-foreground">An administrator needs to enable Gmail incoming email for this deployment.</p>}
      {state?.configured && <>
        <label className="block space-y-1">Receiving Gmail account
          <select aria-label="Receiving Gmail account" className="block w-full rounded-md border bg-background p-2" value={connection} onChange={e => setConnection(e.target.value)} disabled={busy}>
            <option value="">Select your account</option>
            {connections.filter(c => (c.enabled && c.allow_read_access) || c.id === state.route?.connection_id).map(c => <option key={c.id} value={c.id}>{c.email || c.display_name}</option>)}
          </select>
        </label>
        <p className="text-muted-foreground">Connect your Google account below and enable Gmail read access before activating this address.</p>
        <label className="flex items-center gap-2"><input type="checkbox" checked={reply} onChange={e => setReply(e.target.checked)} disabled={busy} />Email the final response back to me</label>
        <div className="flex gap-2">
          <Button size="sm" disabled={busy || !connection} onClick={() => void save(true)}>{busy ? 'Saving…' : state.route?.enabled ? 'Save incoming email' : 'Enable incoming email'}</Button>
          {state.route?.enabled && <Button size="sm" variant="outline" disabled={busy} onClick={() => void save(false)}>Disable incoming email</Button>}
        </div>
        {state.route && <div className="space-y-2 rounded-md border p-3">
          <div className="break-all font-mono text-xs">{state.route.address}</div>
          <Button size="sm" variant="outline" onClick={async () => { try { await navigator.clipboard.writeText(state.route!.address); setCopied(true) } catch { setError('Could not copy the address. Select and copy it manually.') } }}>{copied ? 'Copied' : 'Copy email address'}</Button>
          <p className="text-muted-foreground">{!state.route.enabled ? 'Incoming email is disabled.' : state.error ? state.error : state.watch_ready ? 'Ready to receive email.' : 'Registering your mailbox. This usually takes a few seconds.'}</p>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void refresh()}>Refresh email activity</Button>
          {state.deliveries.length > 0 && <details><summary>Recent email activity</summary><ul className="mt-2 space-y-1">{state.deliveries.map(d => <li key={d.id}>{d.status === 'staged' ? 'Waiting for mailbox sync' : d.status.replaceAll('_', ' ')}{d.error ? ` — ${d.error}` : ''}</li>)}</ul></details>}
        </div>}
      </>}
    </div>
  </FormSection>
}
