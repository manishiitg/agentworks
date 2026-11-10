import type { PollingEvent } from '../services/api-types'

const object = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' ? value as Record<string, unknown> : undefined
const text = (value: unknown): string => typeof value === 'string' ? value : ''

/** SDK and transcript bookends update one row, scoped to the exact native parent. */
export function coalesceNativeHelperActivity(events: PollingEvent[]): PollingEvent[] {
  const result: PollingEvent[] = []
  const helpers = new Map<string, { index: number, record: Record<string, unknown>, sdkID?: string }>()
  for (const event of events) {
    const envelope = object(event.data)
    const payload = object(envelope?.data) || envelope || {}
    const record = object(payload.record)
    if (event.type !== 'coding_agent_native_activity' || record?.kind !== 'helper') {
      result.push(event)
      continue
    }
    const keys = [text(record.task_id), text(record.tool_id)].filter(Boolean).map(id => JSON.stringify([text(record.provider), text(record.native_session_id), id]))
    const previous = keys.map(key => helpers.get(key)).find(Boolean)
    const merged = { ...record }
    if (previous) {
      // A delayed start cannot undo a completed run. A new SDK run may restart it.
      if (record.state === 'started' && ['finished', 'completed', 'failed', 'stopped', 'interrupted'].includes(text(previous.record.state)) && (record.origin !== 'sdk_helper' || record.id === previous.sdkID)) continue
      if (!text(record.name) || ['Agent', 'Task', 'Helper agent'].includes(text(record.name))) merged.name = previous.record.name
      previous.record = merged
      if (record.origin === 'sdk_helper') previous.sdkID = text(record.id)
      const original = result[previous.index]
      result[previous.index] = { ...original, data: { type: original.type, data: { ...payload, record: merged } } as PollingEvent['data'] }
      keys.forEach(key => helpers.set(key, previous))
    } else {
      const helper = { index: result.length, record: merged, sdkID: record.origin === 'sdk_helper' ? text(record.id) : undefined }
      result.push(event)
      keys.forEach(key => helpers.set(key, helper))
    }
  }
  return result
}
