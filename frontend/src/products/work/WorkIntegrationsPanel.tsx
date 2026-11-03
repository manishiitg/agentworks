import { YourComputerPanel } from './YourComputerPanel'
import { useMacCrew } from './useMacCrew'
import { useProjectMode } from './codeLocalFiles'
import { useState, type ReactNode } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { activateTab } from '../../utils/activateTab'
import { Server, Users } from 'lucide-react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { ProjectIntegrationPanel, INTEGRATION_LABELS, type ProjectSecretControls, type IntegrationSection } from '../../components/integrations/ProjectIntegrationPanel'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { useChatStore } from '../../stores/useChatStore'
import { ProjectKnowledgebasePanel } from '../../components/workflow/ProjectKnowledgebasePanel'
import { ProductSlackPanel } from '../../components/integrations/ProductSlackPanel'
import { ProjectMcpSetupPanel } from '../../components/integrations/ProjectMcpSetupPanel'
import { ShareDashboardButton } from '../../components/workflow/ShareDashboardButton'
import WorkflowBotsPanel from '../../components/workflow/WorkflowBotsPanel'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { WorkFoldersSection } from './WorkIdentityPanel'
import { useWorkspacePanelChatDraft } from '../../components/workspace/WorkspacePanelNavigation'
import { ProjectAgentBridgePanel } from '../../components/workspace/ProjectAgentBridgePanel'
import { useProjectProduct } from './projectProduct'
import { isWorkIntegrationTabEnabled } from './workViewGating'
import { useAuthStore } from '../../stores/useAuthStore'
import { integrationProductAvailable } from '../../components/integrations/integrationSections'
import { isLocalDeployment } from '../productSurfaceConfig'
import { ComputerUsePanel } from './ComputerUsePanel'

export type WorkIntegrationTab = 'apps' | 'brain' | 'folders' | 'mac-folders' | 'slack' | 'whatsapp' | 'cli' | 'access' | 'computer-use' | 'coding-apps' | 'message-agents'

export function WorkIntegrationsPanel({ accessLoading = false, selectedSecrets = [], onSelectedSecretsChange = () => {}, onAppsSaved, canEdit = false, workspacePath, projectId, projectTitle, tabId, enabledPanels, onAsk, workflowContextPaths, onWorkflowContextPathsChange }: {
  accessLoading?: boolean
  onAppsSaved?: () => void
  selectedSecrets?: string[]
  onSelectedSecretsChange?: ProjectSecretControls['onSelectedSecretsChange']
  canEdit?: boolean; workspacePath: string; projectId: string; projectTitle: string
  tabId: string; enabledPanels?: Set<string>
  onAsk: (message: string) => Promise<void>
  workflowContextPaths: string[]; onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>
}) {
  const draft = useWorkspacePanelChatDraft(tabId)
  const product = useProjectProduct()
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const [tab, setTab] = useState<WorkIntegrationTab>('apps')
  const [integrationSection, setIntegrationSection] = useState<IntegrationSection>('connected')
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  const projectMode = useProjectMode(sessionId ?? '')
  useWorkspaceViewTarget('mcp', target => {
    // Sharing, where it lives (or laptop access) and connected agents are one Access page (owner, 3 Nov); old names open it.
    const next = target === 'slack-bot' ? 'slack' : ['share', 'computer', 'laptop', 'sync', 'agents', 'agentbridge'].includes(target) ? 'access' : target
    if (next === 'computer-use' && (!canEdit || !isLocalDeployment())) return
    if (!isWorkIntegrationTabEnabled(next === 'github' ? 'secrets' : next, enabledPanels) || next === 'brain' && !integrationProductAvailable('knowledgebase', allowedProducts)) return
    if (['connected', 'available', 'secrets', 'gmail', 'slack-account', 'github'].includes(next)) { setTab('apps'); setIntegrationSection(next as IntegrationSection); return }
    if ((next === 'coding-apps' || next === 'message-agents') && !integrationProductAvailable('bullpen', allowedProducts)) return
    if (['apps', 'brain', 'folders', 'mac-folders', 'slack', 'whatsapp', 'cli', 'access', 'computer-use', 'coding-apps', 'message-agents'].includes(next)) setTab(next as WorkIntegrationTab)
  })
  const labels: Record<WorkIntegrationTab, string> = { apps: INTEGRATION_LABELS[integrationSection], brain: 'Brain', folders: 'Linked projects', 'mac-folders': 'Folders on this Mac', slack: 'Slack bot', whatsapp: 'WhatsApp', cli: 'MCP access', access: 'Access', 'computer-use': 'Computer use', 'coding-apps': 'Coding apps on this Mac', 'message-agents': 'Message other agents' }
  if (accessLoading) return <p role="status" className="p-4 text-sm text-muted-foreground">Loading project access…</p>
  return <div className="flex h-full min-h-0 flex-col bg-background">
    <WorkspaceViewHeader icon={tab === 'access' ? Users : Server} title={labels[tab]} />
    <div data-ui-view-section={tab === 'apps' ? integrationSection : tab} className="min-h-0 flex-1 overflow-y-auto p-4">
      <>
        {tab === 'access' && <AccessPage projectId={projectId} sessionId={sessionId} workspacePath={workspacePath} tabId={tabId} canEdit={canEdit} onAsk={draft} laptopForRun={<ProjectIntegrationPanel workspacePath={workspacePath} tabId={tabId} placeNoun="Crew" section="laptop" canEdit={false} onAsk={draft} selectedSecrets={selectedSecrets} onSelectedSecretsChange={onSelectedSecretsChange} />} />}
        {tab === 'computer-use' && canEdit && isLocalDeployment() && <ComputerUsePanel projectId={projectId} />}
        {tab === 'apps' && <ProjectIntegrationPanel github={projectMode === 'dev'} workspacePath={workspacePath} tabId={tabId} placeNoun="Crew" section={integrationSection} canEdit={canEdit} onAsk={draft} onAppsSaved={onAppsSaved} selectedSecrets={selectedSecrets} onSelectedSecretsChange={onSelectedSecretsChange} onSecretSelectionSaved={onAppsSaved} laptop={<CodeLocalFilesSettings sessionId={sessionId} />} />}
        {tab === 'brain' && <ProjectKnowledgebasePanel workspacePath={workspacePath} />}
        {tab === 'folders' && <WorkFoldersSection onOpenComputer={canEdit ? () => setTab('access') : undefined} workspacePath={workspacePath} workflowContextPaths={workflowContextPaths} onWorkflowContextPathsChange={onWorkflowContextPathsChange} />}
        {tab === 'mac-folders' && <CodeLocalFilesSettings sessionId={sessionId} />}
        {tab === 'coding-apps' && <ProjectAgentBridgePanel target={`workspace:${product.profileId}:${projectId}`} tabId={tabId} onAsk={draft} view="coding" />}
        {tab === 'message-agents' && <ProjectAgentBridgePanel target={`workspace:${product.profileId}:${projectId}`} tabId={tabId} onAsk={draft} view="others" />}
        {tab === 'cli' && <ProjectMcpSetupPanel workspacePath={workspacePath} />}
        {tab === 'slack' && <ProductSlackPanel workspacePath={workspacePath} noun="Crew" section="bot" tabId={tabId} />}
        {tab === 'whatsapp' && <WorkflowBotsPanel workspacePath={workspacePath} fixedChannel="whatsapp" scopeNoun="project" onAsk={onAsk} target={{ profileId: product.profileId, conversationKey: projectId, label: projectTitle }} />}
      </>
    </div>
  </div>
}

/** One Access page (owner, 3 Nov): where the Crew lives, who can use it, and its connected agents. */
function AccessPage({ projectId, sessionId, workspacePath, tabId, canEdit, onAsk, laptopForRun }: { projectId: string; sessionId?: string; workspacePath: string; tabId: string; canEdit: boolean; onAsk: (message: string) => void | Promise<unknown>; laptopForRun: ReactNode }) {
  const { livesOnMac } = useMacCrew(projectId, 15000)
  const heading = (text: string) => <h3 className="mb-2 text-xs font-medium text-muted-foreground">{text}</h3>
  return <div className="max-w-2xl space-y-6">
    <section aria-label="Where it lives">
      {canEdit ? <YourComputerPanel projectId={projectId} sessionId={sessionId} canEdit={canEdit} /> : laptopForRun}
    </section>
    {/* A Crew on this Mac shows Who can use it inside its Where it lives card. */}
    {!livesOnMac && <section aria-label="Who can use it">{heading('Who can use it')}<ShareDashboardButton workspacePath={workspacePath} variant="panel" tabId={tabId} onAsk={onAsk} /></section>}
    <section aria-label="Agents in this Crew">{heading('Agents in this Crew')}<CrewAgentsList projectId={projectId} /></section>
  </div>
}

// A tab named after an AI app holds the questions asked from that app.
const APP_TABS = new Set(['chatgpt', 'claude', 'slack', 'cursor', 'codex', 'gemini', 'ai app'])

/** Agents in this Crew (owner, 3 Nov): its tabs by name, each with one plain line and Open. No account names or roles. */
function CrewAgentsList({ projectId }: { projectId: string }) {
  const tabs = useChatStore(useShallow(state => Object.values(state.chatTabs).filter(tab => tab.metadata?.agentProfileId === 'work' && tab.metadata?.agentProfileProjectId === projectId)))
  const ordered = [...tabs].sort((a, b) => Number(Boolean(b.metadata?.agentProfileIsMainChat)) - Number(Boolean(a.metadata?.agentProfileIsMainChat)))
  return <div className="max-w-2xl space-y-1 text-sm">
    {ordered.map(tab => {
      const main = Boolean(tab.metadata?.agentProfileIsMainChat)
      const name = main ? 'Main' : tab.name
      const line = main ? 'your main chat' : APP_TABS.has(tab.name.trim().toLowerCase()) ? `questions you asked from ${tab.name}` : ''
      return <div key={tab.tabId} className="flex items-center gap-3 rounded-lg px-1 py-1.5">
        <span className="min-w-0 flex-1"><span className="font-medium">{name}</span>{line && <span className="text-muted-foreground"> · {line}</span>}</span>
        <button type="button" onClick={() => activateTab(tab.tabId)} className="rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted">Open</button>
      </div>
    })}
    <button type="button" onClick={() => window.dispatchEvent(new CustomEvent('agentworks:new-agent', { detail: { projectId } }))} className="px-1 py-1.5 text-xs text-primary hover:underline">+ New agent</button>
  </div>
}
