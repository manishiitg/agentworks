import { AgentMark } from '../chat/AgentIcon'
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
  return <>{!mark && status && <span className="shrink-0 text-[10px] text-muted-foreground">{status}</span>}<AgentMark state={mark} /></>
}

