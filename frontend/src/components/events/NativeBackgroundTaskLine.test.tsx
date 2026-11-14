import { expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { backgroundTaskLine, NativeBackgroundTaskLine } from './NativeBackgroundTaskLine'
import { coalesceNativeHelperActivity } from '../../utils/nativeHelperActivity'
import { buildTranscriptItems } from '../../utils/terminalEventTranscript'
import type { PollingEvent } from '../../services/api-types'

it('formats reported running/finished facts, with no invented command usage', () => {
  const start = '2026-11-13T10:00:00Z'
  const record = { work_id: 'a', kind: 'helper', state: 'started', started_at: start, tokens: 18400 }
  expect(backgroundTaskLine(record, Date.parse(start) + 134000)).toBe('Background agent running… · 2m 14s · 18.4k tokens')
  const done = { ...record, state: 'finished', ended_at: '2026-11-13T10:03:02Z', tokens: 24100 }
  expect(backgroundTaskLine(done, Date.parse(start) + 999999)).toBe('Background agent finished · 3m 02s · 24.1k tokens')
  expect(backgroundTaskLine({ ...record, kind: 'background_command', command_label: 'deploy.sh verify', tokens: undefined }, Date.parse(start) + 1000)).toBe('Background shell running… · deploy.sh verify · 1s')
  expect(renderToStaticMarkup(<NativeBackgroundTaskLine record={done} />)).not.toContain('<button')
})

it('keeps one server-owned row per task run across live updates and replay', () => {
  const event = (id: string, work: string, state: string, tokens?: number) => ({ id, session_id: 'task-chat', timestamp: '2026-11-13T10:00:00Z', type: 'coding_agent_native_activity', data: { data: { record: { work_id: work, kind: 'helper', state, tokens } } } }) as unknown as PollingEvent
  const raw = [event('a1', 'a-run1', 'started'), event('b1', 'b-run1', 'started', 500), event('a2', 'a-run1', 'started', 18400), event('a3', 'a-run1', 'finished', 24100), event('a4', 'a-run2', 'started')]
  const rows = coalesceNativeHelperActivity(raw)
  expect(rows).toHaveLength(3)
  expect(buildTranscriptItems(rows)).toHaveLength(3)
  const rawDiagnostic = { id: 'raw-tool', type: 'coding_agent_native_activity', data: { data: { record: { kind: 'tool', state: 'completed' } } } } as unknown as PollingEvent
  expect(buildTranscriptItems([rawDiagnostic])).toHaveLength(0)
  expect(rows[0].id).toBe('a1')
  expect(coalesceNativeHelperActivity(JSON.parse(JSON.stringify(raw)))).toEqual(rows)
})

it('shows typed long foreground helpers but keeps short ones as ordinary steps', () => {
  const start = '2026-11-14T10:00:00Z', at = Date.parse(start)
  const helper = { work_id: 'explore', kind: 'helper', state: 'started', helper_type: 'Explore', helper_description: 'Find routing code', helper_mode: 'foreground', started_at: start }
  expect(backgroundTaskLine(helper, at + 2000)).toBe('')
  expect(backgroundTaskLine(helper, at + 4000)).toBe('Explore · Find routing code · 4s')
  const short = { ...helper, state: 'finished', ended_at: '2026-11-14T10:00:02Z' }
  expect(backgroundTaskLine(short, at + 90000)).toBe('')
  expect(renderToStaticMarkup(<NativeBackgroundTaskLine record={short} />)).toBe('')
  const async = { ...helper, helper_mode: 'async', current_step: 'Grepping coding.go', tokens: 154200 }
  expect(backgroundTaskLine(async, at + 288000)).toBe('Explore · Grepping coding.go · 4m 48s · 154.2k tokens')
  expect(backgroundTaskLine({ ...async, tokens: undefined }, at + 1000)).not.toContain('tokens')
})
