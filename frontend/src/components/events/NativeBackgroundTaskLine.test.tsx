import { expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { backgroundTaskLine, NativeBackgroundTaskLine } from './NativeBackgroundTaskLine'
import { coalesceNativeHelperActivity } from '../../utils/nativeHelperActivity'
import type { PollingEvent } from '../../services/api-types'

it('formats reported running/finished facts, with no invented command usage', () => {
  const start = '2026-11-13T10:00:00Z'
  const record = { work_id: 'a', kind: 'helper', state: 'started', started_at: start, tokens: 18400 }
  expect(backgroundTaskLine(record, Date.parse(start) + 134000)).toBe('Background agent running… · 2m 14s · 18.4k tokens')
  const done = { ...record, state: 'finished', ended_at: '2026-11-13T10:03:02Z', tokens: 24100 }
  expect(backgroundTaskLine(done, Date.parse(start) + 999999)).toBe('Background agent finished · 3m 02s · 24.1k tokens')
  expect(backgroundTaskLine({ ...record, kind: 'background_command', tokens: undefined }, Date.parse(start) + 1000)).toBe('Background task running… · 1s')
  expect(renderToStaticMarkup(<NativeBackgroundTaskLine record={done} />)).not.toContain('<button')
})

it('keeps one server-owned row per task run across live updates and replay', () => {
  const event = (id: string, work: string, state: string, tokens?: number) => ({ id, type: 'coding_agent_native_activity', data: { data: { record: { work_id: work, kind: 'helper', state, tokens } } } }) as unknown as PollingEvent
  const raw = [event('a1', 'a-run1', 'started'), event('b1', 'b-run1', 'started', 500), event('a2', 'a-run1', 'started', 18400), event('a3', 'a-run1', 'finished', 24100), event('a4', 'a-run2', 'started')]
  const rows = coalesceNativeHelperActivity(raw)
  expect(rows).toHaveLength(3)
  expect(rows[0].id).toBe('a1')
  expect(coalesceNativeHelperActivity(JSON.parse(JSON.stringify(raw)))).toEqual(rows)
})
