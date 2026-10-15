import { expect, it, vi } from 'vitest'
import type { ReportHumanInput } from '../services/api-types'

const send = vi.hoisted(() => vi.fn())
vi.mock('./workspacePaneChat', () => ({
  sendWorkspacePaneMessageToChat: send,
  openWorkspacePaneChatWithDraft: vi.fn(),
}))
import { delegateReportHumanInputActionToChat } from './reportHumanInputChat'

const input = { id: 'in-1', question: 'Add a Quotes page?', source: 'user_suggestion', options: [] } as unknown as ReportHumanInput

it('sends a Crew decision to the Crew chat by profile and conversation key, never by a workflow path', async () => {
  send.mockResolvedValue({ tabId: 't', reused: false, queuedBehindRunningTurn: false })
  await delegateReportHumanInputActionToChat({ input, workspacePath: 'Crew/example-crew-aaaa1111', chatTarget: { profileId: 'work', conversationKey: 'crew-1' } })
  expect(send.mock.calls[0][0]).toMatchObject({ profileId: 'work', conversationKey: 'crew-1' })
  expect(send.mock.calls[0][0]).not.toHaveProperty('workspacePath')
  // A workflow decision still routes by its path.
  await delegateReportHumanInputActionToChat({ input, workspacePath: 'Workflow/example' })
  expect(send.mock.calls[1][0]).toMatchObject({ workspacePath: 'Workflow/example' })
})
