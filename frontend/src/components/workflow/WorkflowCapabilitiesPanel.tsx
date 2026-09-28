import { capabilitiesEqual, mergeRemoteCapabilities } from './workflowCapabilitiesSync'
import { SettingsCard } from '../ui/SettingsCard'
import { ToggleRow } from '../ui/ToggleRow'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { LoaderCircle, Save, Search } from 'lucide-react'
import { ToolSelectionSection } from '../ToolSelectionSection'
import SkillsManagerPanel from '../skills/SkillsManagerPanel'
import PlaybooksPanel from '../playbooks/PlaybooksPanel'
import { SecretSelectionSection } from '../secrets/SecretSelectionSection'
import WorkflowLLMConfigurationPanel from './WorkflowLLMConfigurationPanel'
import WorkflowBotsPanel from './WorkflowBotsPanel'
import WorkflowEmailPanel from './WorkflowEmailPanel'
import { CliMcpSetupPanel } from '../integrations/CliMcpSetupPanel'
import ConnectorsBrowser from '../connectors/ConnectorsBrowser'
import { PlaceMcpSection } from '../../products/work/PlaceMcpSection'
import { agentApi, workflowManifestApi } from '../../services/api'
import type { WorkflowCapabilities } from '../../services/api-types'
import { useMCPStore } from '../../stores/useMCPStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { isSelectedServer } from '../../utils/mcpServerAlias'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { AskAIButton } from './AskAIButton'
import type { Skill } from '../../types/skills'
import { getWorkspaceView, type CapabilityViewId } from './workspaceViews'
import { BrowserWorkspacePanel } from './BrowserWorkspacePanel'
import type { BrowserAutomationMode } from '../BrowserAutomationSettings'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { getIdentityTabAskAIMessage, getIntegrationTabAskAIMessage, getWorkspaceAskAIMessage, type IdentityTabId, type IntegrationTabId } from './workspaceAskAI'
import WorkflowIdentityPanel from './WorkflowIdentityPanel'
import WorkflowFolderAccessView from './WorkflowFolderAccessView'
import WorkflowUpdatesView from './WorkflowUpdatesView'
import { nativeAgentToolsEnabled } from '../../utils/nativeAgentTools'

// Which sections exist is decided by the registry in workspaceViews.ts; this
// panel only carries the per-section copy.
export type WorkflowCapabilitySection = CapabilityViewId

type McpTab = IntegrationTabId

const MCP_TABS: Array<{ value: McpTab; label: string }> = [
  { value: 'apps', label: 'MCPs' },
  { value: 'skills', label: 'Skills' },
  { value: 'slack', label: 'Slack' },
  { value: 'whatsapp', label: 'WhatsApp' },
  { value: 'gmail', label: 'Gmail' },
  { value: 'cli', label: 'Connect' },
]
const RELAY_MCP_TABS = MCP_TABS.filter(option => option.value === 'apps' || option.value === 'skills' || option.value === 'gmail')

type IdentityTab = IdentityTabId

const IDENTITY_TABS: Array<{ value: IdentityTab; label: string }> = [
  { value: 'general', label: 'General' },
  { value: 'secrets', label: 'Secrets' },
  { value: 'folders', label: 'Connected work' },
  { value: 'llm', label: 'Models' },
  { value: 'upgrades', label: 'Upgrades' },
]
const RELAY_IDENTITY_TABS = IDENTITY_TABS.filter(option => option.value === 'general' || option.value === 'llm')

interface WorkflowCapabilitiesPanelProps {
  section: WorkflowCapabilitySection
  workspacePath: string | null
  relayMode?: boolean
}

const EMPTY_CAPABILITIES: WorkflowCapabilities = {
  selected_servers: [],
  selected_tools: [],
  selected_skills: [],
  selected_secrets: [],
  selected_global_secret_names: null,
  browser_mode: 'none',
  use_code_execution_mode: false,
}

// `savesViaManifest`: the section edits `capabilities` and persists through the
// Save footer. Sections that write straight to shared state (bots routing and
// credentials) never touch the manifest, so the footer would save nothing.
const SECTION_COPY: Record<WorkflowCapabilitySection, { title: string; description: string; savesViaManifest: boolean }> = {
  playbooks: {
    title: 'Workflow playbooks',
    description: 'Apply proven AgentWorks setups to this workflow with the Builder.',
    savesViaManifest: false,
  },
  mcp: {
    title: 'Integrations',
    description: 'Select the apps, skills, Slack, WhatsApp, and Gmail access this workflow may use.',
    // Selection changes persist immediately, so this long directory can use
    // one uninterrupted scroll surface without a fixed Save footer.
    savesViaManifest: false,
  },
  identity: {
    title: 'Identity',
    description: 'Name, icon, purpose, secrets, connected work, LLMs, and platform upgrades for this workflow.',
    savesViaManifest: false,
  },
  browser: {
    title: 'Browser automation',
    description: 'Watch this workflow’s browser and configure its automation access.',
    savesViaManifest: true,
  },
}

export default function WorkflowCapabilitiesPanel({ section, workspacePath, relayMode = false }: WorkflowCapabilitiesPanelProps) {
  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)
  const [capabilities, setCapabilities] = useState<WorkflowCapabilities>(EMPTY_CAPABILITIES)
  // What the manifest last held, so the footer can tell "edited" from "saved".
  const [loaded, setLoaded] = useState<WorkflowCapabilities>(EMPTY_CAPABILITIES)
  const dirty = useMemo(() => !capabilitiesEqual(capabilities, loaded), [capabilities, loaded])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [cdpPort, setCdpPort] = useState(9222)
  const [cdpConnected, setCdpConnected] = useState<boolean | null>(null)
  const [cdpError, setCdpError] = useState<string | null>(null)
  const [cdpChecking, setCdpChecking] = useState(false)
  const toolList = useMCPStore(state => state.toolList)
  const refreshTools = useMCPStore(state => state.refreshTools)
  const [refreshingServers, setRefreshingServers] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const mcpTabs = relayMode ? RELAY_MCP_TABS : MCP_TABS
  const [tab, setTab] = usePersistentTab<McpTab>(relayMode ? 'relays.tab.workflow-mcp' : 'agentworks.tab.workflow-mcp', 'apps', mcpTabs.map(option => option.value))
  const activeMcpTab = mcpTabs.some(option => option.value === tab) ? tab : mcpTabs[0].value
  const identityTabs = relayMode ? RELAY_IDENTITY_TABS : IDENTITY_TABS
  const [identityTab, setIdentityTab] = usePersistentTab<IdentityTab>(relayMode ? 'relays.tab.workflow-identity' : 'agentworks.tab.workflow-identity', 'general', identityTabs.map(option => option.value))
  const activeIdentityTab = identityTabs.some(option => option.value === identityTab) ? identityTab : 'general'
  // "Available to select for this workflow" means connected -- you can't
  // meaningfully pick tools from a server nobody has authenticated yet. A
  // not-yet-connected server only belongs in the "Connect a new MCP server"
  // browser below, not this checklist. Still include an already-selected
  // server even if it's since been disconnected, so it stays visible/
  // manageable here instead of silently vanishing from the workflow's config.
  const availableServers = useMemo(() => {
    const connected = toolList
      .filter(tool => tool.connection === 'connected' && tool.server)
      .map(tool => tool.server as string)
    return [...new Set([...connected, ...capabilities.selected_servers])]
  }, [toolList, capabilities.selected_servers])
  const selectedAvailableServers = useMemo(() => availableServers.filter(serverName => isSelectedServer(capabilities.selected_servers, serverName)), [availableServers, capabilities.selected_servers])
  const unselectedAvailableServers = useMemo(() => availableServers.filter(serverName => !isSelectedServer(capabilities.selected_servers, serverName)), [availableServers, capabilities.selected_servers])
  const canManagePlatformMCP = useAuthStore(state =>
    state.user?.is_admin === true || (state.isMultiUserModeChecked && !state.isMultiUserMode),
  )
  const copy = SECTION_COPY[section]
  const view = getWorkspaceView(section)

  const SectionIcon = view.icon

  const latest = useRef({ workspacePath, capabilities, loaded, saving })
  latest.current = { workspacePath, capabilities, loaded, saving }
  const loadVersion = useRef(0)
  const saveVersion = useRef(0)
  const saveQueue = useRef<Promise<void>>(Promise.resolve())

  const load = useCallback(async (background = false) => {
    if (!workspacePath) {
      setError('This panel needs an active workflow folder.')
      setLoading(false)
      return
    }
    const version = ++loadVersion.current
    if (!background) setLoading(true)
    setError(null)
    try {
      const response = await workflowManifestApi.getWorkflowManifest(workspacePath)
      if (latest.current.workspacePath !== workspacePath || version !== loadVersion.current || latest.current.saving) return
      const next = { ...EMPTY_CAPABILITIES, ...response.manifest.capabilities }
      setCapabilities(background ? mergeRemoteCapabilities(latest.current.capabilities, latest.current.loaded, next) : next)
      setLoaded(next)
      setCdpPort(response.manifest.capabilities.cdp_ports?.[0] || 9222)
    } catch (cause) {
      if (latest.current.workspacePath === workspacePath && version === loadVersion.current) {
        setError(cause instanceof Error ? cause.message : 'Unable to load workflow capabilities')
      }
    } finally {
      if (latest.current.workspacePath === workspacePath && version === loadVersion.current) setLoading(false)
    }
  }, [workspacePath])

  const handleRefreshServers = useCallback(async () => {
    if (latest.current.saving) return
    setRefreshingServers(true)
    try {
      await Promise.all([refreshTools(), load(true)])
    } finally {
      setRefreshingServers(false)
    }
  }, [refreshTools, load])

  // Refresh follows the active Integrations tab: Apps reloads servers and
  // the manifest; every other tab reloads by remounting, since each tab's
  // content loads on mount.
  const [tabNonce, setTabNonce] = useState(0)
  const handleMcpRefresh = useCallback(() => {
    if (activeMcpTab === 'apps') {
      void handleRefreshServers()
      return
    }
    setTabNonce(nonce => nonce + 1)
  }, [activeMcpTab, handleRefreshServers])
  const mcpRefreshLabel = activeMcpTab === 'apps'
    ? 'Refresh connected integrations'
    : `Refresh ${mcpTabs.find(option => option.value === activeMcpTab)?.label ?? 'view'}`

  // Every Identity tab loads on mount, so Refresh always remounts.
  const [identityTabNonce, setIdentityTabNonce] = useState(0)
  const handleIdentityRefresh = useCallback(() => {
    setIdentityTabNonce(nonce => nonce + 1)
  }, [])
  const identityRefreshLabel = `Refresh ${identityTabs.find(option => option.value === activeIdentityTab)?.label ?? 'view'}`

  // Agent tools (and shell edits) can change this configuration while the panel
  // stays open. Refresh both sources, only for the visible MCP panel.
  useEffect(() => {
    if (section !== 'mcp' || !workspacePath) return
    let refreshing = false
    const refresh = async () => {
      if (document.hidden || refreshing || latest.current.saving) return
      refreshing = true
      try { await handleRefreshServers() } finally { refreshing = false }
    }
    const onFocus = () => { void refresh() }
    const timer = window.setInterval(onFocus, 15000)
    window.addEventListener('focus', onFocus)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener('focus', onFocus)
      ++loadVersion.current
    }
  }, [section, workspacePath, handleRefreshServers])

  useEffect(() => {
    void load()
  }, [load])

  const checkCdpConnection = useCallback(async (port: number) => {
    setCdpChecking(true)
    setCdpConnected(null)
    setCdpError(null)
    try {
      const result = await agentApi.checkCdpPort(port)
      setCdpConnected(result.connected)
      setCdpError(result.connected ? null : result.error || null)
    } catch {
      setCdpConnected(false)
      setCdpError('Unable to check the CDP port.')
    } finally {
      setCdpChecking(false)
    }
  }, [])

  const persist = useCallback((next: WorkflowCapabilities): Promise<void> => {
    if (!workspacePath) {
      setError('This panel needs an active workflow folder before it can save.')
      return Promise.resolve()
    }
    const targetPath = workspacePath
    const version = ++saveVersion.current
    ++loadVersion.current
    setSaving(true)
    setError(null)

    // Chat and settings consume the same manifest store. Update it immediately
    // so the composer never displays the previous provider while this save is
    // in flight. Requests are serialized below, making the server last-write-
    // wins in the same order as the user's selections.
    const manifestStore = useWorkflowManifestStore.getState()
    const current = manifestStore.getWorkflowByPath(targetPath)
    if (current) {
      manifestStore.replaceWorkflowManifest(targetPath, {
        ...current.manifest,
        capabilities: next,
      })
    }

    const save = async () => {
      try {
        const response = await workflowManifestApi.updateWorkflowManifest({
          workspace_path: targetPath,
          capabilities: next,
        })
        if (version !== saveVersion.current) return
        useWorkflowManifestStore.getState().replaceWorkflowManifest(targetPath, response.manifest)
        if (latest.current.workspacePath === targetPath) setLoaded(next)
      } catch (cause) {
        if (version !== saveVersion.current) return
        await useWorkflowManifestStore.getState().refreshWorkflows()
        if (latest.current.workspacePath === targetPath) {
          setError(cause instanceof Error ? cause.message : 'Unable to save workflow capabilities')
        }
      } finally {
        if (version === saveVersion.current && latest.current.workspacePath === targetPath) setSaving(false)
      }
    }

    const queued = saveQueue.current.then(save, save)
    saveQueue.current = queued
    return queued
  }, [workspacePath])

  const save = useCallback(() => persist(capabilities), [capabilities, persist])

  return (
    <section className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
      {section !== 'browser' && (
        <WorkspaceViewHeader
          icon={SectionIcon}
          title={copy.title}
          helpTopic={section === 'mcp'
            ? `Integrations · ${mcpTabs.find(option => option.value === activeMcpTab)?.label ?? 'MCPs'}`
            : section === 'identity'
              ? `Identity · ${identityTabs.find(option => option.value === activeIdentityTab)?.label ?? 'General'}`
              : undefined}
          subtitle={relayMode && section === 'mcp' ? 'Choose the MCP tools, skills, and Gmail account this Relay may use.' : relayMode && section === 'identity' ? 'Name, delete, and choose the Builder model for this Relay.' : copy.description}
          actions={(
            <WorkspaceViewActions
              workspacePath={workspacePath}
              message={section === 'mcp'
                ? relayMode && activeMcpTab === 'apps'
                  ? 'Help me choose from the MCP servers and tools already connected to this platform for this Relay. Explain what each agent can use before changing the selection.'
                  : getIntegrationTabAskAIMessage(activeMcpTab)
                : section === 'identity'
                  ? relayMode
                    ? activeIdentityTab === 'llm'
                      ? 'Help me choose the Builder model for this Relay. Agent execution models are configured per step through execution_llm, so explain the current Builder choice and ask what I want to change.'
                      : 'Help me rename this Relay or explain how to delete it. Show its current name and ask what I want to change.'
                    : getIdentityTabAskAIMessage(activeIdentityTab)
                  : getWorkspaceAskAIMessage(section)}
              onRefresh={section === 'mcp'
                ? handleMcpRefresh
                : section === 'identity'
                  ? handleIdentityRefresh
                  : () => useWorkflowStore.getState().refreshWorkspaceView()}
              refreshing={section === 'mcp' && activeMcpTab === 'apps' && refreshingServers}
              refreshLabel={section === 'mcp' ? mcpRefreshLabel : section === 'identity' ? identityRefreshLabel : `Refresh ${copy.title}`}
            />
          )}
          tabs={section === 'mcp'
            ? { value: activeMcpTab, onChange: (value: string) => setTab(value as McpTab), options: mcpTabs, ariaLabel: 'Integrations' }
            : section === 'identity'
              ? { value: activeIdentityTab, onChange: (value: string) => setIdentityTab(value as IdentityTab), options: identityTabs, ariaLabel: 'Identity' }
              : undefined}
        />
      )}

      <div className={`p-4 ${section === 'browser' ? 'min-h-0 flex-1 !p-0 flex flex-col overflow-hidden relative' : (section === 'mcp' || section === 'identity') ? 'min-h-0 flex-1 overflow-y-auto' : view.managesOwnScroll ? 'min-h-0 flex-1 flex flex-col overflow-hidden' : 'min-h-0 flex-1 overflow-y-auto'}`}>
        {loading ? (
          <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
            <LoaderCircle className="h-4 w-4 animate-spin" /> Loading workflow settings…
          </div>
        ) : (
          <>
            {error && <p className="mb-4 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{error}</p>}
            {section === 'playbooks' && (
              <PlaybooksPanel workspacePath={workspacePath} />
            )}
            {section === 'mcp' && (
              <div>
                {!relayMode && (activeMcpTab === 'apps' || activeMcpTab === 'skills') && (
                  <>
                <div className="flex shrink-0 flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 p-3">
                  <div className="min-w-0 flex-1 basis-48">
                    <p className="text-sm font-semibold text-foreground">{"Can't find what you need?"}</p>
                    <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
                      {canManagePlatformMCP
                        ? 'The builder can connect an app or find a skill for you. New connections are shared with everyone.'
                        : 'Only an admin can connect new apps. Everything below is ready to use.'}
                    </p>
                  </div>
                  <AskAIButton
                    workspacePath={!canWriteWorkflow || !canManagePlatformMCP ? null : workspacePath ?? null}
                    label={canManagePlatformMCP ? 'Ask builder to help' : "Why can't I add one?"}
                    message={canManagePlatformMCP
                      ? searchQuery.trim()
                        ? `Help me find ${JSON.stringify(searchQuery.trim())} for this workflow. Search both the app catalog and the skills library, on the web and in the local lists. If it is an app, help me connect it, reminding me before authorization that the account will be shared by every AgentWorks user and product; if it is a skill, add it to this workflow. Verify it works, then confirm briefly.`
                        : `Help me add an app connection or a skill to this workflow. Ask what I want to connect or which capability I need, then search the app catalog and the skills library and help me set it up. Before authorizing any shared account, remind me that it will be shared by every AgentWorks user and product.`
                      : `Explain how shared app connections work in AgentWorks, why only an administrator can connect a new one, and how I can use already-connected apps and skills in this workflow. Do not attempt to change configuration.`}
                    className="inline-flex shrink-0 items-center justify-center gap-2 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                  />
                </div>
                  </>
                )}
                <div key={activeMcpTab === 'apps' ? 'apps' : `${activeMcpTab}:${tabNonce}`}>
                {activeMcpTab === 'apps' && (
                  <>
                    {workspacePath && (
                      <div className="mt-3 border-t border-border pt-3">
                        <PlaceMcpSection workspacePath={workspacePath} placeNoun="workflow" canEdit={canWriteWorkflow} />
                      </div>
                    )}
                    {relayMode && availableServers.length === 0 && (
                      <p className="rounded-lg border border-dashed border-border p-5 text-sm text-muted-foreground">No MCP servers are connected yet. Connected servers will appear here for this Relay to use.</p>
                    )}
                    {selectedAvailableServers.length > 0 && (
                      <div className="mt-3 border-t border-border pt-3">
                        <div className="mb-3 text-sm font-medium text-muted-foreground">
                          {relayMode ? 'This Relay' : 'This workflow'}
                        </div>
                        <ToolSelectionSection
                          stepId="workflow-selected"
                          availableServers={selectedAvailableServers}
                          selectedServers={capabilities.selected_servers}
                          selectedTools={capabilities.selected_tools}
                          onServerChange={(selected_servers) => {
                            const next = { ...capabilities, selected_servers }
                            setCapabilities(next)
                            void persist(next)
                          }}
                          onToolChange={(selected_tools) => {
                            const next = { ...capabilities, selected_tools }
                            setCapabilities(next)
                            void persist(next)
                          }}
                          onSelectionChange={(selected_servers, selected_tools) => {
                            const next = { ...capabilities, selected_servers, selected_tools }
                            setCapabilities(next)
                            void persist(next)
                          }}
                          agentMode="workflow"
                          hideHeader
                          manageOwnScroll={false}
                          disabled={!canWriteWorkflow}
                        />
                      </div>
                    )}
                    {unselectedAvailableServers.length > 0 && (
                      <div className="mt-3 border-t border-border pt-3">
                        <div className="mb-1 text-sm font-medium text-muted-foreground">
                          Platform connected
                        </div>
                        <p className="mb-3 text-xs leading-5 text-muted-foreground">
                          Shared with everyone. Tick one to let this {relayMode ? 'Relay' : 'workflow'} use it.
                        </p>
                        <ToolSelectionSection
                          stepId="workflow-available"
                          availableServers={unselectedAvailableServers}
                          selectedServers={capabilities.selected_servers}
                          selectedTools={capabilities.selected_tools}
                          onServerChange={(selected_servers) => {
                            const next = { ...capabilities, selected_servers }
                            setCapabilities(next)
                            void persist(next)
                          }}
                          onToolChange={(selected_tools) => {
                            const next = { ...capabilities, selected_tools }
                            setCapabilities(next)
                            void persist(next)
                          }}
                          onSelectionChange={(selected_servers, selected_tools) => {
                            const next = { ...capabilities, selected_servers, selected_tools }
                            setCapabilities(next)
                            void persist(next)
                          }}
                          agentMode="workflow"
                          hideHeader
                          manageOwnScroll={false}
                          disabled={!canWriteWorkflow}
                        />
                      </div>
                    )}
                    {!relayMode && <div className="relative mt-3 shrink-0">
                      <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
                      <input
                        type="text"
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                        placeholder="Search apps"
                        aria-label="Search apps"
                        className="w-full rounded-lg border border-gray-300 bg-white py-2.5 pl-10 pr-3 text-sm text-gray-900 placeholder-gray-400 transition-colors focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100"
                      />
                    </div>}
                    {!relayMode && <div className="mt-3 border-t border-border pt-3">
                      <div className="mb-3 text-sm font-medium text-muted-foreground">
                        Connect a new app
                      </div>
                      <ConnectorsBrowser
                        compact
                        workspacePath={workspacePath}
                        manageOwnScroll={false}
                        query={searchQuery}
                        hideSearch
                        hideConnectedSection
                        hideBanner
                      />
                    </div>}
                  </>
                )}
                {activeMcpTab === 'skills' && (
                  <div className="mt-3 border-t border-border pt-3">
                    <SkillsManagerPanel
                      compact
                      manageOwnScroll={false}
                      selectedOnly
                      emptySelectionText="No skills are used in this workflow yet. Ask the agent to add or create one."
                      headerAction={(
                        <AskAIButton
                          workspacePath={canWriteWorkflow ? workspacePath ?? null : null}
                          iconOnly
                          label="Install a skill"
                          message={relayMode
                            ? 'Help me add a skill to this Relay. Ask which skill I want, find it in the shared library, then attach it to this Relay and verify the selection.'
                            : 'Help me install a specific skill for this workflow. Ask which skill I want, then find it: search the local skills library first, then the web. Import it into the library, add it to this workflow, verify it works, and confirm briefly.'}
                        />
                      )}
                      workspacePath={workspacePath}
                      selectedSkills={capabilities.selected_skills}
                      onToggleSkill={(folderName) => {
                        const selected_skills = capabilities.selected_skills.includes(folderName)
                          ? capabilities.selected_skills.filter(s => s !== folderName)
                          : [...capabilities.selected_skills, folderName]
                        const next = { ...capabilities, selected_skills }
                        setCapabilities(next)
                        void persist(next)
                      }}
                      onAddViaChat={(skill: Skill) => {
                        if (!workspacePath) return
                        void sendWorkspacePaneMessageToChat({
                          workspacePath,
                          message: `Add the ${JSON.stringify(skill.frontmatter.name)} skill to this workflow (skill folder ${JSON.stringify(skill.folder_name)}). Check that it exists in the skills library first; if it does, add it to this workflow's selected skills and briefly confirm what it gives the workflow. If anything needs my input, ask me.`,
                        }).catch(err => {
                          useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to open chat.', 'error')
                        })
                      }}
                    />
                  </div>
                )}
                {!relayMode && activeMcpTab === 'slack' && (
                  <div className="mt-3">
                    <WorkflowBotsPanel workspacePath={workspacePath} fixedChannel="slack" />
                  </div>
                )}
                {!relayMode && activeMcpTab === 'whatsapp' && (
                  <div className="mt-3 border-t border-border pt-3">
                    <WorkflowBotsPanel workspacePath={workspacePath} fixedChannel="whatsapp" />
                  </div>
                )}
                {activeMcpTab === 'gmail' && (
                  <div className="mt-3">
                    <WorkflowEmailPanel workspacePath={workspacePath} scopeNoun={relayMode ? 'relay' : 'workflow'} />
                  </div>
                )}
                {!relayMode && activeMcpTab === 'cli' && (
                  <div className="mt-3">
                    <CliMcpSetupPanel />
                  </div>
                )}
                </div>
              </div>
            )}
            {section === 'identity' && (
              <div key={`${activeIdentityTab}:${identityTabNonce}`}>
                {activeIdentityTab === 'general' && (
                  <WorkflowIdentityPanel workspacePath={workspacePath} relayMode={relayMode} />
                )}
                {!relayMode && activeIdentityTab === 'secrets' && (
                  // Only the workflow's own checklist lives here: the workflow
                  // box plus globals. Values are managed through the rows below
                  // or the builder; the pane scrolls as a whole and the list
                  // takes only its own height.
                  <div>
                    <div className="mb-3 flex shrink-0 flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 p-3">
                      <div className="min-w-0 flex-1 basis-48">
                        <p className="text-sm font-semibold text-foreground">Need a hand with secrets?</p>
                        <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
                          The builder can save a secret for you or explain what is attached. Values stay hidden — never paste one in chat.
                        </p>
                      </div>
                      <AskAIButton
                        workspacePath={!canWriteWorkflow ? null : workspacePath ?? null}
                        label="Ask AI to manage secrets"
                        message={getIdentityTabAskAIMessage('secrets')}
                        className="inline-flex shrink-0 items-center justify-center gap-2 rounded-lg border border-border px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                      />
                    </div>
                    <SecretSelectionSection
                      selectedSecrets={capabilities.selected_secrets}
                      onSecretChange={(selected_secrets) => {
                        const next = { ...capabilities, selected_secrets }
                        setCapabilities(next)
                        void persist(next)
                      }}
                      selectedGlobalSecrets={capabilities.selected_global_secret_names}
                      onGlobalSecretChange={(selected_global_secret_names) => {
                        const next = { ...capabilities, selected_global_secret_names }
                        setCapabilities(next)
                        void persist(next)
                      }}
                      workflowPath={workspacePath || ''}
                    />
                  </div>
                )}
                {!relayMode && activeIdentityTab === 'folders' && (
                  <WorkflowFolderAccessView workspacePath={workspacePath} hideHeader manageOwnScroll={false} />
                )}
                {activeIdentityTab === 'llm' && (
                  <WorkflowLLMConfigurationPanel
                    workspacePath={workspacePath}
                    llmConfig={capabilities.llm_config}
                    scopeNoun={relayMode ? 'Relay Builder' : 'workflow'}
                    builderOnly={relayMode}
                    onChange={(llm_config) => {
                      const next = { ...capabilities, llm_config }
                      setCapabilities(next)
                      void persist(next)
                    }}
                  />
                )}
                {!relayMode && activeIdentityTab === 'llm' && (
                  <div className="mt-4">
                    <SettingsCard title="Agent tools" ariaLabel="Native agent tools">
                      <ToggleRow
                        label="Native agent tools"
                        description="On by default. In Builder and Run-mode chats, let the coding agent use native read and search tools; available tools vary by CLI. File changes still go through AgentWorks. Step agents, schedules, webhooks and read-only users keep AgentWorks tools only. Changing it starts a fresh CLI session on the next message. Applies to Claude Code, Codex, Cursor, Muse and Antigravity."
                        checked={nativeAgentToolsEnabled(capabilities.native_agent_tools)}
                        disabled={!canWriteWorkflow || saving}
                        disabledTitle={canWriteWorkflow ? 'Saving…' : 'Only owners and editors can change this.'}
                        onCheckedChange={checked => {
                          const next = { ...capabilities, native_agent_tools: checked }
                          setCapabilities(next)
                          void persist(next)
                        }}
                      />
                    </SettingsCard>
                  </div>
                )}
                {!relayMode && activeIdentityTab === 'upgrades' && (
                  <WorkflowUpdatesView workspacePath={workspacePath} />
                )}
              </div>
            )}
            {section === 'browser' && (
              <BrowserWorkspacePanel
                workspacePath={workspacePath}
                browserMode={capabilities.browser_mode as BrowserAutomationMode}
                onBrowserModeChange={(browser_mode) => setCapabilities(current => ({ ...current, browser_mode }))}
                cdpPort={cdpPort}
                onCdpPortChange={(port) => {
                  setCdpPort(port)
                  setCapabilities(current => ({ ...current, cdp_ports: [port] }))
                }}
                cdpConnected={cdpConnected}
                cdpError={cdpError}
                cdpChecking={cdpChecking}
                onCheckCdpConnection={checkCdpConnection}
                readOnly={!canWriteWorkflow}
                dirty={dirty}
                saving={saving}
                onSave={() => void save()}
                assistantControl={
                  <WorkspaceViewActions
                    workspacePath={workspacePath}
                    message={getWorkspaceAskAIMessage('browser')}
                    onRefresh={() => useWorkflowStore.getState().refreshWorkspaceView()}
                    refreshLabel="Refresh Browser"
                  />
                }
              />
            )}
            {/* Bots write straight to the shared connector config (routes
                already carry workflow_id), so nothing here goes through the
                manifest Save below. */}
            {/* Bots and Gmail live under the Integrations tabs above, not as sections. */}
          </>
        )}
      </div>

      {/* PLAT-262: Save hidden for a read-only user — nothing in this panel
          can actually persist for that account, so hide the button that
          implies otherwise rather than let it fail after the fact. Also
          hidden for sections that don't save through the manifest at all. */}
      {!loading && canWriteWorkflow && copy.savesViaManifest && section !== 'browser' && (
        <footer className="flex shrink-0 items-center justify-end gap-3 border-t px-4 py-3">
          {dirty && <span className="text-xs text-muted-foreground">Unsaved changes</span>}
          <button
            type="button"
            onClick={() => void save()}
            disabled={saving || !dirty}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {saving ? <LoaderCircle className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
            Save
          </button>
        </footer>
      )}
    </section>
  )
}
