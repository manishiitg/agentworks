import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import { AppShell } from '../../src/components/workspace/AppShell'
import { ProductWorkspaceShell } from '../../src/components/workspace/ProductWorkspaceShell'
import { ProductTopBar } from '../../src/components/workspace/ProductTopBar'
import { ProjectAgentTabs } from '../../src/components/chat/ProjectAgentTabs'
import { GlobalActivityMonitor } from '../../src/components/GlobalActivityMonitor'
import { useChatStore, type ChatTab } from '../../src/stores/useChatStore'
import type { ActiveSessionInfo, PollingEvent } from '../../src/services/api-types'
import type { ProductSurface } from '../../src/products/productSurfaceConfig'

const rows = ['Builder', 'Research'].map((name, index) => ({
  conversation_key: `chat-${index}`, session_id: `session-${index}`, name, pinned: index === 0, can_write: true,
}))
const tabs = rows.map(row => ({ tabId: row.conversation_key, sessionId: row.session_id, name: row.name,
  createdAt: 1, isStreaming: false, config: {}, metadata: { agentProfileConversationKey: row.conversation_key },
})) as ChatTab[]
const sessions = [1, 2].map(index => ({ session_id: index === 1 ? 'session-1' : `other-${index}`, agent_mode: 'multi-agent',
  agent_profile_id: 'work', workspace_path: `Crew/example-${index}`, status: 'running', display_status: 'busy',
  title: `Example ${index}`, created_at: new Date().toISOString(), last_activity: new Date().toISOString(),
})) as ActiveSessionInfo[]
// A fixture supplies admitted records only; no live accounts, agent turns or server writes.
useChatStore.setState({ chatTabs: Object.fromEntries(tabs.map(tab => [tab.tabId, tab])), activeTabId: 'chat-0',
  activeSessionsCache: sessions, getActiveSessions: async () => sessions, tabEvents: {},
})
const surface = (new URLSearchParams(location.search).get('surface') || 'work') as ProductSurface
for (const [name, type] of [['qa:complete-research', 'agent_end'], ['qa:start-research', 'user_message']]) {
  window.addEventListener(name, () => {
    const event = { id: name, type, timestamp: new Date().toISOString(), data: { type, data: {} } } as PollingEvent
    useChatStore.setState({ tabEvents: { 'session-1': [event] } })
  })
}
function Fixture() {
  const [pane, setPane] = useState<'chat' | 'workspace'>('chat')
  return <AppShell><div className="h-full min-h-0">
    <ProductTopBar><GlobalActivityMonitor /></ProductTopBar>
    <ProductWorkspaceShell chatOpen panelOpen splitRatio={.5} mobilePane={pane} onMobilePaneChange={setPane}
      onOpenChat={() => setPane('chat')} onOpenWorkspace={() => setPane('workspace')}
      tabs={<ProjectAgentTabs tabs={tabs} listed={rows.slice(1)} canonicalTabId="chat-0" projectName="Project files" surface={surface} allowSideChats opening={false}
        onSelectTab={id => useChatStore.getState().switchTab(id)} onOpen={async () => false} onRename={async () => false}
        onPin={async () => {}} onOrder={async () => {}} onCloseTab={() => {}} />}
      toolbar={null} divider={null} workspace={<p className="p-4">Workspace content</p>}
      chat={<><div className="min-h-0 flex-1 overflow-auto p-4"><h1 className="text-lg">Your work</h1>{Array.from({ length: 15 }, (_, index) => <p key={index} className="my-4">An example conversation with enough content to scroll.</p>)}</div>
        <div data-tour="chat-input-area" className="shrink-0 p-3"><div className="rounded-xl border border-border p-3"><textarea aria-label="Message" placeholder="Ask this Crew to help with your work…" className="block w-full bg-transparent" /><button type="button" className="h-7">Send</button></div></div></>} />
  </div></AppShell>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
