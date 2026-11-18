import { useState } from 'react'
import { Cloud } from 'lucide-react'
import { Avatar } from '../../components/ui/Avatar'
import { useChatStore } from '../../stores/useChatStore'
import { openTeamAgentChat } from './openTeamAgentChat'
import { teamAgentChats, type TeamAgentChat } from './teamApi'

/**
 * The header of a chat with the Team agent (#3084): "My chat / Everyone's chats". Everyone's chats lists the Team
 * agent's chats the server lets this person read (every current Team member reads them all); opening one shows it in
 * this same chat screen, written only by its person.
 */
export function TeamAgentChatBar({ tabId, projectId }: { tabId: string; projectId: string }) {
  const meta = useChatStore(state => state.chatTabs[tabId]?.metadata)
  const chat = meta?.teamAgentChat
  const [list, setList] = useState<TeamAgentChat[]>()
  if (!chat) return null
  const target = { project: chat.project, connection: chat.connection }
  const info = { program: chat.program, canChangeProgram: chat.canChangeProgram }
  const showAll = () => {
    if (list) { setList(undefined); return }
    teamAgentChats(target).then(setList).catch(error => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not load the Team agent\'s chats. Try again.', 'error'))
  }
  const tab = (on: boolean) => `rounded-lg px-3 py-1 text-xs ${on ? 'bg-background text-foreground shadow-sm ring-1 ring-border' : 'text-muted-foreground hover:bg-muted'}`
  return <div className="relative shrink-0 border-b border-border">
    <div className="flex items-center gap-2 px-4 py-2">
      <Cloud aria-hidden="true" className="h-4 w-4 text-sky-500" /><span className="text-sm font-semibold">Team agent</span>
      <span className="text-xs text-emerald-600 dark:text-emerald-400">● Always online</span>
      <div role="tablist" aria-label="Team agent chats" className="ml-auto grid grid-cols-2 gap-1 rounded-xl border border-border bg-muted/30 p-1">
        <button type="button" role="tab" aria-selected={chat.mine && !list} onClick={() => { setList(undefined); if (!chat.mine) void openTeamAgentChat(projectId, target, undefined, info) }} className={tab(chat.mine && !list)}><span className="font-semibold">My chat</span></button>
        <button type="button" role="tab" aria-selected={!chat.mine || !!list} onClick={showAll} className={tab(!chat.mine || !!list)}><span className="font-semibold">Everyone's chats{list ? ` (${list.length})` : ''}</span></button>
      </div>
    </div>
    {list && <div role="list" aria-label="Everyone's chats with the Team agent" className="absolute inset-x-0 top-full z-20 max-h-80 space-y-1 overflow-y-auto border-b border-border bg-background px-4 py-2 shadow-sm">
      {list.map(row => <button key={row.session_id} type="button" role="listitem" onClick={() => { setList(undefined); void openTeamAgentChat(projectId, target, row, info) }} className="flex w-full min-w-0 items-center gap-3 rounded-xl border border-border px-3.5 py-2.5 text-left text-sm hover:bg-muted/50">
        <Avatar name={row.you ? 'You' : row.person} className="h-7 w-7 text-xs" />
        <span className="min-w-0 flex-1"><span className="block font-medium">{row.you ? 'You' : row.person}</span><span className="block truncate text-xs text-muted-foreground">{row.activity === 'working' ? 'working…' : row.last_message}</span></span>
      </button>)}
      {!list.length && <p className="py-2 text-xs text-muted-foreground">No one has asked the Team agent anything yet.</p>}
    </div>}
  </div>
}
