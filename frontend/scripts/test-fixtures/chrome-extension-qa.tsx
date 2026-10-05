import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserWorkspacePanel } from '../../src/components/workflow/BrowserWorkspacePanel'
import { TooltipProvider } from '../../src/components/ui/tooltip'
import type { BrowserAutomationMode } from '../../src/components/BrowserAutomationSettings'
import { useChromeExtensionChatNotifications } from '../../src/hooks/useChromeExtensionChatNotifications'
import { useChatStore, type ChatTab } from '../../src/stores/useChatStore'
import '../../src/index.css'
const params = new URLSearchParams(location.search)
const profile = params.get('profile') || 'code'
if (params.has('brand')) Object.assign(window, {__APP_RUNTIME_CONFIG__:{appName:params.get('brand')}})
const workspace = profile === 'code' ? '_users/alice/Chats/Code/projects/customer-portal' : profile === 'work' ? 'Chats/Work/projects/customer-portal' : 'Workflow/customer-portal'
document.documentElement.classList.toggle('dark', params.get('theme') !== 'light')
const notifications = params.has('notifications')
if (notifications) useChatStore.setState({activeTabId:'fixture-code-chat', chatTabs:{'fixture-code-chat': {
  tabId:'fixture-code-chat', name:'Code connection QA', sessionId:'fixture-code-session', isStreaming:true,
  metadata:{mode:'multi-agent', agentProfileId:profile, agentProfileWorkspace:workspace},
  config:{queuedMessages:[], inputText:'Unsent draft', mcpOAuthNotificationIDs:[]},
} as unknown as ChatTab}})
function Notifications() {
  useChromeExtensionChatNotifications('fixture-code-chat')
  const messages = useChatStore(state => state.chatTabs['fixture-code-chat']?.config.queuedMessages)
  return <ol aria-label="Automatic chat notifications">{messages?.map((message, index) => <li key={index}>{message}</li>)}</ol>
}
function Fixture() {
  const [mode, setMode] = useState<BrowserAutomationMode>('auto')
  return <TooltipProvider>{notifications && <Notifications />}<main style={{height:"min(760px, 100vh)", maxWidth:860}} className="mx-auto flex flex-col border border-border bg-background text-foreground"><BrowserWorkspacePanel workspacePath={workspace} profileId={profile} scopeNoun="project" browserMode={mode} onBrowserModeChange={setMode} cdpPort={9222} onCdpPortChange={() => {}} cdpConnected={null} cdpError={null} cdpChecking={false} onCheckCdpConnection={() => {}} onSave={() => {}} dirty={mode !== 'auto'} /></main></TooltipProvider>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
