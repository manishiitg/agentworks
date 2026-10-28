const CONTINUITY_MARKER = '[AGENTWORKS CONVERSATION CONTINUITY]'

export function isConversationContinuityNotice(content: string): boolean {
  return content.trimStart().startsWith(CONTINUITY_MARKER)
}

// The server restarts a coding CLI when its tools change (a laptop connecting or dropping) and tells the agent where
// the conversation archive is. That is internal: the person's chat continues, so nothing is shown (owner, 2026-10-28:
// "why did it show conversation reloaded in between of chat").
export function ConversationContinuityNotice(_props: { content: string; timestamp?: string }) {
  return null
}
