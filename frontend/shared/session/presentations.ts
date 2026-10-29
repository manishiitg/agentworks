// Presentation parsing shared by every product surface: the live
// presentation_updated event, and the rows a workspace's ui_presentations
// table holds. Loading rows and the React hook stay app-side.
import type { PollingEvent } from './types'
import { getTypedEventData } from '../../src/generated/event-types'

export const PRESENTATION_UPDATED_EVENT_TYPE = 'presentation_updated'

export type PresentationUpdatedEvent = {
  presentationId: string
  kind: string
  title: string
  workspacePath: string
  payload: Record<string, unknown>
  /** When the server emitted it (ms, 0 when unknown). */
  at: number
  activity: {
    label: string
    destination: string
    detail: string
  }
}

function asString(value: string | undefined): string {
  return typeof value === 'string' ? value : ''
}

// PresentationUpdatedEvent is a real registered event type (cmd/schema-gen ->
// EventDataUnion/EventRegistry/UnifiedEvent -> generated/events-bridge.ts),
// backed by pkg/orchestrator/events.PresentationUpdatedEvent on the Go side.
// getTypedEventData narrows PollingEvent to it and reads event.data.data at
// the depth every other typed event uses -- no defensive unwrapping, because
// the depth is no longer a guess: it is what a real EventData value
// serializes to, confirmed against a live emission.
//
// An earlier version of this file used an ad hoc map[string]interface{} on
// the Go side and walked event.data up to four levels deep here to find it,
// because that path wrapped one level deeper (GenericEventData's own "data"
// field) than a typed event does. Registering a real type removed the guess
// instead of hardening it.
export function parsePresentationUpdatedEvent(event: PollingEvent): PresentationUpdatedEvent | null {
  const data = getTypedEventData(event, PRESENTATION_UPDATED_EVENT_TYPE)
  if (!data || !data.presentation_id || !data.kind) return null
  return {
    presentationId: data.presentation_id,
    kind: data.kind,
    title: asString(data.title),
    workspacePath: asString(data.workspace_path),
    payload: (data.payload as Record<string, unknown> | undefined) ?? {},
    at: Date.parse(event.timestamp || '') || 0,
    activity: {
      label: asString(data.activity?.label),
      destination: asString(data.activity?.destination),
      detail: asString(data.activity?.detail),
    },
  }
}


export type WorkspacePresentation = {
  id: string
  kind: string
  schemaVersion: number
  sessionId?: string
  title: string
  payload: Record<string, unknown>
  resources: Array<Record<string, unknown>>
  actions: Array<Record<string, unknown>>
  status: string
  revision: number
  createdAt: string
  updatedAt: string
}

function parseJSONRecord(value: unknown): Record<string, unknown> | null {
  try {
    const parsed = JSON.parse(String(value || '{}')) as unknown
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : null
  } catch {
    return null
  }
}

function parseJSONArray(value: unknown): Array<Record<string, unknown>> {
  try {
    const parsed = JSON.parse(String(value || '[]')) as unknown
    return Array.isArray(parsed)
      ? parsed.filter((item): item is Record<string, unknown> => !!item && typeof item === 'object' && !Array.isArray(item))
      : []
  } catch {
    return []
  }
}

export function parseWorkspacePresentations(rows: Record<string, unknown>[]): WorkspacePresentation[] {
  return rows.flatMap((row) => {
    const id = typeof row.id === 'string' ? row.id.trim() : ''
    const kind = typeof row.kind === 'string' ? row.kind.trim() : ''
    const title = typeof row.title === 'string' ? row.title.trim() : ''
    const status = typeof row.status === 'string' ? row.status.trim() : ''
    const payload = parseJSONRecord(row.payload_json)
    if (!id || !kind || !title || !status || !payload) return []
    return [{
      id,
      kind,
      schemaVersion: Number(row.schema_version) || 1,
      sessionId: typeof row.session_id === 'string' ? row.session_id : undefined,
      title,
      payload,
      resources: parseJSONArray(row.resources_json),
      actions: parseJSONArray(row.actions_json),
      status,
      revision: Number(row.revision) || 1,
      createdAt: typeof row.created_at === 'string' ? row.created_at : '',
      updatedAt: typeof row.updated_at === 'string' ? row.updated_at : '',
    }]
  })
}

