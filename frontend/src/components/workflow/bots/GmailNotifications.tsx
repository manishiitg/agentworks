import { useState, type ReactNode } from 'react'
import { Loader2, Mail } from 'lucide-react'
import { agentApi } from '../../../services/api'
import { Button } from '../../ui/Button'
import { Card } from '../../ui/Card'
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
import { GoogleAccountConnect } from '../../../products/work/GoogleAccountConnect'
import { GmailInboundPanel } from './GmailInboundPanel'
import { getGoogleAppsAskAIMessage, useGmailInboundUIEnabled } from './gmailAskAI'

function gmailBackendLabel(backend: string | undefined): { name: string; install: string } {
  if (backend === 'gog') return { name: 'gog', install: 'gogcli' }
  return { name: 'gws', install: '@googleworkspace/cli' }
}

type GmailNotificationsBots = Pick<WorkflowBots,
  | 'gmailConnectionsReadOnly' | 'gmailSettingsReadOnly' | 'canRemoveGmailConnection'
  | 'gmailConfig' | 'setGmailConfig' | 'gmailBlockedText' | 'setGmailBlockedText'
  | 'gmailLoading' | 'gmailSaving' | 'gmailTesting' | 'gmailError' | 'gmailSuccess' | 'gmailTestResult'
  | 'gmailBlockedDefaults' | 'gmailDefaultIsBlocked' | 'gmailCanEnable' | 'gmailHasChanges' | 'saveGmail' | 'testGmail'
  | 'gmailConnections' | 'gmailConnectionsBusy' | 'gmailAuthPending' | 'gmailAuthUrl'
  | 'runGmailConnectionAction' | 'connectGmailAccount'
  | 'gmailOAuthClientError' | 'removeGmailMailboxAndClient' | 'loadGmailConnections'
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
  scopeNoun?: 'workflow' | 'project' | 'relay'
  /** The shared Google form supports both company and named OAuth apps. */
  platformConnect?: ReactNode
  onAsk?: (message: string) => void | Promise<void>
}) {
  const incomingGmail = useGmailInboundUIEnabled()
  const {
    gmailConnectionsReadOnly: readOnly, gmailSettingsReadOnly, canRemoveGmailConnection,
    gmailConfig, setGmailConfig, gmailBlockedText, setGmailBlockedText,
    gmailLoading, gmailSaving, gmailTesting, gmailError, gmailSuccess, gmailTestResult,
    gmailBlockedDefaults, gmailDefaultIsBlocked, gmailCanEnable, gmailHasChanges, saveGmail, testGmail,
    gmailConnections, gmailConnectionsBusy, gmailAuthPending, gmailAuthUrl,
    runGmailConnectionAction, connectGmailAccount,
    gmailOAuthClientError, removeGmailMailboxAndClient,
  } = bots

  const handleRemoveMailbox = (conn: { id: string; display_name: string; client_name?: string }) => {
    if (!window.confirm(`Remove "${conn.display_name}"?`)) return
    void removeGmailMailboxAndClient(conn.id, conn.client_name || '')
  }


  return (
    <div className="space-y-4">
      {workspacePath && scopeNoun !== 'relay' && <GmailInboundPanel workspacePath={workspacePath} connections={gmailConnections} onAsk={onAsk} />}
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
            description="Your Google accounts for this project. The agent uses them through the server; the default one sends workflow notifications."
            actions={
              <AskAIButton
                workspacePath={workspacePath}
                onAsk={onAsk}
                label={scopeNoun === 'project' ? 'Ask Crew to set up Gmail' : 'Ask Builder to set up Gmail'}
                message={getGoogleAppsAskAIMessage(scopeNoun, incomingGmail && scopeNoun !== 'relay')}
              />
            }
          >
              {readOnly && <p className="mb-3 text-xs text-muted-foreground" role="status">
                {workspacePath && /(?:^|\/)Chats\/Code\/projects\//.test(workspacePath)
                  ? "Only this Code's owner can manage its Google accounts."
                  : 'An admin manages shared Gmail accounts. You can remove your own connected account.'}
              </p>}
              {gmailOAuthClientError && <StatusBanner tone="error">{gmailOAuthClientError}</StatusBanner>}
              {gmailConnections.length === 0 ? <p className="text-xs text-muted-foreground">No Google account connected yet.</p> : (
                <GoogleAccountList
                  workspacePath={workspacePath}
                  connections={gmailConnections}
                  busyId={gmailConnectionsBusy}
                  readOnly={readOnly}
                  canRemove={canRemoveGmailConnection}
                  onSendTest={conn => runGmailConnectionAction(conn.id, () => agentApi.testGmailConnectionById(conn.id, gmailConfig.default_to || undefined))}
                  onSetDefault={conn => runGmailConnectionAction(conn.id, () => agentApi.setDefaultGmailConnection(conn.id))}
                  onToggle={conn => runGmailConnectionAction(conn.id, () => agentApi.updateGmailConnection(conn.id, { enabled: !conn.enabled }))}
                  onReconnect={conn => connectGmailAccount(conn.id)}
                  onRemove={conn => handleRemoveMailbox(conn)}
                />
              )}
              {gmailAuthPending && gmailAuthUrl && <SignInLinkBox url={gmailAuthUrl} />}
              {platformConnect || (workspacePath && <GoogleAccountConnect key={workspacePath} workspacePath={workspacePath}
                privateAccount={/(?:^|\/)Chats\/Code\/projects\//.test(workspacePath)} readOnly={readOnly} onChanged={() => { void bots.loadGmailConnections() }} />)}
          </FormSection>
          <FormSection
            title="Delivery settings"
            description={<>Account-wide one-way email delivery, used by <code>notify_user</code> in workflows. Turn this off to stop all outbound email. Email replies do not resume an agent.</>}
          >
            {gmailSettingsReadOnly && <p className="mb-3 text-xs text-muted-foreground" role="status">Only an admin can change shared Gmail delivery settings or send a test email.</p>}
            {gmailError && <StatusBanner tone="error">{gmailError}</StatusBanner>}
            {gmailSuccess && <StatusBanner tone="success">{gmailSuccess}</StatusBanner>}
            <Card className="p-4">
              <ToggleRow
                label="Enable Gmail"
                description="Available to notify_user in workflows."
                checked={gmailConfig.enabled}
                onCheckedChange={checked => setGmailConfig({ ...gmailConfig, enabled: checked })}
                disabled={gmailSettingsReadOnly || (!gmailConfig.enabled && !gmailCanEnable)}
                disabledTitle={gmailSettingsReadOnly ? READ_ONLY_TITLE : undefined}
              />
              {!gmailConfig.enabled && !gmailCanEnable && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Sign in a Gmail account above to enable; it switches on automatically once one is connected.</p>}
            </Card>
            <Card className="space-y-3 p-4">
                <div>
                  <Label className="mb-2 block">Default recipients</Label>
                  {/* Deliberately type="text": type="email" rejects a comma-separated
                      list, which is the whole point of this field. */}
                  <Input type="text" inputMode="email" value={gmailConfig.default_to || ''} onChange={event => setGmailConfig({ ...gmailConfig, default_to: event.target.value })} disabled={gmailSettingsReadOnly} placeholder="you@example.com, teammate@example.com" aria-label="Default recipients" />
                  <p className="mt-1 text-xs text-muted-foreground">Where notifications are emailed when a workflow has no recipients of its own. Separate several addresses with commas.</p>
                </div>
                <div>
                  <Label className="mb-2 block">Disallowed recipients</Label>
                  <Textarea value={gmailBlockedText} onChange={event => setGmailBlockedText(event.target.value)} disabled={gmailSettingsReadOnly} rows={3} placeholder="blocked@example.com, no-notify@example.com" className="font-mono" aria-label="Disallowed recipients" />
                  {gmailDefaultIsBlocked && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{gmailBlockedDefaults.join(', ')} {gmailBlockedDefaults.length === 1 ? 'is' : 'are'} both a default recipient and disallowed.</p>}
                </div>
              </Card>
              <Button variant="outline" onClick={testGmail} disabled={gmailSettingsReadOnly || gmailTesting || !gmailConfig.default_to || gmailDefaultIsBlocked} title={gmailSettingsReadOnly ? READ_ONLY_TITLE : undefined} className="w-full">{gmailTesting ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" />Sending…</> : 'Send test email'}</Button>
              {gmailTestResult && <Card className={`p-3 text-sm ${gmailTestResult.success ? 'border-green-300 bg-green-50 text-green-700 dark:border-green-700 dark:bg-green-900/20 dark:text-green-300' : 'border-red-300 bg-red-50 text-red-700 dark:border-red-700 dark:bg-red-900/20 dark:text-red-300'}`}>{gmailTestResult.message}</Card>}
              <div className="flex justify-end">
                <Button onClick={saveGmail} disabled={gmailSettingsReadOnly || !gmailHasChanges || gmailSaving || gmailLoading || gmailDefaultIsBlocked || (gmailConfig.enabled && !gmailCanEnable)} title={gmailSettingsReadOnly ? READ_ONLY_TITLE : undefined}>{gmailSaving ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" />Saving…</> : 'Save'}</Button>
              </div>
          </FormSection>
          </>
        )}
    </div>
  )
}
