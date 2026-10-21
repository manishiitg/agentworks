export const CHAT_TOOL_COMMAND_EVENT = 'chat-tool-command'

export type ChatToolCommand = 'browser'

export function chatToolCommandFromEvent(event: Event): ChatToolCommand | null {
  const detail = (event as CustomEvent<{ command?: unknown }>).detail
  return detail?.command === 'browser' ? 'browser' : null
}
