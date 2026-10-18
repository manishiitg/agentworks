import { ProjectSecretsPanel } from '../../components/integrations/ProjectSecretsPanel'
import { useRef, useState } from 'react'
import { Server, Users } from 'lucide-react'
import SlackAccountsPanel from '../../components/integrations/SlackAccountsPanel'
import { UseSharePanel, UseShareBack, type UseShareChannel } from '../../components/integrations/UseSharePanel'
import SkillsManagerPanel from '../../components/skills/SkillsManagerPanel'
import WorkflowBotsPanel from '../../components/workflow/WorkflowBotsPanel'
import WorkflowEmailPanel from '../../components/workflow/WorkflowEmailPanel'
import { ProjectMcpSetupPanel } from '../../components/integrations/ProjectMcpSetupPanel'
import { IntegrationNavigation, integrationNavigationOptions } from '../../components/integrations/IntegrationNavigation'
import { projectIntegrationSections } from '../../components/integrations/integrationSections'
import { ProjectPluginsPanel, projectPluginTabs, useProjectPluginTab } from '../../components/integrations/ProjectPluginsPanel'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { ProjectVaultPanel } from '../../components/integrations/ProjectVaultPanel'
import { ProjectKnowledgebasePanel } from '../../components/workflow/ProjectKnowledgebasePanel'
import { WorkFoldersSection } from './WorkIdentityPanel'
import { ProjectMcpPanel } from '../../components/integrations/ProjectMcpPanel'
import { McpAppsSection } from './McpAppsSection'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { isWorkIntegrationTabEnabled } from './workViewGating'
import { isProjectProductId, useProjectProduct } from './projectProduct'
import { getGoogleAppsAskAIMessage, useGmailInboundUIEnabled } from '../../components/workflow/bots/gmailAskAI'
import { useCodeFilesPreference } from './codeLocalFiles'
import { ConnectedProjectAccounts } from '../../components/integrations/ConnectedProjectAccounts'

export type WorkIntegrationTab = 'apps' | 'brain' | 'folders' | 'secrets' | 'skills' | 'slack' | 'whatsapp' | 'gmail' | 'cli'

function integrationTabAskAIMessage(noun: string, incomingGmail: boolean): Record<WorkIntegrationTab, string> {
  return {
    apps: `Help me with this ${noun} project's connected apps. Explain what's connected and ask what I want to add or change.`,
    brain: `Explain how Brain stores shared knowledge for this ${noun} project and how its agents can use notes, reference files and reusable skills. Check the current project access: can open and edit (the default, within existing folder permissions), can open only, or off. Explain team sharing through Brain folder roles. Ask what I want before changing settings or permissions.`,
    folders: `Help me attach things to this ${noun} project: folders or workflows and Crews as read-only context. Ask what is needed and why, then set it up; folder access should be read-only unless writing is truly needed. For shared knowledge, open Connections, then choose Brain from Add connection.`,
    secrets: `Help me select project or permitted Vault secrets for this ${noun} project. Never ask for secret values in chat.`,
    skills: `Help me with this ${noun} project's skills. Explain what's available and ask what I want to add or change.`,
    slack: `Help me choose the Slack account this ${noun} uses to read or send messages. Explain current account permissions before changing the selection. Incoming Slack app conversations are configured in Use & share. Never request tokens in chat.`,
    whatsapp: `Help me with this ${noun} project's WhatsApp bot. Explain what's connected and ask what I want to change.`,
    gmail: getGoogleAppsAskAIMessage(`${noun} project`, incomingGmail),
    cli: 'Help me connect an AI agent to this installation through MCP. Explain the HTTP MCP URL and browser sign-in, and ask which AI app I use.',
  }
}

// Both Crew origins retain their own account, bot and feature restrictions.
// MCP client setup is an existing installation-wide path in Use & share.

export function WorkMCPTabBody({ tabId, projectId, workspacePath, onAsk, onSelectedServersChange, vault = false, selectedSecrets = [], onSelectedSecretsChange = () => {}, view }: {
  view?: 'connected' | 'available'
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
      {vault ? <ProjectVaultPanel selectedSecrets={selectedSecrets} onSelectedSecretsChange={onSelectedSecretsChange} disabled={workspacePath.startsWith('_users/')} /> : <ProjectMcpPanel view={view} chatSessionId={chatSessionId} workspacePath={workspacePath} placeNoun="Crew" canEdit={!workspacePath.startsWith('_users/')} onAsk={onAsk}
        selectedServers={selectedServers} onSelectedServersChange={setSelected} />}
    </div>
  )
}

export function WorkIntegrationsPanel({ workspacePath, projectId, projectTitle, tabId, enabledPanels, onAsk, onSelectedServersChange, onSelectedSkillsChange, workflowContextPaths, onWorkflowContextPathsChange, selectedSecrets = [], selectedGlobalSecrets = [], onSelectedSecretsChange, onSelectedGlobalSecretsChange, onOpenDashboard }: {
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
  onOpenDashboard?: () => void
  onAsk: (message: string) => Promise<void>
  onSelectedServersChange: (servers: string[]) => Promise<unknown>
  onSelectedSkillsChange: (skills: string[]) => Promise<unknown>
  workflowContextPaths: string[]
  onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>
}) {
  // The Connect tab points at this installation's API origin. Hosted apps need
  // a public origin; local agents can connect directly to a loopback MCP URL.
  const incomingGmail = useGmailInboundUIEnabled()
  const product = useProjectProduct()
  const chatSessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  const filePreference = useCodeFilesPreference(chatSessionId || '')
  const localCodeSession = product.supportsLocalFiles && filePreference.location === 'computer'
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const visibleTabs = projectIntegrationSections(allowedProducts).filter(option =>
    isWorkIntegrationTabEnabled(option.value, enabledPanels, localCodeSession))
  const [tab, setTab] = useState<WorkIntegrationTab>('apps')
  const activeTab = visibleTabs.some(option => option.value === tab) ? tab : visibleTabs[0]?.value ?? 'apps'
  // Every tab loads on mount, so Refresh always remounts.
  const [tabNonce, setTabNonce] = useState(0)
  const [preferredPluginTab, setPluginTab] = useProjectPluginTab()
  const [overview, setOverview] = useState(true)
  const [sharing, setSharing] = useState(false)
  const [shareChannel, setShareChannel] = useState<UseShareChannel | 'overview'>('overview')
  const pluginTabs = projectPluginTabs(allowedProducts).filter(option => !enabledPanels || (option.value === 'secrets' ? enabledPanels.has('secrets') : option.value === 'skills' ? enabledPanels.has('skills') : option.value === 'vault' || enabledPanels.has('mcp')))
  const pluginTab = overview && pluginTabs.some(option => option.value === 'connected') ? 'connected' : preferredPluginTab
  const activePluginTab = pluginTabs.some(option => option.value === pluginTab) ? pluginTab : pluginTabs[0]?.value ?? 'connected'
  const navigationOptions = integrationNavigationOptions(visibleTabs, pluginTabs)
  const selectConnection = (target: string) => {
    if (target === 'use-share' || target === 'people') { setSharing(true); setShareChannel('overview'); return }
    if (target === 'cli' || (target === 'slack-app' && visibleTabs.some(option => option.value === 'slack')) || (target === 'whatsapp' && visibleTabs.some(option => option.value === 'whatsapp'))) { setSharing(true); setShareChannel(target as UseShareChannel); return }
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
  const selectedSkills = useChatStore(state => state.chatTabs[tabId]?.config.selectedSkills || [])

  const pendingSkillSaves = useRef<Promise<void>>(Promise.resolve())
  const toggleSkill = async (folderName: string) => {
    const next = selectedSkills.includes(folderName)
      ? selectedSkills.filter((name) => name !== folderName)
      : [...selectedSkills, folderName]
    const store = useChatStore.getState()
    const save = onSelectedSkillsChange(next)
    pendingSkillSaves.current = Promise.all([pendingSkillSaves.current, save]).then(() => undefined, () => undefined)
    try {
      await save
    } catch (cause) {
      store.addToast(cause instanceof Error ? cause.message : 'Could not save project skills.', 'error')
      return
    }
    for (const tab of Object.values(store.chatTabs)) {
      if (tab.metadata?.agentProfileId !== product.profileId || tab.metadata?.agentProfileProjectId !== projectId) continue
      store.setTabConfig(tab.tabId, { selectedSkills: next })
      store.setTabMetadata(tab.tabId, { agentProfileRuntimeDirty: true })
    }
  }

  if (localCodeSession) return <p className="p-4 text-sm text-muted-foreground">Integrations are unavailable in Local mode. Manage the local connection in the right-side panel.</p>

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader
        icon={sharing ? Users : Server}
        title={sharing ? 'Use & share' : 'Connections'}
        helpTopic={sharing ? shareChannel === 'overview' ? 'Use & share' : `Use & share · ${shareChannel === 'cli' ? 'MCP access' : shareChannel === 'slack-app' ? 'Slack app' : 'WhatsApp'}` : overview ? 'Connections' : `${activeTab === 'slack' ? 'Connections' : 'Integrations'} · ${connectionLabel}`}
        actions={(
          <WorkspaceViewActions
            workspacePath={workspacePath}
            message={sharing ? 'Help me share this project with people or make it available through its existing channels. Explain current access before changing sharing, routes or permissions.' : activeTab === 'apps' && activePluginTab === 'vault'
              ? `Help me choose from my Vault groups' permitted connections and secrets for this ${product.noun} project. Check my current access and selection; never show secret values.`
              : integrationTabAskAIMessage(product.noun, incomingGmail)[activeTab === 'apps' && (activePluginTab === 'secrets' || activePluginTab === 'skills') ? activePluginTab : activeTab]}
            onAsk={onAsk}
            onRefresh={() => setTabNonce(nonce => nonce + 1)}
            refreshLabel={sharing ? 'Refresh sharing and channels' : overview ? 'Refresh connections' : `Refresh ${connectionLabel}`}
          />
        )}
        below={sharing ? shareChannel !== 'overview' && <UseShareBack label={shareChannel === 'cli' ? 'MCP access' : shareChannel === 'slack-app' ? 'Slack app' : 'WhatsApp'} onBack={() => selectConnection('use-share')} /> : <IntegrationNavigation options={navigationOptions} value={activeConnection} onChange={selectConnection} overview={overview} onOverview={() => selectConnection('overview')} canEdit={!workspacePath.startsWith('_users/')} />}
        tabs={!sharing && !overview && activeTab === 'apps' && (activePluginTab === 'connected' || activePluginTab === 'available') ? { value: activePluginTab, onChange: selectConnection, options: mcpTabs, ariaLabel: 'MCP servers' } : undefined}
      />
      <div key={`${sharing ? shareChannel : activeTab}:${tabNonce}`} className="min-h-0 flex-1 overflow-y-auto p-4">
        {sharing ? <>
          {shareChannel === 'overview' ? <UseSharePanel workspacePath={workspacePath} slack={visibleTabs.some(option => option.value === 'slack')} whatsapp={visibleTabs.some(option => option.value === 'whatsapp')} onChannel={selectConnection} onDashboard={onOpenDashboard} /> : shareChannel === 'cli' ? <ProjectMcpSetupPanel workspacePath={workspacePath} /> : <WorkflowBotsPanel channelOnly workspacePath={workspacePath} fixedChannel={shareChannel === 'slack-app' ? 'slack' : 'whatsapp'} scopeNoun="project" ownBotOnly={product.profileId === 'code'} onAsk={onAsk} target={{ profileId: product.profileId, conversationKey: projectId, label: projectTitle }} />}
        </> : <>
        {overview && <ConnectedProjectAccounts workspacePath={workspacePath} options={navigationOptions} onOpen={selectConnection} />}
        {activeTab === 'apps' && <ProjectPluginsPanel tab={activePluginTab}
          connections={(!enabledPanels || enabledPanels.has('mcp')) ? (view => product.profileId === 'code' ? (<ProjectMcpPanel view={view} chatSessionId={chatSessionId} workspacePath={workspacePath} placeNoun={product.noun} canEdit={!workspacePath.startsWith('_users/')} onAsk={onAsk} selectedServers={selectedServers} onSelectedServersChange={async servers => {
              await onSelectedServersChange(servers)
              const store = useChatStore.getState()
              for (const chat of Object.values(store.chatTabs)) {
                if (chat.metadata?.agentProfileId === 'code' && chat.metadata?.agentProfileProjectId === projectId) {
                  store.setTabConfig(chat.tabId, { selectedServers: servers.length ? servers : ['NO_SERVERS'] })
                  store.setTabMetadata(chat.tabId, { agentProfileRuntimeDirty: true, agentProfileMCPSelectionInitialized: true })
                }
              }
            }} />) : (<WorkMCPTabBody view={view}
          tabId={tabId}
          projectId={projectId}
          workspacePath={workspacePath}
          onAsk={onAsk}
          onSelectedServersChange={onSelectedServersChange}
        />)) : undefined}
          secrets={(!enabledPanels || enabledPanels.has('secrets')) ? <ProjectSecretsPanel placeNoun={product.noun}
            selectedSecrets={selectedSecrets} workspacePath={workspacePath} onSecretChange={names => onSelectedSecretsChange?.(names)}
            selectedGlobalSecrets={selectedGlobalSecrets} onGlobalSecretChange={onSelectedGlobalSecretsChange ? names => onSelectedGlobalSecretsChange(names ?? []) : undefined} /> : undefined}
          skills={(!enabledPanels || enabledPanels.has('skills')) ? (<SkillsManagerPanel
          compact
          manageOwnScroll={false}
          workspacePath={workspacePath}
          selectedSkills={selectedSkills}
          onBeforeUninstall={() => pendingSkillSaves.current}
          onUninstalled={async name => {
            const currentSelection = useChatStore.getState().chatTabs[tabId]?.config.selectedSkills || []
            await onSelectedSkillsChange(currentSelection.filter(skill => skill !== name))
            const store = useChatStore.getState()
            for (const tab of Object.values(store.chatTabs)) {
              if (tab.metadata?.agentProfileId !== product.profileId || tab.metadata?.agentProfileProjectId !== projectId) continue
              store.setTabConfig(tab.tabId, { selectedSkills: (tab.config.selectedSkills || []).filter(skill => skill !== name) })
              store.setTabMetadata(tab.tabId, { agentProfileRuntimeDirty: true })
            }
          }}
          onToggleSkill={folderName => { void toggleSkill(folderName) }}
          selectionScopeLabel="main chat"
          emptySelectionText={`No skills are installed in this ${product.noun} yet. Ask the agent to add or create one.`}
        />) : undefined}
          vault={<WorkMCPTabBody vault tabId={tabId} projectId={projectId} workspacePath={workspacePath} onAsk={onAsk} onSelectedServersChange={onSelectedServersChange} selectedSecrets={selectedGlobalSecrets} onSelectedSecretsChange={names => onSelectedGlobalSecretsChange?.(names)} />}
        />}
        {activeTab === 'brain' && <ProjectKnowledgebasePanel workspacePath={workspacePath} onAsk={onAsk} />}
        {activeTab === 'folders' && <WorkFoldersSection
          workspacePath={workspacePath}
          workflowContextPaths={workflowContextPaths}
          onWorkflowContextPathsChange={onWorkflowContextPathsChange}
          onAsk={onAsk}
          askMessage={integrationTabAskAIMessage(product.noun, incomingGmail).folders}
        />}
        {activeTab === 'slack' && <SlackAccountsPanel workspacePath={workspacePath} />}
        {activeTab === 'gmail' && (
          <>
            {/* The deployment's Google app: an admin stores the Google OAuth client once. */}
            {isAdmin && <McpAppsSection />}
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
