import { ProductSlackPanel } from '../integrations/ProductSlackPanel'
import { selectWorkspacePaneWorkflowTab } from '../../utils/workspacePaneChat'
import { capabilitiesEqual, mergeRemoteCapabilities } from './workflowCapabilitiesSync'
import React, { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { LoaderCircle, Plus, Save, Users } from 'lucide-react'
import { WorkspacePanelBack, useWorkspacePanelChatDraft } from '../workspace/WorkspacePanelNavigation'
import SkillsManagerPanel from '../skills/SkillsManagerPanel'
import PlaybooksPanel from '../playbooks/PlaybooksPanel'
import WorkflowLLMConfigurationPanel from './WorkflowLLMConfigurationPanel'
import WorkflowBotsPanel from './WorkflowBotsPanel'
import WorkflowEmailPanel from './WorkflowEmailPanel'
import { ProjectMcpSetupPanel } from '../integrations/ProjectMcpSetupPanel'
import { integrationNavigationOptions } from '../integrations/IntegrationNavigation'
import { projectChannelLabel, type ProjectChannel } from '../integrations/projectChannels'
import { ProjectAgentBridgePanel } from '../workspace/ProjectAgentBridgePanel'
import { ShareDashboardButton } from './ShareDashboardButton'
import { integrationProductAvailable, projectIntegrationSections } from '../integrations/integrationSections'
import { ProjectPluginsPanel, projectPluginTabs, type PluginTab } from '../integrations/ProjectPluginsPanel'
import { ProjectIntegrationPanel, INTEGRATION_LABELS, type IntegrationSection } from '../integrations/ProjectIntegrationPanel'
import { CodeLocalFilesSettings } from '../../products/work/CodeFilesPanel'
import { ProjectKnowledgebasePanel } from './ProjectKnowledgebasePanel'
import { agentApi, workflowManifestApi } from '../../services/api'
import type { WorkflowCapabilities } from '../../services/api-types'
import { useMCPStore } from '../../stores/useMCPStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import type { Skill } from '../../types/skills'
import { getWorkspaceView, type CapabilityViewId } from './workspaceViews'
import { BrowserWorkspacePanel } from './BrowserWorkspacePanel'
import type { BrowserAutomationMode } from '../BrowserAutomationSettings'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import WorkflowIdentityPanel from './WorkflowIdentityPanel'
import WorkflowFolderAccessView from './WorkflowFolderAccessView'
import WorkflowUpdatesView from './WorkflowUpdatesView'


// Which sections exist is decided by the registry in workspaceViews.ts; this
// panel only carries the per-section copy.
export type WorkflowCapabilitySection = CapabilityViewId

type McpTab = 'apps' | 'brain' | 'folders' | 'secrets' | 'skills' | 'slack' | 'whatsapp' | 'gmail' | 'cli'

type IdentityTab = 'general' | 'secrets' | 'llm' | 'laptop' | 'upgrades'

const IDENTITY_TABS: Array<{ value: IdentityTab; label: string }> = [
  { value: 'general', label: 'General' },
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
    title: 'MCPs',
    description: 'Choose the Vault MCP connections this workflow uses. Add and manage accounts through chat.',
    // Selection changes persist immediately, so this long directory can use
    // one uninterrupted scroll surface without a fixed Save footer.
    savesViaManifest: false,
  },
  identity: {
    title: 'Settings',
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
  const vaultTabId = useChatStore(state => {
    const active = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    return active && active.metadata && active.metadata.presetQueryId === presetQueryId && active.metadata.phaseId === 'workflow-builder' ? active.tabId : ''
  })
  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)
  const [capabilities, setCapabilities] = useState<WorkflowCapabilities>(EMPTY_CAPABILITIES)
  // What the manifest last held, so the footer can tell "edited" from "saved".
  const [loaded, setLoaded] = useState<WorkflowCapabilities>(EMPTY_CAPABILITIES)
  const dirty = useMemo(() => !capabilitiesEqual(capabilities, loaded), [capabilities, loaded])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [cdpConnected, setCdpConnected] = useState<boolean | null>(null)
  const [cdpError, setCdpError] = useState<string | null>(null)
  const [cdpChecking, setCdpChecking] = useState(false)
  const refreshTools = useMCPStore(state => state.refreshTools)
  const [refreshingServers, setRefreshingServers] = useState(false)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const mcpTabs = projectIntegrationSections(allowedProducts, relayMode)
  const [tab, setTab] = useState<McpTab>('apps')
  const activeMcpTab = mcpTabs.some(option => option.value === tab) ? tab : mcpTabs[0].value
  const [preferredPluginTab, setPluginTab] = useState<PluginTab>('connected')
  const [sharing, setSharing] = useState(false)
  const [integrationSection, setIntegrationSection] = useState<IntegrationSection>('connected')
  const [shareChannel, setShareChannel] = useState<ProjectChannel | 'agentbridge'>('cli')
  const pluginTabs = projectPluginTabs(allowedProducts)
  const pluginTab = pluginTabs.some(option => option.value === preferredPluginTab) ? preferredPluginTab : pluginTabs[0].value
  const identityTabs = relayMode ? RELAY_IDENTITY_TABS : IDENTITY_TABS
  const [identityTab, setIdentityTab] = usePersistentTab<IdentityTab>(relayMode ? 'relays.tab.workflow-identity' : 'agentworks.tab.workflow-identity', 'general', identityTabs.map(option => option.value))
  const activeIdentityTab = identityTabs.some(option => option.value === identityTab) ? identityTab : 'general'
  const navigationOptions = integrationNavigationOptions(mcpTabs, pluginTabs)
  const selectConnection = (target: string) => {
    if (['connected', 'available', 'secrets', 'gmail', 'slack-account', ...(!relayMode ? ['laptop'] : [])].includes(target)) { setSharing(false); setTab('apps'); setPluginTab('connected'); setIntegrationSection(target as IntegrationSection); return }
    if (target === 'share' || target === 'agentbridge' && integrationProductAvailable('bullpen', allowedProducts) || target === 'cli' || (target === 'slack' && mcpTabs.some(option => option.value === 'slack')) || (target === 'whatsapp' && mcpTabs.some(option => option.value === 'whatsapp'))) { setSharing(true); setShareChannel(target as ProjectChannel | 'agentbridge'); return }
    if (target === 'overview' || target === 'apps') { setSharing(false); setTab('apps'); setPluginTab(pluginTabs.some(option => option.value === 'connected') ? 'connected' : pluginTabs[0].value); return }
    if (!mcpTabs.some(option => option.value === target) && !pluginTabs.some(option => option.value === target)) return
    setSharing(false)
    if (mcpTabs.some(option => option.value === target)) setTab(target as McpTab)
    else if (pluginTabs.some(option => option.value === target)) { setTab('apps'); setPluginTab(target as typeof pluginTab) }
  }
  const prepareDraft = useWorkspacePanelChatDraft(vaultTabId)
  const askInCurrentChat = async (message: string) => {
    if (!vaultTabId) throw new Error('Open your own project chat to make this change.')
    await prepareDraft(message)
  }
  const activeConnection = activeMcpTab === 'apps' ? pluginTab : activeMcpTab
  const connectionLabel = activeMcpTab === 'apps' && pluginTab !== 'skills' ? INTEGRATION_LABELS[integrationSection] : navigationOptions.find(option => option.value === (activeConnection === 'available' ? 'connected' : activeConnection))?.label ?? 'MCPs'
  const serverTabs = pluginTabs.filter(option => option.value === 'connected' || option.value === 'available').map(option => ({ ...option, label: option.value === 'connected' ? 'Connected' : 'Available' }))
  // Existing bubble and Ctrl+K targets open their resource directly.
  useWorkspaceViewTarget('mcp', selectConnection, section === 'mcp')
  useWorkspaceViewTarget('identity', target => {
    if (identityTabs.some(option => option.value === target)) setIdentityTab(target as IdentityTab)
  }, section === 'identity')
  const copy = SECTION_COPY[section]
  const view = getWorkspaceView(section)

  const SectionIcon = view.icon

  const latest = useRef({ workspacePath, capabilities, loaded, saving })
  latest.current = { workspacePath, capabilities, loaded, saving }
  // Native Vault writes return saved selections; mirror them without a second write.
  const syncVaultSelections = (patch: Partial<WorkflowCapabilities>) => {
    const next = { ...latest.current.capabilities, ...patch }
    latest.current.capabilities = next
    latest.current.loaded = { ...latest.current.loaded, ...patch }
    setCapabilities(next); setLoaded(latest.current.loaded)
    if (!workspacePath) return
    const store = useWorkflowManifestStore.getState()
    const current = store.getWorkflowByPath(workspacePath)
    if (current) store.replaceWorkflowManifest(workspacePath, { ...current.manifest, capabilities: { ...current.manifest.capabilities, ...patch } })
  }
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
    if (activeMcpTab === 'apps' && (pluginTab === 'connected' || pluginTab === 'available')) {
      void handleRefreshServers()
    }
    setTabNonce(nonce => nonce + 1)
  }, [activeMcpTab, pluginTab, handleRefreshServers])
  const mcpRefreshLabel = `Refresh ${connectionLabel}`

  // Every Identity tab loads on mount, so Refresh always remounts.
  const [identityTabNonce, setIdentityTabNonce] = useState(0)
  const handleIdentityRefresh = useCallback(() => {
    setIdentityTabNonce(nonce => nonce + 1)
  }, [])
  const identityRefreshLabel = `Refresh ${identityTabs.find(option => option.value === activeIdentityTab)?.label ?? 'view'}`

  // Agent tools (and shell edits) can change this configuration while the panel
  // stays open. Refresh both sources, only for the visible MCP panel.
  useEffect(() => {
    if (section !== 'mcp' || !workspacePath || !relayMode) return
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
  }, [relayMode, section, workspacePath, handleRefreshServers])

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
  const skillsView = section === 'mcp' && activeMcpTab === 'apps' && pluginTab === 'skills'

  return (
    <section className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
      {section === 'mcp' && !sharing && activeMcpTab === 'apps' && pluginTab === 'available' && <WorkspacePanelBack label="Back to MCPs" onBack={() => selectConnection('connected')} />}
      {section !== 'browser' && (
        <WorkspaceViewHeader
          hideHeader={skillsView}
          icon={section === 'mcp' && sharing ? Users : SectionIcon}
          title={section === 'mcp' && sharing ? shareChannel === 'agentbridge' ? 'AgentBridge' : projectChannelLabel(shareChannel) : section === 'mcp' ? connectionLabel : copy.title}
          actions={(
            <WorkspaceViewActions
              onRefresh={section === 'mcp'
                ? sharing ? () => setTabNonce(nonce => nonce + 1) : handleMcpRefresh
                : section === 'identity'
                  ? handleIdentityRefresh
                  : () => useWorkflowStore.getState().refreshWorkspaceView()}
              refreshing={section === 'mcp' && activeMcpTab === 'apps' && (pluginTab === 'connected' || pluginTab === 'available') && refreshingServers}
              refreshLabel={section === 'mcp' ? sharing ? 'Refresh sharing and channels' : mcpRefreshLabel : section === 'identity' ? identityRefreshLabel : `Refresh ${copy.title}`}
            >
              {section === 'mcp' && relayMode && !sharing && activeMcpTab === 'apps' && pluginTab === 'connected' && canWriteWorkflow && <button type="button" onClick={() => selectConnection('available')} className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Plus className="h-3.5 w-3.5" aria-hidden="true" />Add an MCP</button>}
            </WorkspaceViewActions>
          )}
          tabs={section === 'mcp'
            ? undefined
            // Settings, Models and Upgrades are separate items in the side list, so Settings has no sub-tabs (owner, 2026-10-20).
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
                <div data-ui-view-section={sharing ? shareChannel : activeConnection} key={`${workspacePath}:${sharing ? shareChannel : activeMcpTab}:${tabNonce}`}>
                {sharing ? <>
                  {workspacePath && (shareChannel === 'agentbridge' ? <ProjectAgentBridgePanel target={`workflow:${presetQueryId}`} tabId={vaultTabId} onAsk={askInCurrentChat} /> : shareChannel === 'share' ? <ShareDashboardButton workspacePath={workspacePath} variant="panel" tabId={vaultTabId} onAsk={askInCurrentChat} /> : shareChannel === 'cli' ? <ProjectMcpSetupPanel workspacePath={workspacePath} /> : <>{shareChannel === 'slack' ? <ProductSlackPanel workspacePath={workspacePath} noun={relayMode ? 'Flow' : 'Goal'} allowBots={!relayMode} section={relayMode ? 'account' : 'bot'} tabId={vaultTabId} /> : <WorkflowBotsPanel workspacePath={workspacePath} fixedChannel="whatsapp" />}</>)}
                </> : <>
                {activeMcpTab === 'apps' && pluginTab !== 'skills' && workspacePath && <ProjectIntegrationPanel workspacePath={workspacePath} tabId={vaultTabId} placeNoun={relayMode ? 'Flow' : 'Goal'} section={integrationSection} canEdit={canWriteWorkflow} onAsk={askInCurrentChat}
                  onAppsSaved={selected_servers => syncVaultSelections({ selected_servers })}
                  selectedSecrets={capabilities.selected_secrets}
                  onSelectedSecretsChange={async selected_secrets => { const next = { ...latest.current.capabilities, selected_secrets }; await persist(next, true); setCapabilities(next) }}
                  onSecretSelectionSaved={inventory => syncVaultSelections({ selected_secrets: inventory.selected_secrets, selected_global_secret_names: inventory.selected_global_secret_names })}
                  laptop={!relayMode ? <CodeLocalFilesSettings workflowPath={workspacePath} /> : undefined} />}
                {activeMcpTab === 'apps' && workspacePath && pluginTab === 'skills' && <ProjectPluginsPanel tab="skills" skills={<SkillsManagerPanel
                      compact
                      manageOwnScroll={false}
                      customOnly
                      profileId={relayMode ? 'relays' : 'agentworks'}
                      selectionScopeLabel={relayMode ? 'Flow' : 'Goal'}
                      emptySelectionText="No custom skills have been added here yet."
                      workspacePath={workspacePath}
                      selectedSkills={capabilities.selected_skills}
                      onBeforeUninstall={() => saveQueue.current}
                      onUninstalled={async () => {
                        await load()
                        await useWorkflowManifestStore.getState().refreshWorkflows()
                        await useWorkflowStore.getState().refreshWorkspaceView()
                      }}
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
                          message: `Add the ${JSON.stringify(skill.frontmatter.name)} skill to this workflow (skill folder ${JSON.stringify(skill.folder_name)}). Use the skill installed in this workflow. Attach it to the main chat or the steps that need it, and briefly confirm what it gives the workflow. If anything needs my input, ask me.`,
                        }).catch(err => {
                          useChatStore.getState().addToast(err instanceof Error ? err.message : 'Failed to open chat.', 'error')
                        })
                      }}
                    />} />}
                {!relayMode && activeMcpTab === 'brain' && workspacePath && (
                  <ProjectKnowledgebasePanel workspacePath={workspacePath} disabled={!canWriteWorkflow} />
                )}
                {!relayMode && activeMcpTab === 'folders' && (
                  <WorkflowFolderAccessView workspacePath={workspacePath} hideHeader manageOwnScroll={false} />
                )}
                {activeMcpTab === 'gmail' && (
                  <div className="mt-3">
                    <WorkflowEmailPanel workspacePath={workspacePath} />
                  </div>
                )}
                </>}
                </div>
              </div>
            )}
            {section === 'identity' && (
              <div key={`${activeIdentityTab}:${identityTabNonce}`}>
                {activeIdentityTab === 'general' && (
                  <WorkflowIdentityPanel workspacePath={workspacePath} relayMode={relayMode} />
                )}
                {activeIdentityTab === 'llm' && (
                  <><WorkflowLLMConfigurationPanel
                    workspacePath={workspacePath}
                    llmConfig={capabilities.llm_config}
                    scopeNoun={relayMode ? 'Flow Builder' : 'workflow'}
                    builderOnly={relayMode}
                    onChange={(llm_config) => {
                      const next = { ...capabilities, llm_config }
                      setCapabilities(next)
                      void persist(next)
                    }}
                  /></>
                )}

                {!relayMode && activeIdentityTab === 'upgrades' && (
                  <WorkflowUpdatesView workspacePath={workspacePath} />
                )}
              </div>
            )}
            {section === 'browser' && (
              <BrowserWorkspacePanel
                profileId={relayMode ? 'relay' : 'workflow'}
                workspacePath={workspacePath}
                onSendToAgent={workspacePath ? message => sendWorkspacePaneMessageToChat({ workspacePath, message }) : undefined}
                browserMode={capabilities.browser_mode as BrowserAutomationMode}
                onBrowserModeChange={(browser_mode) => setCapabilities(current => ({ ...current, browser_mode }))}
                cdpConnected={cdpConnected}
                cdpError={cdpError}
                cdpChecking={cdpChecking}
                onCheckCdpConnection={checkCdpConnection}
                readOnly={!canWriteWorkflow}
                dirty={dirty}
                saving={saving}
                onSave={() => void save()}
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
