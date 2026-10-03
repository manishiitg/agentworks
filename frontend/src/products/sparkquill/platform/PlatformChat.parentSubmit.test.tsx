// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { SparkQuillConversation, submitToParentChat, applyFamilyEngineToOpenTabs } from './PlatformChat'
import { useChatStore } from '../../../stores/useChatStore'

describe('parent page answers', () => {
  it('preserves the other role model unless the shared engine changes', () => {
    const setMetadata = vi.fn()
    const state = vi.spyOn(useChatStore, 'getState').mockReturnValue({
      chatTabs: {
        parent: { tabId: 'parent', metadata: { agentProfileId: 'sparkquill', agentProfileEngine: 'codex', agentProfileModelID: 'luna' } },
        child: { tabId: 'child', metadata: { agentProfileId: 'sparkquill-child', agentProfileEngine: 'codex', agentProfileModelID: 'astra' } },
      },
      setTabMetadata: setMetadata,
    } as unknown as ReturnType<typeof useChatStore.getState>)
    try {
      applyFamilyEngineToOpenTabs('parent', 'codex', 'sol', 'high')
      expect(setMetadata).toHaveBeenCalledWith('parent', { agentProfileEngine: 'codex', agentProfileModelID: 'sol', agentProfileReasoningEffort: 'high' })
      expect(setMetadata).toHaveBeenCalledWith('child', { agentProfileEngine: 'codex' })
      setMetadata.mockClear()
      applyFamilyEngineToOpenTabs('parent', 'codex', 'sol', 'high', 'family-account')
      expect(setMetadata).toHaveBeenCalledWith('parent', { agentProfileEngine: 'codex', agentProfileModelID: 'sol', agentProfileReasoningEffort: 'high', agentProfileConnectionID: 'family-account' })
      expect(setMetadata).toHaveBeenCalledWith('child', { agentProfileEngine: 'codex', agentProfileConnectionID: 'family-account' })
      setMetadata.mockClear()
      applyFamilyEngineToOpenTabs('parent', 'claude', 'sonnet')
      expect(setMetadata).toHaveBeenCalledWith('child', { agentProfileEngine: 'claude', agentProfileConnectionID: '', agentProfileModelID: '', agentProfileReasoningEffort: undefined })
    } finally { state.mockRestore() }
  })
  it('submits a page choice to the open parent conversation', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    const submit = vi.fn()
    await act(async () => {
      root.render(<SparkQuillConversation events={[]} isStreaming={false} isRestoring={false} streamingText="" onSubmitQuery={submit} />)
    })

    expect(submitToParentChat('q1: 2/3')).toBe(true)
    expect(submit).toHaveBeenCalledWith('q1: 2/3')

    await act(async () => root.unmount())
    expect(submitToParentChat('q1: 2/3')).toBe(false)
  })
})
