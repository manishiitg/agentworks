// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { flattenRunLog } from './CrewRunLog'
import type { ExecutionLogsResponse } from '../../services/api-types'

const response = (steps: Record<string, unknown>) => ({ success: true, steps } as unknown as ExecutionLogsResponse)

it('lists every execution of every step as one plain entry, with its status and text', () => {
  const entries = flattenRunLog(response({
    one: { executions: [{ attempt: 0, file_path: 'a.json', fast_path: true, content: { success: false, exit_code: 1, output: 'Traceback...\nValueError', mode: 'scripted_fast_path' } }] },
    two: { executions: [{ attempt: 2, file_path: 'b.json', content: { success: true, exit_code: 0, output: 'done' } }] },
  }))
  expect(entries.map(entry => [entry.title, entry.state, entry.exitCode, entry.text])).toEqual([
    ['Saved script', 'stopped', 1, 'Traceback...\nValueError'],
    ['Attempt 2', 'finished', 0, 'done'],
  ])
})

it('uses the error when nothing was printed, and adds it when it says more', () => {
  const [onlyError, both, same] = flattenRunLog(response({ s: { executions: [
    { attempt: 1, file_path: '1', content: { success: false, error: 'boom' } },
    { attempt: 2, file_path: '2', content: { success: false, output: 'out', error: 'extra' } },
    { attempt: 3, file_path: '3', content: { success: false, output: 'exit code 1: boom', error: 'boom' } },
  ] } }))
  expect(onlyError.text).toBe('boom')
  expect(both.text).toBe('out\n\nextra')
  expect(same.text).toBe('exit code 1: boom')
})

it('handles a missing response, a step with no executions and unknown results', () => {
  expect(flattenRunLog(null)).toEqual([])
  expect(flattenRunLog(response({ s: {} }))).toEqual([])
  expect(flattenRunLog(response({ s: { executions: [{ attempt: 1, file_path: 'x', content: {} }] } }))[0].state).toBe('idle')
})

it('keeps when it ran, its script, why it failed and whether its output was cut', () => {
  const [entry] = flattenRunLog(response({ s: { executions: [{ attempt: 0, file_path: 'a.json', fast_path: true, content: {
    success: false, exit_code: 1, output: 'x', timestamp: '2026-11-16T18:36:56+05:30', script_path: '/Users/me/Crew/x/code/main.py', failure_reason: 'execution_error', output_truncated: true, validation_error: 'bad shape',
  } }] } }))
  expect(entry.facts).toContain('Ran ')
  expect(entry.facts).toContain('script main.py')
  expect(entry.facts).not.toContain('/Users/me')
  expect(entry.facts).toContain('reason: execution error')
  expect(entry.facts).toContain('output was cut short')
  expect(entry.facts).toContain('validation: bad shape')
})

it('has no facts line when the log carries none', () => {
  expect(flattenRunLog(response({ s: { executions: [{ attempt: 1, file_path: 'x', content: { success: true } }] } }))[0].facts).toBe('')
})
