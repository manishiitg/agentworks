import { shortAgo } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import type { ActiveSessionInfo } from '../../services/api-types'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { hasActiveSessionWork } from '../../utils/activitySessions'
import { AgentRowLine, agentRowThemeProps } from './SidebarAgentRowLine'

/** A project's agent row when that project is not the open one: the same row as the open project's, with its
 * marks and last-active time read from the session the app already holds (#3378, #3382). */
export function ProjectAgentRow({ projectId, sessionId, session, local, activity, status, onOpen, ...line }: {
  projectId: string; sessionId: string; session?: ActiveSessionInfo; local: boolean; activity?: 'running' | 'waiting' | ''; status?: string; onOpen: () => void
} & Pick<Parameters<typeof AgentRowLine>[0], 'name' | 'person' | 'inTeam' | 'setup' | 'place'>) {
  const tabId = useChatStore(state => local ? Object.values(state.chatTabs).find(tab => tab.sessionId === sessionId && tab.metadata?.agentProfileProjectId === projectId)?.tabId : undefined)
  const state = useAgentMenuState(tabId || `sidebar:${projectId}:${sessionId}`, activity || undefined, local ? { sessionId } : undefined)
  const remoteState = activity === 'waiting' || runtimeNeedsUserInput(session) ? 'waiting' : activity === 'running' || session && hasActiveSessionWork(session) ? 'running' : null
  const mark = local ? state : remoteState
  const shownStatus = mark ? undefined : status
  const ago = !mark && !status ? shortAgo(session?.last_activity) : ''
  return <div {...agentRowThemeProps(mark, false)} className="group relative rounded-md">
    <AgentRowLine {...line} status={shownStatus} ago={ago} state={mark} onClick={onOpen} />
  </div>
}
