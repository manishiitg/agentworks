import { useCallback, useEffect, useRef, useState } from 'react'
import { agentApi } from '../../../services/api'
import type { GmailConnection, GmailInboundState } from '../../../services/api-types'
import { Button } from '../../ui/Button'
import { FormSection } from '../../ui/FormSection'
import { AskAIButton } from '../AskAIButton'
import { buildAskAIMessage } from '../../../utils/askAIMessage'

const setupMessage = buildAskAIMessage({
  view: 'Incoming email',
  summary: 'Help me connect Gmail and choose what incoming email should start.',
  instructions: 'Inspect get_gmail_trigger and list_gmail_connections for this target. Explain the current setup, then ask which mailbox, task or saved workflow route, senders and email conditions I want. Use manage_gmail_trigger for configuration; discover account and route IDs yourself. sender_allowlist accepts exact addresses or @domains with OR; subject_contains_any and body_contains_any support alternative phrases. Only widen senders when I ask. For requested automated notifications, use allow_automatic with an explicit sender_allowlist and explain the saved rule. Preserve existing settings unless I ask to change them. If needed, prepare a Google consent link with action="connect" and wait for me to complete consent before enabling the trigger. If the deployment is not configured, explain what its administrator must enable; never request credentials in chat or edit server credential files. Return the receiving address and verified readiness. The Incoming email panel is read-only.',
})

const fetchMessage = buildAskAIMessage({
  view: 'Incoming email',
  summary: 'Fetch recent matching Gmail emails and show them in this chat.',
  instructions: 'Inspect get_gmail_trigger and list_gmail_connections for this target. Use the saved connection_id, or the only readable account if no trigger is set; ask which mailbox if several fit. Read recent Gmail messages with google_workspace_cli through the supported Google tools/API bridge. When a trigger exists, search its receiving address and apply its saved sender and content rules, including OR alternatives; explain any condition you cannot verify. Show recent matches with sender, subject, received time and a brief summary. Do not treat saved delivery activity as freshly fetched email. Mailbox reading does not require Pub/Sub to be enabled. If read consent is missing, explain how to connect it. This request is to read and summarize mail; do not change settings, replay deliveries, start the saved workflow or send replies. Never request credentials in chat.',
})

function errorMessage(error: unknown): string {
  const response = (error as { response?: { data?: unknown } })?.response?.data
  return typeof response === 'string' ? response : 'Could not load incoming email settings.'
}

// Configuration belongs to Builder tools. Both Email and Triggers show this
// same persisted route without granting mutation authority to the pane.
export function GmailInboundPanel({ workspacePath, connections = [], refreshToken = 0, onCounts, onAsk }: { workspacePath: string; connections?: GmailConnection[]; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void; onAsk?: (message: string) => void | Promise<void> }) {
  const [state, setState] = useState<GmailInboundState | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const generation = useRef(0)

  const refresh = useCallback(async () => {
    const current = ++generation.current
    setError('')
    try {
      const result = await agentApi.getGmailInboundRoute(workspacePath)
      if (current === generation.current) setState(result)
    } catch (e) {
      if (current === generation.current) setError(errorMessage(e))
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
  const filters = route?.filters
  const conditions = [
    ...(filters?.sender_allowlist?.length ? [`Sender is ${filters.sender_allowlist.join(' OR ')}`] : []),
    ...(filters?.subject_contains || []).map(term => `Subject contains “${term}”`),
    ...(filters?.body_contains || []).map(term => `Body contains “${term}”`),
    ...(filters?.subject_contains_any?.length ? [`Subject contains any: ${filters.subject_contains_any.map(term => `“${term}”`).join(' OR ')}`] : []),
    ...(filters?.body_contains_any?.length ? [`Body contains any: ${filters.body_contains_any.map(term => `“${term}”`).join(' OR ')}`] : []),
    ...(filters?.has_attachments === undefined ? [] : [filters.has_attachments ? 'Has attachments' : 'No attachments']),
    ...(filters?.new_threads_only ? ['New threads only'] : []),
  ]
  return <FormSection title="Incoming email" description="Ask Builder to connect Gmail, choose the workflow route, or disable this trigger. This panel shows the saved configuration." actions={<AskAIButton workspacePath={workspacePath} onAsk={onAsk} message={setupMessage} />}>
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
        <p className="text-muted-foreground">Email final response: {route.reply ? 'On' : 'Off'} · {filters?.sender_allowlist?.length ? 'Listed senders only' : 'Owner email only'}</p>
        {filters?.allow_automatic && <p className="text-muted-foreground">Automated notifications from listed senders: Allowed</p>}
        <p className="text-muted-foreground">Filters: {conditions.length ? `${conditions.join(' · ')} (all condition groups must match)` : 'None'}</p>
        <p className="text-muted-foreground">{!route.enabled ? 'Incoming email is disabled.' : state?.error ? state.error : state?.watch_ready ? 'Ready to receive email.' : 'Registering your mailbox. This usually takes a few seconds.'}</p>
        {!!state?.deliveries.length && <details><summary>Recent email activity</summary><ul className="mt-2 space-y-1">{state.deliveries.map(d => <li key={d.id}>{d.status === 'staged' ? 'Waiting for mailbox sync' : d.status.replaceAll('_', ' ')}{d.error ? ` — ${d.error}` : ''}</li>)}</ul></details>}
      </div>}
      <AskAIButton workspacePath={workspacePath} onAsk={onAsk} message={fetchMessage} label="Fetch emails" />
      <p className="text-xs text-muted-foreground">Fetch matching emails into chat. Recent email activity shows saved trigger deliveries.</p>
    </div>
  </FormSection>
}
