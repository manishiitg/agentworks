import { expect, it } from 'vitest'
import { attachmentDisplayName, getDisplaySafeUserMessageContent, getUserMessageAttachmentPaths } from './chatMessageContent'

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

// A PDF chip showed its upload id ("0ca67187-99fe-…") instead of the name the person gave the file.
it('names an attached file by its own name, not its upload id', () => {
  expect(attachmentDisplayName('uploads/0ca67187-99fe-4ebd-98fd-9f09f7022e3d-Report 2026.pdf')).toBe('Report 2026.pdf')
  expect(attachmentDisplayName('docs/plan-v2.md')).toBe('plan-v2.md')
})

// Owner, 22 Nov: a fork's first-turn note showed as the person's own message.
it('keeps a first-turn fork, copy or helper note out of the person\'s message', () => {
  const note = '[This chat is a fork. It is "B", a separate agent forked from "A". The parent\'s job was: review tools]'
  expect(getDisplaySafeUserMessageContent(note + '\n\ngo')).toBe('go')
  expect(getDisplaySafeUserMessageContent('[This chat is "Helper", a helper agent opened by "A".]\n\nCheck the list')).toBe('Check the list')
  // Owner, 24 Nov: a fork whose parent had no purpose saved its note without the closing bracket.
  const unclosed = '[This chat is a fork. It is "B", a separate agent forked from "A". To reach it, send through AgentBridge; send only a hand-off, a blocker or a result.'
  expect(getDisplaySafeUserMessageContent(unclosed + '\n\ncan you create a skill')).toBe('can you create a skill')
  expect(getDisplaySafeUserMessageContent('[This chat is my note] but\n\nnot a first-turn note')).toBe('[This chat is my note] but\n\nnot a first-turn note')
})
