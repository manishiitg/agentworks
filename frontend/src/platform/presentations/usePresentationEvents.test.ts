import { describe, expect, it } from 'vitest'
import { parsePresentationUpdatedEvent, PRESENTATION_UPDATED_EVENT_TYPE } from './usePresentationEvents'
import type { PollingEvent } from '../../services/api-types'

// PresentationUpdatedEvent is a real registered event type (cmd/schema-gen),
// so the wire shape here is not assumed: it is what getTypedEventData expects
// for every typed event -- event.data.type / event.data.data, matching
// tool_call_end and the rest of the catalog. Confirmed against a real
// emission from a live show_video call before this file was written.

function typedEvent(fields: Record<string, unknown>): PollingEvent {
  return {
    id: 'e1',
    type: PRESENTATION_UPDATED_EVENT_TYPE,
    data: {
      type: PRESENTATION_UPDATED_EVENT_TYPE,
      data: fields,
    },
  } as unknown as PollingEvent
}

describe('parsePresentationUpdatedEvent', () => {
  it('parses a real presentation_updated event', () => {
    const event = typedEvent({
      presentation_id: 'presentation-abc',
      kind: 'media.video',
      title: 'Launch video',
      workspace_path: 'Chats/Example Product/projects/demo',
		payload: { path: 'outputs/final.mp4' },
		activity: { label: 'Video ready', destination: 'Videos panel', detail: 'Ready to review' },
    })
    expect(parsePresentationUpdatedEvent(event)).toEqual({
      presentationId: 'presentation-abc',
      kind: 'media.video',
      title: 'Launch video',
      workspacePath: 'Chats/Example Product/projects/demo',
		payload: { path: 'outputs/final.mp4' },
		at: 0,
		activity: { label: 'Video ready', destination: 'Videos panel', detail: 'Ready to review' },
    })
  })

  it('ignores events of any other type', () => {
    const event = {
      id: 'e2',
      type: 'tool_call_end',
      data: { type: 'tool_call_end', data: { presentation_id: 'x', kind: 'media.video' } },
    } as unknown as PollingEvent
    expect(parsePresentationUpdatedEvent(event)).toBeNull()
  })

  it('rejects an event missing the fields a renderer needs to dispatch on', () => {
    expect(parsePresentationUpdatedEvent(typedEvent({ kind: 'media.video' }))).toBeNull() // no id
    expect(parsePresentationUpdatedEvent(typedEvent({ presentation_id: 'x' }))).toBeNull() // no kind
  })

  it('defaults title, workspace path, and payload rather than throwing when absent', () => {
    const parsed = parsePresentationUpdatedEvent(typedEvent({ presentation_id: 'x', kind: 'media.video' }))
		expect(parsed).toEqual({
			presentationId: 'x', kind: 'media.video', title: '', workspacePath: '', payload: {}, at: 0,
			activity: { label: '', destination: '', detail: '' },
		})
  })

  // The real wire shape, captured from a live show_video call's persisted
  // ui_events entry, for the untyped map[string]interface{} path this
  // replaced. Kept as a regression guard: if the Go side ever falls back to
  // an untyped emit again, this documents exactly why it breaks parsing --
  // an extra GenericEventData wrapper level this parser no longer expects.
  it('does NOT parse the old three-level GenericEventData wrapping (documents why typing this mattered)', () => {
    const event = {
      id: 'e3',
      type: PRESENTATION_UPDATED_EVENT_TYPE,
      data: {
        type: PRESENTATION_UPDATED_EVENT_TYPE,
        data: {
          data: { presentation_id: 'presentation-c6e96c8d55ad5c53', kind: 'media.video' },
        },
      },
    } as unknown as PollingEvent
    expect(parsePresentationUpdatedEvent(event)).toBeNull()
  })
})
