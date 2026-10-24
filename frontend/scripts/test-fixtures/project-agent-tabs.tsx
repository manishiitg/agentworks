import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import { ProjectAgentTabs } from '../../src/components/chat/ProjectAgentTabs'
import { useChatStore, type ChatTab } from '../../src/stores/useChatStore'
import { useProductSurfaceStore } from '../../src/stores/useProductSurfaceStore'
import type { ProductSurface } from '../../src/products/productSurfaceConfig'

const params = new URLSearchParams(location.search)
const surface = (params.get('surface') || 'agentworks') as ProductSurface
document.documentElement.classList.toggle('dark', params.get('theme') !== 'light')
useProductSurfaceStore.setState({ productSurface: surface })
const names = ['Builder', 'Research', 'Review', 'Writing', 'Support', 'Other Builder']
const initial = names.map((name, index) => ({ conversation_key: 'chat-' + index, conversation_id: 'identity-' + index,
  session_id: 'session-' + index, name, pinned: true, can_write: index < 5, tab_owner_id: index < 5 ? 'me' : 'other', tab_owner_name: index < 5 ? 'me@example.com' : 'alex.taylor@example.com' }))
const makeTab = (row: typeof initial[number]) => ({ tabId: row.conversation_key, sessionId: row.session_id, name: row.name, createdAt: 1, isStreaming: false, config: {}, metadata: {
  agentProfileConversationKey: row.conversation_key, agentProfileTabOwnerID: row.tab_owner_id, agentProfileTabOwnerName: row.tab_owner_name, agentProfileTabCanWrite: row.can_write,
} }) as ChatTab
useChatStore.setState({ chatTabs: Object.fromEntries(initial.map(row => [row.conversation_key, makeTab(row)])), activeTabId: 'chat-0', activeSessionsCache: [], tabEvents: {} })
function Fixture() {
  const [rows, setRows] = useState(initial)
  const [calls, setCalls] = useState<unknown[]>([])
  const tabs = useChatStore(state => state.chatTabs)
  const record = (action: string, data: unknown) => setCalls(previous => [...previous, { action, data }])
  return <main className="min-h-screen bg-background p-3 text-foreground"><header className="flex w-full border-b border-border pb-2">
    <ProjectAgentTabs tabs={rows.map(row => tabs[row.conversation_key])} listed={rows.slice(1)} canonicalTabId="chat-0" projectName="Example" surface={surface} allowSideChats opening={false}
      onSelectTab={id => useChatStore.getState().switchTab(id)}
      onOpen={async name => { record('open', name); const row = { ...initial[1], conversation_key: 'created', session_id: 'created-session', name }; useChatStore.setState(state => ({ chatTabs: { ...state.chatTabs, created: makeTab(row) }, activeTabId: 'created' })); setRows(previous => [...previous, row]) }}
      onRename={async (tab, name) => { record('rename', name); useChatStore.getState().renameTab(tab.tabId, name); setRows(previous => previous.map(row => row.conversation_key === tab.tabId ? { ...row, name } : row)) }}
      onPin={async (key, pinned) => { record('pin', { key, pinned }); setRows(previous => previous.map(row => row.conversation_key === key ? { ...row, pinned } : row)) }}
      onOrder={async keys => { record('order', keys); setRows(previous => [previous[0], ...keys.map(key => previous.find(row => row.conversation_key === key)!), ...previous.slice(1).filter(row => !keys.includes(row.conversation_key))]) }}
      onCloseTab={id => { record('close', id); setRows(previous => previous.filter(row => row.conversation_key !== id)) }} />
  </header><p className="mt-8 text-sm text-muted-foreground">Shared project tab controls</p><output data-testid="calls" className="sr-only">{JSON.stringify(calls)}</output></main>
}
createRoot(document.getElementById('root')!).render(<Fixture />)
