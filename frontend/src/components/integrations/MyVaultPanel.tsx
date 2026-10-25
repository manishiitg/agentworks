import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { KeyRound, MessageCircle, Plug, RefreshCw, Users } from 'lucide-react'
import { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { Button } from '../ui/Button'
import SlackAdminPanel from '../admin/SlackAdminPanel'
import PersonalAccountsPanel from './PersonalAccountsPanel'
import { ProjectVaultPanel } from './ProjectVaultPanel'
import { LaptopAccountsPanel } from './LaptopAccountsPanel'
import { VaultSlackBots } from './VaultSlackBots'
import { useVaultSetupDraft } from './useVaultSetupDraft'

interface VaultView {
  group: { ID: string; Name: string; ProjectRoot?: string }
  role: 'owner' | 'member'
  members: string[]
  connections?: { id: string; label: string; provider: string; status: string; sign_in_error?: string }[]
  secret_names: string[]
}
type Props = { workspacePath: string; tabId?: string; disabled?: boolean; projectControls?: ReactNode }

/** The complete My Vault lives beside the current chat, with no separate page or chat. */
export function MyVaultPanel(props: Props) {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  if (props.disabled) return <ProjectVaultPanel {...props} />
  return <MyVaultContents key={`${server}:${actor}:${props.workspacePath}`} {...props} />
}

function MyVaultContents({ workspacePath, tabId, projectControls }: Props) {
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const setup = useVaultSetupDraft(tabId)
  const streaming = useChatStore(state => Boolean(tabId && state.chatTabs[tabId]?.isStreaming))
  const wasStreaming = useRef(false)
  const generation = useRef(0)
  const [vaults, setVaults] = useState<VaultView[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const refresh = useCallback(async () => {
    const version = ++generation.current
    setLoading(true)
    try {
      const result = JSON.parse(await agentApi.myVaultsOp({ operation: 'list' })) as { vaults?: VaultView[] }
      if (version === generation.current) { setVaults(result.vaults || []); setError('') }
    } catch { if (version === generation.current) setError('Your vaults could not be loaded. Try refreshing or ask in chat.') }
    finally { if (version === generation.current) setLoading(false) }
  }, [])
  useEffect(() => {
    void refresh()
    const focus = () => { void refresh() }
    window.addEventListener('focus', focus)
    return () => { generation.current++; window.removeEventListener('focus', focus) }
  }, [refresh])
  useEffect(() => {
    if (wasStreaming.current && !streaming) void refresh()
    wasStreaming.current = streaming
  }, [streaming, refresh])

  return <section aria-label="My Vault" className="space-y-5 text-sm">
    <section aria-label="This project's connections" className="space-y-3">
      {projectControls || <ProjectVaultPanel workspacePath={workspacePath} tabId={tabId} />}
    </section>
    <details className="rounded-lg border border-border p-3" aria-label="My accounts, computers and vaults">
      <summary className="cursor-pointer font-semibold">My accounts, computers and vaults</summary>
      <div className="space-y-5 pt-4">
    <div className="flex items-start justify-between gap-3">
      <div><h2 className="font-semibold">Accounts, computers and secrets</h2><p className="mt-1 text-xs leading-5 text-muted-foreground">Connect once, then choose where to use each account or computer.</p></div>
      <Button size="sm" variant="ghost" aria-label="Refresh My Vault" onClick={() => { void refresh() }}><RefreshCw className="h-4 w-4" /></Button>
    </div>
    {(error || setup.error) && <p role="alert" className="text-xs text-destructive">{error || setup.error}</p>}
    <div className="flex flex-wrap gap-2">
      <Button size="sm" onClick={() => { void setup.open('Help me connect an app in My Vault. Inspect saved accounts first, guide sign-in or API-key setup, return the real provider link and verify the connection. Keep project selections unless I ask to change them.') }}><Plug className="mr-2 h-4 w-4" />Connect an app</Button>
      <Button size="sm" variant="outline" onClick={() => { void setup.open('Help me add a secret to a Vault I own. Ask for its name and value, store it securely and never repeat the value. Keep project selections unchanged.') }}><KeyRound className="mr-2 h-4 w-4" />Add a secret</Button>
      <Button size="sm" variant="outline" onClick={() => { void setup.open('Help me create or share a Vault. Preserve existing memberships and grants unless I ask to change them.') }}><Users className="mr-2 h-4 w-4" />Create or share a vault</Button>
    </div>

    <PersonalAccountsPanel onChanged={() => { void refresh() }} />
    <LaptopAccountsPanel />
    <VaultSlackBots />

    <section aria-label="Your vaults" className="space-y-3">
      <h2 className="font-semibold">Your vaults</h2>
      {loading && <p role="status" className="text-xs text-muted-foreground">Loading your vaults…</p>}
      {!loading && !vaults.length && <p className="text-xs text-muted-foreground">No vaults yet. Connect an app or create your first vault above.</p>}
      {vaults.map(vault => <details key={vault.group.ID} className="overflow-hidden rounded-lg border border-border">
        <summary className="cursor-pointer px-3 py-2.5 font-medium">{vault.group.Name}<span className="ml-2 text-xs font-normal text-muted-foreground">{vault.group.ProjectRoot ? 'Project vault' : vault.role === 'owner' ? 'You manage this vault' : 'Shared with you'}</span></summary>
        <div className="space-y-3 border-t border-border p-3">
          {vault.role === 'owner' && <Button size="sm" variant="outline" onClick={() => { void setup.open(`Manage the Vault “${vault.group.Name}” (${vault.group.ID}). Help me manage its accounts, secrets, members or owners, sync tools or remove an empty vault. Inspect its current state and keep unrelated permissions and project selections.`) }}><MessageCircle className="mr-2 h-3.5 w-3.5" />Manage in chat</Button>}
          {vault.connections?.map(connection => <div key={connection.id} className="flex items-center justify-between gap-2 text-xs">
            <div className="min-w-0"><p className="break-words font-medium">{connection.label || connection.provider}</p><p className="mt-1 break-words text-muted-foreground">{connection.sign_in_error || (connection.status === 'active' ? 'Connected' : connection.status === 'authentication_required' ? 'Sign-in required' : 'Not connected')}</p></div>
            {vault.role === 'owner' && <Button size="xs" variant="ghost" onClick={() => { void setup.open(`Check or finish setup for connection ${connection.id} in Vault “${vault.group.Name}” (${vault.group.ID}). Reuse the saved account, return its real sign-in link and verify the connection. Keep project selections.`) }}>Open in chat</Button>}
          </div>)}
          {vault.secret_names.map(name => <p key={name} className="flex items-center gap-2 break-all font-mono text-xs"><KeyRound className="h-3.5 w-3.5 shrink-0" />{name.split('__').slice(1).join('__') || name}</p>)}
          {!vault.connections?.length && !vault.secret_names.length && <p className="text-xs text-muted-foreground">No accounts or secrets yet.</p>}
        </div>
      </details>)}
    </section>

    {isAdmin && <details className="rounded-lg border border-border p-3">
      <summary className="cursor-pointer font-medium">Company sign-in apps</summary>
      <p className="mt-2 text-xs text-muted-foreground">Set up the company app used when people connect their accounts.</p>
      <Button size="sm" variant="outline" className="mt-3" onClick={() => { void setup.open('Help me configure a company OAuth sign-in app. Inspect the existing setup, ask for the required client ID and secret, store them securely and never repeat secret values. Preserve existing accounts and grants.') }}>Configure in chat</Button>
    </details>}
    {isAdmin && <details className="rounded-lg border border-border p-3"><summary className="cursor-pointer font-medium">Company Slack bot</summary><div className="mt-3"><SlackAdminPanel /></div></details>}
      </div>
    </details>
  </section>
}
