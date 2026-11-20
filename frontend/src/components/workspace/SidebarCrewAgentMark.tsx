import { AgentMark, shortAgo } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import { useChatStore } from '../../stores/useChatStore'
import type { ActiveSessionInfo } from '../../services/api-types'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { hasActiveSessionWork } from '../../utils/activitySessions'

/** Inactive project rows use the same marks as the open Crew, including unread completions. */
export function ProjectAgentMark({ projectId, sessionId, session, local, activity, status }: { projectId: string; sessionId: string; session?: ActiveSessionInfo; local: boolean; activity?: 'running' | 'waiting' | ''; status?: string }) {
  const tabId = useChatStore(state => local ? Object.values(state.chatTabs).find(tab => tab.sessionId === sessionId && tab.metadata?.agentProfileProjectId === projectId)?.tabId : undefined)
  const state = useAgentMenuState(tabId || `sidebar:${projectId}:${sessionId}`, activity || undefined, local ? { sessionId } : undefined)
  const remoteState = activity === 'waiting' || runtimeNeedsUserInput(session) ? 'waiting' : activity === 'running' || session && hasActiveSessionWork(session) ? 'running' : null
  const mark = local ? state : remoteState
  // A quiet row keeps its "2m" after you switch projects, like the open project's rows (#3378).
  const ago = !mark && !status ? shortAgo(session?.last_activity) : ''
  return <>{!mark && status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}{ago && <span className="shrink-0 text-[11px] text-muted-foreground">{ago}</span>}<AgentMark state={mark} dot /></>
}

