import type { ExecutionInspectionResponse } from '../services/api-types'

/** Reuse ConversationViewer's existing message/tool rendering contract. */
export function inspectionConversation(response: ExecutionInspectionResponse, toolsOnly = false): string {
 const messages: Array<{Role: string; Parts: Array<Record<string, unknown>>}> = []
 const tools = new Map<string, { index: number; resultIndex: number; name: string; arguments: string; result?: string; error?: string; status: string }>()
 response.transcript.events.forEach((event, index) => {
  if (event.tool_call) {
   const call = event.tool_call
   const key = call.tool_call_id || `${call.tool_name}:${index}`
   const previous = tools.get(key)
   tools.set(key, { index: previous?.index ?? index, resultIndex: index, name: call.tool_name || previous?.name || 'Tool', arguments: call.arguments ?? previous?.arguments ?? '', result: call.result ?? previous?.result, error: call.error ?? previous?.error, status: call.status })
  } else if (!toolsOnly && event.text) {
   const role = event.role === 'system' ? 'system' : event.role === 'assistant' || event.type === 'assistant_message' ? 'ai' : 'human'
   // A provider may emit both conversation_start and system_prompt with the same prompt.
   if (role === 'system' && messages.some(message => message.Role === role && message.Parts[0]?.Text === event.text)) return
   messages.push({Role: role, Parts: [{Text: event.text, inspectionIndex: index}]})
  }
 })
 tools.forEach((tool, key) => {
  messages.push({Role: 'ai', Parts: [{ID: key, FunctionCall: {Name: tool.name, Arguments: tool.arguments}, inspectionIndex: tool.index}]})
  messages.push({Role: 'tool', Parts: [{ToolCallID: key, Name: tool.name, Content: tool.error || tool.result || (tool.status === 'running' ? 'Running…' : 'No result recorded'), inspectionIndex: tool.resultIndex + 0.1}]})
 })
 messages.sort((a, b) => Number(a.Parts[0].inspectionIndex ?? -1) - Number(b.Parts[0].inspectionIndex ?? -1))
 return JSON.stringify({conversation_history: messages})
}
