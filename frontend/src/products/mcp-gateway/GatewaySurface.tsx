import { useProductUrlView } from '../../hooks/useProductUrlView'
import { useWorkspaceViewPreference } from '../../hooks/useWorkspaceViewPreference'
import { normalizeViewFrom } from '../../utils/workspaceViewPreference'
import { useCallback, useEffect, useState } from 'react'
import { ScrollText, PlugZap, BarChart3, Vault } from 'lucide-react'
import { gatewayBaseUrl } from '../productSurfaceConfig'
import { ProductBreadcrumb } from '../../components/workspace/ProductBreadcrumb'
import { ModePresetBar } from '../../components/ModePresetBar'
import { ProductWorkspaceShell } from '../../components/workspace/ProductWorkspaceShell'
import { WorkspacePageContent } from '../../components/workspace/WorkspacePageContent'
import { useRegisterPanelSwitcher } from '../../stores/usePanelSwitcherStore'
import { VAULT_PANEL_SECTIONS } from '../productPanels'
import { GlobalHumanFeedbackPrompt } from '../../components/GlobalHumanFeedbackPrompt'
import { UpdateProgressToast } from '../../components/UpdateProgressToast'
import SchedulesPage from '../../components/SchedulesPage'
import AdminPages from '../../components/AdminPages'
import LlmModalHost from '../../components/topbar/LlmModalHost'
import { GatewayServersPanel } from './GatewayServersPanel'
import { VaultReadOnlyContext } from './vaultReadOnly'
import { GatewayWorkspacePane, gatewayPanels, type GatewayPanel } from './GatewayWorkspacePane'
import { GatewayAuditPanel } from './GatewayAuditPanel'
import { GatewayConnectPanel } from './GatewayConnectPanel'
import { WorkspaceViewTabs } from '../../components/workflow/WorkspaceViewTabs'
import { SettingsCardLayout } from '../../components/ui/SettingsCard'
import { GatewayFeedbackBoundary } from './gatewayConsoleShared'
import { GATEWAY_AUTH_REQUIRED_EVENT } from './gatewayAdminApi'
import { useAuthStore } from '../../stores/useAuthStore'
import { useAppStore } from '../../stores/useAppStore'
import { useLLMStore } from '../../stores/useLLMStore'
import { useChatStore } from '../../stores/useChatStore'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
type VaultPage = 'audit' | 'connect'
import { openLastProjectVaultChat } from '../../utils/projectVaultNavigation'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'



const normalizeVaultPanel = normalizeViewFrom(gatewayPanels.map(panel => panel.id))

function GatewayAdminWorkspace({ base, standalone, page, onPage }: { base: string; standalone: boolean; page: VaultPage | null; onPage: (page: VaultPage | null) => void }) {
  const [panel, setPanel] = useWorkspaceViewPreference<GatewayPanel>('mcp-gateway', base, 'servers', normalizeVaultPanel)
  const [peopleTabRequest, setPeopleTabRequest] = useState<{ tab: 'users' | 'groups'; token: number }>()
  const openPanel = (next: GatewayPanel) => { setPanel(next); onPage(null) }
  useProductUrlView('mcp-gateway', { view: page ?? panel })
  useRegisterPanelSwitcher('mcp-gateway', gatewayPanels.map(({ id, label }) => ({ id, label, sections: VAULT_PANEL_SECTIONS[id] })), (id, section) => {
    const next = normalizeVaultPanel(id)
    if (!next) return
    openPanel(next)
    if (id === 'people' && (section === 'users' || section === 'groups')) setPeopleTabRequest(current => ({ tab: section, token: (current?.token ?? 0) + 1 }))
  })
  useEffect(() => {
    const open = (event: Event) => {
      const requested = normalizeVaultPanel((event as CustomEvent).detail)
      if (requested) openPanel(requested)
    }
    const home = (event: Event) => { if ((event as CustomEvent).detail === 'mcp-gateway') openPanel('servers') }
    window.addEventListener('vault-open-panel', open)
    window.addEventListener('open-product-home', home)
    return () => { window.removeEventListener('vault-open-panel', open); window.removeEventListener('open-product-home', home) }
  }, [setPanel, onPage])
  const addCustomServer = async () => {
    try {
      const tabId = await openLastProjectVaultChat()
      await openWorkspacePaneChatWithDraft({ tabId, message: 'Help me connect a custom MCP app in Vault. Ask for its name and URL, then guide setup in this chat.' })
    } catch (error) { useChatStore.getState().addToast(error instanceof Error ? error.message : 'Open a Crew or Goal chat to set up Vault.', 'error') }
  }
  const title = page === 'audit' ? 'Audit logs' : page === 'connect' ? 'Vault MCP endpoint' : gatewayPanels.find(item => item.id === panel)?.label ?? 'Vault'
  return <ProductWorkspaceShell testId="gateway-setup-panel" chatOpen panelOpen={false} splitRatio={1} toolbarWhenPanelClosed
    onOpenChat={() => {}} onOpenWorkspace={() => {}} toolbar={null} workspace={null} divider={null}
    chatProps={{ 'aria-label': 'Vault management' }}
    tabs={<div className="flex min-w-0 flex-1 items-center gap-2"><ProductBreadcrumb surface="mcp-gateway" standalone={standalone} section={title} menuItems={[
      { id: 'my-vaults', label: 'My vaults', icon: Vault, onSelect: () => { void openLastProjectVaultChat().catch(error => useChatStore.getState().addToast(error.message, 'error')) } },
      ...gatewayPanels.map(item => ({ ...item, active: !page && panel === item.id, onSelect: () => openPanel(item.id) })),
      { id: 'audit', label: 'Audit logs', icon: ScrollText, active: page === 'audit', onSelect: () => onPage('audit') },
      { id: 'connect', label: 'Vault MCP endpoint', icon: PlugZap, active: page === 'connect', onSelect: () => onPage('connect') },
    ]} onHome={() => openPanel('servers')} /></div>}
    chat={page ? <GatewayManagementPage base={base} page={page} /> : <GatewayWorkspacePane base={base} panel={panel} chatBusy={false} peopleTabRequest={peopleTabRequest}
      servers={<GatewayServersPanel base={base} view={panel === 'available-mcps' ? 'available' : 'connected'} onConnected={() => openPanel('servers')} onBrowse={() => openPanel('available-mcps')} standalone={standalone} onAddCustom={addCustomServer} />} modelSettings={null} />}
  />
}

function GatewayManagementPage({ base, page }: { base: string; page: VaultPage }) {
  const [auditTab, setAuditTab] = useState<'logs' | 'analysis'>('logs')
  return <section aria-label={page === 'audit' ? 'Vault audit page' : 'Vault MCP endpoint page'} className="flex h-full min-h-0 flex-col">
    {page === 'audit' && <WorkspaceViewTabs value={auditTab} onChange={value => setAuditTab(value === 'analysis' ? 'analysis' : 'logs')}
      options={[{ value: 'logs', label: 'Logs', icon: ScrollText }, { value: 'analysis', label: 'Analysis', icon: BarChart3 }]} ariaLabel="Audit tabs" />}
    <WorkspacePageContent className="min-h-0 flex-1 overflow-y-auto"><SettingsCardLayout unboxed><GatewayFeedbackBoundary>
      {page === 'audit' ? <GatewayAuditPanel base={base} tab={auditTab} /> : <GatewayConnectPanel base={base} />}
    </GatewayFeedbackBoundary></SettingsCardLayout></WorkspacePageContent>
  </section>
}

/** Same header, global pages, chat runtime and split shell as Crew and Code. */
const normalizeVaultPage = normalizeViewFrom(['workspace', 'audit', 'connect'] as const)

export function GatewaySurface({ standalone = false }: { standalone?: boolean } = {}) {
  const base = gatewayBaseUrl() ?? (standalone ? window.location.origin : null)
  const user = useAuthStore(state => state.user)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const checkAuth = useAuthStore(state => state.checkAuth)
  const showProviders = useLLMStore(state => state.showLLMModal)
  const showSchedules = useAppStore(state => state.showSchedulesOverview)
  const adminPage = useAppStore(state => state.adminPage)
  const [savedPage, selectPage] = useWorkspaceViewPreference('mcp-gateway', `${base ?? 'main'}:page`, 'workspace', normalizeVaultPage)
  const page = savedPage === 'workspace' ? null : savedPage
  const setPage = useCallback((next: VaultPage | null) => selectPage(next ?? 'workspace'), [selectPage])
  useEffect(() => {
    try {
      const requested = sessionStorage.getItem('vault.requested-panel')
      if (requested === 'audit' || requested === 'connect') {
        sessionStorage.removeItem('vault.requested-panel')
        setPage(requested)
      } else if (requested) setPage(null)
    } catch { /* Optional navigation request. */ }
  }, [setPage])
  useEffect(() => {
    const open = (event: Event) => {
      const requested = (event as CustomEvent).detail
      setPage(requested === 'audit' || requested === 'connect' ? requested : null)
    }
    window.addEventListener('vault-open-panel', open)
    return () => window.removeEventListener('vault-open-panel', open)
  }, [setPage])
  useEffect(() => {
    const onAuthRequired = () => { void checkAuth() }
    window.addEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
    return () => window.removeEventListener(GATEWAY_AUTH_REQUIRED_EVENT, onAuthRequired)
  }, [checkAuth])
  // Vault readers see every Vault screen with nothing they can change.
  const canViewVault = user?.is_admin === true || user?.is_vault_reader === true
  return <VaultReadOnlyContext.Provider value={user?.is_admin !== true}><div className="flex h-screen min-h-0 bg-background" data-testid="gateway-surface">
    <UpdateProgressToast /><GlobalHumanFeedbackPrompt />
    <ModePresetBar reduced />
    <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      <LlmModalHost />
      {showSchedules && !showProviders && <SchedulesPage />}
      {adminPage && !showProviders && <AdminPages />}
      <div className={showProviders || showSchedules || adminPage ? 'hidden' : 'h-full'}>
        {!base ? <div className="grid h-full place-items-center p-6 text-sm text-muted-foreground">Vault needs an MCP Gateway endpoint for this deployment.</div>
          : canViewVault ? <GatewayAdminWorkspace key={`${workspace}:${base}:${user?.id ?? ''}:${user?.is_admin === true ? 'admin' : 'reader'}`} base={base} standalone={standalone} page={page} onPage={setPage} />
          : <main className="grid h-full place-items-center p-6 text-sm text-muted-foreground" role="status">Vault needs an administrator or a Vault reader account. Your product administrator manages this access in Users &amp; access.</main>}
      </div>
    </div>
  </div></VaultReadOnlyContext.Provider>
}
