import { useCallback, useEffect, useRef, useState } from 'react'
import { agentApi } from '../../../services/api'
import type { GmailConnection, GmailInboundFilters, GmailInboundRule, GmailInboundState } from '../../../services/api-types'
import { Button } from '../../ui/Button'
import { FormSection } from '../../ui/FormSection'
import { AskAIButton } from '../AskAIButton'
import { buildAskAIMessage } from '../../../utils/askAIMessage'

const setupMessage = buildAskAIMessage({
  view: 'Incoming email',
  summary: 'Help me connect Gmail and choose what incoming email should start.',
  instructions: 'Inspect get_gmail_trigger and list_gmail_connections for this target. Explain the current setup, then ask which mailbox, task or saved workflow route, senders and email conditions I want. Use manage_gmail_trigger for configuration; discover account and route IDs yourself. sender_allowlist accepts exact addresses or @domains with OR; subject_contains_any and body_contains_any support alternative phrases. For different actions, configure ordered named rules with stable IDs: a Crew/Code instruction or a workflow saved route and groups for each rule. First matching enabled rule wins; no match skips mail. Read the saved rules and preserve untouched rules and their IDs before replacing the list. Common filters restrict every rule. Only propose additional senders when I ask. Public mailbox domain entries such as @gmail.com are rejected; use exact addresses. If sender_consent.required and not approved, tell me to review the saved configuration and confirm in the Incoming email pane. Tool calls and chat messages cannot grant sender consent. Never claim external senders are active before approval. For requested automated notifications, use allow_automatic with an explicit sender_allowlist and explain the saved rule. Preserve existing settings unless I ask to change them. If needed, prepare a Google consent link with action="connect" and wait for me to complete consent before enabling the trigger. If the deployment is not configured, read setup.admin_setup and explain Google sign-in versus automatic receiving. If setup.provisioning.can_prepare is true, use setup_gmail_inbound(action="prepare") with the registered client and its project ID; use registered project metadata or ask for the project ID if missing. Show the reviewed plan and review_url. I must open that URL, review the changes and complete Google Cloud consent myself; never follow it through agent tools. Inspect setup_gmail_inbound(action="status") afterward. Explain that this handles APIs, resources, IAM and private server configuration without environment edits or a restart. Google Cloud project permissions and a public HTTPS endpoint are still required. If I am not an app administrator, explain that an app administrator with Google project permissions must complete this one-time setup. Offer the manual checklist only if automatic setup is unavailable or I request it. An empty oauth_clients list means no eligible OAuth-client/topic mapping, not necessarily missing sign-in. Do not stop at asking an administrator; never request credentials in chat or edit server credential files. Return the receiving address and verified readiness. Configuration in the Incoming email panel is read-only; only the signed-in owner can confirm or revoke sender access there.',
})

const fetchMessage = buildAskAIMessage({
  view: 'Incoming email',
  summary: 'Fetch recent matching Gmail emails and show them in this chat.',
  instructions: 'Inspect get_gmail_trigger and list_gmail_connections for this target. Use the saved connection_id, or the only readable account if no trigger is set; ask which mailbox if several fit. Read recent Gmail messages with google_workspace_cli through the supported Google tools/API bridge. When a trigger exists, search its receiving address and apply its saved sender and content rules, including OR alternatives and ordered rules. Identify which enabled rule each message matches without running its action; explain any condition you cannot verify. Show recent matches with sender, subject, received time and a brief summary. Do not treat saved delivery activity as freshly fetched email. Mailbox reading does not require Pub/Sub to be enabled. If read consent is missing, explain how to connect it. This request is to read and summarize mail; do not change settings, replay deliveries, start the saved workflow or send replies. Never request credentials in chat.',
})

function filterConditions(filters?: GmailInboundFilters) {
  return [
    ...(filters?.sender_allowlist?.length ? [`Sender is ${filters.sender_allowlist.join(' OR ')}`] : []),
    ...(filters?.subject_contains || []).map(term => `Subject contains “${term}”`),
    ...(filters?.body_contains || []).map(term => `Body contains “${term}”`),
    ...(filters?.subject_contains_any?.length ? [`Subject contains any: ${filters.subject_contains_any.map(term => `“${term}”`).join(' OR ')}`] : []),
    ...(filters?.body_contains_any?.length ? [`Body contains any: ${filters.body_contains_any.map(term => `“${term}”`).join(' OR ')}`] : []),
    ...(filters?.has_attachments === undefined ? [] : [filters.has_attachments ? 'Has attachments' : 'No attachments']),
    ...(filters?.new_threads_only ? ['New threads only'] : []),
  ]
}

function workflowAction(rule: { step_id?: string; route_selections?: Record<string, string> | null }) {
  return rule.step_id ? `Step ${rule.step_id}` : Object.keys(rule.route_selections || {}).length ? Object.entries(rule.route_selections!).map(([step, branch]) => `${step} → ${branch}`).join(', ') : 'Full workflow'
}

function EmailRuleCard({ rule, order, workflow, commonFilters }: { rule: GmailInboundRule; order: number; workflow?: boolean; commonFilters?: GmailInboundFilters }) {
  const conditions = filterConditions(rule.filters)
  const senderList = rule.filters?.sender_allowlist?.length ? rule.filters.sender_allowlist : commonFilters?.sender_allowlist
  return <li className="min-w-0 space-y-2 rounded-md border bg-background/50 p-3">
    <div className="flex items-start gap-2">
      <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-medium">{order}</span>
      <p className="min-w-0 flex-1 break-words font-medium">{rule.name}</p>
      <span className={`shrink-0 text-xs ${rule.enabled === false ? 'text-muted-foreground' : 'text-primary'}`}>{rule.enabled === false ? 'Paused' : 'Enabled'}</span>
    </div>
    <p className="break-words text-xs text-muted-foreground">Senders: {senderList?.length ? senderList.join(' OR ') : 'Owner email only'}</p>
    <p className="break-words text-xs text-muted-foreground">Matches: {conditions.length ? conditions.join(' · ') : 'Any email allowed by the common policy'}{conditions.length > 1 ? ' (all condition groups must match)' : ''}</p>
    {rule.filters?.allow_automatic && <p className="text-xs text-muted-foreground">Automated notifications from listed senders: Allowed</p>}
    {workflow ? <>
      <p className="break-words text-xs"><span className="font-medium">Runs: </span>{workflowAction(rule)}</p>
      {!!rule.group_names?.length && <p className="break-words text-xs text-muted-foreground">Groups: {rule.group_names.join(', ')}</p>}
    </> : <div className="rounded border bg-muted/30 p-2 text-xs">
      <p className="mb-1 font-medium">Message to chat</p>
      <p className="whitespace-pre-wrap break-words">{rule.instruction}</p>
      <p className="mt-1 text-muted-foreground">Incoming email is included as context.</p>
    </div>}
  </li>
}

function errorMessage(error: unknown, fallback = 'Could not load incoming email settings.'): string {
  const response = (error as { response?: { data?: unknown } })?.response?.data
  return typeof response === 'string' ? response : fallback
}

// Configuration belongs to Builder tools. Both Email and Triggers show this
// same persisted route without granting mutation authority to the pane.
export function GmailInboundPanel({ workspacePath, connections = [], refreshToken = 0, onCounts, onAsk }: { workspacePath: string; connections?: GmailConnection[]; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void; onAsk?: (message: string) => void | Promise<void> }) {
  const [state, setState] = useState<GmailInboundState | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [senderRiskAccepted, setSenderRiskAccepted] = useState(false)
  const [confirmingSenders, setConfirmingSenders] = useState(false)
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

  useEffect(() => {
    const job = state?.setup?.provisioning?.job
    if (!job || ['Receiving infrastructure ready', 'Setup failed', 'Setup cancelled', 'Consent link expired'].includes(job.stage)) return
    const timer = setTimeout(() => { void refresh() }, 5000)
    return () => clearTimeout(timer)
  }, [state, refresh])

  useEffect(() => { setSenderRiskAccepted(false) }, [workspacePath, state?.sender_consent?.config_hash])

  const confirmSenders = async (action: 'approve' | 'revoke') => {
    const consent = state?.sender_consent
    if (!consent || confirmingSenders || (action === 'approve' && (!senderRiskAccepted || consent.blocked_reason))) return
    const current = generation.current
    setConfirmingSenders(true)
    try {
      await agentApi.confirmGmailSenderConsent(workspacePath, consent.config_hash, action)
      if (current === generation.current) {
        setSenderRiskAccepted(false)
        await refresh()
      }
    } catch (e) {
      if (current === generation.current) {
        await refresh()
        setError(errorMessage(e, 'Could not confirm email sender access.'))
      }
    } finally { setConfirmingSenders(false) }
  }

  const route = state?.route
  const account = connections.find(connection => connection.id === route?.connection_id)
  const filters = route?.filters
  const conditions = filterConditions(filters)
  const rules = route?.rules || []
  const provisioning = state?.setup?.provisioning
  const setupJob = provisioning?.job
  return <FormSection title="Incoming email" description="Ask Builder to connect Gmail, choose chat instructions or workflow routes, or disable this trigger. This panel shows the saved configuration." actions={<AskAIButton workspacePath={workspacePath} onAsk={onAsk} message={setupMessage} />}>
    <div className="space-y-3 text-sm">
      {error && <p role="alert" className="text-destructive">{error}</p>}
      {state && (!state.configured || state.setup?.oauth_clients.length === 0) && <div className="space-y-2 rounded-md border p-3">
        <p className="font-medium">Automatic incoming email is not set up</p>
        <p className="text-muted-foreground">Google sign-in connects your account. To start chats or workflows when mail arrives, this server also needs Google Pub/Sub, Google's mailbox event delivery service. Saved filters alone cannot receive mail.</p>
        <p className="text-muted-foreground">One-time setup requires Google Cloud project permissions. Ask Builder to prepare the setup; an app administrator reviews the plan and completes Google consent. AgentWorks handles the Cloud resources and server configuration. An app administrator role alone does not grant Google Cloud permissions.</p>
        <details>
          <summary className="cursor-pointer font-medium">Manual setup checklist (optional)</summary>
          <ol className="mt-2 list-decimal space-y-2 pl-5 text-muted-foreground">
            <li>Use the Google Cloud project that owns your Google sign-in OAuth client. Enable the Gmail and Pub/Sub APIs.</li>
            <li>Create a Pub/Sub topic. Give <code className="break-all">gmail-api-push@system.gserviceaccount.com</code> the Pub/Sub Publisher role on that topic.</li>
            <li>Create an authenticated push subscription with a service account and permission for Pub/Sub to create its authentication tokens. Use the same public HTTPS email event URL for the endpoint and audience.</li>
            <li>Set <code>GMAIL_INBOUND_TOPICS</code> (OAuth client name → full topic name), <code>GMAIL_INBOUND_AUDIENCE</code> (event URL), and <code>GMAIL_INBOUND_PUSH_EMAIL</code> (push service account email) in the server environment, then restart the backend.</li>
            <li>Ask Builder to verify setup, connect your mailbox with read access, and configure its incoming email rules.</li>
          </ol>
          {state.setup?.admin_setup?.push_endpoint && <p className="mt-2">This deployment's event URL: <code className="break-all">{state.setup.admin_setup.push_endpoint}</code></p>}
          <p className="mt-2 text-muted-foreground">An empty OAuth client list can mean the topic mapping is missing even when Google sign-in works. Local development needs a public HTTPS tunnel.</p>
          <a className="mt-2 inline-block underline" href="https://developers.google.com/workspace/gmail/api/guides/push" target="_blank" rel="noreferrer">Google setup documentation</a>
        </details>
        <p className="text-xs text-muted-foreground">Ask AI above to set up this deployment through Builder. Account connections and email rules are configured through Builder.</p>
      </div>}
      {setupJob && <section aria-label="Incoming email server setup" className="space-y-2 rounded-md border p-3">
        <h4 className="font-medium">Server setup · {setupJob.stage}</h4>
        <p className="break-words text-muted-foreground">Google project: {setupJob.plan.project_id} · OAuth app: {setupJob.plan.client_name}</p>
        <p className="break-all text-xs text-muted-foreground">Receiving URL: {setupJob.plan.push_endpoint}</p>
        {setupJob.error && <p role="alert" className="text-destructive">{setupJob.error}</p>}
        {setupJob.review_url && <a className="inline-block rounded-md border px-3 py-2 text-primary underline" href={setupJob.review_url} target="_blank" rel="noreferrer">Review setup and continue with Google</a>}
        <p className="text-xs text-muted-foreground">Ask Builder to check progress. Infrastructure readiness is separate from connecting a mailbox and testing email delivery.</p>
      </section>}
      {state?.configured && state.setup?.oauth_clients.length !== 0 && !route && <p className="text-muted-foreground">No Gmail trigger configured. Ask Builder to link a connected account.</p>}
      {route && state?.sender_consent?.required && <section className="space-y-3 rounded-md border p-3" aria-label="Email sender access">
        <h4 className="font-medium">{state.sender_consent.approved ? 'Additional sender access approved' : 'Your approval is required for additional senders'}</h4>
        <p className="text-muted-foreground">These senders can start this target using your tools, connected accounts and files. Email replies may share its results with them. Domain entries cover every address at that domain.</p>
        <ul className="space-y-1">{state.sender_consent.senders.map(sender => <li key={sender}><code className="break-all">{sender}</code></li>)}</ul>
        {state.sender_consent.blocked_reason && <p role="alert" className="text-destructive">{state.sender_consent.blocked_reason} Ask Builder to use exact addresses.</p>}
        {state.sender_consent.approved ? <Button size="sm" variant="outline" disabled={confirmingSenders} onClick={() => { void confirmSenders('revoke') }}>Revoke additional sender access</Button> : <>
          <p className="text-muted-foreground">Additional senders are blocked until you approve. Review the saved conditions and actions below. Approval covers this configuration; changes to senders, rules, replies or enabled state require approval again.</p>
          <label className="flex items-start gap-2"><input type="checkbox" className="mt-1" checked={senderRiskAccepted} disabled={confirmingSenders || !!state.sender_consent.blocked_reason} onChange={event => setSenderRiskAccepted(event.target.checked)} /><span>I trust these senders to run this target with my access and receive enabled email replies.</span></label>
          <Button size="sm" disabled={!senderRiskAccepted || confirmingSenders || !!state.sender_consent.blocked_reason} onClick={() => { void confirmSenders('approve') }}>{confirmingSenders ? 'Confirming…' : 'Approve additional senders'}</Button>
        </>}
      </section>}
      {route && <div className="space-y-2 rounded-md border p-3">
        <p className="font-medium">{route.name || 'Gmail trigger'} · {route.enabled ? 'Enabled' : 'Disabled'}</p>
        <p className="text-muted-foreground">Receiving account: {account?.email || account?.display_name || route.connection_id}</p>
        <div className="break-all font-mono text-xs">{route.address}</div>
        <Button size="sm" variant="outline" onClick={async () => { try { await navigator.clipboard.writeText(route.address); setCopied(true) } catch { setError('Could not copy the address. Select and copy it manually.') } }}>{copied ? 'Copied' : 'Copy email address'}</Button>
        {!rules.length && <p className="text-muted-foreground">Starts: {route.workflow_trigger ? workflowAction(route) : 'A project chat; replies continue the same chat'}</p>}
        {!rules.length && !!route.group_names?.length && <p className="text-muted-foreground">Groups: {route.group_names.join(', ')}</p>}
        <p className="text-muted-foreground">Email final response: {route.reply ? 'On' : 'Off'} · {rules.length ? 'Sender policy is shown on each rule' : filters?.sender_allowlist?.length ? 'Listed senders only' : 'Owner email only'}</p>
        {filters?.allow_automatic && <p className="text-muted-foreground">Automated notifications from listed senders: Allowed</p>}
        <p className="text-muted-foreground">{rules.length ? 'Common filters' : 'Filters'}: {conditions.length ? `${conditions.join(' · ')} (all condition groups must match)` : 'None'}</p>
        {!!rules.length && <section className="space-y-2 border-t pt-3" aria-label="Email rules">
          <h4 className="font-medium">Email rules · {rules.length}</h4>
          <p className="text-xs text-muted-foreground">Checked in order. The first matching enabled rule runs; no match skips the email. Ask Builder to add, change, pause, or reorder rules.</p>
          <ol className="space-y-2">{rules.map((rule, index) => <EmailRuleCard key={rule.id} rule={rule} order={index + 1} workflow={route.workflow_trigger} commonFilters={filters} />)}</ol>
        </section>}
        <p className="text-muted-foreground">{!route.enabled ? 'Incoming email is disabled.' : state?.error ? state.error : state?.sender_consent?.required && !state.sender_consent.approved ? 'Additional email senders are awaiting owner approval.' : state?.watch_ready ? 'Ready to receive email.' : 'Registering your mailbox. This usually takes a few seconds.'}</p>
        {!!state?.deliveries.length && <details><summary>Recent email activity</summary><ul className="mt-2 space-y-1">{state.deliveries.map(d => <li key={d.id}>{d.rule_name ? `${d.rule_name} · ` : d.rule_id ? `${d.rule_id} · ` : ''}{d.status === 'staged' ? 'Waiting for mailbox sync' : d.status.replaceAll('_', ' ')}{d.error ? ` — ${d.error}` : ''}</li>)}</ul></details>}
      </div>}
      <AskAIButton workspacePath={workspacePath} onAsk={onAsk} message={fetchMessage} label="Fetch emails" />
      <p className="text-xs text-muted-foreground">Fetch matching emails into chat. Recent email activity shows saved trigger deliveries.</p>
    </div>
  </FormSection>
}
