export interface WorkflowSessionIdentity {
  sessionId?: string | null
  triggeredBy?: string | null
  botPlatform?: string | null
  parentSessionId?: string | null
  sessionKind?: string | null
}

/** Internal child sessions remain observable under their owning execution tree,
 * but must never restore or compete as independent top-level chats. */
export function isInternalChildSession(identity: WorkflowSessionIdentity): boolean {
  return Boolean((identity.parentSessionId || '').trim())
}

/**
 * Scheduled sessions are an independent read-only lane. This applies to both
 * workflow schedules and generic scheduled agent sessions.
 */
export function isScheduledSession(identity: WorkflowSessionIdentity): boolean {
  const trigger = (identity.triggeredBy || '').toLowerCase()
  const sessionId = (identity.sessionId || '').toLowerCase()

  return trigger.includes('schedule') ||
    trigger === 'cron' ||
    trigger === 'webhook' ||
    sessionId.startsWith('schedule-') ||
    sessionId.includes('-schedule-')
}

/**
 * Schedule and bot runs are independent, read-only workflow lanes. They may run
 * alongside the workflow's single interactive builder chat.
 */
export function isExternalReadOnlyWorkflowSession(identity: WorkflowSessionIdentity): boolean {
  const trigger = (identity.triggeredBy || '').toLowerCase()
  const sessionId = (identity.sessionId || '').toLowerCase()
  const botPlatform = (identity.botPlatform || '').toLowerCase()

  return isScheduledSession(identity) ||
    trigger.includes('bot') ||
    trigger.includes('whatsapp') ||
    trigger.includes('slack') ||
    botPlatform !== '' ||
    sessionId.startsWith('bot-')
}

/** Background activity is visible in the monitor. Discovery may refresh an
 * already-open run, but only an explicit user action may open its chat tab. */
export function shouldDiscoverWorkflowChatTab(identity: WorkflowSessionIdentity, hasOpenTab: boolean): boolean {
  return hasOpenTab || !isExternalReadOnlyWorkflowSession(identity)
}

/** Prefer the durable session identity for older webhook sessions stamped cron. */
export function workflowTriggerLabel(identity: WorkflowSessionIdentity): 'Webhook' | 'Scheduled' | 'Manual' | undefined {
  const id = (identity.sessionId || '').toLowerCase()
  const trigger = (identity.triggeredBy || '').toLowerCase()
  if (id.startsWith('schedule-webhook--') || trigger === 'webhook') return 'Webhook'
  if (id.startsWith('schedule-manual--') || trigger === 'manual') return 'Manual'
  if (isScheduledSession(identity)) return 'Scheduled'
  return undefined
}
