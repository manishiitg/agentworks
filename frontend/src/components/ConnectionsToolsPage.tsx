import { lazy, Suspense, useState } from 'react'
import { Blocks, Cpu, Globe, KeyRound, Plug, Sparkles, Users, Terminal, Info, RotateCcw, DollarSign, Server } from 'lucide-react'
import { WorkspacePageHeader } from './workspace/WorkspacePageHeader'
import { AccountWhatsAppPanel, AccountGitHubPanel, AccountComputerUsePanel } from './integrations/AccountConnectionPanels'
import { AccountSlackOverview } from './integrations/AccountSlackOverview'
import PersonalAccountsPanel from './integrations/PersonalAccountsPanel'
import { BrowserConnectionsPanel } from './BrowserConnectionsDialog'
import { useCanManageVault } from './integrations/OpenVaultButton'
import { APP_VERSION } from '../version'
import { openLocalSetup } from '../utils/homeNavigation'
import { Button } from './ui/Button'
import { useCanReviewCode } from '../hooks/useCanReviewCode'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'

const UsersContent = lazy(() => import('./UsersPage').then(module => ({ default: module.UsersPageContent })))
const ConnectContent = lazy(() => import('./McpConnectPage').then(module => ({ default: module.McpConnectPageContent })))
const SettingsAiAccounts = lazy(() => import('./providers/SettingsAiAccounts'))
const CostsOverview = lazy(() => import('./providers/CostsOverview'))
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
  { id: 'servers', label: 'Connected servers', icon: Server },
  { id: 'costs', label: 'Costs', icon: DollarSign },
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
  const canReview = useCanReviewCode()
  const localMac = window.electronAPI?.getDesktopConnection?.() === 'local'
  const availableCategories = categories.filter(item => item.id === 'users' ? user?.is_admin === true : item.id === 'servers' ? localMac : item.id === 'costs' ? canReview : item.id !== 'connect' || Boolean(user))
  const [category, setCategory] = useState<Category>('apps')
  const current = availableCategories.find(item => item.id === category) || availableCategories[0]
  const choose = (next: Category) => setCategory(next)
  return <section aria-label="Settings" className="flex h-full min-h-0 flex-col bg-background">
    <WorkspacePageHeader page="connections" title="Settings" icon={Plug} />
    <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <nav aria-label="Settings sections" className="flex shrink-0 gap-1 overflow-x-auto border-b border-border bg-muted/10 p-3 md:w-52 md:flex-col md:overflow-y-auto md:border-b-0 md:border-r md:px-3 md:py-7">
        {availableCategories.map(({ id, label, icon: Icon }) => <button key={id} type="button" aria-current={current.id === id ? 'page' : undefined} onClick={() => choose(id)} className={`flex shrink-0 items-center gap-2.5 rounded-md px-3 py-2 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${current.id === id ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}`}><Icon className="h-4 w-4 shrink-0" aria-hidden="true" />{label}</button>)}
      </nav>
      <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-5xl space-y-7 px-5 py-7 sm:px-10 sm:py-10">
          {current.id !== 'ai' && <h2 className="text-2xl font-semibold tracking-tight">{current.label}</h2>}
          <Suspense fallback={<p role="status" className="text-sm text-muted-foreground">Loading settings…</p>}>
            {current.id === 'apps' && <div className="space-y-4">
              <AccountSlackOverview />
              <PersonalAccountsPanel provider="google" hideHeading />
              <AccountWhatsAppPanel />
              <AccountGitHubPanel />
              <AccountComputerUsePanel />
            </div>}
            {current.id === 'ai' && <SettingsAiAccounts onCosts={canReview ? () => choose('costs') : undefined} />}
            {current.id === 'servers' && localMac && <SettingsAiAccounts serversOnly />}
            {current.id === 'costs' && canReview && <CostsOverview />}
            {current.id === 'browser' && <BrowserConnectionsPanel open onClose={() => {}} inline startSetup />}
            {current.id === 'mcp' && <AccountMcpPanel />}
            {current.id === 'skills' && <div className="space-y-2 rounded-lg border border-border p-4"><h3 className="text-sm font-medium">Skills belong to your work</h3><p className="text-sm text-muted-foreground">Open a Crew’s Skills panel to view, add or manage its skills. Plugin and tool selections also stay with that Crew; there is no separate account-wide plugin library.</p></div>}
            {current.id === 'secrets' && <AccountVaultSecrets />}
            {current.id === 'users' && user?.is_admin && <UsersContent />}
            {current.id === 'connect' && user && <ConnectContent />}
            {current.id === 'about' && <div className="space-y-4"><div><h3 className="text-sm font-medium">AgentWorks</h3><p className="text-sm text-muted-foreground">Version {APP_VERSION}</p></div>{window.electronAPI?.getDesktopConnection?.() === 'local' && <Button variant="outline" size="sm" onClick={openLocalSetup}><RotateCcw className="h-4 w-4" />Run setup again</Button>}</div>}
          </Suspense>
        </div>
      </main>
    </div>
  </section>
}

function AccountVaultSecrets() {
  const canManage = useCanManageVault()
  return canManage
    ? <VaultSecrets mode="vault" />
    : <p role="status" className="text-sm text-muted-foreground">Secrets are managed by Vault managers. Ask a Vault manager to set up the secrets your Crew needs.</p>
}
