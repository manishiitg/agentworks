// A message this chat sent to another agent (the send_message tool), read back from its tool-call event so the chat can
// show "Message sent to <agent>" the way it shows a message it receives, instead of an anonymous "1 tool call".
//
// Pure functions only: the same read feeds the live chat and restored history.
import { formatToolCallResult } from './toolCallFormatting'

export interface AgentMessageSendView {
  /** Who it was addressed to, as the agent wrote it, without the "agent:" prefix; '' for a reply to an inbox. */
  target: string
  /** The recipient's own name for itself when the server gave one (a Crew title); the row prefers it over the raw id. */
  targetLabel: string
  message: string
  /** The server accepted it. Delivery to the other agent is reported separately by that agent's own answer. */
  accepted: boolean
  failed: boolean
  error: string
  state: string
}

function parseRecord(text: string | undefined): Record<string, unknown> | null {
  if (!text || !text.trim()) return null
  try {
    const parsed: unknown = JSON.parse(text)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : null
  } catch {
    return null
  }
}

function str(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function baseToolName(raw: string): string {
  const match = /^mcp__.+?__(.+)$/.exec(raw.trim())
  return (match ? match[1] : raw.trim()).toLowerCase()
}

// ask_project_chat is the same send, to another tab of this Crew (owner, 2026-10-28: the Builder's message to a tab
// showed only as "1 tool call").
export function isAgentMessageSendToolCall(name: string): boolean {
  const base = baseToolName(name || '')
  return ['send_message', 'ask_project_chat', 'messages', 'sessions', 'call_tool'].includes(base)
}

export function parseAgentMessageSend(name: string, args: string | undefined, result: string | undefined, status?: string): AgentMessageSendView | null {
  if (!isAgentMessageSendToolCall(name)) return null
  let input = parseRecord(args) ?? {}
  let tool = baseToolName(name)
  if (tool === 'call_tool') {
    tool = baseToolName(str(input.tool_name))
    input = input.arguments && typeof input.arguments === 'object' ? input.arguments as Record<string, unknown> : {}
  }
  if (!['send_message', 'ask_project_chat', 'messages', 'sessions'].includes(tool)) return null
  if (tool === 'messages' && input.action !== 'send' || tool === 'sessions' && input.action !== 'ask') return null
  const message = str(input.message)
  if (!message) return null
  const target = (str(input.target) || str(input.chat)).replace(/^agent:/i, '')
  const formatted = result ? formatToolCallResult(result) : null
  const raw = parseRecord(formatted?.text) ?? parseRecord(result)
  const answer = raw?.result && typeof raw.result === 'object' ? raw.result as Record<string, unknown> : raw
  const failed = status === 'error' || Boolean(formatted?.isError) || answer?.accepted === false
  const targetLabel = str((answer?.result as Record<string, unknown> | undefined)?.to_label) || str(answer?.to_label)
  const accepted = answer?.accepted === true
  const receipt = str(answer?.state) || str(answer?.delivery)
  const state = failed ? 'Couldn’t send' : receipt === 'delivered' ? 'Received' : accepted ? 'Sent' : status === 'running' ? 'Sending…' : 'Delivery unconfirmed'
  return { target, targetLabel, message, accepted, failed, state, error: failed ? (formatted?.text ?? '').trim() : '' }
}
