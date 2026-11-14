import { expect, it, vi } from 'vitest'
import { captureChatContextDestination, contextAttachmentRemoval, stageCapturedChatContext, type CapturedChatContext } from './chatContextAttachment'
import { invalidateChatIdentity } from './chatIdentity'

const fixture = vi.hoisted(() => ({ state: undefined as unknown as { activeTabId: string; setTabViewMode: (id: string, mode: string) => void; getTab: (id: string) => typeof tab | undefined; setTabConfig: (id: string, config: Partial<typeof tab.config>) => void } }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => fixture.state } }))
vi.mock('../services/api', () => ({ default: {} }))
const tab = { viewMode: 'terminal', sessionId: 'session-a', metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew-a', agentProfileConversationKey: 'crew-a:chat:agent' }, config: { inputText: 'Typed draft', autoRun: true, fileContext: [] as { name: string; path: string; type: 'file' }[] } }

// PLAT-746-1005: completing a capture after typing must preserve that draft;
// switching chat, session or account must never deposit context in a new one.
it('stages removable context without sending, erasing typing or crossing chat/session/account boundaries', () => {
  tab.sessionId = 'session-a'
  tab.config = { inputText: 'Typed draft', autoRun: true, fileContext: [] }
  fixture.state = { activeTabId: 'tab', setTabViewMode: (_, mode) => { tab.viewMode = mode }, getTab: id => id === 'tab' ? tab : undefined, setTabConfig: (_, patch) => { Object.assign(tab.config, patch) } }
  const destination = captureChatContextDestination('tab')!
  tab.config.inputText = 'Typing continued while capture waited'
  const captured = { source: 'browser', preview_path: 'crew/uploads/crop.png', files: [{ name: 'crop.png', path: 'crew/uploads/crop.png', type: 'file' }, { name: 'element.json', path: 'crew/uploads/element.json', type: 'file' }] } as CapturedChatContext
  expect(stageCapturedChatContext(destination, captured)).toBe(true)
  expect(tab.config.inputText).toBe('Typing continued while capture waited')
  expect(tab.config.autoRun).toBe(false)
  expect(tab.viewMode).toBe('formatted')
  expect(tab.config.fileContext).toHaveLength(2)
  expect([...contextAttachmentRemoval(tab.config.fileContext, 'crew/uploads/element.json')]).toEqual(['crew/uploads/element.json', 'crew/uploads/crop.png'])
  fixture.state.activeTabId = 'another-tab'
  expect(stageCapturedChatContext(destination, captured)).toBe(false)
  fixture.state.activeTabId = 'tab'
  tab.sessionId = 'session-b'
  expect(stageCapturedChatContext(destination, captured)).toBe(false)
  tab.sessionId = 'session-a'
  invalidateChatIdentity()
  expect(stageCapturedChatContext(destination, captured)).toBe(false)
})
