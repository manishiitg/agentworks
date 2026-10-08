import { UserRoundCog } from 'lucide-react'

/** Pulse's icon everywhere: a person at work, the agent that owns the goal
 * (owner, 2026-10-08: an expert, not a heartbeat). */
export const PulseIcon = UserRoundCog

/** A workflow's Pulse conversation (goal_lead_conversation.go session ids). */
export function isPulseConversationSession(sessionId: string | null | undefined): boolean {
  return (sessionId || '').trim().startsWith('schedule-goallead--')
}
