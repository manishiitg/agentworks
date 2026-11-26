import { expect, it } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { currentNextStepsEvent, nextStepsOf, parseNextSteps, stripNextSteps } from './nextSteps'

const reply = 'X is done.\n\n```next\n{"summary": "X moved; Substack is next.", "question": "Which project next?", "options": ["Substack", "hetzner-ssh"]}\n```\n'

it('reads the card from the end of a reply and hides the block from its text', () => {
  expect(parseNextSteps(reply)).toEqual({ summary: 'X moved; Substack is next.', question: 'Which project next?', options: ['Substack', 'hetzner-ssh'] })
  expect(stripNextSteps(reply)).toBe('X is done.')
  // Still streaming: the open block is hidden too.
  expect(stripNextSteps('X is done.\n\n```next\n{"summary": "X mo')).toBe('X is done.')
  expect(stripNextSteps('Plain reply.')).toBe('Plain reply.')
  expect(parseNextSteps('```next\nnot json\n```')).toBeUndefined()
  expect(parseNextSteps('```next\n{"question": "Which?", "options": []}\n```')).toBeUndefined()
})

it('shows the card only for the last reply after the person last wrote', () => {
  const completion = (id: string, content: string) => ({ id, type: 'unified_completion', data: { data: { content } } }) as unknown as PollingEvent
  const user = { id: 'u2', type: 'user_message', data: {} } as unknown as PollingEvent
  expect(currentNextStepsEvent([completion('c1', reply)])?.id).toBe('c1')
  expect(nextStepsOf(completion('c1', reply))?.options).toEqual(['Substack', 'hetzner-ssh'])
  expect(currentNextStepsEvent([completion('c1', reply), user])).toBeUndefined()
  expect(currentNextStepsEvent([completion('c1', reply), user, completion('c2', 'Done, nothing waiting.')])).toBeUndefined()
})
