import { projectProductForPath } from '../../../products/work/projectProduct'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { agentApi } from '../../../services/api'
import { useWorkflowManifestStore } from '../../../stores/useWorkflowManifestStore'
import { useAuthStore } from '../../../stores/useAuthStore'
import { useCanWriteWorkflow } from '../../../hooks/useCanWriteWorkflow'
import type {
    BotRoute, GmailConfigRequest, GmailConfigResponse, GmailConnection, GmailOAuthClient, GmailTestResponse,
    GoogleServiceGrant, SlackConfig, SlackConfigRequest, SlackTestResponse, SlackUsableBot, ChannelRoute, WhatsAppRoute, WhatsAppStatus,
} from '../../../services/api-types'
import { routeId, type ChannelKind, type WorkflowRoute } from './types'
import { gmailOAuthAttemptCompleted } from './gmailOAuthState'
import { slackConnectionStatus } from './slackConnectionStatus'
import { resolveWorkflowSlackConnection, sameBotWorkspacePath, selectedWorkflowSlackReady } from './slackWorkflowConnection'
import { withSlackDryRun } from './slackDryRun'

type WaRoute = WhatsAppRoute

const SLUG_RE = /^[a-z0-9-]+$/
const SLACK_CHANNEL_RE = /^[CDG][A-Z0-9]{2,}$/

// Server errors come back as plain-text bodies; prefer them over axios's
// generic "Request failed with status code 409".
const serverErrorMessage = (err: unknown, fallback: string): string => {
  const data = (err as { response?: { data?: unknown } })?.response?.data
  if (typeof data === 'string' && data.trim()) return data.trim()
  return err instanceof Error ? err.message : fallback
}

const gmailErrorMessage = (err: unknown, fallback: string): string => {
  const data = (err as { response?: { data?: { error?: string; required_access?: string } } })?.response?.data
  if (data?.required_access === 'admin') return 'Only an admin can manage shared Gmail accounts and delivery settings.'
  if (typeof data?.error === 'string' && data.error.trim()) return data.error.trim()
  return serverErrorMessage(err, fallback)
}

const normalizeGmailEmails = (values: string | string[] | undefined): string[] => {
  const source = Array.isArray(values) ? values : [values || '']
  const seen = new Set<string>()
  const result: string[] = []
  for (const raw of source) {
    for (const part of String(raw).split(/[\s,;]+/)) {
      const email = part.trim().toLowerCase()
      if (!email || seen.has(email)) continue
      seen.add(email)
      result.push(email)
    }
  }
  return result
}

const emptyGmailConfig: GmailConfigResponse = {
  enabled: false,
  default_to: '',
  auth: { gws_installed: false, authenticated: false, has_gmail_scope: false },
  ready: false,
}

// All state, loaders, effects, routing writes, and handlers for the Bots
// panel. The panel and its children are composition over this hook's result.
export type BotRouteTarget = {
  profileId: string
  conversationKey: string
  label: string
}

export function useWorkflowBots(workspacePath: string | null, target?: BotRouteTarget, section: 'bots' | 'email' | 'all' = 'all') {
  // A Code's Google accounts are its own: list and connect only those.
  const gmailScopeWorkspace = projectProductForPath(workspacePath || undefined)?.profileId === 'code' ? (workspacePath || undefined) : undefined
  // ── Workflow identity ─────────────────────────────────────────────────────
  // Crew passes a fresh target object when chat state changes. Effects must
  // depend on its values so typing does not reload the Slack setup panel.
  const targetProfileId = target?.profileId
  const targetConversationKey = target?.conversationKey
  const targetLabel = target?.label
  const hasTarget = target !== undefined
  const workflows = useWorkflowManifestStore(state => state.workflows)
  const refreshWorkflows = useWorkflowManifestStore(state => state.refreshWorkflows)
  const updateWorkflow = useWorkflowManifestStore(state => state.updateWorkflow)
  const workflow = useMemo(
    () => (workspacePath ? workflows.find(w => w.workspace_path === workspacePath) : undefined),
    [workflows, workspacePath],
  )
  const workflowId = targetConversationKey || workflow?.manifest.id || null
  const workflowLabel = (id: string) => workflows.find(w => w.manifest.id === id)?.manifest.label || id
  // Every write here lands in shared connector config immediately -- there's
  // no Save step for the panel to gate -- so each mutating control disables.
  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)
  const readOnly = hasTarget ? false : !canWriteWorkflow
  // Shared Gmail accounts and delivery settings are admin-managed on the server.
  // A Code owner manages its private accounts independently of the shared settings.
  const canManageSharedGmail = useAuthStore(state => state.user?.is_admin === true
    || (state.isMultiUserModeChecked && !state.isMultiUserMode))
  const userId = useAuthStore(state => state.user?.id)
  const codeOwner = /^_users\/([^/]+)\//.exec((workspacePath || '').replace(/^\/+/, ''))?.[1]
  const gmailConnectionsReadOnly = readOnly || (gmailScopeWorkspace
    ? !!codeOwner && codeOwner !== userId
    : !canManageSharedGmail)
  const gmailSettingsReadOnly = readOnly || !canManageSharedGmail
  const canRemoveGmailConnection = useCallback((connection: GmailConnection) =>
    !readOnly && (connection.can_remove ?? !gmailConnectionsReadOnly),
  [readOnly, gmailConnectionsReadOnly])
  // Slack apps are owner-managed (writers get a 403 server-side); readers see
  // everything disabled through readOnly as usual.
  const canManageWorkflowSlack = !readOnly && (workflow?.my_access || 'owner') === 'owner'
  // Pairing, unpairing, and naming your own WhatsApp numbers is
  // account-level: the backend keys the WhatsApp service by user ID and
  // requires no workflow write access, so read-only users can manage their
  // own devices. Shared state (routing slugs) stays behind readOnly.
  const canManageOwnWhatsAppDevices: boolean = true

  const routeMatchesTarget = useCallback((route: ChannelRoute | WaRoute) => {
    if (hasTarget) {
      return route.profile_id === targetProfileId && route.conversation_key === targetConversationKey
    }
    return route.workflow_id === workflowId
  }, [hasTarget, targetProfileId, targetConversationKey, workflowId])

  const routeForTarget = useCallback((): ChannelRoute => hasTarget ? {
    workspace_path: workspacePath || '',
    profile_id: targetProfileId,
    conversation_key: targetConversationKey,
    profile_label: targetLabel,
  } : {
    workflow_id: workflowId || '',
    workspace_path: workflow?.workspace_path || '',
  }, [hasTarget, targetProfileId, targetConversationKey, targetLabel, workflowId, workflow?.workspace_path, workspacePath])

  useEffect(() => {
    if (workflows.length === 0) void refreshWorkflows()
  }, [refreshWorkflows, workflows.length])

  // ── Panel navigation ──────────────────────────────────────────────────────
  const [setup, setSetup] = useState<ChannelKind | null>(null)
  const [expandedChip, setExpandedChip] = useState<string | null>(null)
  const [routeSaving, setRouteSaving] = useState<string | null>(null)
  const [routeError, setRouteError] = useState<string | null>(null)
  const [newSlackChannel, setNewSlackChannel] = useState('')
  const [newWaSlug, setNewWaSlug] = useState('')
  const [addError, setAddError] = useState<Partial<Record<ChannelKind, string>>>({})

  // ── Slack ─────────────────────────────────────────────────────────────────
  const [slackConfig, setSlackConfig] = useState<SlackConfig>({ enabled: false, bot_token: '', app_token: '' })
  const [slackOriginal, setSlackOriginal] = useState<SlackConfig>(slackConfig)
  const [slackLoading, setSlackLoading] = useState(true)
  const [slackSaving, setSlackSaving] = useState(false)
  const [slackTesting, setSlackTesting] = useState(false)
  const [slackError, setSlackError] = useState<string | null>(null)
  const [slackSuccess, setSlackSuccess] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<SlackTestResponse | null>(null)
  const [testReply, setTestReply] = useState<string | null>(null)
  const [pollingForReply, setPollingForReply] = useState(false)
  const [showBotToken, setShowBotToken] = useState(false)
  const [showAppToken, setShowAppToken] = useState(false)
  const [allowedEmails, setAllowedEmails] = useState('')
  const [emailsDirty, setEmailsDirty] = useState(false)
  const [emailsSaving, setEmailsSaving] = useState(false)
  const [emailsSaved, setEmailsSaved] = useState(false)

  // ── This workflow's own Slack app ─────────────────────────────────────────
  // Owners edit these drafts; admins own the platform default below instead.
  const [slackConnName, setSlackConnName] = useState('')
  const [slackConnBot, setSlackConnBot] = useState('')
  const [slackConnApp, setSlackConnApp] = useState('')
  const [slackConnEnabled, setSlackConnEnabled] = useState(true)
  const [slackConnSaving, setSlackConnSaving] = useState(false)
  const [slackConnTesting, setSlackConnTesting] = useState(false)
  const [slackConnTestResult, setSlackConnTestResult] = useState<SlackTestResponse | null>(null)
  const [slackConnConfirmDelete, setSlackConnConfirmDelete] = useState(false)
  const [slackConnShowBot, setSlackConnShowBot] = useState(false)
  const [slackConnShowApp, setSlackConnShowApp] = useState(false)
  // Bumped after saves so the form resyncs from the server without clobbering
  // typing on unrelated reloads (route adds reload the Slack config too).
  const [slackConnSyncKey, setSlackConnSyncKey] = useState(0)

  // ── WhatsApp ──────────────────────────────────────────────────────────────
  const [waStatus, setWaStatus] = useState<WhatsAppStatus | null>(null)
  const [waError, setWaError] = useState<string | null>(null)
  const [waRouting, setWaRouting] = useState<Record<string, WaRoute>>({})
  const [waRoutingError, setWaRoutingError] = useState<string | null>(null)
  const [qrBust, setQrBust] = useState<number>(() => Date.now())
  const [qrImageURL, setQrImageURL] = useState<string | null>(null)
  const [qrLoading, setQrLoading] = useState(false)
  const [qrError, setQrError] = useState<string | null>(null)
  const [waUnpairConfirmSlot, setWaUnpairConfirmSlot] = useState<string | null>(null)
  const [waUnpairingSlot, setWaUnpairingSlot] = useState<string | null>(null)
  const waPollingRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // ── Gmail (account-wide, shared by every workflow) ────────────────────────
  const [gmailConfig, setGmailConfig] = useState<GmailConfigResponse>(emptyGmailConfig)
  const [gmailOriginal, setGmailOriginal] = useState({ enabled: false, default_to: '', blocked_recipients: [] as string[] })
  const [gmailBlockedText, setGmailBlockedText] = useState('')
  const [gmailLoading, setGmailLoading] = useState(false)
  const [gmailChecking, setGmailChecking] = useState(false)
  const [gmailSaving, setGmailSaving] = useState(false)
  const [gmailTesting, setGmailTesting] = useState(false)
  const [gmailError, setGmailError] = useState<string | null>(null)
  const [gmailSuccess, setGmailSuccess] = useState<string | null>(null)
  const [gmailTestResult, setGmailTestResult] = useState<GmailTestResponse | null>(null)
  // Multi-account senders. Empty is the normal state for an install that has
  // not adopted connections, and the card then simply says so.
  const [gmailConnections, setGmailConnections] = useState<GmailConnection[]>([])
  const [gmailConnectionsBusy, setGmailConnectionsBusy] = useState<string | null>(null)
  // Named OAuth clients (the Google Cloud app a connection authorizes
  // under). Treated 1:1 with a sending account in the UI: registering one
  // creates its account and starts sign-in immediately
  // (createGmailOAuthClient), and removing the account also removes its
  // client (removeGmailMailboxAndClient) — upload+email is one action, not
  // two separate entities to manage. The backend still allows a client to
  // back more than one account (an older client created before this UI
  // simplification might), which is why removal only cascades when the
  // client truly has no other account left using it.
  const [gmailOAuthClients, setGmailOAuthClients] = useState<GmailOAuthClient[]>([])
  const [gmailOAuthClientsBusy, setGmailOAuthClientsBusy] = useState(false)
  const [gmailOAuthClientError, setGmailOAuthClientError] = useState<string | null>(null)
  // The email the operator intends to sign in with this client — used to
  // derive the client's internal name (slugified) instead of asking for an
  // arbitrary label, since the mailbox is what an operator actually thinks
  // in terms of. The email itself is not verified until sign-in; this is a
  // naming hint, not a claim about which account will actually authorize.
  const [gmailNewClientEmail, setGmailNewClientEmail] = useState('')
  // Set while a Google sign-in is in flight, so the row can say it is waiting.
  const [gmailAuthPending, setGmailAuthPending] = useState<string | null>(null)
  // The authorize URL handed to the browser, kept so the UI can offer it as
  // copyable text. openExternal/window.open land in whichever Chrome profile
  // is frontmost, which is often not the profile holding the mailbox being
  // connected. Copying the address bar out of that tab does not work: by
  // then Chrome has followed Google's redirects, and the bar holds a
  // continuation URL whose token is bound to the originating profile's
  // cookies — pasting it into another profile fails with a bare "400 ...
  // malformed". Only this original authorize URL is safe to paste anywhere.
  const [gmailAuthUrl, setGmailAuthUrl] = useState<string | null>(null)
  const [gmailTestedTo, setGmailTestedTo] = useState<string | null>(null)

  // ── Loaders ───────────────────────────────────────────────────────────────
  // The bot manager still enforces allowed_emails on every incoming message
  // (see bot_connector.go HandleIncomingMessage), so this stays editable here.
  const loadEmails = useCallback(async () => {
    try {
      const cfg = await agentApi.getBotConfig()
      if (Array.isArray(cfg.allowed_emails)) setAllowedEmails(cfg.allowed_emails.join(', '))
    } catch { /* ignore */ }
  }, [])

  // ── "One of my bots": workflow/crew bots this user manages ────────────────
  const [myBots, setMyBots] = useState<SlackUsableBot[]>([])
  const [newMyBotChannel, setNewMyBotChannel] = useState('')
  const [myBotSaving, setMyBotSaving] = useState<string | null>(null)
  const [myBotError, setMyBotError] = useState<string | null>(null)
  const loadMyBots = useCallback(async () => {
    try {
      const data = await agentApi.listUsableSlackBots()
      setMyBots(Array.isArray(data.bots) ? data.bots : [])
    } catch {
      // Listing is best effort: the option then explains how to add a bot.
      setMyBots([])
    }
  }, [])

  const [projectSlackSelectionId, setProjectSlackSelectionId] = useState<string>('')
  const loadSlack = useCallback(async () => {
    try {
      setSlackLoading(true)
      setSlackError(null)
      const [data] = await Promise.all([agentApi.getSlackFeedbackConfig(), loadMyBots()])
      setSlackConfig(data)
      setSlackOriginal(data)
      if (hasTarget && workspacePath) {
        try {
          const selection = await agentApi.getProjectSlackSelection(targetProfileId || '', workspacePath)
          setProjectSlackSelectionId(selection.slack_connection_id || '')
        } catch {
          setProjectSlackSelectionId('')
        }
      }
    } catch (err) {
      setSlackError(err instanceof Error ? err.message : 'Failed to load Slack configuration')
    } finally {
      setSlackLoading(false)
    }
  }, [hasTarget, targetProfileId, workspacePath, loadMyBots])

  const loadWaStatus = useCallback(async () => {
    try {
      const s = await agentApi.getWhatsAppStatus()
      setWaStatus(s)
      setWaError(null)
      return s
    } catch (err) {
      setWaError(err instanceof Error ? err.message : String(err))
      return null
    }
  }, [])

  const loadWaRouting = useCallback(async () => {
    try {
      const data = await agentApi.getWhatsAppRouting()
      setWaRouting(data.routing || {})
      setWaRoutingError(null)
    } catch (err) {
      setWaRoutingError(err instanceof Error ? err.message : String(err))
    }
  }, [])

  const refreshWaQR = useCallback(() => {
    setQrBust(Date.now())
  }, [])

  const loadGmailConnections = useCallback(async (attempt = 0) => {
    try {
      const data = await agentApi.listGmailConnections(gmailScopeWorkspace)
      const connections = data.connections || []
      setGmailConnections(connections)
      // The server answers the first read with "checking" while it runs each
      // account's auth check in the background (~5s per gws call) and never
      // pushes the result, so a row sat on "Checking…" with a "Sign in with
      // Google" button for an account that was already connected (RTS,
      // 2026-09-03). Re-read while anything is still checking, bounded so a
      // check that never settles cannot poll forever.
      if (attempt < 15 && connections.some(entry => entry.auth?.checking)) {
        window.setTimeout(() => { void loadGmailConnections(attempt + 1) }, 2000)
      }
    } catch {
      // A registry that cannot be read must not break the single-account view
      // this panel has always shown.
      setGmailConnections([])
    }
  }, [gmailScopeWorkspace])

  const loadGmailOAuthClients = useCallback(async () => {
    try {
      const data = await agentApi.listGmailOAuthClients()
      setGmailOAuthClients(data.clients || [])
    } catch {
      setGmailOAuthClients([])
    }
  }, [])

  // Every mutation re-reads the list rather than patching local state, so the
  // server stays the single source of truth for status and which is default.
  const runGmailConnectionAction = useCallback(
    async (id: string, action: () => Promise<unknown>): Promise<boolean> => {
      try {
        setGmailConnectionsBusy(id)
        setGmailError(null)
        await action()
        await loadGmailConnections()
        return true
      } catch (error) {
        setGmailError(gmailErrorMessage(error, 'Connection action failed'))
        return false
      } finally {
        setGmailConnectionsBusy(null)
      }
    },
    [loadGmailConnections],
  )

  // Open Google's consent screen and refresh once the account is connected.
  //
  // Electron blocks window.open, so prefer the desktop bridge that opens the
  // system browser. Signing in there is also better than a popup: people can
  // see the real accounts.google.com address bar, which is exactly what they
  // should check before typing a password.
  const connectGmailAccount = useCallback(async (id: string) => {
    try {
      setGmailError(null)
      setGmailAuthPending(id)
      setGmailAuthUrl(null)
      // Capture server state before starting. Reconnect often begins while
      // the old token is still healthy, so `ready === true` cannot prove the
      // new consent flow completed.
      const beforeData = await agentApi.listGmailConnections(gmailScopeWorkspace)
      const before = beforeData.connections.find(entry => entry.id === id)
      if (!before) throw new Error(`Gmail connection ${id} no longer exists.`)
      const { auth_url } = await agentApi.startGmailConnectionAuth(id)
      setGmailAuthUrl(auth_url)

      const electronAPI = (window as unknown as {
        electronAPI?: { openExternal?: (url: string) => void }
      }).electronAPI
      const popup = electronAPI?.openExternal
        ? (electronAPI.openExternal(auth_url), null)
        : window.open(auth_url, 'gmail-oauth', 'width=520,height=680')

      if (!electronAPI?.openExternal && !popup) {
        setGmailError('Allow pop-ups for this app, then try connecting again.')
        setGmailAuthPending(null)
        setGmailAuthUrl(null)
        return
      }

      // The callback lands on the server, not in this document, so there is no
      // event here to listen for — and with an external browser there is no
      // window to watch closing either. Poll until the server reports that
      // this attempt changed the persisted connection, not merely `ready`
      // from a credential that existed before Reconnect was clicked.
      //
      // The window matches the server's 15-minute pending-state TTL
      // (gmailOAuthPendingTTL) rather than guessing shorter: anyone whose
      // mailbox lives in a different Chrome profile has to open that profile
      // and paste the copied link across by hand, and giving up while the
      // server would still accept the callback strands them mid-sign-in.
      const startedAt = Date.now()
      let popupClosedNoticeShown = false
      let pollInFlight = false
      const timer = window.setInterval(async () => {
        if (pollInFlight) return
        pollInFlight = true
        const timedOut = Date.now() - startedAt > 15 * 60 * 1000
        if (popup?.closed && !popupClosedNoticeShown) {
          popupClosedNoticeShown = true
          // Closing the first popup often means the mailbox belongs to a
          // different browser profile. Keep the server attempt and copyable
          // link alive so it can be pasted there instead.
          setGmailError('The sign-in window was closed. Use the Copy link option below to continue in another browser profile.')
        }

        try {
          const data = await agentApi.listGmailConnections(gmailScopeWorkspace).catch(() => null)
          const after = data?.connections?.find(entry => entry.id === id)
          const connected = gmailOAuthAttemptCompleted(before, after)
          if (!connected && !timedOut) return

          window.clearInterval(timer)
          setGmailAuthPending(null)
          setGmailAuthUrl(null)
          setGmailError(connected ? null : 'Sign-in did not complete before the link expired. Try connecting again.')
          await loadGmailConnections()
        } finally {
          pollInFlight = false
        }
      }, 1500)
    } catch (error) {
      setGmailAuthPending(null)
      setGmailAuthUrl(null)
      setGmailError(gmailErrorMessage(error, 'Could not start Google sign-in'))
    }
  }, [gmailScopeWorkspace, loadGmailConnections])

  // Registers the OAuth client, creates its sending account, and immediately
  // opens Google's consent screen for it — upload + email + one click is the
  // whole flow; there's no separate "now click Sign in with Google" step to
  // remember, since 99% of the time the mailbox being added is the same one
  // the client's credentials are for.
  // allowReadAccess opts the new mailbox into gmail.readonly on top of
  // gmail.send. Defaults off: notifications only ever send, so the consent
  // screen asks for the minimum unless the operator deliberately widens it.
  // services requests additional Google Workspace scopes (Drive, Sheets...)
  // beyond Gmail — see GoogleServiceGrant. Also defaults to none.
  const createGmailOAuthClient = useCallback(async (
    email: string,
    clientSecretJson: unknown,
    allowReadAccess = false,
    allowAgentWriteAccess = false,
    services: GoogleServiceGrant[] = [],
  ) => {
    const trimmedEmail = email.trim()
    // Backend name pattern is lowercase letters/digits/hyphens only (it
    // becomes a directory name) — an email's @ and . don't qualify, so
    // derive a slug from it rather than asking the operator for a second,
    // arbitrary label.
    const name = trimmedEmail.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63)
    try {
      setGmailOAuthClientsBusy(true)
      setGmailOAuthClientError(null)
      const client = await agentApi.createGmailOAuthClient(name, clientSecretJson)
      const connection = await agentApi.createGmailConnection({
        ...(gmailScopeWorkspace ? { workspace_path: gmailScopeWorkspace } : {}),
        display_name: trimmedEmail,
        client_name: client.name,
        allow_read_access: allowReadAccess,
        allow_agent_write_access: allowAgentWriteAccess,
        services,
      })
      await Promise.all([loadGmailOAuthClients(), loadGmailConnections()])
      setGmailNewClientEmail('')
      void connectGmailAccount(connection.id)
      return true
    } catch (error) {
      setGmailOAuthClientError(gmailErrorMessage(error, 'Failed to register the OAuth client'))
      return false
    } finally {
      setGmailOAuthClientsBusy(false)
    }
  }, [gmailScopeWorkspace, loadGmailOAuthClients, loadGmailConnections, connectGmailAccount])

  // Account owners can remove their own credentials. Only an admin also
  // cleans up an unused shared OAuth client registration.
  const removeGmailMailboxAndClient = useCallback(async (connectionId: string, clientName: string) => {
    try {
      setGmailConnectionsBusy(connectionId)
      setGmailError(null)
      await agentApi.deleteGmailConnection(connectionId)
      const stillInUse = gmailConnections.some(c => c.id !== connectionId && c.client_name === clientName)
      if (canManageSharedGmail && !stillInUse && clientName) {
        await agentApi.deleteGmailOAuthClient(clientName).catch(() => {
          // Best-effort: the account is already gone either way, and an
          // orphaned client can still be removed later from its own row.
        })
      }
      await Promise.all([loadGmailConnections(), loadGmailOAuthClients()])
    } catch (error) {
      setGmailError(gmailErrorMessage(error, 'Failed to remove the sending account'))
    } finally {
      setGmailConnectionsBusy(null)
    }
  }, [canManageSharedGmail, gmailConnections, loadGmailConnections, loadGmailOAuthClients])

  const loadGmail = useCallback(async (background = false) => {
    try {
      if (background) setGmailChecking(true)
      else setGmailLoading(true)
      setGmailError(null)
      const data = await agentApi.getGmailFeedbackConfig()
      if (background) {
        setGmailConfig(current => ({ ...current, auth: data.auth, ready: data.ready }))
      } else {
        const blocked = normalizeGmailEmails(data.blocked_recipients)
        setGmailConfig({ ...data, blocked_recipients: blocked })
        setGmailBlockedText(blocked.join(', '))
        setGmailOriginal({ enabled: data.enabled, default_to: data.default_to || '', blocked_recipients: blocked })
      }
    } catch (error) {
      setGmailError(gmailErrorMessage(error, 'Failed to load Gmail configuration'))
    } finally {
      setGmailLoading(false)
      setGmailChecking(false)
    }
    void loadGmailConnections()
    void loadGmailOAuthClients()
  }, [loadGmailConnections, loadGmailOAuthClients])

  useEffect(() => {
    void loadEmails()
    if (section !== 'bots') {
      void loadGmail()
    }
    if (section !== 'email') {
      void loadSlack()
      void loadWaStatus()
      void loadWaRouting()
    }
  }, [section, loadEmails, loadGmail, loadSlack, loadWaRouting, loadWaStatus])

  // ── This workflow's Slack app: derived state ──────────────────────────────
  const canManageSlackDefault = slackOriginal.manage_default_allowed === true
  const slackSelection = useMemo(
    () => resolveWorkflowSlackConnection(
      slackOriginal.connections,
      workspacePath,
      hasTarget ? projectSlackSelectionId : workflow?.manifest.capabilities.slack_connection_id,
      slackOriginal.default_connection_id,
      targetProfileId ?? null,
    ),
    [slackOriginal.connections, slackOriginal.default_connection_id, hasTarget, targetProfileId, workspacePath, projectSlackSelectionId, workflow?.manifest.capabilities.slack_connection_id],
  )
  const slackOwnConnId = slackSelection.own?.id || null
  useEffect(() => {
    const own = slackSelection.own
    setSlackConnName(own?.display_name || '')
    setSlackConnBot(own?.bot_token || '')
    setSlackConnApp(own?.app_token || '')
    setSlackConnEnabled(own ? own.enabled : true)
    setSlackConnTestResult(null)
    setSlackConnConfirmDelete(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slackOwnConnId, slackConnSyncKey])
  const slackConnHasChanges = (() => {
    const own = slackSelection.own
    if (!own) return slackConnName.trim() !== '' || slackConnBot !== '' || slackConnApp !== '' || slackConnEnabled !== true
    return slackConnName !== (own.display_name || '')
      || slackConnBot !== (own.bot_token || '')
      || slackConnApp !== (own.app_token || '')
      || slackConnEnabled !== own.enabled
  })()

  // ── WhatsApp: status polling while the pairing screen is open ─────────────
  // Not yet paired → poll /status every 3s so a fresh QR (rotating every
  // ~20s on the server) plus the transition to "paired" shows up without the
  // user having to refresh. Only runs while the WhatsApp setup screen is open.
  useEffect(() => {
    if (setup !== 'whatsapp') {
      if (waPollingRef.current) {
        clearInterval(waPollingRef.current)
        waPollingRef.current = null
      }
      return
    }

    let cancelled = false
    let lastQRKey: string | undefined
    const tick = async () => {
      try {
        const s = await agentApi.getWhatsAppStatus()
        if (cancelled) return
        setWaStatus(s)
        setWaError(null)
        const key = s.qr_expires_at || ''
        if (key !== lastQRKey) {
          lastQRKey = key
          setQrBust(Date.now())
        }
      } catch (err) {
        if (cancelled) return
        setWaError(err instanceof Error ? err.message : String(err))
      }
    }
    tick()
    waPollingRef.current = setInterval(tick, 3000)
    return () => {
      cancelled = true
      if (waPollingRef.current) {
        clearInterval(waPollingRef.current)
        waPollingRef.current = null
      }
    }
  }, [setup])

  // One person, one WhatsApp: the QR is shown only while the account's phone
  // is unpaired.
  useEffect(() => {
    if (setup !== 'whatsapp' || !waStatus?.enabled || waStatus?.paired || !waStatus?.qr_available) {
      setQrLoading(false)
      setQrError(null)
      setQrImageURL(prev => {
        if (prev) URL.revokeObjectURL(prev)
        return null
      })
      return
    }

    let cancelled = false
    setQrLoading(true)
    setQrError(null)
    agentApi.getWhatsAppPairQR(384, qrBust)
      .then(blob => {
        if (cancelled) return
        const nextURL = URL.createObjectURL(blob)
        setQrImageURL(prev => {
          if (prev) URL.revokeObjectURL(prev)
          return nextURL
        })
      })
      .catch(err => {
        if (cancelled) return
        setQrImageURL(prev => {
          if (prev) URL.revokeObjectURL(prev)
          return null
        })
        setQrError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setQrLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [setup, waStatus?.enabled, waStatus?.paired, waStatus?.qr_available, qrBust])

  useEffect(() => {
    return () => {
      if (qrImageURL) URL.revokeObjectURL(qrImageURL)
    }
  }, [qrImageURL])

  // Show accessible workflow destinations in the shared connector overview.
  // Crew remains scoped to its selected project.
  const workflowRoutes = useMemo<WorkflowRoute[]>(() => {
    const visible = (route: ChannelRoute | WaRoute) => routeMatchesTarget(route) || (!hasTarget && !!route.workflow_id && workflows.some(w => w.manifest.id === route.workflow_id))
    const describe = (route: ChannelRoute | WaRoute) => ({
      target_label: route.profile_id ? route.profile_label || route.conversation_key : workflows.find(w => w.manifest.id === route.workflow_id)?.manifest.label || route.workflow_id,
      current_target: routeMatchesTarget(route),
      workshop_mode: route.workshop_mode,
      send_full_details: route.send_full_details,
    })
    const slack = Object.entries(slackOriginal.channel_routing || {})
      .filter(([, route]) => visible(route))
      .map(([key, route]) => ({ kind: 'slack' as const, key, ...describe(route), blocked_emails: route.blocked_emails }))
    const wa = Object.entries(waRouting)
      .filter(([, route]) => visible(route))
      .map(([key, route]) => ({ kind: 'whatsapp' as const, key, ...describe(route) }))
    return [...slack, ...wa]
  }, [slackOriginal.channel_routing, waRouting, workflows, hasTarget, routeMatchesTarget])
  const myRoutes = useMemo(() => workflowRoutes.filter(route => route.current_target), [workflowRoutes])

  // Slack channel routing writes: base on the last loaded config so unsaved
  // setup-screen token edits cannot ride along. Masked tokens round-trip as
  // "no change" server-side.
  const saveSlackRouting = useCallback(async (next: Record<string, BotRoute>) => {
    const request: SlackConfigRequest = {
      enabled: slackOriginal.enabled,
      bot_token: slackOriginal.bot_token || '',
      app_token: slackOriginal.app_token || '',
      bot_mode: slackOriginal.bot_mode || false,
      channel_routing: next,
    }
    await agentApi.updateSlackFeedbackConfig(request)
    await loadSlack()
  }, [loadSlack, slackOriginal])

  // WhatsApp routing writes: PUT replaces the whole map, so always send the
  // full map with only this workflow's entries changed.
  const saveWaRouting = useCallback(async (next: Record<string, WaRoute>) => {
    const data = await agentApi.updateWhatsAppRouting(next)
    setWaRouting(data.routing || {})
    setWaRoutingError(null)
  }, [])

  const withRouteSaving = async (id: string, fn: () => Promise<void>) => {
    setRouteSaving(id)
    setRouteError(null)
    try {
      await fn()
    } catch (err) {
      setRouteError(err instanceof Error ? err.message : 'Failed to save routing')
    } finally {
      setRouteSaving(null)
    }
  }

  const removeRoute = (route: WorkflowRoute) => withRouteSaving(routeId(route), async () => {
    if (route.kind === 'slack') {
      const next = { ...(slackOriginal.channel_routing || {}) }
      delete next[route.key]
      await saveSlackRouting(next)
    } else {
      const next = { ...waRouting }
      delete next[route.key]
      await saveWaRouting(next)
    }
    if (expandedChip === routeId(route)) setExpandedChip(null)
  })

  const updateRoute = (route: WorkflowRoute, patch: { workshop_mode?: string; send_full_details?: boolean; blocked_emails?: string[] }) => withRouteSaving(routeId(route), async () => {
    if (route.kind === 'slack') {
      const current = slackOriginal.channel_routing?.[route.key]
      if (!current) return
      const nextRoute: BotRoute = { ...current }
      nextRoute.workshop_mode = 'run'
      nextRoute.bot_grant = 'run'
      if ('blocked_emails' in patch) nextRoute.blocked_emails = patch.blocked_emails
    if ('send_full_details' in patch) nextRoute.send_full_details = !!patch.send_full_details
      await saveSlackRouting({ ...(slackOriginal.channel_routing || {}), [route.key]: nextRoute })
    } else {
      const current = waRouting[route.key]
      if (!current) return
      const nextRoute: WaRoute = { ...current }
      if ('workshop_mode' in patch) nextRoute.workshop_mode = 'run'
      if ('send_full_details' in patch) nextRoute.send_full_details = !!patch.send_full_details
      await saveWaRouting({ ...waRouting, [route.key]: nextRoute })
    }
  })

  const addSlackRoute = () => {
    if (!workflowId) return
    const channels = Array.from(new Set(
      newSlackChannel
        .split(/[\s,;]+/)
        .map(value => value.trim().toUpperCase())
        .filter(Boolean),
    ))
    if (channels.length === 0) return
    const invalid = channels.find(channel => !SLACK_CHANNEL_RE.test(channel))
    if (invalid) {
      setAddError(e => ({ ...e, slack: `Slack channel ID "${invalid}" is invalid. Use IDs like C1234567890 or G1234567890.` }))
      return
    }
    const existingOther = channels.find(channel => {
      const existing = slackOriginal.channel_routing?.[channel]
      return existing && !routeMatchesTarget(existing)
    })
    if (existingOther) {
      const existing = slackOriginal.channel_routing?.[existingOther]
      setAddError(e => ({ ...e, slack: `${existingOther} is already routed to ${existing?.profile_label || workflowLabel(existing?.workflow_id || '')}.` }))
      return
    }
    const existingSame = channels.find(channel => {
      const existing = slackOriginal.channel_routing?.[channel]
      return existing && routeMatchesTarget(existing)
    })
    if (existingSame) {
      setAddError(e => ({ ...e, slack: `${existingSame} is already routed to this ${target ? 'project' : 'workflow'}.` }))
      return
    }
    setAddError(e => ({ ...e, slack: undefined }))
    void withRouteSaving(`slack:${channels.join(',')}`, async () => {
      const next = { ...(slackOriginal.channel_routing || {}) }
      const baseRoute = routeForTarget()
      for (const channel of channels) {
        next[channel] = {
          ...baseRoute,
          workshop_mode: 'run',
          bot_grant: 'run',
          send_full_details: true,
        }
      }
      await saveSlackRouting(next)
      setNewSlackChannel('')
    })
  }

  const addWaRoute = () => {
    const slug = newWaSlug.trim().toLowerCase()
    if (!slug || !workflowId) return
    if (!SLUG_RE.test(slug)) {
      setAddError(e => ({ ...e, whatsapp: 'Slugs can only contain a-z, 0-9 and dashes.' }))
      return
    }
    const existing = waRouting[slug]
    if (existing && !routeMatchesTarget(existing)) {
      setAddError(e => ({ ...e, whatsapp: `@${slug} is already routed to ${existing.profile_label || workflowLabel(existing.workflow_id || '')}.` }))
      return
    }
    if (existing) {
      setAddError(e => ({ ...e, whatsapp: `@${slug} is already routed to this workflow.` }))
      return
    }
    setAddError(e => ({ ...e, whatsapp: undefined }))
    void withRouteSaving(`whatsapp:${slug}`, async () => {
      const route: WaRoute = { ...routeForTarget() }
      await saveWaRouting({ ...waRouting, [slug]: route })
      setNewWaSlug('')
    })
  }

  // ── Slack handlers (setup screen) ─────────────────────────────────────────
  const handleEmailsSave = async () => {
    setEmailsSaving(true)
    try {
      const emails = allowedEmails.split(',').map(e => e.trim()).filter(e => e.length > 0)
      await agentApi.saveBotConfig({ allowed_emails: emails })
      setEmailsDirty(false)
      setEmailsSaved(true)
      setTimeout(() => setEmailsSaved(false), 2000)
    } catch { /* ignore */ } finally { setEmailsSaving(false) }
  }

  const handleSlackSave = async () => {
    try {
      setSlackSaving(true)
      setSlackError(null)
      setSlackSuccess(null)
      const request: SlackConfigRequest = {
        enabled: slackConfig.enabled, bot_token: slackConfig.bot_token || '',
        app_token: slackConfig.app_token || '',
        bot_mode: slackConfig.enabled,
        channel_routing: slackOriginal.channel_routing || {},
      }
      await agentApi.updateSlackFeedbackConfig(request)
      setTestResult(null)
      setSlackSuccess('Saved successfully!')
      await loadSlack()
      setTimeout(() => setSlackSuccess(null), 3000)
      return true
    } catch (err) {
      setSlackError(err instanceof Error ? err.message : 'Failed to save Slack configuration')
      return false
    } finally { setSlackSaving(false) }
  }

  const pollForTestReply = (testId: string) => {
    let attempts = 0
    const poll = async () => {
      if (attempts >= 60) { setPollingForReply(false); return }
      try {
        const reply = await agentApi.getTestConnectionReply(testId)
        if (reply?.received) { setTestReply(reply.reply); setPollingForReply(false); return }
      } catch { /* ignore 204 */ }
      attempts++
      setTimeout(poll, 1000)
    }
    poll()
  }

  const handleSlackTest = async () => {
    try {
      setSlackTesting(true)
      setSlackError(null)
      setTestResult(null)
      setTestReply(null)
      setPollingForReply(false)
      // The backend tests saved settings; persist form edits before testing.
      if (JSON.stringify(slackConfig) !== JSON.stringify(slackOriginal) && !await handleSlackSave()) {
        return
      }
      const result = await agentApi.testSlackConnection()
      setTestResult(result)
      if (result.success && result.test_id) { setPollingForReply(true); pollForTestReply(result.test_id) }
    } catch (err) {
      setTestResult({ success: false, message: err instanceof Error ? err.message : 'Connection test failed' })
    } finally { setSlackTesting(false) }
  }

  // ── This workflow/project's Slack app: actions ──────────────────────────
  const saveWorkflowSlackConnection = async (): Promise<string | null> => {
    if (!workspacePath) return null
    const scopeNoun = target ? 'Project' : 'Workflow'
    try {
      setSlackConnSaving(true)
      setSlackError(null)
      setSlackSuccess(null)
      let conn = slackSelection.own
      if (conn) {
        conn = await agentApi.updateSlackConnection(conn.id, {
          display_name: slackConnName.trim() || conn.display_name,
          // Masked values round-trip as "no change" server-side.
          bot_token: slackConnBot,
          app_token: slackConnApp,
          enabled: slackConnEnabled,
        })
      } else {
        conn = await agentApi.createSlackConnection({
          display_name: slackConnName.trim() || (target?.label || workflow?.manifest.label || 'Workflow Slack'),
          bot_token: slackConnBot,
          app_token: slackConnApp,
          enabled: slackConnEnabled,
          workspace_path: workspacePath,
          ...(target ? { profile_id: target.profileId } : {}),
        })
      }
      // The own bot is this workflow/project's Slack identity: it answers
      // for it in any channel and sends its notifications, so saving selects it.
      if (target) {
        if (projectSlackSelectionId !== conn.id) {
          await agentApi.setProjectSlackSelection(target.profileId, workspacePath, conn.id)
        }
      } else {
        const capabilities = workflow?.manifest.capabilities
        if (capabilities && (capabilities.slack_connection_id || '') !== conn.id) {
          await updateWorkflow(workspacePath, {
            capabilities: { ...capabilities, slack_connection_id: conn.id },
          })
        }
      }
      setSlackConnTestResult(null)
      setSlackSuccess(`${scopeNoun} Slack app saved.`)
      await loadSlack()
      setSlackConnSyncKey(key => key + 1)
      setTimeout(() => setSlackSuccess(null), 3000)
      return conn.id
    } catch (err) {
      setSlackError(err instanceof Error ? err.message : `Failed to save the ${scopeNoun.toLowerCase()} Slack app`)
      return null
    } finally { setSlackConnSaving(false) }
  }

  const selectWorkflowSlackConnection = async (connectionId: string | null) => {
    if (!workspacePath) return false
    try {
      setSlackConnSaving(true)
      setSlackError(null)
      if (target) {
        await agentApi.setProjectSlackSelection(target.profileId, workspacePath, connectionId || '')
      } else {
        const capabilities = workflow?.manifest.capabilities
        if (!capabilities) return false
        await updateWorkflow(workspacePath, {
          capabilities: { ...capabilities, slack_connection_id: connectionId || '' },
        })
      }
      await loadSlack()
      return true
    } catch (err) {
      setSlackError(err instanceof Error ? err.message : `Failed to switch the ${target ? 'project' : 'workflow'} Slack app`)
      return false
    } finally { setSlackConnSaving(false) }
  }

  const testWorkflowSlackConnection = async () => {
    try {
      setSlackConnTesting(true)
      setSlackError(null)
      setSlackConnTestResult(null)
      // The backend tests saved settings; persist form edits before testing.
      let id = slackSelection.own?.id || null
      if (!id || slackConnHasChanges) {
        id = await saveWorkflowSlackConnection()
        if (!id) return
      }
      const result = await agentApi.testSlackConnectionEntry(id)
      setSlackConnTestResult(result.success ? await withSlackDryRun(id, result, target ? 'crew' : 'workflow') : result)
    } catch (err) {
      setSlackConnTestResult({ success: false, message: err instanceof Error ? err.message : 'Connection test failed' })
    } finally { setSlackConnTesting(false) }
  }

  const removeWorkflowSlackConnection = async () => {
    const conn = slackSelection.own
    if (!conn || !workspacePath) return false
    const scopeNoun = target ? 'project' : 'workflow'
    if (!slackConnConfirmDelete) {
      setSlackConnConfirmDelete(true)
      window.setTimeout(() => setSlackConnConfirmDelete(false), 5000)
      return false
    }
    try {
      setSlackConnSaving(true)
      setSlackError(null)
      // A selected app cannot be deleted; move back to the platform default first.
      if (target) {
        if (projectSlackSelectionId === conn.id) {
          await agentApi.setProjectSlackSelection(target.profileId, workspacePath, '')
        }
      } else {
        const capabilities = workflow?.manifest.capabilities
        if (capabilities && (capabilities.slack_connection_id || '') === conn.id) {
          await updateWorkflow(workspacePath, {
            capabilities: { ...capabilities, slack_connection_id: '' },
          })
        }
      }
      await agentApi.deleteSlackConnection(conn.id)
      setSlackConnConfirmDelete(false)
      setSlackSuccess(`${target ? 'Project' : 'Workflow'} Slack app removed; the ${scopeNoun} now uses the shared bot.`)
      await loadSlack()
      setSlackConnSyncKey(key => key + 1)
      setTimeout(() => setSlackSuccess(null), 3000)
      return true
    } catch (err) {
      setSlackError(err instanceof Error ? err.message : `Failed to remove the ${scopeNoun} Slack app`)
      return false
    } finally { setSlackConnSaving(false) }
  }

  // ── WhatsApp handlers (setup screen) ──────────────────────────────────────
  // Drops the account's linked phone, restarts its connector session and
  // refreshes local status. Two-step confirmation prevents accidental clicks.
  const handleUnpairWhatsAppDevice = useCallback(async (slot: string) => {
    if (waUnpairConfirmSlot !== slot) {
      setWaUnpairConfirmSlot(slot)
      window.setTimeout(() => {
        setWaUnpairConfirmSlot(current => (current === slot ? null : current))
      }, 5000)
      return
    }
    try {
      setWaUnpairingSlot(slot)
      setWaError(null)
      await agentApi.unpairWhatsApp()
      setWaUnpairConfirmSlot(null)
      await loadWaStatus()
      setQrBust(Date.now())
    } catch (err) {
      setWaError(err instanceof Error ? err.message : 'Failed to unpair')
    } finally {
      setWaUnpairingSlot(null)
    }
  }, [loadWaStatus, waUnpairConfirmSlot])

  // ── Gmail derived state + save/test ───────────────────────────────────────
  const gmailCurrentBlocked = normalizeGmailEmails(gmailBlockedText)
  const gmailDefaultRecipients = normalizeGmailEmails(gmailConfig.default_to)
  const gmailBlockedDefaults = gmailDefaultRecipients.filter(email => gmailCurrentBlocked.includes(email))
  const gmailDefaultIsBlocked = gmailBlockedDefaults.length > 0
  const gmailTestPassed = gmailTestResult?.success === true
    && gmailTestedTo === (gmailConfig.default_to || '')
    && !gmailDefaultIsBlocked
  // An authenticated account with the Gmail scope is reason enough to enable
  // the channel (user decision 2026-09-03; the server auto-enables on the same
  // condition). A passing test still counts, for a host that is authenticated
  // some other way.
  const gmailCanEnable = gmailTestPassed
    || (gmailConfig.auth?.authenticated === true && gmailConfig.auth?.has_gmail_scope === true)
  const gmailHasChanges = gmailConfig.enabled !== gmailOriginal.enabled
    || (gmailConfig.default_to || '') !== gmailOriginal.default_to
    || JSON.stringify(gmailCurrentBlocked) !== JSON.stringify(gmailOriginal.blocked_recipients)

  const saveGmail = async () => {
    try {
      setGmailSaving(true)
      setGmailError(null)
      setGmailSuccess(null)
      const request: GmailConfigRequest = {
        enabled: gmailConfig.enabled,
        default_to: gmailConfig.default_to || '',
        blocked_recipients: gmailCurrentBlocked,
      }
      const data = await agentApi.updateGmailFeedbackConfig(request)
      const blocked = normalizeGmailEmails(data.blocked_recipients)
      setGmailConfig({ ...data, blocked_recipients: blocked })
      setGmailBlockedText(blocked.join(', '))
      setGmailOriginal({ enabled: data.enabled, default_to: data.default_to || '', blocked_recipients: blocked })
      setGmailSuccess('Gmail notification settings saved.')
    } catch (error) {
      setGmailError(gmailErrorMessage(error, 'Failed to save Gmail configuration'))
    } finally {
      setGmailSaving(false)
    }
  }

  const testGmail = async () => {
    try {
      setGmailTesting(true)
      setGmailError(null)
      setGmailTestResult(null)
      const request: GmailConfigRequest = {
        enabled: gmailConfig.enabled,
        default_to: gmailConfig.default_to || '',
        blocked_recipients: gmailCurrentBlocked,
      }
      const result = await agentApi.testGmailConnection(request)
      setGmailTestResult(result)
      setGmailTestedTo(result.success ? (gmailConfig.default_to || '') : null)
    } catch (error) {
      setGmailTestResult({ success: false, message: error instanceof Error ? error.message : 'Test failed' })
      setGmailTestedTo(null)
    } finally {
      setGmailTesting(false)
    }
  }

  // ── Connection status per channel ─────────────────────────────────────────
  // A workflow's explicitly selected app can run without the platform default.
  // The route form must not disappear just because the shared app is disabled.
  const selectedSlackReady = selectedWorkflowSlackReady(slackSelection)
  const slackReady = selectedSlackReady || (slackOriginal.enabled && !!slackOriginal.bot_mode)
  const slackStatusLabel = selectedSlackReady ? 'Connected' : slackConnectionStatus(slackOriginal.enabled, !!slackOriginal.bot_mode, slackLoading, slackTesting, testResult)

  const waLinkedDevices = waStatus?.devices?.filter(device => device.paired) || []
  const waReady = !!waStatus?.enabled && (waStatus.paired || waLinkedDevices.length > 0)
  const waStatusLabel = !waStatus ? (waError ? 'Unavailable' : 'Loading…')
    : !waStatus.enabled ? 'Disabled on server'
      : !waReady ? 'Not paired'
        : waStatus.connected || waLinkedDevices.some(device => device.connected) ? 'Connected' : 'Paired, offline'

  // ── "One of my bots": share a bot this user manages with this target ─────
  // A route names this workflow folder (or crew project); the server checks
  // the user manages the bot and can write this target.
  const myBotDestination = useMemo(() => {
    const path = hasTarget ? (workspacePath || '') : (workflow?.workspace_path || workspacePath || '')
    return { workspace_path: path, ...(hasTarget && targetProfileId ? { profile_id: targetProfileId } : {}) }
  }, [hasTarget, targetProfileId, workflow?.workspace_path, workspacePath])
  const routesHere = useCallback((bot: SlackUsableBot) => (bot.channel_routes || []).filter(route =>
    sameBotWorkspacePath(route.workspace_path, myBotDestination.workspace_path) && (route.profile_id || '') === (myBotDestination.profile_id || '')), [myBotDestination])
  // Bots other than this target's own, with the channels routed here.
  const myOtherBots = useMemo(
    () => myBots.filter(bot => !(sameBotWorkspacePath(bot.workspace_path, myBotDestination.workspace_path) && (bot.profile_id || '') === (myBotDestination.profile_id || ''))),
    [myBots, myBotDestination],
  )

  const replaceMyBot = (updated: SlackUsableBot) => {
    setMyBots(prev => prev.map(bot => bot.id === updated.id ? { ...bot, ...updated, channel_routes: updated.channel_routes || [] } : bot))
  }

  const addMyBotChannel = async (botId: string) => {
    const bot = myBots.find(entry => entry.id === botId)
    if (!bot || !myBotDestination.workspace_path) return
    const channels = Array.from(new Set(
      newMyBotChannel.split(/[\s,;]+/).map(value => value.trim().toUpperCase()).filter(Boolean),
    ))
    if (channels.length === 0) return
    const invalid = channels.find(channel => !SLACK_CHANNEL_RE.test(channel))
    if (invalid) {
      setMyBotError(`Slack channel ID "${invalid}" is invalid. Use IDs like C1234567890 or G1234567890.`)
      return
    }
    const taken = channels.map(channel => (bot.channel_routes || []).find(route => route.channel_id === channel)).find(Boolean)
    if (taken) {
      const here = sameBotWorkspacePath(taken.workspace_path, myBotDestination.workspace_path) && (taken.profile_id || '') === (myBotDestination.profile_id || '')
      setMyBotError(here
        ? `${taken.channel_id} already answers here on ${bot.display_name}.`
        : `${taken.channel_id} on ${bot.display_name} already answers for ${taken.label || taken.workspace_path}. Remove it there first.`)
      return
    }
    setMyBotError(null)
    setMyBotSaving(`add:${botId}`)
    try {
      for (const channel of channels) {
        replaceMyBot(await agentApi.addSlackBotChannelRoute(botId, channel, myBotDestination))
      }
      setNewMyBotChannel('')
    } catch (err) {
      setMyBotError(serverErrorMessage(err, 'Failed to add the channel'))
      void loadMyBots()
    } finally {
      setMyBotSaving(null)
    }
  }

  const removeMyBotChannel = async (botId: string, channelId: string) => {
    setMyBotError(null)
    setMyBotSaving(`remove:${botId}:${channelId}`)
    try {
      replaceMyBot(await agentApi.removeSlackBotChannelRoute(botId, channelId))
    } catch (err) {
      setMyBotError(serverErrorMessage(err, 'Failed to remove the channel'))
      void loadMyBots()
    } finally {
      setMyBotSaving(null)
    }
  }

  const slackHasChanges = JSON.stringify(slackConfig) !== JSON.stringify(slackOriginal)

  return {
    // identity / access
    workflowId, readOnly, canManageOwnWhatsAppDevices,
    // navigation
    setup, setSetup, expandedChip, setExpandedChip,
    routeSaving, routeError, newSlackChannel, setNewSlackChannel, newWaSlug, setNewWaSlug, addError, setAddError,
    // slack
    slackConfig, setSlackConfig, slackOriginal, loadSlack, slackLoading, slackSaving, slackTesting, slackError, slackSuccess,
    testResult, testReply, pollingForReply, showBotToken, setShowBotToken, showAppToken, setShowAppToken,
    allowedEmails, setAllowedEmails, emailsDirty, setEmailsDirty, emailsSaving, emailsSaved, setEmailsSaved,
    handleEmailsSave, handleSlackSave, handleSlackTest, slackHasChanges, slackReady, slackStatusLabel,
    canManageSlackDefault, canManageWorkflowSlack, hasProfileTarget: !!target,
    // slack: this workflow's own app
    slackSelection, slackAppDefaultName: targetLabel || workflow?.manifest.label || '',
    slackConnName, setSlackConnName, slackConnBot, setSlackConnBot, slackConnApp, setSlackConnApp,
    slackConnEnabled, setSlackConnEnabled, slackConnSaving, slackConnTesting, slackConnTestResult,
    slackConnConfirmDelete, slackConnShowBot, setSlackConnShowBot, slackConnShowApp, setSlackConnShowApp,
    slackConnHasChanges, saveWorkflowSlackConnection, selectWorkflowSlackConnection,
    testWorkflowSlackConnection, removeWorkflowSlackConnection,
    // slack: "One of my bots"
    myOtherBots, myBotRoutesHere: routesHere, newMyBotChannel, setNewMyBotChannel,
    myBotSaving, myBotError, setMyBotError, addMyBotChannel, removeMyBotChannel,
    // whatsapp
    waStatus, waError, waRoutingError, qrImageURL, qrLoading, qrError,
    waUnpairConfirmSlot, waUnpairingSlot, refreshWaQR, handleUnpairWhatsAppDevice,
    waReady, waStatusLabel,
    // routes
    myRoutes, workflowRoutes, removeRoute, updateRoute, addSlackRoute, addWaRoute,
    // gmail
    gmailConnectionsReadOnly, gmailSettingsReadOnly, canRemoveGmailConnection,
    gmailConfig, setGmailConfig, gmailBlockedText, setGmailBlockedText,
    gmailLoading, gmailChecking, gmailSaving, gmailTesting, gmailError, gmailSuccess, gmailTestResult,
    gmailBlockedDefaults, gmailDefaultIsBlocked, gmailTestPassed, gmailCanEnable, gmailHasChanges, loadGmail, saveGmail, testGmail,
    // gmail senders (multi-account)
    gmailConnections, gmailConnectionsBusy, gmailAuthPending, gmailAuthUrl,
    loadGmailConnections, runGmailConnectionAction, connectGmailAccount,
    // gmail OAuth clients (named Google Cloud apps connections authorize under)
    gmailOAuthClients, gmailOAuthClientsBusy, gmailOAuthClientError,
    gmailNewClientEmail, setGmailNewClientEmail,
    loadGmailOAuthClients, createGmailOAuthClient, removeGmailMailboxAndClient,
  }
}

export type WorkflowBots = ReturnType<typeof useWorkflowBots>
