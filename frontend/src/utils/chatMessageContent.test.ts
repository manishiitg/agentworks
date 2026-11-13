import { expect, it } from 'vitest'
import { getDisplaySafeUserMessageContent, getUserMessageAttachmentPaths } from './chatMessageContent'

// PLAT-746: native async answers leaked their transport envelope into the chat.
it('shows selected and typed answers without changing the native reply payload', () => {
  const raw = '<send_user_message_question_reply>\n' + JSON.stringify([{ questionItemId: 'internal-id', question: 'Which chat?', answer: 'The code chat' }, { answer: 'Option A, my note' }]) + '\n</send_user_message_question_reply>'
  expect(getDisplaySafeUserMessageContent(raw)).toBe('The code chat\n\nOption A, my note')
  // PLAT-746-899: the server's carry-on note after the block is for the agent, not shown as the person's words.
  expect(getDisplaySafeUserMessageContent(raw + '\n\nCarry on with the task.')).toBe('The code chat\n\nOption A, my note')
  const malformed = '<send_user_message_question_reply>not json</send_user_message_question_reply>'
  expect(getDisplaySafeUserMessageContent(malformed)).toBe(malformed)
  expect(getDisplaySafeUserMessageContent('ordinary answer')).toBe('ordinary answer')
})

// PLAT-746-938: native transport trims leading blank lines on image-only sends.
it('renders caption-less attachment context as files, not a raw path bubble', () => {
  const path = 'Crew/example/uploads/image.png'
  for (const prefix of ['', '\n\n', '  \n']) {
    const content = `${prefix}📁 Files in context: ${path}`
    expect(getUserMessageAttachmentPaths(content)).toEqual([path])
    expect(getDisplaySafeUserMessageContent(content)).toBe('')
  }
  const captioned = `Read this\n\n📁 Files in context: ${path}`
  expect(getUserMessageAttachmentPaths(captioned)).toEqual([path])
  expect(getDisplaySafeUserMessageContent(captioned)).toBe('Read this')
  const prose = `Please explain 📁 Files in context: ${path}`
  expect(getUserMessageAttachmentPaths(prose)).toEqual([])
  expect(getDisplaySafeUserMessageContent(prose)).toBe(prose)
})
