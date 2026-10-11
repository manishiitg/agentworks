import type { PollingEvent } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { readInspectionTarget } from '../../utils/stepInspection'
import { InspectExecutionButton } from './InspectExecutionButton'

const emptyEvents: PollingEvent[] = []
function payload(event: PollingEvent): Record<string, unknown> {
 let data: unknown = event.data
 for (let depth = 0; depth < 4 && data && typeof data === 'object'; depth++) {
  const record = data as Record<string, unknown>
  if (!record.data || typeof record.data !== 'object') return record
  data = record.data
 }
 return data && typeof data === 'object' ? data as Record<string, unknown> : {}
}

// The compact execute_step row represents its launch TOOL call. Resolve its
// explicit server-issued receipt, never a guessed step name/latest run match.
export function RunInspectionButton({event}: {event: PollingEvent}) {
 const tab = useChatStore(state => state.activeTabId ? state.chatTabs[state.activeTabId] : undefined)
 const sessionId = tab?.sessionId
 const events = useChatStore(state => sessionId ? state.tabEvents[sessionId] ?? emptyEvents : emptyEvents)
 const started = payload(event)
 const callId = started.tool_call_id
 if (!sessionId || !callId || (event.session_id && event.session_id !== sessionId)) return null
 const end = events.find(candidate => candidate.type === 'tool_call_end' && payload(candidate).tool_call_id === callId)
 if (!end) return null
 const result = payload(end).result
 const executionId = typeof result === 'string' ? result.match(/execution_id:\s*"([^"\n]+)"/)?.[1] : undefined
 if (!executionId) return null
 for (const candidate of events) {
  if (candidate.type !== 'background_agent_started' && candidate.type !== 'background_agent_completed') continue
  const fields = payload(candidate)
  const metadata = fields.metadata as Record<string, unknown> | undefined
  const target = readInspectionTarget(metadata?.inspection)
  if (target?.execution_id === executionId && target.session_id === sessionId) return <InspectExecutionButton target={target} />
 }
 return null
}
