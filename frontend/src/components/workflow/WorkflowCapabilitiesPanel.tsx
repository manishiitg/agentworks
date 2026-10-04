import { selectWorkspacePaneWorkflowTab } from '../../utils/workspacePaneChat'
import { capabilitiesEqual, mergeRemoteCapabilities } from './workflowCapabilitiesSync'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { LoaderCircle, Save } from 'lucide-react'
import SkillsManagerPanel from '../skills/SkillsManagerPanel'
import PlaybooksPanel from '../playbooks/PlaybooksPanel'
import { SecretSelectionSection } from '../secrets/SecretSelectionSection'
import WorkflowLLMConfigurationPanel from './WorkflowLLMConfigurationPanel'
import WorkflowBotsPanel from './WorkflowBotsPanel'
import WorkflowEmailPanel from './WorkflowEmailPanel'
import { CliMcpSetupPanel } from '../integrations/CliMcpSetupPanel'
import { WorkspaceViewBreadcrumbs } from './WorkspaceViewBreadcrumbs'
import { IntegrationSectionPicker } from '../integrations/IntegrationSectionPicker'
import { ProjectPluginsPanel, PROJECT_PLUGIN_TABS, useProjectPluginTab } from '../integrations/ProjectPluginsPanel'
import { ProjectVaultPanel } from '../integrations/ProjectVaultPanel'
import { ProjectMcpPanel } from '../integrations/ProjectMcpPanel'
import { agentApi, workflowManifestApi } from '../../services/api'
import type { WorkflowCapabilities } from '../../services/api-types'
import { useMCPStore } from '../../stores/useMCPStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'
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

// Which sections exist is decided by the registry in workspaceViews.ts; this
// panel only carries the per-section copy.
export type WorkflowCapabilitySection = CapabilityViewId

type McpTab = IntegrationTabId

const MCP_TABS: Array<{ value: McpTab; label: string }> = [
  { value: 'apps', label: 'Plugins' },
  { value: 'slack', label: 'Slack' },
  { value: 'whatsapp', label: 'WhatsApp' },
  { value: 'gmail', label: 'Google apps' },
  { value: 'cli', label: 'Connect' },
]
const RELAY_MCP_TABS = MCP_TABS.filter(option => option.value === 'apps' || option.value === 'skills' || option.value === 'secrets' || option.value === 'gmail')

type IdentityTab = IdentityTabId

const IDENTITY_TABS: Array<{ value: IdentityTab; label: string }> = [
  { value: 'general', label: 'General' },
  { value: 'folders', label: 'Connected work' },
  { value: 'llm', label: 'Models' },
  { value: 'upgrades', label: 'Upgrades' },
]
const RELAY_IDENTITY_TABS = IDENTITY_TABS.filter(option => option.value === 'general' || option.value === 'llm')

interface WorkflowCapabilitiesPanelProps {
  section: WorkflowCapabilitySection
  workspacePath: string | null
  presetQueryId?: string | null
  relayMode?: boolean
}

const EMPTY_CAPABILITIES: WorkflowCapabilities = {
  selected_servers: [],
  selected_tools: [],
  selected_skills: [],
  selected_secrets: [],
  selected_global_secret_names: [],
  browser_mode: 'auto',
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
    description: 'Name, icon, purpose, connected work, LLMs, and platform upgrades for this workflow.',
    savesViaManifest: false,
  },
  browser: {
    title: 'Browser automation',
    description: 'Watch this workflow’s browser and configure its automation access.',
    savesViaManifest: true,
  },
}

export default function WorkflowCapabilitiesPanel({ section, workspacePath, presetQueryId, relayMode = false }: WorkflowCapabilitiesPanelProps) {
  const chatSessionId = useChatStore(state => presetQueryId ? selectWorkspacePaneWorkflowTab(state.chatTabs, presetQueryId, state.activeTabId)?.sessionId ?? undefined : undefined)
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
  const refreshTools = useMCPStore(state => state.refreshTools)
  const [refreshingServers, setRefreshingServers] = useState(false)
  const mcpTabs = relayMode ? RELAY_MCP_TABS : MCP_TABS
  const [tab, setTab] = usePersistentTab<McpTab>(relayMode ? 'relays.tab.workflow-mcp' : 'agentworks.tab.workflow-mcp', 'apps', mcpTabs.map(option => option.value))
  const activeMcpTab = mcpTabs.some(option => option.value === tab) ? tab : mcpTabs[0].value
  const [integrationMenu, setIntegrationMenu] = useState(false)
  const [pluginTab, setPluginTab] = useProjectPluginTab()
  const identityTabs = relayMode ? RELAY_IDENTITY_TABS : IDENTITY_TABS
  const [identityTab, setIdentityTab] = usePersistentTab<IdentityTab>(relayMode ? 'relays.tab.workflow-identity' : 'agentworks.tab.workflow-identity', 'general', identityTabs.map(option => option.value))
  const activeIdentityTab = identityTabs.some(option => option.value === identityTab) ? identityTab : 'general'
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

  const persist = useCallback((next: WorkflowCapabilities, rejectOnError = false): Promise<void> => {
    if (!workspacePath) {
      setError('This panel needs an active workflow folder before it can save.')
      if (rejectOnError) return Promise.reject(new Error('This panel needs an active workflow folder before it can save.'))
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
        if (rejectOnError) throw cause
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
          title={section === 'mcp' && !integrationMenu ? <WorkspaceViewBreadcrumbs parent="Integrations" current={mcpTabs.find(option => option.value === activeMcpTab)?.label ?? 'Plugins'} onBack={() => setIntegrationMenu(true)} /> : copy.title}
          helpTopic={section === 'mcp'
            ? `Integrations · ${mcpTabs.find(option => option.value === activeMcpTab)?.label ?? 'MCPs'}`
            : section === 'identity'
              ? `Identity · ${identityTabs.find(option => option.value === activeIdentityTab)?.label ?? 'General'}`
              : undefined}
          actions={(
            <WorkspaceViewActions
              workspacePath={workspacePath}
              message={section === 'mcp'
                ? activeMcpTab === 'apps' && pluginTab === 'vault'
                  ? 'Help me choose from my Vault groups’ permitted connections and secrets for this project. Check current access and project selection; never show secret values.'
                  : relayMode && activeMcpTab === 'apps'
                  ? 'Help me choose from the MCP servers and tools already connected to this platform for this Relay. Explain what each agent can use before changing the selection.'
                  : relayMode && activeMcpTab === 'gmail'
                    ? 'Help me connect Google apps to this Relay, including Drive, Sheets, Calendar or Gmail. Inspect the authorized connections and service grants, explain what its agents can use, and ask which access is needed. Plan creation needs no Google credentials.'
                  : getIntegrationTabAskAIMessage(activeMcpTab === 'apps' && (pluginTab === 'secrets' || pluginTab === 'skills') ? pluginTab : activeMcpTab)
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
            ? (!integrationMenu && activeMcpTab === 'apps' ? { value: pluginTab, onChange: (value: string) => setPluginTab(value as typeof pluginTab), options: [...PROJECT_PLUGIN_TABS], ariaLabel: 'Plugins' } : undefined)
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
                {integrationMenu ? <IntegrationSectionPicker options={mcpTabs} onSelect={value => { setTab(value as McpTab); setIntegrationMenu(false) }} /> : <div key={`${activeMcpTab}:${tabNonce}`}>
                {activeMcpTab === 'apps' && workspacePath && <ProjectPluginsPanel tab={pluginTab}
                  connections={view => <ProjectMcpPanel view={view} chatSessionId={chatSessionId} workspacePath={workspacePath} placeNoun="workflow" canEdit={canWriteWorkflow}
                      selectedServers={capabilities.selected_servers}
                      onSelectedServersChange={async selected_servers => { const next = { ...latest.current.capabilities, selected_servers }; await persist(next, true); setCapabilities(next) }} />}
                  secrets={<SecretSelectionSection showGlobalSecrets={false}
                  workflowPath={workspacePath || ''} selectedSecrets={capabilities.selected_secrets}
                  selectedGlobalSecrets={capabilities.selected_global_secret_names ?? []}
                  onSecretChange={async selected_secrets => { const next = { ...latest.current.capabilities, selected_secrets }; await persist(next,true);setCapabilities(next) }}
                  onGlobalSecretChange={async names => { const next = { ...latest.current.capabilities, selected_global_secret_names:names ?? [] }; await persist(next,true);setCapabilities(next) }} />}
                  skills={<SkillsManagerPanel
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
                    />}
                  vault={<ProjectVaultPanel disabled={!canWriteWorkflow} selectedServers={capabilities.selected_servers} selectedSecrets={capabilities.selected_global_secret_names ?? []}
                    onSelectedServersChange={async selected_servers => { const next = { ...latest.current.capabilities, selected_servers }; await persist(next, true); setCapabilities(next) }}
                    onSelectedSecretsChange={async selected_global_secret_names => { const next = { ...latest.current.capabilities, selected_global_secret_names }; await persist(next, true); setCapabilities(next) }} />}
                />}
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
                </div>}
              </div>
            )}
            {section === 'identity' && (
              <div key={`${activeIdentityTab}:${identityTabNonce}`}>
                {activeIdentityTab === 'general' && (
                  <WorkflowIdentityPanel workspacePath={workspacePath} relayMode={relayMode} />
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
