import { useEffect, useMemo, useRef, useState } from 'react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { AlertTriangle, Search, Server } from 'lucide-react'
import ConnectorsBrowser from '../../components/connectors/ConnectorsBrowser'
import { ToolSelectionSection } from '../../components/ToolSelectionSection'
import { isSelectedServer, serverNamesMatch } from '../../utils/mcpServerAlias'
import SkillsManagerPanel from '../../components/skills/SkillsManagerPanel'
import WorkflowBotsPanel from '../../components/workflow/WorkflowBotsPanel'
import WorkflowEmailPanel from '../../components/workflow/WorkflowEmailPanel'
import { CliMcpSetupPanel } from '../../components/integrations/CliMcpSetupPanel'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { useChatStore } from '../../stores/useChatStore'
import { useMCPStore } from '../../stores/useMCPStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { isWorkIntegrationTabEnabled } from './workViewGating'
import { isProjectProductId, useProjectProduct } from './projectProduct'
import { crewTemplates } from './crewTemplates'

export type WorkIntegrationTab = 'apps' | 'skills' | 'slack' | 'whatsapp' | 'gmail' | 'cli'

const INTEGRATION_TABS: Array<{ value: WorkIntegrationTab; label: string }> = [
  { value: 'apps', label: 'MCPs' },
  { value: 'skills', label: 'Skills' },
  { value: 'slack', label: 'Slack' },
  { value: 'whatsapp', label: 'WhatsApp' },
  { value: 'gmail', label: 'Gmail' },
  { value: 'cli', label: 'Connect' },
]

function integrationTabAskAIMessage(noun: string): Record<WorkIntegrationTab, string> {
  return {
    apps: `Help me with this ${noun} project's connected apps. Explain what's connected and ask what I want to add or change.`,
    skills: `Help me with this ${noun} project's skills. Explain what's available and ask what I want to add or change.`,
    slack: `Help me with this ${noun} project's Slack bot. Explain what's connected and ask what I want to change.`,
    whatsapp: `Help me with this ${noun} project's WhatsApp bot. Explain what's connected and ask what I want to change.`,
    gmail: `Help me with this ${noun} project's Gmail. Explain the setup and ask what I want to change.`,
    cli: 'Help me connect an AI agent to this installation through MCP. Explain the HTTP MCP URL and browser sign-in, and ask which AI app I use.',
  }
}

// Code has no Gmail. Its Slack and WhatsApp tabs stay hidden until Code's
// 1:1 direct-message bots land (docs/design/code_product.md step 3): the
// shared bots panel creates channel routes, which Code must not have.
const CODE_HIDDEN_INTEGRATION_TABS = new Set<WorkIntegrationTab>(['gmail', 'slack', 'whatsapp'])

export function WorkMCPTabBody({ tabId, projectId, workspacePath, onAsk, onSelectedServersChange }: {
  tabId: string
  projectId: string
  workspacePath: string
  onAsk: (message: string) => Promise<void>
  onSelectedServersChange: (servers: string[]) => Promise<unknown>
}) {
  const selectedServers = useChatStore(state => state.chatTabs[tabId]?.config.selectedServers || [])
  const toolList = useMCPStore(state => state.toolList)
  const toolsLoading = useMCPStore(state => state.isLoadingTools)
  // Mirror the workflow tab: connected servers plus already-selected ones
  // (a selected-but-since-disconnected server stays visible/manageable
  // instead of silently vanishing from the project's config).
  const availableServers = useMemo(() => {
    const connected = toolList
      .filter(tool => tool.connection === 'connected' && tool.server)
      .map(tool => tool.server as string)
    return [...new Set([...connected, ...selectedServers.filter(server => server !== 'NO_SERVERS')])]
  }, [toolList, selectedServers])
  const actualSelected = selectedServers.filter(server => server !== 'NO_SERVERS')
  const selectedAvailableServers = useMemo(() => availableServers.filter(serverName => isSelectedServer(actualSelected, serverName)), [availableServers, actualSelected])
  const unselectedAvailableServers = useMemo(() => availableServers.filter(serverName => !isSelectedServer(actualSelected, serverName)), [availableServers, actualSelected])
  const [searchQuery, setSearchQuery] = useState('')
  // Selected for this project but not connected to the platform (e.g. a Crew
  // the Builder created with an app that still needs sign-in). Its tools do
  // not work until someone connects it, so say so and offer the way.
  // While the tool list reloads, keep showing what was last known instead of
  // blinking the notice away (it vanished right after Connect on RTS: the
  // reload hid it mid-action, 2026-09-28 QA #205 BUG_ID_004).
  const lastNeedsConnecting = useRef<string[]>([])
  const needsConnecting = useMemo(() => {
    if (toolsLoading) return lastNeedsConnecting.current
    const next = actualSelected.filter(serverName =>
      !toolList.some(tool => tool.server && serverNamesMatch(tool.server, serverName) && tool.connection === 'connected'))
    lastNeedsConnecting.current = next
    return next
  }, [toolsLoading, actualSelected, toolList])
  // Connecting a platform app is admin-only (ConnectorsBrowser's rule); for
  // anyone else Connect cannot do anything, so say who can.
  const canConnectApps = useAuthStore(state =>
    state.user?.is_admin === true || (state.isMultiUserModeChecked && !state.isMultiUserMode),
  )
  const connectSectionRef = useRef<HTMLDivElement>(null)
  const [connectFocus, setConnectFocus] = useState<string | null>(null)
  useEffect(() => {
    if (!connectFocus) return
    connectSectionRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    const timer = window.setTimeout(() => setConnectFocus(null), 2500)
    return () => window.clearTimeout(timer)
  }, [connectFocus])
  const startConnect = (serverName: string) => {
    setSearchQuery(serverName)
    setConnectFocus(serverName)
  }

  const setSelected = async (servers: string[]) => {
    const store = useChatStore.getState()
    const selected = servers.length > 0 ? servers : ['NO_SERVERS']
    try {
      await onSelectedServersChange(servers)
    } catch (cause) {
      store.addToast(cause instanceof Error ? cause.message : 'Could not save project integrations.', 'error')
      return
    }
    for (const tab of Object.values(store.chatTabs)) {
      if (!projectId || !isProjectProductId(tab.metadata?.agentProfileId) || tab.metadata?.agentProfileProjectId !== projectId) continue
      store.setTabConfig(tab.tabId, { selectedServers: selected })
      store.setTabMetadata(tab.tabId, { agentProfileMCPSelectionInitialized: true, agentProfileRuntimeDirty: true })
    }
  }

  return (
    <div className="flex flex-col gap-3">
      {needsConnecting.length > 0 && (
        <div data-testid="work-mcp-needs-connecting" className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/30 dark:text-amber-200">
          <div className="flex items-center gap-2 font-medium">
            <AlertTriangle className="h-4 w-4 shrink-0" />
            Needs connecting
          </div>
          <p className="mt-1 text-xs leading-5">
            This project uses {needsConnecting.length === 1 ? 'an app that is' : 'apps that are'} not connected yet. Its tools won't work until {needsConnecting.length === 1 ? 'it is' : 'they are'} connected.
          </p>
          <ul className="mt-2 space-y-1.5">
            {needsConnecting.map(serverName => (
              <li key={serverName} className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{serverName}</span>
                {canConnectApps ? (
                  <button
                    type="button"
                    className="rounded border border-amber-400 px-2 py-0.5 text-xs hover:bg-amber-100 dark:border-amber-600 dark:hover:bg-amber-900/40"
                    onClick={() => startConnect(serverName)}
                  >
                    Connect
                  </button>
                ) : (
                  <span className="text-xs text-amber-800/80 dark:text-amber-200/80">Only an admin can connect it.</span>
                )}
                <button
                  type="button"
                  className="rounded px-2 py-0.5 text-xs underline-offset-2 hover:underline"
                  onClick={() => void onAsk(`Help me connect ${serverName} for this project. It is selected but not connected yet; walk me through signing in or adding its credentials.`)}
                >
                  Ask agent
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
      {selectedAvailableServers.length > 0 && (
        <div>
          <div className="mb-3 text-sm font-medium text-muted-foreground">
            This project
          </div>
          <ToolSelectionSection
            stepId="project-selected"
            availableServers={selectedAvailableServers}
            selectedServers={actualSelected}
            selectedTools={[]}
            onServerChange={(servers) => void setSelected(servers)}
            onToolChange={() => {}}
            query={searchQuery}
            agentMode="multi-agent"
            hideHeader
            hideToolDetails
            manageOwnScroll={false}
          />
        </div>
      )}
      {unselectedAvailableServers.length > 0 && (
        <div className="mt-3 border-t border-border pt-3">
          <div className="mb-1 text-sm font-medium text-muted-foreground">
            Platform connected
          </div>
          <p className="mb-3 text-xs leading-5 text-muted-foreground">
            Shared with everyone. Tick one to let this project use it.
          </p>
          <ToolSelectionSection
            stepId="project-available"
            availableServers={unselectedAvailableServers}
            selectedServers={actualSelected}
            selectedTools={[]}
            onServerChange={(servers) => void setSelected(servers)}
            onToolChange={() => {}}
            query={searchQuery}
            agentMode="multi-agent"
            hideHeader
            hideToolDetails
            manageOwnScroll={false}
          />
        </div>
      )}
      <div className="relative mt-3 shrink-0">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
        <input
          type="text"
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder="Search apps"
          aria-label="Search apps"
          className="w-full rounded-lg border border-gray-300 bg-white py-2.5 pl-10 pr-3 text-sm text-gray-900 placeholder-gray-400 transition-colors focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100"
        />
      </div>
      <div
        ref={connectSectionRef}
        data-testid="work-mcp-connect-section"
        className={`mt-3 scroll-mt-3 border-t border-border pt-3 transition-colors ${connectFocus ? 'rounded-md bg-amber-50/70 ring-2 ring-amber-300 dark:bg-amber-950/20 dark:ring-amber-700/60' : ''}`}
      >
        <div className="mb-3 text-sm font-medium text-muted-foreground">
          {connectFocus ? `Connect ${connectFocus}` : 'Connect a new app'}
        </div>
        <ConnectorsBrowser
          compact
          manageOwnScroll={false}
          workspacePath={workspacePath}
          workspaceLabel="project"
          assistantLabel="agent"
          onAskAI={(message) => void onAsk(message)}
          query={searchQuery}
          hideSearch
          hideConnectedSection
        />
      </div>
    </div>
  )
}

export function WorkIntegrationsPanel({ workspacePath, projectId, projectTitle, projectTemplates, tabId, enabledPanels, onAsk, onSelectedServersChange, onSelectedSkillsChange }: {
  workspacePath: string
  projectId: string
  projectTitle: string
  projectTemplates: Array<{ id: string; version: number }>
  tabId: string
  enabledPanels?: Set<string>
  onAsk: (message: string) => Promise<void>
  onSelectedServersChange: (servers: string[]) => Promise<unknown>
  onSelectedSkillsChange: (skills: string[]) => Promise<unknown>
}) {
  // The Connect tab points at this installation's API origin. Hosted apps need
  // a public origin; local agents can connect directly to a loopback MCP URL.
  const product = useProjectProduct()
  const visibleTabs = INTEGRATION_TABS.filter(option =>
    isWorkIntegrationTabEnabled(option.value, enabledPanels) &&
    !(product.profileId === 'code' && CODE_HIDDEN_INTEGRATION_TABS.has(option.value)))
  const [tab, setTab] = usePersistentTab<WorkIntegrationTab>('agentworks.tab.crew-integrations', 'apps', INTEGRATION_TABS.map(option => option.value))
  const activeTab = visibleTabs.some(option => option.value === tab) ? tab : visibleTabs[0].value
  // Every tab loads on mount, so Refresh always remounts.
  const [tabNonce, setTabNonce] = useState(0)
  const selectedSkills = useChatStore(state => state.chatTabs[tabId]?.config.selectedSkills || [])
  const templates = crewTemplates.filter(item => projectTemplates.some(installed => installed.id === item.id && installed.version === item.version))

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
        subtitle="Choose connected apps, skills, and bots for this project."
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
        {activeTab === 'apps' && <WorkMCPTabBody
          tabId={tabId}
          projectId={projectId}
          workspacePath={workspacePath}
          onAsk={onAsk}
          onSelectedServersChange={onSelectedServersChange}
        />}
        {activeTab === 'skills' && <div className="space-y-3">
          {templates.map(template => <div key={template.id} className="rounded-lg border border-primary/20 bg-primary/5 p-3">
            <p className="text-xs font-semibold text-foreground">Included with {template.name}</p>
            <p className="mt-1 text-xs text-muted-foreground">These skills live in this {product.noun}’s files and are selected only for this {product.noun}.</p>
            {template.selectedSkills.map(skill => <div key={skill} className="mt-2 flex items-center justify-between gap-2 text-xs">
              <span className="font-medium text-foreground">{skill}</span>
              <button type="button" onClick={() => { void toggleSkill(skill) }} className="rounded-md border border-border px-2 py-1 font-semibold text-primary hover:bg-primary/10">
                {selectedSkills.includes(skill) ? 'Selected · remove' : 'Select skill'}
              </button>
            </div>)}
          </div>)}
          <SkillsManagerPanel
            compact
            manageOwnScroll={false}
            workspacePath={workspacePath}
            selectedSkills={selectedSkills}
            onToggleSkill={folderName => { void toggleSkill(folderName) }}
            selectionLabel="Skills for this project"
            emptySelectionText="No project skills yet — pick one below."
            selectionScopeLabel="project"
            libraryReadOnly={product.profileId === 'code'}
            libraryReadOnlyHint="Ask the agent to install or create a skill; it stays private to this workspace."
          />
          {product.profileId === 'code' ? <p className="text-xs text-muted-foreground">Skills you add here stay in this workspace’s skills/ folder: ask the agent to install or create one. The shared library above is read-only from a Code.</p> : null}
        </div>}
        {activeTab === 'slack' && <WorkflowBotsPanel
          workspacePath={workspacePath}
          fixedChannel="slack"
          scopeNoun="project"
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
        {activeTab === 'gmail' && <WorkflowEmailPanel workspacePath={workspacePath} scopeNoun="project" onAsk={onAsk} />}
        {activeTab === 'cli' && <CliMcpSetupPanel />}
      </div>
    </div>
  )
}
