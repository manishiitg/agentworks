import { useEffect, useState } from 'react'
import { prepareSignInWindow } from '../../../utils/signInWindow'
import { googleAppApi } from '../../../api/googleApp'
import { GoogleAccountConnect } from '../../../products/work/GoogleAccountConnect'
import { changeGoogleAccountAccess } from '../../../products/work/googleAccountAccess'
import type { GmailConnection } from '../../../services/api-types'
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
import type { WorkflowBots } from './useWorkflowBots'
import { StatusBanner } from './StatusBanner'
import { GoogleAccountList } from '../../../products/work/GoogleAccountList'
import PersonalAccountsPanel from '../../integrations/PersonalAccountsPanel'

function gmailBackendLabel(backend: string | undefined): { name: string; install: string } {
  if (backend === 'gog') return { name: 'gog', install: 'gogcli' }
  return { name: 'gws', install: '@googleworkspace/cli' }
}

type GmailNotificationsBots = Pick<WorkflowBots,
  | 'gmailConnectionsReadOnly' | 'gmailSettingsReadOnly' | 'canRemoveGmailConnection'
  | 'gmailConfig' | 'setGmailConfig' | 'gmailBlockedText' | 'setGmailBlockedText'
  | 'gmailLoading' | 'gmailSaving' | 'gmailTesting' | 'gmailError' | 'gmailSuccess' | 'gmailTestResult'
  | 'gmailBlockedDefaults' | 'gmailDefaultIsBlocked' | 'gmailCanEnable' | 'gmailHasChanges' | 'saveGmail' | 'testGmail'
  | 'gmailConnections' | 'gmailConnectionsBusy'
  | 'runGmailConnectionAction'
  | 'gmailOAuthClientError' | 'removeGmailMailboxAndClient' | 'loadGmailConnections'
>

export function GmailNotifications({ bots, workspacePath }: {
  bots: GmailNotificationsBots
  workspacePath: string | null
}) {
  const [signInUrl, setSignInUrl] = useState('')
  const [tabOpened, setTabOpened] = useState(false)
  useEffect(() => {
    if (!signInUrl) return
    const refresh = () => { void bots.loadGmailConnections() }
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [signInUrl, bots.loadGmailConnections])
  const {
    gmailConnectionsReadOnly: readOnly, gmailSettingsReadOnly, canRemoveGmailConnection,
    gmailConfig, setGmailConfig, gmailBlockedText, setGmailBlockedText,
    gmailLoading, gmailSaving, gmailTesting, gmailError, gmailSuccess, gmailTestResult,
    gmailBlockedDefaults, gmailDefaultIsBlocked, gmailCanEnable, gmailHasChanges, saveGmail, testGmail,
    gmailConnections, gmailConnectionsBusy,
    runGmailConnectionAction,
    gmailOAuthClientError, removeGmailMailboxAndClient,
  } = bots

  const reconnect = (conn: GmailConnection) => {
    if (readOnly || conn.can_manage === false || conn.can_reconnect === false) return
    const signIn = prepareSignInWindow()
    setSignInUrl('')
    void runGmailConnectionAction(conn.id, async () => {
      const result = await googleAppApi.reconnect(conn.id, {
        workspace_path: workspacePath || '', services: conn.services || [],
        allow_read_access: !!conn.allow_read_access, allow_agent_write_access: !!conn.allow_agent_write_access,
      })
      setTabOpened(signIn.open(result.auth_url)); setSignInUrl(result.auth_url)
    }).then(ok => { if (!ok) signIn.close() })
  }

  const handleRemoveMailbox = (conn: { id: string; display_name: string; client_name?: string; personal?: boolean }) => {
    if (!window.confirm(conn.personal ? `Remove "${conn.display_name}" from this item? Other items stay connected.` : `Remove "${conn.display_name}"?`)) return
    void removeGmailMailboxAndClient(conn.id, conn.client_name || '')
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
              </span>
            }
          >
              {workspacePath && <PersonalAccountsPanel provider="google" workspacePath={workspacePath} onChanged={bots.loadGmailConnections} />}
              {gmailOAuthClientError && <StatusBanner tone="error">{gmailOAuthClientError}</StatusBanner>}
              {gmailConnections.some(account => !account.personal) && <div className="mt-4"><p className="mb-2 text-xs font-medium">Company accounts</p><GoogleAccountList
                workspacePath={workspacePath}
                connections={gmailConnections.filter(account => !account.personal)}
                busyId={gmailConnectionsBusy} readOnly={readOnly} canRemove={canRemoveGmailConnection}
                onSendTest={conn => runGmailConnectionAction(conn.id, () => agentApi.testGmailConnectionById(conn.id, gmailConfig.default_to || undefined))}
                onSetDefault={conn => runGmailConnectionAction(conn.id, () => agentApi.setDefaultGmailConnection(conn.id))}
                onToggle={conn => runGmailConnectionAction(conn.id, () => agentApi.updateGmailConnection(conn.id, { enabled: !conn.enabled }))}
                onReconnect={reconnect} onRemove={handleRemoveMailbox} onChangeAccess={conn => changeGoogleAccountAccess(conn, workspacePath || '')}
              /></div>}
              {!readOnly && gmailConnections.some(conn => !conn.personal && conn.can_manage !== false && conn.can_reconnect !== false) && <GoogleAccountConnect workspacePath={workspacePath || ''} canConnect={false} readOnly={readOnly} onChanged={bots.loadGmailConnections} />}
              {signInUrl && <div className="mt-3 text-xs"><p role="status">{tabOpened ? 'Finish signing in with Google, then return here.' : 'Your browser blocked the sign-in tab. Open Google sign-in below.'}</p><a href={signInUrl} target="_blank" rel="noopener noreferrer" className="mt-1 inline-block text-primary underline">Open Google sign-in</a></div>}
          </FormSection>
          <details className="rounded-lg border border-border p-3">
          <summary className="cursor-pointer text-sm font-medium">Company delivery defaults · {gmailConfig.enabled ? 'On' : 'Off'}{gmailSettingsReadOnly ? ' · Admin managed' : ''}</summary>
          <FormSection
            className="mt-3"
            title="Company delivery defaults"
            description="Company sender and recipients. Personal Google accounts work independently."
          >
            {gmailSettingsReadOnly && <p className="mb-3 text-xs text-muted-foreground" role="status">Company settings are managed by an administrator.</p>}
            {gmailError && <StatusBanner tone="error">{gmailError}</StatusBanner>}
            {gmailSuccess && <StatusBanner tone="success">{gmailSuccess}</StatusBanner>}
            <Card className="p-4">
              <ToggleRow
                label="Workflow email notifications (company default)"
                description="Use the company sender for workflow summaries."
                checked={gmailConfig.enabled}
                onCheckedChange={checked => setGmailConfig({ ...gmailConfig, enabled: checked })}
                disabled={gmailSettingsReadOnly || (!gmailConfig.enabled && !gmailCanEnable)}
                disabledTitle={gmailSettingsReadOnly ? READ_ONLY_TITLE : undefined}
              />
              {!gmailConfig.enabled && !gmailCanEnable && <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">Company sender not ready. Personal accounts are unaffected.</p>}
            </Card>
            <Card className="space-y-3 p-4">
                <div>
                  <Label className="mb-2 block">Default recipients</Label>
                  {/* Deliberately type="text": type="email" rejects a comma-separated
                      list, which is the whole point of this field. */}
                  <Input type="text" inputMode="email" value={gmailConfig.default_to || ''} onChange={event => setGmailConfig({ ...gmailConfig, default_to: event.target.value })} disabled={gmailSettingsReadOnly} placeholder="you@example.com, teammate@example.com" aria-label="Default recipients" />
                  <p className="mt-1 text-xs text-muted-foreground">Fallback recipients · Separate addresses with commas.</p>
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
          </details>
          </>
        )}
    </div>
  )
}
