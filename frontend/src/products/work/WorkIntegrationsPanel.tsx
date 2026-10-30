import { useProjectMode } from './codeLocalFiles'
import { useState } from 'react'
import { Cable, Server, Users } from 'lucide-react'
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
import { CrewPublishedAgents } from './CrewPublishedAgents'
import { useWorkspacePanelChatDraft } from '../../components/workspace/WorkspacePanelNavigation'
import { ProjectAgentBridgePanel } from '../../components/workspace/ProjectAgentBridgePanel'
import { useProjectProduct } from './projectProduct'
import { isWorkIntegrationTabEnabled } from './workViewGating'
import { useAuthStore } from '../../stores/useAuthStore'
import { integrationProductAvailable } from '../../components/integrations/integrationSections'

export type WorkIntegrationTab = 'apps' | 'brain' | 'folders' | 'slack' | 'whatsapp' | 'cli' | 'agents' | 'agentbridge'

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
  const [shareSection, setShareSection] = useState(false)
  const [integrationSection, setIntegrationSection] = useState<IntegrationSection>('connected')
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  const projectMode = useProjectMode(sessionId ?? '')
  useWorkspaceViewTarget('mcp', target => {
    if (target === 'share') { setShareSection(true); return }
    const next = target === 'slack-bot' ? 'slack' : target
    if (next === 'agentbridge' && !integrationProductAvailable('bullpen', allowedProducts)) return
    if (!isWorkIntegrationTabEnabled(next === 'github' ? 'secrets' : next, enabledPanels) || next === 'brain' && !integrationProductAvailable('knowledgebase', allowedProducts) || next === 'agents' && !canEdit) return
    if (['connected', 'available', 'secrets', 'gmail', 'slack-account', 'laptop', 'github'].includes(next)) { setShareSection(false); setTab('apps'); setIntegrationSection(next as IntegrationSection); return }
    if (['apps', 'brain', 'folders', 'slack', 'whatsapp', 'cli', 'agents', 'agentbridge'].includes(next)) {
      setShareSection(false); setTab(next as WorkIntegrationTab)
    }
  })
  const labels: Record<WorkIntegrationTab, string> = { apps: INTEGRATION_LABELS[integrationSection], brain: 'Brain', folders: 'Linked projects & folders', slack: 'Slack bot', whatsapp: 'WhatsApp', cli: 'MCP access', agents: 'Chats from your computer', agentbridge: 'AgentBridge' }
  if (accessLoading) return <p role="status" className="p-4 text-sm text-muted-foreground">Loading project access…</p>
  return <div className="flex h-full min-h-0 flex-col bg-background">
    <WorkspaceViewHeader icon={shareSection ? Users : tab === 'agentbridge' ? Cable : Server} title={shareSection ? 'Access' : labels[tab]} />
    <div data-ui-view-section={shareSection ? 'share' : tab === 'apps' ? integrationSection : tab} className="min-h-0 flex-1 overflow-y-auto p-4">
      {shareSection ? <ShareDashboardButton workspacePath={workspacePath} variant="panel" tabId={tabId} onAsk={draft} /> : <>
        {tab === 'agentbridge' && <ProjectAgentBridgePanel target={`workspace:${product.profileId}:${projectId}`} tabId={tabId} onAsk={draft} />}
        {tab === 'apps' && <ProjectIntegrationPanel github={projectMode === 'dev'} workspacePath={workspacePath} tabId={tabId} placeNoun="Crew" section={integrationSection} canEdit={canEdit} onAsk={draft} onAppsSaved={onAppsSaved} selectedSecrets={selectedSecrets} onSelectedSecretsChange={onSelectedSecretsChange} onSecretSelectionSaved={onAppsSaved} laptop={<CodeLocalFilesSettings sessionId={sessionId} />} />}
        {tab === 'brain' && <ProjectKnowledgebasePanel workspacePath={workspacePath} />}
        {tab === 'folders' && <WorkFoldersSection workspacePath={workspacePath} workflowContextPaths={workflowContextPaths} onWorkflowContextPathsChange={onWorkflowContextPathsChange} />}
        {tab === 'agents' && canEdit && <CrewPublishedAgents profileId={product.profileId} projectId={projectId} />}
        {tab === 'cli' && <ProjectMcpSetupPanel workspacePath={workspacePath} />}
        {tab === 'slack' && <ProductSlackPanel workspacePath={workspacePath} noun="Crew" section="bot" tabId={tabId} />}
        {tab === 'whatsapp' && <WorkflowBotsPanel workspacePath={workspacePath} fixedChannel="whatsapp" scopeNoun="project" onAsk={onAsk} target={{ profileId: product.profileId, conversationKey: projectId, label: projectTitle }} />}
      </>}
    </div>
  </div>
}
