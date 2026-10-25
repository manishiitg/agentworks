import { useState } from 'react'
import { Cable, Server, Users, Vault } from 'lucide-react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { ProjectVaultControls, type ProjectSecretControls, type VaultSection } from '../../components/integrations/ProjectVaultControls'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { useChatStore } from '../../stores/useChatStore'
import { MyVaultPanel } from '../../components/integrations/MyVaultPanel'
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

export type WorkIntegrationTab = 'vault' | 'brain' | 'folders' | 'slack' | 'whatsapp' | 'cli' | 'agents' | 'agentbridge'

export function WorkIntegrationsPanel({ accessLoading = false, selectedSecrets = [], selectedGlobalSecrets = [], onSelectedSecretsChange = () => {}, onSelectedGlobalSecretsChange = () => {}, canEdit = false, workspacePath, projectId, projectTitle, tabId, enabledPanels, onAsk, workflowContextPaths, onWorkflowContextPathsChange }: {
  accessLoading?: boolean
  selectedSecrets?: string[]; selectedGlobalSecrets?: string[]
  onSelectedSecretsChange?: ProjectSecretControls['onSelectedSecretsChange']; onSelectedGlobalSecretsChange?: ProjectSecretControls['onSelectedGlobalSecretsChange']
  canEdit?: boolean; workspacePath: string; projectId: string; projectTitle: string
  tabId: string; enabledPanels?: Set<string>
  onAsk: (message: string) => Promise<void>
  workflowContextPaths: string[]; onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>
}) {
  const draft = useWorkspacePanelChatDraft(tabId)
  const product = useProjectProduct()
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const [tab, setTab] = useState<WorkIntegrationTab>('vault')
  const [shareSection, setShareSection] = useState(false)
  const [vaultSection, setVaultSection] = useState<VaultSection>('vault')
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? undefined)
  useWorkspaceViewTarget('mcp', target => {
    if (target === 'share') { setShareSection(true); return }
    const next = target === 'slack-bot' ? 'slack' : target
    if (next === 'agentbridge' && !integrationProductAvailable('bullpen', allowedProducts)) return
    if (!isWorkIntegrationTabEnabled(next, enabledPanels) || next === 'brain' && !integrationProductAvailable('knowledgebase', allowedProducts) || next === 'agents' && !canEdit) return
    if (['vault', 'connected', 'available', 'secrets', 'gmail', 'slack-account', 'laptop'].includes(next)) { setShareSection(false); setTab('vault'); setVaultSection(next as VaultSection); return }
    if (['vault', 'brain', 'folders', 'slack', 'whatsapp', 'cli', 'agents', 'agentbridge'].includes(next)) {
      setShareSection(false); setTab(next as WorkIntegrationTab)
    }
  })
  const labels: Record<WorkIntegrationTab, string> = { vault: 'My Vault', brain: 'Brain', folders: 'Linked projects & folders', slack: 'Slack bot', whatsapp: 'WhatsApp', cli: 'MCP access', agents: 'Chats from your computer', agentbridge: 'AgentBridge' }
  if (accessLoading) return <p role="status" className="p-4 text-sm text-muted-foreground">Loading project access…</p>
  return <div className="flex h-full min-h-0 flex-col bg-background">
    <WorkspaceViewHeader icon={shareSection ? Users : tab === 'agentbridge' ? Cable : tab === 'vault' ? Vault : Server} title={shareSection ? 'Access' : labels[tab]} />
    <div data-ui-view-section={shareSection ? 'share' : tab} className="min-h-0 flex-1 overflow-y-auto p-4">
      {shareSection ? <ShareDashboardButton workspacePath={workspacePath} variant="panel" tabId={tabId} onAsk={draft} /> : <>
        {tab === 'agentbridge' && <ProjectAgentBridgePanel target={`workspace:${product.profileId}:${projectId}`} tabId={tabId} onAsk={draft} />}
        {tab === 'vault' && <MyVaultPanel workspacePath={workspacePath} tabId={tabId} disabled={!canEdit} projectControls={<ProjectVaultControls workspacePath={workspacePath} placeNoun="Crew" section={vaultSection} canEdit={canEdit} onAsk={draft} selectedSecrets={selectedSecrets} selectedGlobalSecrets={selectedGlobalSecrets} onSelectedSecretsChange={onSelectedSecretsChange} onSelectedGlobalSecretsChange={onSelectedGlobalSecretsChange} laptop={<CodeLocalFilesSettings sessionId={sessionId} />} />} />}
        {tab === 'brain' && <ProjectKnowledgebasePanel workspacePath={workspacePath} onAsk={onAsk} />}
        {tab === 'folders' && <WorkFoldersSection workspacePath={workspacePath} workflowContextPaths={workflowContextPaths} onWorkflowContextPathsChange={onWorkflowContextPathsChange} onAsk={onAsk} askMessage="Help me choose the projects and folders this Crew uses." />}
        {tab === 'agents' && canEdit && <CrewPublishedAgents profileId={product.profileId} projectId={projectId} />}
        {tab === 'cli' && <ProjectMcpSetupPanel workspacePath={workspacePath} />}
        {tab === 'slack' && <ProductSlackPanel workspacePath={workspacePath} noun="Crew" section="bot" />}
        {tab === 'whatsapp' && <WorkflowBotsPanel workspacePath={workspacePath} fixedChannel="whatsapp" scopeNoun="project" onAsk={onAsk} target={{ profileId: product.profileId, conversationKey: projectId, label: projectTitle }} />}
      </>}
    </div>
  </div>
}
