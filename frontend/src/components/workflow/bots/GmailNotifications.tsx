import { useEffect, useState, type ReactNode } from 'react'
import { AlertTriangle, Loader2, Mail } from 'lucide-react'
import { agentApi } from '../../../services/api'
import type { GmailConnection, GoogleServiceGrant } from '../../../services/api-types'
import { Button } from '../../ui/Button'
import { Card } from '../../ui/Card'
import { Checkbox } from '../../ui/checkbox'
import { FormSection } from '../../ui/FormSection'
import { Input } from '../../ui/Input'
import { Label } from '../../ui/label'
import { Textarea } from '../../ui/Textarea'
import { ToggleRow } from '../../ui/ToggleRow'
import { READ_ONLY_TITLE } from '../../../hooks/useCanWriteWorkflow'
import { AskAIButton } from '../AskAIButton'
import type { WorkflowBots } from './useWorkflowBots'
import { StatusBanner } from './StatusBanner'
import { GoogleAccountList } from '../../../products/work/GoogleAccountList'
import { GmailSetupGuide } from './GmailSetupGuide'

// ── Email notifications (account-wide, shared by every workflow) ──────────

// Maps a raw OAuth scope to what it actually grants, so the connection card
// reads as "Gmail: send + read" instead of an unlabeled "gmail.modify" chip
// nobody can place. Keyed on both the full scope URL and Google's bare OIDC
// literals (some tokens carry "email"/"openid" as-is, others carry the
// equivalent https://www.googleapis.com/auth/userinfo.email long form —
// Google can return either depending on how a given consent was originally
// requested, so both keys map to the same label).
//
// gmail.modify (full read/write/organize) and the legacy default "tasks"
// scope predate this connector's current send-only-by-default design and its
// opt-in Drive/Sheets/Docs/Slides/Calendar catalog (google_services.go) --
// they show up here on connections authorized before that shipped, not
// something the current "+ Add account" flow can request. Reconnecting an
// existing connection re-requests exactly its current allow_read_access +
// services, which drops any such legacy scope the stored config no longer
// asks for.
const GMAIL_SCOPE_LABELS: Record<string, { label: string; detail: string }> = {
  'openid': { label: 'Sign-in', detail: 'OpenID sign-in — identifies which Google account this is' },
  'email': { label: 'Email address', detail: 'Read the account’s email address (identity only, not mailbox content)' },
  'profile': { label: 'Basic profile', detail: 'Read the account’s basic Google profile (name, picture)' },
  'https://www.googleapis.com/auth/userinfo.email': { label: 'Email address', detail: 'Read the account’s email address (identity only, not mailbox content)' },
  'https://www.googleapis.com/auth/userinfo.profile': { label: 'Basic profile', detail: 'Read the account’s basic Google profile (name, picture)' },
  'https://www.googleapis.com/auth/gmail.send': { label: 'Gmail: send', detail: 'Send email as this account. Cannot read or search the mailbox.' },
  'https://www.googleapis.com/auth/gmail.compose': { label: 'Gmail: drafts + send', detail: 'Create drafts and send or reply to email. Does not grant mailbox-wide editing.' },
  'https://www.googleapis.com/auth/gmail.readonly': { label: 'Gmail: read', detail: 'Read and search this mailbox. Cannot send.' },
  'https://www.googleapis.com/auth/gmail.modify': { label: 'Gmail: full (legacy)', detail: 'Full mailbox access — read, send, and organize. Broader than this connector currently requests; carried over from an older connection.' },
  'https://www.googleapis.com/auth/drive.readonly': { label: 'Drive: read', detail: 'Read files in Google Drive. Cannot create or modify them.' },
  'https://www.googleapis.com/auth/drive': { label: 'Drive: read+write', detail: 'Read and modify files in Google Drive.' },
  'https://www.googleapis.com/auth/spreadsheets.readonly': { label: 'Sheets: read', detail: 'Read Google Sheets spreadsheets. Cannot modify them.' },
  'https://www.googleapis.com/auth/spreadsheets': { label: 'Sheets: read+write', detail: 'Read and modify Google Sheets spreadsheets.' },
  'https://www.googleapis.com/auth/documents.readonly': { label: 'Docs: read', detail: 'Read Google Docs documents. Cannot modify them.' },
  'https://www.googleapis.com/auth/documents': { label: 'Docs: read+write', detail: 'Read and modify Google Docs documents.' },
  'https://www.googleapis.com/auth/presentations.readonly': { label: 'Slides: read', detail: 'Read Google Slides presentations. Cannot modify them.' },
  'https://www.googleapis.com/auth/presentations': { label: 'Slides: read+write', detail: 'Read and modify Google Slides presentations.' },
  'https://www.googleapis.com/auth/calendar.readonly': { label: 'Calendar: read', detail: 'Read Google Calendar events. Cannot create or modify them.' },
  'https://www.googleapis.com/auth/calendar': { label: 'Calendar: read+write', detail: 'Read and modify Google Calendar events.' },
  'https://www.googleapis.com/auth/tasks.readonly': { label: 'Tasks: read (legacy)', detail: 'Read Google Tasks. Not requestable from this connector’s current Add account flow; carried over from an older connection.' },
  'https://www.googleapis.com/auth/tasks': { label: 'Tasks: read+write (legacy)', detail: 'Read and modify Google Tasks. Not requestable from this connector’s current Add account flow; carried over from an older connection.' },
}

// Falls back to the old last-path-segment shorthand for any scope this
// account was granted that isn't in the lookup above, so an unrecognized
// scope still shows something rather than disappearing silently.
function formatGmailScope(scope: string): { label: string; title: string } {
  const known = GMAIL_SCOPE_LABELS[scope.trim()]
  if (known) return { label: known.label, title: `${known.detail}\n\n(${scope})` }
  const parts = scope.split('/')
  return { label: parts[parts.length - 1] || scope, title: scope }
}

type GmailCapabilityState = 'none' | 'read' | 'write'

interface GmailCapabilityRow {
  label: string
  state: GmailCapabilityState
  detail: string
}

// One full read+send+organize scope can stand in for a narrower one this
// connector would otherwise request separately (gmail.modify implies both
// gmail.readonly and gmail.send) — checked here so a legacy connection's
// broader grant still shows the capability as present instead of "not
// authorized" just because the exact narrower scope string isn't in the list.
function gmailScopesGrant(scopes: string[], ...anyOf: string[]): boolean {
  return anyOf.some(scope => scopes.includes(scope))
}

// Builds the full permission matrix — every capability this connector can
// request, PLUS any legacy scope this specific account still carries — each
// marked by what's actually granted right now (from conn.auth.scopes, the
// live truth from Google) rather than the stored allow_read_access/services
// config, which can go stale for a connection authorized before that config
// existed (see GMAIL_SCOPE_LABELS' gmail.modify/tasks comment).
function gmailCapabilityMatrix(scopes: string[]): GmailCapabilityRow[] {
  const has = (...anyOf: string[]) => gmailScopesGrant(scopes, ...anyOf)
  const rows: GmailCapabilityRow[] = [
    {
      label: 'Gmail: Send',
      state: has('https://www.googleapis.com/auth/gmail.send', 'https://www.googleapis.com/auth/gmail.modify') ? 'write' : 'none',
      detail: 'Send email as this account.',
    },
    {
      label: 'Gmail: Read',
      state: has('https://www.googleapis.com/auth/gmail.readonly', 'https://www.googleapis.com/auth/gmail.modify') ? 'read' : 'none',
      detail: 'Read and search this mailbox.',
    },
    {
      label: 'Gmail: Agent drafts/send',
      state: has('https://www.googleapis.com/auth/gmail.compose', 'https://www.googleapis.com/auth/gmail.modify') ? 'write' : 'none',
      detail: 'Let agents create drafts and send or reply. Separate from notification delivery.',
    },
    {
      label: 'Drive',
      state: has('https://www.googleapis.com/auth/drive') ? 'write' : has('https://www.googleapis.com/auth/drive.readonly') ? 'read' : 'none',
      detail: 'Google Drive files.',
    },
    {
      label: 'Sheets',
      state: has('https://www.googleapis.com/auth/spreadsheets') ? 'write' : has('https://www.googleapis.com/auth/spreadsheets.readonly') ? 'read' : 'none',
      detail: 'Google Sheets spreadsheets.',
    },
    {
      label: 'Docs',
      state: has('https://www.googleapis.com/auth/documents') ? 'write' : has('https://www.googleapis.com/auth/documents.readonly') ? 'read' : 'none',
      detail: 'Google Docs documents.',
    },
    {
      label: 'Slides',
      state: has('https://www.googleapis.com/auth/presentations') ? 'write' : has('https://www.googleapis.com/auth/presentations.readonly') ? 'read' : 'none',
      detail: 'Google Slides presentations.',
    },
    {
      label: 'Calendar',
      state: has('https://www.googleapis.com/auth/calendar') ? 'write' : has('https://www.googleapis.com/auth/calendar.readonly') ? 'read' : 'none',
      detail: 'Google Calendar events.',
    },
  ]
  // Tasks isn't offered by the current Add-account flow at all, so it only
  // ever shows up here (green) on a connection that already carries it from
  // before that flow existed — never as a "not authorized" gray row, since
  // there both would be nothing to reconnect toward and nothing to grant it.
  if (has('https://www.googleapis.com/auth/tasks', 'https://www.googleapis.com/auth/tasks.readonly')) {
    rows.push({
      label: 'Tasks (legacy)',
      state: has('https://www.googleapis.com/auth/tasks') ? 'write' : 'read',
      detail: 'Google Tasks — carried over from before this connector had a Drive/Sheets/Docs/Slides/Calendar picker; not requestable from Add account today.',
    })
  }
  return rows
}

const GMAIL_CAPABILITY_STATE_STYLE: Record<GmailCapabilityState, string> = {
  write: 'border-green-600/40 bg-green-500/15 text-green-700 dark:border-green-500/40 dark:bg-green-500/10 dark:text-green-400',
  read: 'border-amber-600/40 bg-amber-500/15 text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-400',
  none: 'border-border bg-transparent text-muted-foreground/50',
}

const GMAIL_CAPABILITY_STATE_LABEL: Record<GmailCapabilityState, string> = {
  write: 'read + write',
  read: 'read-only',
  none: 'not authorized',
}

// Names the CLI backend a GmailAuthStatus was computed against, so the UI
// never assumes gws — a deployment may be configured for gog instead.
function gmailBackendLabel(backend: string | undefined): { name: string; install: string } {
  if (backend === 'gog') return { name: 'gog', install: 'gogcli' }
  return { name: 'gws', install: '@googleworkspace/cli' }
}

type GmailNotificationsBots = Pick<WorkflowBots,
  | 'readOnly'
  | 'gmailConfig' | 'setGmailConfig' | 'gmailBlockedText' | 'setGmailBlockedText'
  | 'gmailLoading' | 'gmailSaving' | 'gmailTesting' | 'gmailError' | 'gmailSuccess' | 'gmailTestResult'
  | 'gmailBlockedDefaults' | 'gmailDefaultIsBlocked' | 'gmailCanEnable' | 'gmailHasChanges' | 'saveGmail' | 'testGmail'
  | 'gmailConnections' | 'gmailConnectionsBusy' | 'gmailAuthPending' | 'gmailAuthUrl'
  | 'runGmailConnectionAction' | 'connectGmailAccount'
  | 'gmailOAuthClientsBusy' | 'gmailOAuthClientError'
  | 'gmailNewClientEmail' | 'setGmailNewClientEmail'
  | 'createGmailOAuthClient' | 'removeGmailMailboxAndClient'
>

// Shown while a sign-in is in flight, so the link can be pasted into a
// different Chrome profile than the one the auto-opened tab landed in —
// copying the address bar out of that tab does not work (see gmailAuthUrl's
// comment in useWorkflowBots.ts for why).
function SignInLinkBox({ url }: { url: string }) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(url)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
    window.setTimeout(() => setCopyState('idle'), 2000)
  }

  return (
    <div className="mt-2 rounded-md border border-border bg-muted/40 p-2">
      <p className="text-xs text-muted-foreground">
        A tab opened in your default Chrome profile. If this mailbox lives in a different profile,
        open that profile and paste this link there — copying the address bar out of the tab that
        opened will not work, since Google ties it to the profile it started in.
      </p>
      <div className="mt-1.5 flex items-center gap-2">
        <code className="flex-1 truncate rounded bg-background px-2 py-1 font-mono text-[11px] text-muted-foreground">
          {url}
        </code>
        <Button
          variant="outline"
          size="sm"
          onClick={handleCopy}
          className="shrink-0"
        >
          {copyState === 'copied' ? 'Copied!' : copyState === 'failed' ? 'Copy failed' : 'Copy link'}
        </Button>
      </div>
    </div>
  )
}

export function GmailNotifications({ bots, workspacePath, scopeNoun = 'workflow', onAsk, platformConnect }: {
  bots: GmailNotificationsBots
  workspacePath: string | null
  scopeNoun?: 'workflow' | 'project'
  /** When the server has a Google app, adding an account is this one sign-in form: no client
   *  file to upload, so the upload form and its setup guide are not shown at all. */
  platformConnect?: ReactNode
  onAsk?: (message: string) => void | Promise<void>
}) {
  const {
    readOnly,
    gmailConfig, setGmailConfig, gmailBlockedText, setGmailBlockedText,
    gmailLoading, gmailSaving, gmailTesting, gmailError, gmailSuccess, gmailTestResult,
    gmailBlockedDefaults, gmailDefaultIsBlocked, gmailCanEnable, gmailHasChanges, saveGmail, testGmail,
    gmailConnections, gmailConnectionsBusy, gmailAuthPending, gmailAuthUrl,
    runGmailConnectionAction, connectGmailAccount,
    gmailOAuthClientsBusy, gmailOAuthClientError,
    gmailNewClientEmail, setGmailNewClientEmail,
    createGmailOAuthClient, removeGmailMailboxAndClient,
  } = bots

  const handleRemoveMailbox = (conn: { id: string; display_name: string; client_name?: string }) => {
    if (!window.confirm(`Remove "${conn.display_name}"? This also removes its OAuth client if no other account uses it.`)) return
    void removeGmailMailboxAndClient(conn.id, conn.client_name || '')
  }

  const [newClientFile, setNewClientFile] = useState<File | null>(null)
  // Off by default: notifications only ever send, so the consent screen asks
  // for gmail.send alone unless the operator deliberately widens it here.
  const [newClientAllowRead, setNewClientAllowRead] = useState(false)
  // Independent and off by default: notification delivery may always send,
  // but project agents may draft/send/reply only after this opt-in is granted.
  const [newClientAllowAgentWrite, setNewClientAllowAgentWrite] = useState(false)
  // Additional Google Workspace services (Drive, Sheets, Docs, Slides,
  // Calendar...) the new mailbox may also be authorized for, beyond Gmail.
  // Keyed by service id; { write: false } means "selected, read-only" —
  // absence means not selected at all. Read-only is the default access
  // level for the same reason Gmail read is opt-in: the safer grant.
  const [newClientServices, setNewClientServices] = useState<Record<string, { write: boolean }>>({})
  const [serviceCatalog, setServiceCatalog] = useState<Record<string, string> | null>(null)

  // Edit-access panel for an EXISTING connection — same shape as the
  // newClient* state above (reused deliberately, same checkbox UI), but this
  // only ever edits one connection at a time and seeds from that
  // connection's current stored values when opened. Applying the selection
  // persists it and immediately starts a fresh Google consent flow; scope
  // changes cannot take effect without reconnecting.
  const [editingGrantsConnId, setEditingGrantsConnId] = useState<string | null>(null)
  const [editAllowRead, setEditAllowRead] = useState(false)
  const [editAllowAgentWrite, setEditAllowAgentWrite] = useState(false)
  const [editServices, setEditServices] = useState<Record<string, { write: boolean }>>({})

  const openEditGrants = (conn: GmailConnection) => {
    setEditingGrantsConnId(conn.id)
    setEditAllowRead(conn.allow_read_access ?? false)
    setEditAllowAgentWrite(conn.allow_agent_write_access ?? false)
    const seeded: Record<string, { write: boolean }> = {}
    for (const grant of conn.services || []) {
      seeded[grant.service] = { write: grant.write ?? false }
    }
    setEditServices(seeded)
  }

  const reconnectWithEditedGrants = async (conn: GmailConnection) => {
    const services: GoogleServiceGrant[] = Object.entries(editServices).map(([service, grant]) => ({ service, write: grant.write }))
    const ok = await runGmailConnectionAction(conn.id, () => agentApi.updateGmailConnection(conn.id, {
      allow_read_access: editAllowRead,
      allow_agent_write_access: editAllowAgentWrite,
      services,
      services_set: true,
    }))
    if (ok) {
      setEditingGrantsConnId(null)
      await connectGmailAccount(conn.id)
    }
  }
  useEffect(() => {
    let cancelled = false
    agentApi.getGoogleServiceCatalog().then(catalog => { if (!cancelled) setServiceCatalog(catalog) }).catch(() => {
      // Best-effort: the "add account" form still works Gmail-only if this fails.
    })
    return () => { cancelled = true }
  }, [])
  const [newClientParseError, setNewClientParseError] = useState<string | null>(null)
  // Collapsed by default once at least one account exists — no reason to
  // keep the upload form permanently on screen once the common case (one
  // mailbox already set up) is done.
  const [showAddClientForm, setShowAddClientForm] = useState(false)

  const handleAddOAuthClient = async (): Promise<boolean> => {
    if (!newClientFile) return false
    setNewClientParseError(null)
    let parsed: unknown
    try {
      parsed = JSON.parse(await newClientFile.text())
    } catch {
      setNewClientParseError('That file is not valid JSON — download the client_secret.json Google Cloud gave you and upload it unmodified.')
      return false
    }
    const services: GoogleServiceGrant[] = Object.entries(newClientServices).map(([service, grant]) => ({ service, write: grant.write }))
    // createGmailOAuthClient derives the internal client name from this
    // email — asking for the mailbox directly is what an operator actually
    // thinks in terms of, not an arbitrary label for the Google Cloud app.
    const ok = await createGmailOAuthClient(gmailNewClientEmail.trim(), parsed, newClientAllowRead, newClientAllowAgentWrite, services)
    if (ok) {
      setNewClientFile(null)
      setNewClientAllowRead(false)
      setNewClientAllowAgentWrite(false)
      setNewClientServices({})
    }
    return ok
  }

  return (
    <div className="space-y-4">
      {gmailLoading ? (
        <div className="flex items-center justify-center py-12"><Loader2 className="w-8 h-8 animate-spin text-primary" /></div>
      ) : (
        <>
          <FormSection
            title={
              <span className="flex items-center gap-2">
                <Mail className="h-4 w-4 shrink-0 text-muted-foreground" />
                <span>Gmail</span>
                <span className={`inline-flex items-center gap-1 text-[11px] font-medium ${gmailConfig.enabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>
                  <span className={`h-1.5 w-1.5 rounded-full ${gmailConfig.enabled ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
                  {gmailConfig.enabled ? 'On' : 'Off'}
                </span>
              </span>
            }
            description={platformConnect
              ? 'Your Google accounts for this project. The agent uses them through the server; the default one sends notifications.'
              : 'Sending accounts used for notifications. Uploading the Google Cloud client file signs in that mailbox; a workflow may select an account, otherwise the default is used.'}
            actions={
              <AskAIButton
                workspacePath={workspacePath}
                onAsk={onAsk}
                label={scopeNoun === 'project' ? 'Ask Crew to set up Gmail' : 'Ask Builder to set up Gmail'}
                message="Help me set up Gmail notifications: connect a sending account (Google Cloud OAuth client upload and Google sign-in), choose default recipients, and send a test email. Guide me through the settings UI without requesting secrets in chat."
              />
            }
          >
              {!platformConnect && !(gmailConfig.auth.authenticated && gmailConfig.auth.has_gmail_scope) && gmailConnections.length === 0 && (
                <Card className="border-amber-300 bg-amber-50 p-4 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-100">
                  <div className="flex gap-2"><AlertTriangle className="h-4 w-4 flex-shrink-0" /><div><strong>No account connected yet.</strong> Add a sending account below and sign in with Google. <code>{gmailBackendLabel(gmailConfig.auth.backend).install}</code> must be installed on the server host.</div></div>
                </Card>
              )}

              {!platformConnect && <GmailSetupGuide backend={gmailConfig.auth.backend} />}

                {gmailOAuthClientError && <StatusBanner tone="error">{gmailOAuthClientError}</StatusBanner>}
                {newClientParseError && <StatusBanner tone="error">{newClientParseError}</StatusBanner>}

                {gmailConnections.length === 0 ? (
                  <p className="text-xs text-muted-foreground">{platformConnect ? 'No Google account connected yet.' : 'No sending accounts yet — add one below.'}</p>
                ) : platformConnect ? (
                  <GoogleAccountList
                    connections={gmailConnections}
                    busyId={gmailConnectionsBusy}
                    readOnly={readOnly}
                    onSendTest={conn => runGmailConnectionAction(conn.id, () => agentApi.testGmailConnectionById(conn.id, gmailConfig.default_to || undefined))}
                    onToggle={conn => runGmailConnectionAction(conn.id, () => agentApi.updateGmailConnection(conn.id, { enabled: !conn.enabled }))}
                    onReconnect={conn => connectGmailAccount(conn.id)}
                    onRemove={conn => handleRemoveMailbox(conn)}
                  />
                ) : (
                  <ul className="space-y-2">
                    {gmailConnections.map(conn => (
                      <li key={conn.id} className="rounded-md border border-border p-3">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className={`h-2 w-2 flex-shrink-0 rounded-full ${conn.ready ? 'bg-green-500' : conn.auth?.checking ? 'bg-muted-foreground' : 'bg-amber-500'}`} />
                          <span className="text-sm font-medium">{conn.display_name}</span>
                          {conn.is_default && <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">Default</span>}
                          {!conn.enabled && <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">Disabled</span>}
                          <span
                            className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground"
                            title={conn.allow_read_access ? 'Authorized to send and to read/search this mailbox' : 'Authorized to send only — cannot read this mailbox'}
                          >
                            {conn.allow_read_access ? 'Send + read' : 'Send only'}
                          </span>
                          {conn.allow_agent_write_access && (
                            <span
                              className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground"
                              title="Agents may create Gmail drafts and send or reply after Google grants gmail.compose"
                            >
                              Agent drafts + send
                            </span>
                          )}
                          {(conn.services || []).map(grant => (
                            <span
                              key={grant.service}
                              className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground"
                              title={`Authorized for ${serviceCatalog?.[grant.service] || grant.service} (${grant.write ? 'read + write' : 'read-only'}) — a workflow can use this via the google_workspace_cli tool`}
                            >
                              {serviceCatalog?.[grant.service] || grant.service}{grant.write ? '' : ' (ro)'}
                            </span>
                          ))}
                          <span className="ml-auto text-xs text-muted-foreground">
                            {conn.auth?.checking ? 'Checking…' : conn.ready ? 'Connected' : 'Not connected'}
                          </span>
                        </div>
                        <p className="mt-1 font-mono text-xs text-muted-foreground">
                          {conn.email || 'Address not known yet'}
                          {conn.client_name && <span className="ml-2 text-muted-foreground/70">via {conn.client_name}</span>}
                        </p>
                        {conn.auth?.scopes && conn.auth.scopes.length > 0 && (
                          <div className="mt-1.5">
                            <p className="text-[10px] uppercase tracking-wide text-muted-foreground/70">
                              Currently authorized (green = read+write, amber = read-only, gray = not granted)
                            </p>
                            <p className="mt-1 flex flex-wrap gap-1">
                              {gmailCapabilityMatrix(conn.auth.scopes).map(row => (
                                <span
                                  key={row.label}
                                  className={`rounded border px-1.5 py-0.5 text-[10px] ${GMAIL_CAPABILITY_STATE_STYLE[row.state]}`}
                                  title={`${row.detail} Currently: ${GMAIL_CAPABILITY_STATE_LABEL[row.state]}.`}
                                >
                                  {row.label}
                                </span>
                              ))}
                            </p>
                            <details className="mt-1">
                              <summary className="cursor-pointer text-[10px] text-muted-foreground/70 hover:text-muted-foreground">
                                Raw granted scopes ({conn.auth.scopes.length})
                              </summary>
                              <p className="mt-1 flex flex-wrap gap-1">
                                {conn.auth.scopes.map(scope => {
                                  const formatted = formatGmailScope(scope)
                                  return (
                                    <span key={scope} className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground" title={formatted.title}>
                                      {formatted.label}
                                    </span>
                                  )
                                })}
                              </p>
                            </details>
                          </div>
                        )}

                        <div className="mt-2 flex flex-wrap gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => connectGmailAccount(conn.id)}
                            disabled={readOnly || gmailAuthPending !== null}
                            title={readOnly ? READ_ONLY_TITLE : undefined}
                            className="border-primary text-primary hover:bg-primary/10 hover:text-primary"
                          >
                            {gmailAuthPending === conn.id ? 'Waiting for Google…' : conn.ready ? 'Reconnect' : 'Sign in with Google'}
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => runGmailConnectionAction(conn.id, () => agentApi.setDefaultGmailConnection(conn.id))}
                            disabled={readOnly || conn.is_default || !conn.enabled || gmailConnectionsBusy === conn.id}
                            title={readOnly ? READ_ONLY_TITLE : undefined}
                          >
                            Make default
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => runGmailConnectionAction(conn.id, () => agentApi.testGmailConnectionById(conn.id, gmailConfig.default_to || undefined))}
                            disabled={readOnly || gmailConnectionsBusy === conn.id}
                            title={readOnly ? READ_ONLY_TITLE : undefined}
                          >
                            Send test
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => runGmailConnectionAction(conn.id, () => agentApi.updateGmailConnection(conn.id, { enabled: !conn.enabled }))}
                            disabled={readOnly || gmailConnectionsBusy === conn.id}
                            title={readOnly ? READ_ONLY_TITLE : undefined}
                          >
                            {conn.enabled ? 'Disable' : 'Enable'}
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => editingGrantsConnId === conn.id ? setEditingGrantsConnId(null) : openEditGrants(conn)}
                            disabled={readOnly || gmailConnectionsBusy === conn.id}
                            title={readOnly ? READ_ONLY_TITLE : 'Change what this connection is authorized for. Saving only updates the request — Google requires a fresh Reconnect afterward for it to take effect.'}
                          >
                            {editingGrantsConnId === conn.id ? 'Cancel edit' : 'Edit access'}
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => handleRemoveMailbox(conn)}
                            disabled={readOnly || gmailConnectionsBusy === conn.id}
                            title={readOnly ? READ_ONLY_TITLE : undefined}
                            className="ml-auto text-red-600 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300"
                          >
                            Remove
                          </Button>
                        </div>

                        {editingGrantsConnId === conn.id && (
                          <div className="mt-2 space-y-1.5 rounded-md border border-border bg-muted/20 p-2">
                            <div className="flex items-start gap-2 text-xs text-muted-foreground">
                              <Checkbox
                                id={`edit-allow-read-${conn.id}`}
                                checked={editAllowRead}
                                disabled={readOnly}
                                onCheckedChange={value => setEditAllowRead(value === true)}
                                className="mt-0.5"
                              />
                              <label htmlFor={`edit-allow-read-${conn.id}`}>Also allow <strong>reading</strong> this mailbox</label>
                            </div>
                            <div className="flex items-start gap-2 text-xs text-muted-foreground">
                              <Checkbox
                                id={`edit-allow-agent-write-${conn.id}`}
                                checked={editAllowAgentWrite}
                                disabled={readOnly}
                                onCheckedChange={value => setEditAllowAgentWrite(value === true)}
                                className="mt-0.5"
                              />
                              <label htmlFor={`edit-allow-agent-write-${conn.id}`}>
                                <strong>Allow agents to create drafts and send/reply</strong>
                                <span className="block text-[11px] text-muted-foreground/80">Off by default. Notification emails are separate and do not require this.</span>
                              </label>
                            </div>
                            {serviceCatalog && Object.keys(serviceCatalog).length > 0 && (
                              <div className="space-y-1.5 border-t border-border pt-1.5">
                                {Object.entries(serviceCatalog).map(([service, label]) => {
                                  const grant = editServices[service]
                                  return (
                                    <div key={service} className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                                      <div className="flex items-center gap-2">
                                        <Checkbox
                                          id={`edit-service-${conn.id}-${service}`}
                                          checked={!!grant}
                                          disabled={readOnly}
                                          onCheckedChange={value => setEditServices(prev => {
                                            const next = { ...prev }
                                            if (value === true) next[service] = { write: false }
                                            else delete next[service]
                                            return next
                                          })}
                                        />
                                        <label htmlFor={`edit-service-${conn.id}-${service}`}>{label}</label>
                                      </div>
                                      {grant && (
                                        <div
                                          className="flex items-center gap-1.5 pl-1 text-[11px]"
                                          title="Off (read-only) is the safer default. Turn on only if a workflow must create or edit, not just read. Changing this requires Reconnect below to take effect."
                                        >
                                          <Checkbox
                                            id={`edit-service-${conn.id}-${service}-write`}
                                            checked={grant.write}
                                            disabled={readOnly}
                                            onCheckedChange={value => setEditServices(prev => ({ ...prev, [service]: { write: value === true } }))}
                                          />
                                          <label htmlFor={`edit-service-${conn.id}-${service}-write`}>
                                            allow write access{grant.write ? '' : ' (off = read-only)'}
                                          </label>
                                        </div>
                                      )}
                                    </div>
                                  )
                                })}
                              </div>
                            )}
                            <div className="flex gap-2 pt-1">
                              <Button
                                onClick={() => reconnectWithEditedGrants(conn)}
                                disabled={readOnly || gmailConnectionsBusy === conn.id || gmailAuthPending !== null}
                                title={readOnly ? READ_ONLY_TITLE : undefined}
                              >
                                {gmailConnectionsBusy === conn.id
                                  ? <><Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />Preparing…</>
                                  : 'Reconnect with selected access'}
                              </Button>
                              <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setEditingGrantsConnId(null)}
                              >
                                Cancel
                              </Button>
                            </div>
                          </div>
                        )}

                        {gmailAuthPending === conn.id && gmailAuthUrl && (
                          <SignInLinkBox url={gmailAuthUrl} />
                        )}
                      </li>
                    ))}
                  </ul>
                )}

                {platformConnect ? platformConnect : (
<>
                {gmailConnections.length > 0 && !showAddClientForm ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setShowAddClientForm(true)}
                    disabled={readOnly}
                    title={readOnly ? READ_ONLY_TITLE : undefined}
                  >
                    + Add account
                  </Button>
                ) : (
                  <div className="flex flex-wrap items-center gap-2 border-t border-border pt-3">
                    <Input
                      type="email"
                      autoFocus={gmailConnections.length > 0}
                      value={gmailNewClientEmail}
                      onChange={event => setGmailNewClientEmail(event.target.value)}
                      disabled={readOnly}
                      placeholder="Mailbox to connect (e.g. you@example.com)"
                      className="flex-1"
                      aria-label="Mailbox to connect"
                    />
                    <Input
                      type="file"
                      accept="application/json"
                      disabled={readOnly}
                      onChange={event => setNewClientFile(event.target.files?.[0] || null)}
                      className="flex-1"
                      aria-label="Google Cloud client file"
                    />
                    <div
                      className="flex basis-full items-start gap-2 text-xs text-muted-foreground"
                      title="Send-only is the default and is all notifications need. Read access lets a workflow search or read this mailbox too; it is fixed at sign-in, so changing it later means reconnecting."
                    >
                      <Checkbox
                        id="new-client-allow-read"
                        checked={newClientAllowRead}
                        disabled={readOnly}
                        onCheckedChange={value => setNewClientAllowRead(value === true)}
                        className="mt-0.5"
                      />
                      <label htmlFor="new-client-allow-read">
                        Also allow <strong>reading</strong> this mailbox
                        <span className="block text-[11px] text-muted-foreground/80">Off by default: sending is all notifications need. Turn on only if a workflow must read or search mail.</span>
                      </label>
                    </div>
                    <div
                      className="flex basis-full items-start gap-2 text-xs text-muted-foreground"
                      title="Off by default. When enabled, reconnect requests gmail.compose and agents may create drafts and send or reply."
                    >
                      <Checkbox
                        id="new-client-allow-agent-write"
                        checked={newClientAllowAgentWrite}
                        disabled={readOnly}
                        onCheckedChange={value => setNewClientAllowAgentWrite(value === true)}
                        className="mt-0.5"
                      />
                      <label htmlFor="new-client-allow-agent-write">
                        <strong>Allow agents to create drafts and send/reply</strong>
                        <span className="block text-[11px] text-muted-foreground/80">Off by default. Notification emails can still send without giving agents this permission.</span>
                      </label>
                    </div>
                    {serviceCatalog && Object.keys(serviceCatalog).length > 0 && (
                      <div className="basis-full space-y-1.5 border-t border-border pt-2">
                        <p className="text-xs text-muted-foreground">Also connect other Google Workspace services for this mailbox — a workflow can then use them directly. Read-only by default; fixed at sign-in like above.</p>
                        {Object.entries(serviceCatalog).map(([service, label]) => {
                          const grant = newClientServices[service]
                          return (
                            <div key={service} className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                              <div className="flex items-center gap-2">
                                <Checkbox
                                  id={`new-client-service-${service}`}
                                  checked={!!grant}
                                  disabled={readOnly}
                                  onCheckedChange={value => setNewClientServices(prev => {
                                    const next = { ...prev }
                                    if (value === true) next[service] = { write: false }
                                    else delete next[service]
                                    return next
                                  })}
                                />
                                <label htmlFor={`new-client-service-${service}`}>{label}</label>
                              </div>
                              {grant && (
                                <div className="flex items-center gap-1.5 pl-1 text-[11px]" title="Off (read-only) is the safer default. Turn on only if a workflow must create or edit, not just read.">
                                  <Checkbox
                                    id={`new-client-service-${service}-write`}
                                    checked={grant.write}
                                    disabled={readOnly}
                                    onCheckedChange={value => setNewClientServices(prev => ({ ...prev, [service]: { write: value === true } }))}
                                  />
                                  <label htmlFor={`new-client-service-${service}-write`}>
                                    allow write access{grant.write ? '' : ' (off = read-only)'}
                                  </label>
                                </div>
                              )}
                            </div>
                          )
                        })}
                      </div>
                    )}
                    <Button
                      variant="outline"
                      disabled={readOnly || !gmailNewClientEmail.trim() || !newClientFile || gmailOAuthClientsBusy}
                      title={readOnly ? READ_ONLY_TITLE : undefined}
                      onClick={async () => {
                        if (await handleAddOAuthClient()) setShowAddClientForm(false)
                      }}
                    >
                      {gmailOAuthClientsBusy ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Add & sign in'}
                    </Button>
                  </div>
                )}
</>
)}
          </FormSection>
          <FormSection
            title="Delivery settings"
            description={<>Account-wide one-way email delivery, shared by <code>notify_user</code> across every workflow and product chat. Turn this off to stop all outbound email. Email replies do not resume an agent.</>}
          >
            {gmailError && <StatusBanner tone="error">{gmailError}</StatusBanner>}
            {gmailSuccess && <StatusBanner tone="success">{gmailSuccess}</StatusBanner>}
            <Card className="p-4">
              <ToggleRow
                label="Enable Gmail"
                description="Available to notify_user across workflows and product chats."
                checked={gmailConfig.enabled}
                onCheckedChange={checked => setGmailConfig({ ...gmailConfig, enabled: checked })}
                disabled={readOnly || (!gmailConfig.enabled && !gmailCanEnable)}
                disabledTitle={readOnly ? READ_ONLY_TITLE : undefined}
              />
              {!gmailConfig.enabled && !gmailCanEnable && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Sign in a Gmail account above to enable; it switches on automatically once one is connected.</p>}
            </Card>
            <Card className="space-y-3 p-4">
                <div>
                  <Label className="mb-2 block">Default recipients</Label>
                  {/* Deliberately type="text": type="email" rejects a comma-separated
                      list, which is the whole point of this field. */}
                  <Input type="text" inputMode="email" value={gmailConfig.default_to || ''} onChange={event => setGmailConfig({ ...gmailConfig, default_to: event.target.value })} disabled={readOnly} placeholder="you@example.com, teammate@example.com" aria-label="Default recipients" />
                  <p className="mt-1 text-xs text-muted-foreground">Where notifications are emailed when a workflow has no recipients of its own. Separate several addresses with commas.</p>
                </div>
                <div>
                  <Label className="mb-2 block">Disallowed recipients</Label>
                  <Textarea value={gmailBlockedText} onChange={event => setGmailBlockedText(event.target.value)} disabled={readOnly} rows={3} placeholder="blocked@example.com, no-notify@example.com" className="font-mono" aria-label="Disallowed recipients" />
                  {gmailDefaultIsBlocked && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{gmailBlockedDefaults.join(', ')} {gmailBlockedDefaults.length === 1 ? 'is' : 'are'} both a default recipient and disallowed.</p>}
                </div>
              </Card>
              <Button variant="outline" onClick={testGmail} disabled={readOnly || gmailTesting || !gmailConfig.default_to || gmailDefaultIsBlocked} title={readOnly ? READ_ONLY_TITLE : undefined} className="w-full">{gmailTesting ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" />Sending…</> : 'Send test email'}</Button>
              {gmailTestResult && <Card className={`p-3 text-sm ${gmailTestResult.success ? 'border-green-300 bg-green-50 text-green-700 dark:border-green-700 dark:bg-green-900/20 dark:text-green-300' : 'border-red-300 bg-red-50 text-red-700 dark:border-red-700 dark:bg-red-900/20 dark:text-red-300'}`}>{gmailTestResult.message}</Card>}
              <div className="flex justify-end">
                <Button onClick={saveGmail} disabled={readOnly || !gmailHasChanges || gmailSaving || gmailLoading || gmailDefaultIsBlocked || (gmailConfig.enabled && !gmailCanEnable)} title={readOnly ? READ_ONLY_TITLE : undefined}>{gmailSaving ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" />Saving…</> : 'Save'}</Button>
              </div>
          </FormSection>
          </>
        )}
    </div>
  )
}
