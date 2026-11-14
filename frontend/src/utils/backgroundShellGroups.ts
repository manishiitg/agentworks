import type { PollingEvent } from '../services/api-types'
import type { TranscriptItem } from './terminalEventTranscript'

export type BackgroundShellGroup = { kind: 'shell_group'; key: string; rows: TranscriptItem[]; events: PollingEvent[]; count: number }
const object = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {}
const fields = (event: PollingEvent) => { const outer = object(event.data); return outer.data ? object(outer.data) : outer }
const identity = (event: PollingEvent, id: string) => JSON.stringify([event.session_id, id])

export function endedBackgroundShell(event: PollingEvent): Record<string, unknown> | undefined {
  const record = object(fields(event).record)
  if (event.type === 'coding_agent_native_activity' && record.kind === 'background_command' && typeof record.work_id === 'string' && record.work_id && ['finished', 'completed', 'failed', 'stopped', 'interrupted'].includes(String(record.state))) return record
}

/** Absorb only adjacent, explicitly linked shell tool rows. Never drop their details. */
export function groupBackgroundShells<T extends { kind: string; key: string; event?: PollingEvent; events?: PollingEvent[]; toolCount?: number }>(items: T[], protectedKey?: string): (T | BackgroundShellGroup)[] {
  const out: (T | BackgroundShellGroup)[] = []
  let block: T[] = []
  const flush = () => {
    let run: T[] = [], count = new Set<string>()
    const emit = () => {
      if (count.size && run.length > 1) out.push({ kind: 'shell_group', key: run[0].key, events: run.flatMap(row => row.event ? [row.event] : row.events || []), rows: run.map<TranscriptItem>(row => row.event
        ? { kind: 'event', key: row.key, event: row.event }
        : { kind: 'tools', key: row.key, events: row.events!, toolCount: row.toolCount! }), count: count.size })
      else out.push(...run)
      run = []; count = new Set()
    }
    for (const [index, row] of block.entries()) {
      const record = row.event && endedBackgroundShell(row.event)
      if (record) {
        run.push(row); count.add(identity(row.event!, String(record.work_id)))
      } else if (row.kind === 'tools' && row.events?.length) {
        // Only neighboring shell records can own this wrapper. A distant
        // matching ID across an unrelated step must not absorb the step.
        const tools = new Set<string>()
        for (const direction of [-1, 1]) {
          for (let next = index + direction; next >= 0 && next < block.length; next += direction) {
            const adjacent = block[next].event
            const shell = adjacent && endedBackgroundShell(adjacent)
            if (!shell) break
            if (typeof shell.tool_id === 'string' && shell.tool_id) tools.add(identity(adjacent!, shell.tool_id))
          }
        }
        if (row.events.every(event => {
          const id = fields(event).tool_call_id
          return typeof id === 'string' && id && tools.has(identity(event, id))
        })) run.push(row)
        else { emit(); out.push(row) }
      }
      else { emit(); out.push(row) }
    }
    emit(); block = []
  }
  for (const row of items) {
    const protectedRow = Boolean(protectedKey && (row.key === protectedKey || row.event?.id === protectedKey || row.events?.some(event => event.id === protectedKey)))
    if (!protectedRow && (row.kind === 'tools' || (row.kind === 'event' && row.event && endedBackgroundShell(row.event)))) {
      const session = row.event?.session_id || row.events?.[0]?.session_id
      const first = block[0]
      if (first && session !== (first.event?.session_id || first.events?.[0]?.session_id)) flush()
      block.push(row)
    }
    else { flush(); out.push(row) }
  }
  flush()
  return out
}
