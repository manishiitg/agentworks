import { lazy, Suspense, useState } from 'react'
import { Blocks, Cpu, Globe, KeyRound, Plug, Sparkles, Users, Terminal, Info, RotateCcw } from 'lucide-react'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { AccountWhatsAppPanel, AccountGitHubPanel, AccountComputerUsePanel } from './integrations/AccountConnectionPanels'
import { AccountSlackOverview } from './integrations/AccountSlackOverview'
import PersonalAccountsPanel from './integrations/PersonalAccountsPanel'
import { BrowserConnectionsPanel } from './BrowserConnectionsDialog'
import { useCanManageVault } from './integrations/OpenVaultButton'
import { APP_VERSION } from '../version'
import { openLocalSetup } from '../utils/homeNavigation'
import { Button } from './ui/Button'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'

const UsersContent = lazy(() => import('./UsersPage').then(module => ({ default: module.UsersPageContent })))
const ConnectContent = lazy(() => import('./McpConnectPage').then(module => ({ default: module.McpConnectPageContent })))
const CodingProvidersPanel = lazy(() => import('./providers/CodingProvidersPanel'))
const VaultSecrets = lazy(() => import('./secrets/SecretSelectionSection'))
const AccountMcpPanel = lazy(() => import('./integrations/AccountMcpPanel'))
const categories = [
  { id: 'apps', label: 'Apps', icon: Plug },
  { id: 'ai', label: 'AI accounts', icon: Cpu },
  { id: 'browser', label: 'Browser', icon: Globe },
  { id: 'mcp', label: 'MCP servers', icon: Blocks },
  { id: 'skills', label: 'Plugins & skills', icon: Sparkles },
  { id: 'secrets', label: 'Secrets', icon: KeyRound },
  { id: 'users', label: 'Users & access', icon: Users },
  { id: 'connect', label: 'Connect AI tools', icon: Terminal },
  { id: 'about', label: 'About', icon: Info },
] as const
type Category = typeof categories[number]['id']

/** Account setup only: never pass a Crew path or alter a project's selections. */
export default function ConnectionsToolsPage() {
  const actor = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  // Remount personal inventories on account/network changes; no previous account's rows survive.
  return <ConnectionsToolsContent key={JSON.stringify([workspace, actor])} />
}

function ConnectionsToolsContent() {
  const user = useAuthStore(state => state.user)
  const availableCategories = categories.filter(item => item.id === 'users' ? user?.is_admin === true : item.id !== 'connect' || Boolean(user))
  const [category, setCategory] = useState<Category>('apps')
  const choose = (next: Category) => setCategory(next)
  return <section aria-label="Settings" className="flex h-full min-h-0 flex-col bg-background">
    <WorkspacePageHeader page="connections" title="Settings" icon={Plug} />
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-5xl space-y-5 p-4 sm:p-6">
        <div><h2 className="text-xl font-semibold tracking-tight">Settings</h2><p className="mt-1 text-sm text-muted-foreground">Manage your accounts, connections and tools.</p></div>
          <nav aria-label="Connection categories" className="flex gap-1 overflow-x-auto border-b border-border">{availableCategories.map(({ id, label, icon: Icon }) => <button key={id} type="button" aria-current={category === id ? 'page' : undefined} onClick={() => choose(id)} className={`flex shrink-0 items-center gap-2 border-b-2 px-3 py-2 text-xs font-medium ${category === id ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}><Icon className="h-3.5 w-3.5" aria-hidden="true" />{label}</button>)}</nav>
          <Suspense fallback={<p role="status" className="text-sm text-muted-foreground">Loading settings…</p>}>
            {category === 'apps' && <div className="space-y-4">
              <AccountSlackOverview />
              <PersonalAccountsPanel provider="google" hideHeading />
              <AccountWhatsAppPanel />
              <AccountGitHubPanel />
              <AccountComputerUsePanel />
            </div>}
            {category === 'ai' && <div className="h-[65vh] min-h-96"><CodingProvidersPanel embedded isOpen onClose={() => choose('apps')} /></div>}
            {category === 'browser' && <BrowserConnectionsPanel open onClose={() => {}} inline startSetup />}
            {category === 'mcp' && <AccountMcpPanel />}
            {category === 'skills' && <div className="space-y-2 rounded-lg border border-border p-4"><h3 className="text-sm font-medium">Skills belong to your work</h3><p className="text-sm text-muted-foreground">Open a Crew’s Skills panel to view, add or manage its skills. Plugin and tool selections also stay with that Crew; there is no separate account-wide plugin library.</p></div>}
            {category === 'secrets' && <AccountVaultSecrets />}
            {category === 'users' && user?.is_admin && <UsersContent />}
            {category === 'connect' && user && <ConnectContent />}
            {category === 'about' && <div className="space-y-4"><div><h3 className="text-sm font-medium">AgentWorks</h3><p className="text-sm text-muted-foreground">Version {APP_VERSION}</p></div>{window.electronAPI?.getDesktopConnection?.() === 'local' && <Button variant="outline" size="sm" onClick={openLocalSetup}><RotateCcw className="h-4 w-4" />Run setup again</Button>}</div>}
          </Suspense>
      </div>
    </div>
  </section>
}

function AccountVaultSecrets() {
  const canManage = useCanManageVault()
  return canManage
    ? <VaultSecrets mode="vault" />
    : <p role="status" className="text-sm text-muted-foreground">Secrets are managed by Vault managers. Ask a Vault manager to set up the secrets your Crew needs.</p>
}
