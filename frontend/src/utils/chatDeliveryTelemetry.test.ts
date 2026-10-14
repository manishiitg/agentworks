// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import {
  configureChatDeliveryTelemetryTransport,
  recordChatDeliveryTelemetry,
  recordChatSubmissionTelemetry,
  type ChatDeliveryTelemetryBatch,
} from './chatDeliveryTelemetry'

afterEach(() => vi.useRealTimers())

describe('chat delivery telemetry', () => {
  it('ships exact timing milestones without message content', async () => {
    vi.useFakeTimers()
    const batches: ChatDeliveryTelemetryBatch[] = []
    configureChatDeliveryTelemetryTransport(async batch => { batches.push(batch) })
    const event = {
      id: 'event-1',
      type: 'unified_completion',
      session_id: 'session-1',
      timestamp: '2026-09-22T07:10:20.597Z',
      data: { data: { content: 'sensitive reply text' } },
    } as PollingEvent

    recordChatDeliveryTelemetry('sse_received', 'session-1', [event], 'sse', 'tab-1')
    await vi.advanceTimersByTimeAsync(101)

    expect(batches).toHaveLength(1)
    expect(batches[0].events[0]).toMatchObject({
      phase: 'sse_received',
      session_id: 'session-1',
      event_id: 'event-1',
      event_type: 'unified_completion',
      transport: 'sse',
      tab_id: 'tab-1',
      server_event_time: '2026-09-22T07:10:20.597Z',
    })
    expect(JSON.stringify(batches[0])).not.toContain('sensitive reply text')
  })

  it('records one receipt when the same event arrives through replay and SSE', async () => {
    vi.useFakeTimers()
    const batches: ChatDeliveryTelemetryBatch[] = []
    configureChatDeliveryTelemetryTransport(async batch => { batches.push(batch) })
    const event = {
      id: 'dedupe-event-1',
      type: 'unified_completion',
      timestamp: '2026-09-22T07:11:20.597Z',
      data: {},
    } as PollingEvent

    recordChatDeliveryTelemetry('catchup_received', 'dedupe-session', [event], 'catchup')
    recordChatDeliveryTelemetry('sse_received', 'dedupe-session', [event], 'sse')
    recordChatDeliveryTelemetry('processed', 'dedupe-session', [event], 'processor')
    recordChatDeliveryTelemetry('painted', 'dedupe-session', [event], 'renderer')
    await vi.advanceTimersByTimeAsync(101)

    expect(batches.flatMap(batch => batch.events).map(item => item.phase)).toEqual([
      'catchup_received',
      'processed',
      'painted',
    ])
  })

  it('bounds telemetry work for a large historical replay', async () => {
    vi.useFakeTimers()
    const batches: ChatDeliveryTelemetryBatch[] = []
    configureChatDeliveryTelemetryTransport(async batch => { batches.push(batch) })
    const events = Array.from({ length: 100 }, (_, index) => ({
      id: `bulk-event-${index}`,
      type: 'unified_completion',
      timestamp: `2026-09-22T07:12:${String(index % 60).padStart(2, '0')}.000Z`,
      data: {},
    })) as PollingEvent[]

    recordChatDeliveryTelemetry('catchup_received', 'bulk-session', events, 'catchup')
    await vi.advanceTimersByTimeAsync(101)

    const recorded = batches.flatMap(batch => batch.events)
    expect(recorded).toHaveLength(20)
    expect(recorded[0].event_id).toBe('bulk-event-80')
    expect(recorded.at(-1)?.event_id).toBe('bulk-event-99')
  })

  it('correlates submission and CLI acknowledgement without message content', async () => {
    vi.useFakeTimers()
    const batches: ChatDeliveryTelemetryBatch[] = []
    configureChatDeliveryTelemetryTransport(async batch => { batches.push(batch) })

    recordChatSubmissionTelemetry('submitted', 'session-send', 'submission-1', {
      tabId: 'tab-send', observedPerformanceMS: 100,
    })
    recordChatSubmissionTelemetry('api_acknowledged', 'session-send', 'submission-1', {
      tabId: 'tab-send', observedPerformanceMS: 245.25, elapsedMS: 145.25,
      deliveryStatus: 'sent_to_cli', provider: 'codex-cli', deliverySource: 'mcpagent_session',
    })
    await vi.advanceTimersByTimeAsync(101)

    expect(batches.flatMap(batch => batch.events)).toMatchObject([
      { phase: 'submitted', submission_id: 'submission-1', performance_ms: 100 },
      {
        phase: 'api_acknowledged', submission_id: 'submission-1', elapsed_ms: 145.25,
        delivery_status: 'sent_to_cli', provider: 'codex-cli', delivery_source: 'mcpagent_session',
      },
    ])
    expect(JSON.stringify(batches)).not.toContain('message')
  })

  it('records where a send went and when its row was staged, before the request exists', async () => {
    vi.useFakeTimers()
    const batches: ChatDeliveryTelemetryBatch[] = []
    configureChatDeliveryTelemetryTransport(async batch => { batches.push(batch) })

    recordChatSubmissionTelemetry('send_decision', 'session-hold', 'submission-9', { tabId: 'tab-9', transport: 'server', observedPerformanceMS: 10 })
    recordChatSubmissionTelemetry('send_staged', 'session-hold', 'submission-9', { tabId: 'tab-9', observedPerformanceMS: 12 })
    await vi.advanceTimersByTimeAsync(101)

    expect(batches.flatMap(batch => batch.events)).toMatchObject([
      { phase: 'send_decision', submission_id: 'submission-9', transport: 'server' },
      { phase: 'send_staged', submission_id: 'submission-9' },
    ])
  })
})
