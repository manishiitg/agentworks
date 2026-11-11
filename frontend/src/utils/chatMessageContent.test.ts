import { expect, it } from 'vitest'
import { getDisplaySafeUserMessageContent } from './chatMessageContent'

// PLAT-746: native async answers leaked their transport envelope into the chat.
it('shows selected and typed answers without changing the native reply payload', () => {
  const raw = '<send_user_message_question_reply>\n' + JSON.stringify([{ questionItemId: 'internal-id', question: 'Which chat?', answer: 'The code chat' }, { answer: 'Option A, my note' }]) + '\n</send_user_message_question_reply>'
  expect(getDisplaySafeUserMessageContent(raw)).toBe('The code chat\n\nOption A, my note')
  const malformed = '<send_user_message_question_reply>not json</send_user_message_question_reply>'
  expect(getDisplaySafeUserMessageContent(malformed)).toBe(malformed)
  expect(getDisplaySafeUserMessageContent('ordinary answer')).toBe('ordinary answer')
})
