import { expect, it } from 'vitest'
import { bringBackMessage, discardMessage } from './ChatBranchChip'

// #2922: the own-copy menu asks the chat's agent, naming every copy, and discarding never touches the shared folder.
it('names each own copy in the request to the agent', () => {
  const branches = [{ repo: 'web-app', branch: 'chat/fix-login' }, { repo: 'api', branch: 'chat/fix-login' }]
  expect(bringBackMessage(branches)).toContain('chat/fix-login in web-app, chat/fix-login in api')
  expect(bringBackMessage(branches)).toContain('merge the branch into the branch it started from')
  expect(discardMessage(branches)).toContain('The shared folder stays as it is')
})
