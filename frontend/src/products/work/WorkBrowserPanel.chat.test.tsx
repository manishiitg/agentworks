// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { WorkBrowserPanel } from './WorkWorkspacePane'
import { CREW_PRODUCT, ProjectProductProvider } from './projectProduct'

vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const browser = vi.hoisted(() => ({ prepare: undefined as undefined | ((message: string) => Promise<void>) }))
vi.mock('../../components/workflow/BrowserWorkspacePanel', () => ({
  BrowserWorkspacePanel: ({ assistantControl }: { assistantControl: React.ReactElement<{ onAsk: typeof browser.prepare }> }) => {
    browser.prepare = assistantControl.props.onAsk
    return null
  },
}))

afterEach(() => { browser.prepare = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('sends browser help to the named chat represented by the Browser pane', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.spyOn(api, 'get').mockResolvedValue({ data: { mode: 'headless', port: 9222 } } as Awaited<ReturnType<typeof api.get>>)
  const previous = useChatStore.getState()
  const root = createRoot(document.createElement('div'))
  const tabId = await previous.createChatTab('My browser chat', {
    mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'crew-one',
    agentProfileConversationKey: 'crew-one:chat:mine', agentProfileBuilder: false,
    agentProfileWorkspace: 'Crew/example-crew-one',
  })
  try {
    await act(async () => root.render(<ProjectProductProvider value={CREW_PRODUCT}>
      <WorkBrowserPanel tabId={tabId} workspacePath="Crew/example-crew-one" onAsk={message => sendWorkspacePaneMessageToChat({ tabId, message })} />
    </ProjectProductProvider>))
    expect(browser.prepare).toBeDefined()
    await act(async () => { await browser.prepare!('Help me save this browser procedure as a skill') })
    expect(useChatStore.getState().getTabConfig(tabId)?.queuedMessages).toContain('Help me save this browser procedure as a skill')
    expect(useChatStore.getState().activeTabId).toBe(tabId)
  } finally {
    act(() => root.unmount())
    useChatStore.setState(previous, true)
  }
})
