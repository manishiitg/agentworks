import type { PollingEvent } from '../services/api-types'

type RuntimeEventScope = {
  kind: 'session' | 'delegation' | 'workshop'
  id?: string
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' ? value as Record<string, unknown> : undefined
}

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === 'string' && value.trim()) return value.trim()
  }
  return undefined
}

function isRootLikeExecutionId(value?: string): boolean {
  return !value || value.startsWith('main:') || value.startsWith('session:')
}

export function getEventPayloadParts(event: PollingEvent) {
  const eventRecord = event as unknown as Record<string, unknown>
  const agentEvent = asRecord(event.data)
  const innerData = asRecord(agentEvent?.data)
  const metadata = asRecord(innerData?.metadata) || asRecord(agentEvent?.metadata)
  return { eventRecord, agentEvent, innerData, metadata }
}

export function getRuntimeEventScope(event: PollingEvent): RuntimeEventScope {
  const { eventRecord, agentEvent, innerData, metadata } = getEventPayloadParts(event)
  const component = firstString(eventRecord.component, innerData?.component, agentEvent?.component)
  const correlationId = firstString(
    eventRecord.correlation_id,
    innerData?.correlation_id,
    agentEvent?.correlation_id,
    metadata?.correlation_id
  )
  const delegationId = firstString(innerData?.delegation_id, agentEvent?.delegation_id, metadata?.delegation_id)
  const workshopStepId = firstString(metadata?.workshop_step_id, innerData?.workshop_step_id, agentEvent?.workshop_step_id)
  const executionId = firstString(eventRecord.execution_id)
  const parentExecutionId = firstString(
    eventRecord.parent_execution_id,
    metadata?.parent_execution_id,
    innerData?.parent_execution_id,
    agentEvent?.parent_execution_id
  )
  const backgroundAgentId = firstString(
    innerData?.background_agent_id,
    agentEvent?.background_agent_id,
    innerData?.agent_id,
    agentEvent?.agent_id
  )
  const executionKind = firstString(eventRecord.execution_kind)

  if (component?.startsWith('delegation-')) return { kind: 'delegation', id: component }
  if (delegationId?.startsWith('delegation-')) return { kind: 'delegation', id: delegationId }
  if (correlationId?.startsWith('delegation-')) return { kind: 'delegation', id: correlationId }
  if ((executionKind === 'workflow_step' || executionId?.startsWith('workflow-step:')) && !isRootLikeExecutionId(executionId)) {
    return { kind: 'workshop', id: executionId }
  }
  if (!isRootLikeExecutionId(parentExecutionId)) return { kind: 'workshop', id: parentExecutionId }
  if (!isRootLikeExecutionId(backgroundAgentId)) return { kind: 'workshop', id: backgroundAgentId }
  if (correlationId?.startsWith('workshop-')) return { kind: 'workshop', id: correlationId }
  if (workshopStepId?.startsWith('workshop-')) return { kind: 'workshop', id: workshopStepId }

  return { kind: 'session' }
}
