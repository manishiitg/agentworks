import { expect, it } from 'vitest'
import { authorChatVisual } from './chatVisualAuthoring'
import { compileChatVisual } from './chatVisualSpec'

it('authors a sequence that streams into the same visual the finished reply saves', () => {
  const sequence = {
    title: 'Handoff',
    participants: [{ id: 'lead', title: 'Lead' }, { id: 'worker', title: 'Worker' }],
    messages: [
      { from: 'lead', to: 'worker', label: 'Review' },
      { from: 'worker', to: 'lead', label: 'Findings', reply: true },
    ],
  }
  const result = authorChatVisual({ sequence })
  if ('error' in result) throw new Error(result.error)
  const lines = result.markdown.split('\n').slice(1, -1)
  const partial = compileChatVisual(lines.slice(0, -1).join('\n'), true)
  expect(partial?.elements.visual.props.messages).toEqual(sequence.messages.slice(0, 1))
  expect(compileChatVisual(lines.join('\n'))?.elements.visual.props).toEqual(sequence)
})
