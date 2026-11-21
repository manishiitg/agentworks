import { expect, it, vi } from 'vitest'
import { applyNextPromptKey, currentNextPrompt } from './nextPrompt'
import type { PollingEvent } from '../services/api-types'

const row = (id: string, type: string, fields: Record<string, unknown> = {}): PollingEvent => ({ id, type, timestamp: '', session_id: 'chat', data: { data: fields } } as PollingEvent)
it('rejects predictions for an older input and keeps the same event identity while reply tokens change (PLAT-746-992)', () => {
  const input = row('input', 'user_message'), suggestion = row('prediction', 'next_prompt', { text: 'Check the diff', for_message_id: 'input' })
  expect(currentNextPrompt([input, suggestion])).toBe(suggestion)
  expect(currentNextPrompt([input, suggestion, row('token', 'streaming_chunk')])).toBe(suggestion)
  expect(currentNextPrompt([input, suggestion, row('new-input', 'user_message')])).toBeUndefined()
  expect(currentNextPrompt([row('other-input', 'user_message'), suggestion])).toBeUndefined()
  expect(currentNextPrompt([row('typed-instruction', 'user_message', { content: 'Check the diff' })])).toBeUndefined()
})
it('Tab fills only a non-auto-run draft, Escape only dismisses, and modified or composing keys do nothing', () => {
  const fill = vi.fn(), dismiss = vi.fn()
  const key = { key: 'Tab', ctrlKey: false, altKey: false, metaKey: false, shiftKey: false, isComposing: false }
  expect(applyNextPromptKey(key, 'Check the diff', fill, dismiss)).toBe(true)
  expect(fill).toHaveBeenCalledExactlyOnceWith('Check the diff', { autoRun: false })
  fill.mockClear(); dismiss.mockClear()
  expect(applyNextPromptKey({ ...key, key: 'Escape' }, 'Check the diff', fill, dismiss)).toBe(true)
  expect(fill).not.toHaveBeenCalled(); expect(dismiss).toHaveBeenCalledOnce()
  dismiss.mockClear()
  for (const event of [{ ...key, shiftKey: true }, { ...key, isComposing: true }, { ...key, key: 'Enter' }]) expect(applyNextPromptKey(event, 'Check the diff', fill, dismiss)).toBe(false)
  expect(fill).not.toHaveBeenCalled(); expect(dismiss).not.toHaveBeenCalled()
})
