import { SecretSelectionSection } from '../../components/secrets/SecretSelectionSection'
import { useState } from 'react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { Server } from 'lucide-react'
import SkillsManagerPanel from '../../components/skills/SkillsManagerPanel'
import WorkflowBotsPanel from '../../components/workflow/WorkflowBotsPanel'
import WorkflowEmailPanel from '../../components/workflow/WorkflowEmailPanel'
import { CliMcpSetupPanel } from '../../components/integrations/CliMcpSetupPanel'
import { ProjectMcpPanel } from '../../components/integrations/ProjectMcpPanel'
import { McpAppsSection } from './McpAppsSection'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { isWorkIntegrationTabEnabled } from './workViewGating'
import { isProjectProductId, useProjectProduct } from './projectProduct'

export type WorkIntegrationTab = 'apps' | 'secrets' | 'skills' | 'slack' | 'whatsapp' | 'gmail' | 'cli'

const INTEGRATION_TABS: Array<{ value: WorkIntegrationTab; label: string }> = [
  { value: 'apps', label: 'MCPs' },
  { value: 'secrets', label: 'Secrets' },
  { value: 'skills', label: 'Skills' },
  { value: 'slack', label: 'Slack' },
  { value: 'whatsapp', label: 'WhatsApp' },
  { value: 'gmail', label: 'Google apps' },
  { value: 'cli', label: 'Connect' },
]

function integrationTabAskAIMessage(noun: string): Record<WorkIntegrationTab, string> {
  return {
    apps: `Help me with this ${noun} project's connected apps. Explain what's connected and ask what I want to add or change.`,
    secrets: `Help me select project or permitted Vault secrets for this ${noun} project. Never ask for secret values in chat.`,
    skills: `Help me with this ${noun} project's skills. Explain what's available and ask what I want to add or change.`,
    slack: `Help me with this ${noun} project's Slack bot. Explain what's connected and ask what I want to change.`,
    whatsapp: `Help me with this ${noun} project's WhatsApp bot. Explain what's connected and ask what I want to change.`,
    gmail: `Help me with this ${noun} project's Google apps (Gmail, Drive, Calendar, Docs, Sheets, Slides). Explain the setup and ask what I want to change.`,
    cli: 'Help me connect an AI agent to this installation through MCP. Explain the HTTP MCP URL and browser sign-in, and ask which AI app I use.',
  }
}

// Code: Slack (its own bot, 1:1 DMs) and WhatsApp (owner). Its MCPs tab is
// the shared MCP browser, scoped to this Code's private and permitted Vault connections;
// the always-on MCP "Connect" tab is hidden. Google (Gmail, Drive, Calendar, Docs,
// Sheets, Slides) is the Google apps tab: the Code's own private gog accounts.
const CODE_HIDDEN_INTEGRATION_TABS = new Set<WorkIntegrationTab>(['cli'])

export function WorkMCPTabBody({ tabId, projectId, workspacePath, onAsk, onSelectedServersChange }: {
  tabId: string
  projectId: string
  workspacePath: string
  onAsk: (message: string) => Promise<void>
  onSelectedServersChange: (servers: string[]) => Promise<unknown>
}) {
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
      <ProjectMcpPanel workspacePath={workspacePath} placeNoun="Crew" canEdit={!workspacePath.startsWith('_users/')} onAsk={onAsk}
        selectedServers={selectedServers} onSelectedServersChange={setSelected} />
    </div>
  )
}

export function WorkIntegrationsPanel({ workspacePath, projectId, projectTitle, tabId, enabledPanels, onAsk, onSelectedServersChange, onSelectedSkillsChange, selectedSecrets = [], selectedGlobalSecrets = [], onSelectedSecretsChange, onSelectedGlobalSecretsChange }: {
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
  onSelectedSkillsChange: (skills: string[]) => Promise<unknown>
}) {
  // The Connect tab points at this installation's API origin. Hosted apps need
  // a public origin; local agents can connect directly to a loopback MCP URL.
  const product = useProjectProduct()
  const isAdmin = useAuthStore(state => state.user?.is_admin === true)
  const visibleTabs = INTEGRATION_TABS.filter(option =>
    isWorkIntegrationTabEnabled(option.value, enabledPanels) &&
    !(product.profileId === 'code' && CODE_HIDDEN_INTEGRATION_TABS.has(option.value)))
  const [tab, setTab] = usePersistentTab<WorkIntegrationTab>('agentworks.tab.crew-integrations', 'apps', INTEGRATION_TABS.map(option => option.value))
  const activeTab = visibleTabs.some(option => option.value === tab) ? tab : visibleTabs[0].value
  // Every tab loads on mount, so Refresh always remounts.
  const [tabNonce, setTabNonce] = useState(0)
  const selectedServers = useChatStore(state => state.chatTabs[tabId]?.config.selectedServers || [])
  const selectedSkills = useChatStore(state => state.chatTabs[tabId]?.config.selectedSkills || [])

  const toggleSkill = async (folderName: string) => {
    const next = selectedSkills.includes(folderName)
      ? selectedSkills.filter((name) => name !== folderName)
      : [...selectedSkills, folderName]
    const store = useChatStore.getState()
    try {
      await onSelectedSkillsChange(next)
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

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader
        icon={Server}
        title="Integrations"
        helpTopic={`Integrations · ${visibleTabs.find(option => option.value === activeTab)?.label ?? 'MCPs'}`}
        actions={(
          <WorkspaceViewActions
            workspacePath={workspacePath}
            message={integrationTabAskAIMessage(product.noun)[activeTab]}
            onAsk={onAsk}
            onRefresh={() => setTabNonce(nonce => nonce + 1)}
            refreshLabel={`Refresh ${visibleTabs.find(option => option.value === activeTab)?.label ?? 'view'}`}
          />
        )}
        tabs={{ value: activeTab, onChange: (value: string) => setTab(value as WorkIntegrationTab), options: visibleTabs, ariaLabel: 'Integrations' }}
      />
      <div key={`${activeTab}:${tabNonce}`} className="min-h-0 flex-1 overflow-y-auto p-4">
        {activeTab === 'apps' && product.profileId === 'code' && (
          // A Code is a place like a Crew: its connections are its own, added
          // by its owner (a shared Code arrives under the owner's _users/ path).
          <>
            <ProjectMcpPanel workspacePath={workspacePath} placeNoun="Code" canEdit={!workspacePath.startsWith('_users/')} onAsk={onAsk} selectedServers={selectedServers} onSelectedServersChange={async servers => {
              await onSelectedServersChange(servers)
              const store = useChatStore.getState()
              for (const chat of Object.values(store.chatTabs)) {
                if (chat.metadata?.agentProfileId === 'code' && chat.metadata?.agentProfileProjectId === projectId) {
                  store.setTabConfig(chat.tabId, { selectedServers: servers.length ? servers : ['NO_SERVERS'] })
                  store.setTabMetadata(chat.tabId, { agentProfileRuntimeDirty: true, agentProfileMCPSelectionInitialized: true })
                }
              }
            }} />
          </>
        )}
        {activeTab === 'apps' && product.profileId !== 'code' && <WorkMCPTabBody
          tabId={tabId}
          projectId={projectId}
          workspacePath={workspacePath}
          onAsk={onAsk}
          onSelectedServersChange={onSelectedServersChange}
        />}
        {activeTab === 'secrets' && <SecretSelectionSection selectedSecrets={selectedSecrets} selectedGlobalSecrets={selectedGlobalSecrets} workflowPath={workspacePath} onSecretChange={names => onSelectedSecretsChange?.(names)} onGlobalSecretChange={names => onSelectedGlobalSecretsChange?.(names ?? [])} />}
        {activeTab === 'skills' && <SkillsManagerPanel
          compact
          selectedOnly
          manageOwnScroll={false}
          workspacePath={workspacePath}
          selectedSkills={selectedSkills}
          onToggleSkill={folderName => { void toggleSkill(folderName) }}
          selectionScopeLabel="project"
          emptySelectionText={`No skills are used in this ${product.noun} yet. Ask the agent to add or create one.`}
        />}
        {activeTab === 'slack' && <WorkflowBotsPanel
          workspacePath={workspacePath}
          fixedChannel="slack"
          scopeNoun="project"
          ownBotOnly={product.profileId === 'code'}
          onAsk={onAsk}
          target={{ profileId: product.profileId, conversationKey: projectId, label: projectTitle }}
        />}
        {activeTab === 'whatsapp' && <WorkflowBotsPanel
          workspacePath={workspacePath}
          fixedChannel="whatsapp"
          scopeNoun="project"
          onAsk={onAsk}
          target={{ profileId: product.profileId, conversationKey: projectId, label: projectTitle }}
        />}
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
        {activeTab === 'cli' && <CliMcpSetupPanel />}
      </div>
    </div>
  )
}
