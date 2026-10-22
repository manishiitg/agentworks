import { ProjectSecretsPanel } from '../../components/integrations/ProjectSecretsPanel'
import { useState } from 'react'
import { Laptop, Plus, Server, Users } from 'lucide-react'
import { WorkspacePanelBack } from '../../components/workspace/WorkspacePanelNavigation'
import { ProductSlackPanel } from '../../components/integrations/ProductSlackPanel'
import { ShareDashboardButton } from '../../components/workflow/ShareDashboardButton'
import { projectChannelLabel, type ProjectChannel } from '../../components/integrations/projectChannels'
import WorkflowBotsPanel from '../../components/workflow/WorkflowBotsPanel'
import WorkflowEmailPanel from '../../components/workflow/WorkflowEmailPanel'
import { ProjectMcpSetupPanel } from '../../components/integrations/ProjectMcpSetupPanel'
import { IntegrationNavigation, integrationNavigationOptions } from '../../components/integrations/IntegrationNavigation'
import { integrationProductAvailable, projectIntegrationSections } from '../../components/integrations/integrationSections'
import { ProjectPluginsPanel, projectPluginTabs, useProjectPluginTab } from '../../components/integrations/ProjectPluginsPanel'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { ProjectVaultPanel } from '../../components/integrations/ProjectVaultPanel'
import { ProjectKnowledgebasePanel } from '../../components/workflow/ProjectKnowledgebasePanel'
import { WorkFoldersSection } from './WorkIdentityPanel'
import { ProjectMcpPanel } from '../../components/integrations/ProjectMcpPanel'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { isWorkIntegrationTabEnabled } from './workViewGating'
import { isProjectProductId, useProjectProduct } from './projectProduct'
import { getGoogleAppsAskAIMessage } from '../../components/workflow/bots/gmailAskAI'
import { CrewPublishedAgents } from './CrewPublishedAgents'
import { ConnectedProjectAccounts } from '../../components/integrations/ConnectedProjectAccounts'

export type WorkIntegrationTab = 'apps' | 'brain' | 'folders' | 'secrets' | 'skills' | 'slack' | 'whatsapp' | 'gmail' | 'cli' | 'agents'

function integrationTabAskAIMessage(noun: string): Record<WorkIntegrationTab, string> {
  return {
    agents: 'Explain how to publish an agent from my computer into this project and reopen its private chat. Do not publish or change permissions without asking.',
    apps: `Help me with this ${noun} project's connected apps. Explain what's connected and ask what I want to add or change.`,
    brain: `Explain how Brain stores shared knowledge for this ${noun} project and how its agents can use notes, reference files and reusable skills. Check the current project access: can open and edit (the default, within existing folder permissions), can open only, or off. Explain team sharing through Brain folder roles. Ask what I want before changing settings or permissions.`,
    folders: `Help me attach things to this ${noun} project: folders or workflows and Crews as reference material. Ask what is needed and why, then set it up; folders should allow viewing only unless saving changes is needed. For shared knowledge, open Connections, then choose Brain from Add connection.`,
    secrets: `Help me select project or permitted Vault secrets for this ${noun} project. Guide setup in chat using Vault, store values encrypted and never repeat them.`,
    skills: `Help me with this ${noun} project's skills. Explain what's available and ask what I want to add or change.`,
    slack: `Help me choose the Slack account this ${noun} uses to read or send messages. Explain current account permissions before changing the selection. Use manage_slack_account for Act in Slack and manage_slack_bot to choose an existing Vault bot on this same Slack page. Manage credentials through Vault chat and never repeat token values.`,
    whatsapp: `Help me with this ${noun} project's WhatsApp bot. Explain what's connected and ask what I want to change.`,
    gmail: getGoogleAppsAskAIMessage(`${noun} project`),
    cli: 'Help me connect an AI agent to this installation through MCP. Explain the HTTP MCP URL and browser sign-in, and ask which AI app I use.',
  }
}

// Both Crew origins retain their own account, bot and feature restrictions.
// MCP client setup is an existing installation-wide path in Use & share.

export function WorkMCPTabBody({ tabId, projectId, workspacePath, onAsk, onSelectedServersChange, canEdit = false, vault = false, selectedSecrets = [], onSelectedSecretsChange = () => {}, view }: {
  view?: 'connected' | 'available'
  canEdit?: boolean
  vault?: boolean
  selectedSecrets?: string[]
  onSelectedSecretsChange?: (names: string[]) => Promise<unknown> | void
  tabId: string
  projectId: string
  workspacePath: string
  onAsk: (message: string) => Promise<void>
  onSelectedServersChange: (servers: string[]) => Promise<unknown>
}) {
  const chatSessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  const selectedServers = useChatStore(state => state.chatTabs[tabId]?.config.selectedServers || [])
  const setSelected = async (servers: string[]) => {
    const store = useChatStore.getState()
    const selected = servers.length > 0 ? servers : ['NO_SERVERS']
    try {
      await onSelectedServersChange(servers)
    } catch (cause) {
      store.addToast(cause instanceof Error ? cause.message : 'Could not save project integrations.', 'error')
      throw cause
    }
    for (const tab of Object.values(store.chatTabs)) {
      if (!projectId || !isProjectProductId(tab.metadata?.agentProfileId) || tab.metadata?.agentProfileProjectId !== projectId) continue
      store.setTabConfig(tab.tabId, { selectedServers: selected })
      store.setTabMetadata(tab.tabId, { agentProfileMCPSelectionInitialized: true, agentProfileRuntimeDirty: true })
    }
  }

  return (
    <div className="flex flex-col gap-3">
      {vault ? <ProjectVaultPanel selectedSecrets={selectedSecrets} onSelectedSecretsChange={onSelectedSecretsChange} selectedServers={selectedServers} onSelectedServersChange={setSelected} disabled={!canEdit} /> : <ProjectMcpPanel view={view} chatSessionId={chatSessionId} workspacePath={workspacePath} placeNoun="Crew" canEdit={canEdit} onAsk={onAsk}
        selectedServers={selectedServers} onSelectedServersChange={setSelected} />}
    </div>
  )
}

export function WorkIntegrationsPanel({ canEdit = false, workspacePath, projectId, projectTitle, tabId, enabledPanels, onAsk, onSelectedServersChange, workflowContextPaths, onWorkflowContextPathsChange, selectedSecrets = [], selectedGlobalSecrets = [], onSelectedSecretsChange, onSelectedGlobalSecretsChange }: {
  canEdit?: boolean
  workspacePath: string
  projectId: string
  projectTitle: string
  projectTemplates: Array<{ id: string; version: number }>
  tabId: string
  selectedSecrets?: string[]
  selectedGlobalSecrets?: string[]
  onSelectedSecretsChange?: (names: string[]) => Promise<unknown>
  onSelectedGlobalSecretsChange?: (names: string[]) => Promise<unknown>
  enabledPanels?: Set<string>
  onAsk: (message: string) => Promise<void>
  onSelectedServersChange: (servers: string[]) => Promise<unknown>
  workflowContextPaths: string[]
  onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>
}) {
  // The Connect tab points at this installation's API origin. Hosted apps need
  // a public origin; local agents can connect directly to a loopback MCP URL.
  const product = useProjectProduct()
  const chatSessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const visibleTabs = projectIntegrationSections(allowedProducts).filter(option =>
    isWorkIntegrationTabEnabled(option.value, enabledPanels))
  const [tab, setTab] = useState<WorkIntegrationTab>('apps')
  const activeTab = (tab === 'agents' && canEdit) || visibleTabs.some(option => option.value === tab) ? tab : visibleTabs[0]?.value ?? 'apps'
  // Every tab loads on mount, so Refresh always remounts.
  const [tabNonce, setTabNonce] = useState(0)
  const [preferredPluginTab, setPluginTab] = useProjectPluginTab()
  const [overview, setOverview] = useState(true)
  const [sharing, setSharing] = useState(false)
  const [shareChannel, setShareChannel] = useState<ProjectChannel>('cli')
  const [shareSection, setShareSection] = useState<'people' | 'owners' | undefined>()
  const [slackSection, setSlackSection] = useState<'account' | 'bot' | undefined>()
  const pluginTabs = projectPluginTabs(allowedProducts).filter(option => option.value !== 'skills').filter(option => !enabledPanels || (option.value === 'secrets' ? enabledPanels.has('secrets') || integrationProductAvailable('mcp-gateway', allowedProducts) : enabledPanels.has('mcp')))
  const pluginTab = overview && pluginTabs.some(option => option.value === 'connected') ? 'connected' : preferredPluginTab
  const activePluginTab = pluginTabs.some(option => option.value === pluginTab) ? pluginTab : pluginTabs[0]?.value ?? 'connected'
  const navigationOptions = [...integrationNavigationOptions(visibleTabs, pluginTabs), ...(canEdit ? [{ value: 'agents', label: 'Chats from your computer', icon: Laptop }] : [])]
  const selectConnection = (target: string) => {
    if (target === 'share' || target === 'share-owners') { setSharing(true); setShareChannel('share'); setShareSection(target === 'share-owners' ? 'owners' : 'people'); return }
    if ((target === 'slack-account' || target === 'slack-bot') && visibleTabs.some(option => option.value === 'slack')) { setSharing(true); setShareChannel('slack'); setSlackSection(target === 'slack-bot' ? 'bot' : 'account'); return }
    setShareSection(undefined); setSlackSection(undefined)
    if (target === 'agents' && canEdit) { setSharing(false); setOverview(false); setTab('agents'); return }
    if (target === 'share' || target === 'cli' || (target === 'slack' && visibleTabs.some(option => option.value === 'slack')) || (target === 'whatsapp' && visibleTabs.some(option => option.value === 'whatsapp'))) { setSharing(true); setShareChannel(target as ProjectChannel); return }
    if (target === 'overview' || target === 'apps') { setSharing(false); setOverview(true); setTab('apps'); return }
    if (!visibleTabs.some(option => option.value === target) && !pluginTabs.some(option => option.value === target)) return
    setSharing(false); setOverview(false)
    if (visibleTabs.some(option => option.value === target)) setTab(target as WorkIntegrationTab)
    else if (pluginTabs.some(option => option.value === target)) { setTab('apps'); setPluginTab(target as typeof pluginTab) }
  }
  // Existing bubble and Ctrl+K targets open their resource directly.
  useWorkspaceViewTarget('mcp', selectConnection)
  const activeConnection = activeTab === 'apps' ? activePluginTab : activeTab
  const connectionLabel = navigationOptions.find(option => option.value === (activeConnection === 'available' ? 'connected' : activeConnection))?.label ?? 'MCPs'
  const mcpTabs = pluginTabs.filter(option => option.value === 'connected' || option.value === 'available').map(option => ({ ...option, label: option.value === 'connected' ? 'Connected' : 'Available' }))
  const selectedServers = useChatStore(state => state.chatTabs[tabId]?.config.selectedServers || [])



  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {!sharing && !overview && activeTab === 'apps' && activePluginTab === 'available' && <WorkspacePanelBack label="Back to MCPs" onBack={() => selectConnection('connected')} />}
      <WorkspaceViewHeader
        icon={sharing ? Users : Server}
        title={sharing ? shareChannel === 'share' ? shareSection === 'owners' ? 'Owners & collaborators' : 'People & access' : shareChannel === 'slack' && slackSection ? slackSection === 'bot' ? 'Slack bot' : 'Slack account' : projectChannelLabel(shareChannel) : 'Connections'}
        helpTopic={sharing ? projectChannelLabel(shareChannel) : overview ? 'Connections' : `${activeTab === 'slack' ? 'Connections' : 'Integrations'} · ${connectionLabel}`}
        actions={(
          <WorkspaceViewActions
            workspacePath={workspacePath}
            message={sharing ? 'Help me share this project with people or make it available through its existing channels. Explain current access before changing sharing, routes or permissions.' : integrationTabAskAIMessage(product.noun)[activeTab === 'apps' && activePluginTab === 'secrets' ? activePluginTab : activeTab]}
            onAsk={onAsk}
            onRefresh={() => setTabNonce(nonce => nonce + 1)}
            refreshLabel={sharing ? 'Refresh sharing and channels' : overview ? 'Refresh connections' : `Refresh ${connectionLabel}`}
          >
            {!sharing && !overview && activeTab === 'apps' && activePluginTab === 'connected' && canEdit && <button type="button" onClick={() => selectConnection('available')} className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Plus className="h-3.5 w-3.5" aria-hidden="true" />Add an MCP</button>}
          </WorkspaceViewActions>
        )}
        below={!sharing && !overview && activeTab === 'apps' && (activePluginTab === 'connected' || activePluginTab === 'available')
          ? undefined
          : !sharing ? <IntegrationNavigation options={navigationOptions} value={activeConnection} onChange={selectConnection} overview={overview} onOverview={() => selectConnection('overview')} canEdit={canEdit} /> : undefined}
        /* No Connected/Available tabs: the MCPs page lists yours and offers Add (owner, 2026-10-20). */
      />
      <div data-ui-view-section={sharing ? shareChannel === 'share' && shareSection === 'owners' ? 'share-owners' : shareChannel === 'slack' && slackSection ? `slack-${slackSection}` : shareChannel : activeConnection} key={`${sharing ? shareChannel : activeTab}:${tabNonce}`} className={`min-h-0 flex-1 overflow-y-auto p-4 ${sharing ? 'w-full max-w-3xl' : ''}`}>
        {sharing ? <>
          {shareChannel === 'share' ? <ShareDashboardButton workspacePath={workspacePath} variant="panel" section={shareSection} /> : shareChannel === 'cli' ? <ProjectMcpSetupPanel workspacePath={workspacePath} /> : <>{shareChannel === 'slack' ? <ProductSlackPanel workspacePath={workspacePath} noun="Crew" section={slackSection} /> : <WorkflowBotsPanel workspacePath={workspacePath} fixedChannel="whatsapp" scopeNoun="project" onAsk={onAsk} target={{ profileId: product.profileId, conversationKey: projectId, label: projectTitle }} />}</>}
        </> : <>
        {overview && canEdit && <button type="button" onClick={() => selectConnection('agents')} className="mb-4 flex w-full items-center gap-2 rounded-md border border-border p-3 text-left text-sm hover:bg-muted/40"><Laptop className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />Chats from your computer</button>}
        {activeTab === 'agents' && canEdit && <CrewPublishedAgents key={`${product.profileId}:${projectId}`} profileId={product.profileId} projectId={projectId} />}
        {overview && <ConnectedProjectAccounts workspacePath={workspacePath} options={navigationOptions} onOpen={selectConnection} />}
        {activeTab === 'apps' && <ProjectPluginsPanel tab={activePluginTab}
          connections={(!enabledPanels || enabledPanels.has('mcp')) ? (view => (<ProjectMcpPanel view={view} chatSessionId={chatSessionId} workspacePath={workspacePath} placeNoun={product.noun} canEdit={canEdit} onAsk={onAsk} selectedServers={selectedServers} onSelectedServersChange={async servers => {
              await onSelectedServersChange(servers)
              const store = useChatStore.getState()
              for (const chat of Object.values(store.chatTabs)) {
                if (chat.metadata?.agentProfileId === 'work' && chat.metadata?.agentProfileProjectId === projectId) {
                  store.setTabConfig(chat.tabId, { selectedServers: servers.length ? servers : ['NO_SERVERS'] })
                  store.setTabMetadata(chat.tabId, { agentProfileRuntimeDirty: true, agentProfileMCPSelectionInitialized: true })
                }
              }
            }} />)) : undefined}
          secrets={(!enabledPanels || enabledPanels.has('secrets')) ? <ProjectSecretsPanel placeNoun={product.noun}
            selectedSecrets={selectedSecrets} workspacePath={workspacePath} onSecretChange={names => onSelectedSecretsChange?.(names)}
            selectedGlobalSecrets={selectedGlobalSecrets} onGlobalSecretChange={onSelectedGlobalSecretsChange ? names => onSelectedGlobalSecretsChange(names ?? []) : undefined} /> : undefined}

          vault={<WorkMCPTabBody canEdit={canEdit} vault tabId={tabId} projectId={projectId} workspacePath={workspacePath} onAsk={onAsk} onSelectedServersChange={onSelectedServersChange} selectedSecrets={selectedGlobalSecrets} onSelectedSecretsChange={names => onSelectedGlobalSecretsChange?.(names)} />}
        />}
        {activeTab === 'brain' && <ProjectKnowledgebasePanel workspacePath={workspacePath} onAsk={onAsk} />}
        {activeTab === 'folders' && <WorkFoldersSection
          workspacePath={workspacePath}
          workflowContextPaths={workflowContextPaths}
          onWorkflowContextPathsChange={onWorkflowContextPathsChange}
          onAsk={onAsk}
          askMessage={integrationTabAskAIMessage(product.noun).folders}
        />}
        {activeTab === 'gmail' && (
          <>
            {/* The deployment's Google app: an admin stores the Google OAuth client once. */}
            {/* All products use the server's Google app; the shared email panel
                preserves private Code ownership and shared-account admin permissions. */}
            <WorkflowEmailPanel
              workspacePath={workspacePath}
              scopeNoun="project"
              onAsk={onAsk}
            />
          </>
        )}
        </>}
      </div>
    </div>
  )
}
