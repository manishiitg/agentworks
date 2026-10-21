import { expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { UnifiedCompletionEventDisplay } from './UnifiedCompletionEvent'
import { buildCleanConversationItems } from '../../../utils/cleanConversation'
import { buildTranscriptItems } from '../../../../shared/session/transcript/terminalEventTranscript'
import type { PollingEvent } from '../../../services/api-types'

it('shows the recorded interrupted/no-answer outcome across chat renderers without treating ordinary empty completions as failures', () => {
  const payload = {
    status: 'completed', final_result: '',
    metadata: { retained_turn_outcome: 'interrupted', final_response_missing: true },
  }
  const event = (data: Record<string, unknown>): PollingEvent => ({
    id: 'interrupted', type: 'unified_completion', session_id: 'product-example',
    execution_id: 'main:product-example', execution_kind: 'main_agent',
    data: { type: 'unified_completion', data },
  }) as PollingEvent
  const notice = 'This reply was interrupted and no answer was produced. Send your message again.'
  expect(buildCleanConversationItems([event(payload), event(payload)]).map(item => item.content)).toEqual([notice])
  expect(buildTranscriptItems([event(payload)])).toEqual([expect.objectContaining({ kind: 'event' })])
  const html = renderToStaticMarkup(<UnifiedCompletionEventDisplay event={payload} />)
  expect(html).toContain(notice)
  expect(html).not.toContain('Completed')

  for (const metadata of [{}, { retained_turn_outcome: 'interrupted' }, { final_response_missing: true }]) {
    const ordinary = { ...payload, metadata }
    expect(buildCleanConversationItems([event(ordinary)])).toEqual([])
    expect(buildTranscriptItems([event(ordinary)])).toEqual([])
    expect(renderToStaticMarkup(<UnifiedCompletionEventDisplay event={ordinary} />)).not.toContain(notice)
  }
  const answered = { ...payload, final_result: 'Here is the answer.' }
  expect(buildCleanConversationItems([event(answered)]).map(item => item.content)).toEqual(['Here is the answer.'])
  expect(renderToStaticMarkup(<UnifiedCompletionEventDisplay event={answered} />)).not.toContain(notice)
})
