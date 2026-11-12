// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useChatStore } from '../stores/useChatStore'
import { useChatRuntimeActivity } from './useChatRuntimeActivity'
import { useTabTurnActive } from './useTabTurnActive'
import { sessionStreamingState } from '../utils/sessionStreamingState'
import type { ActiveSessionInfo, RuntimeSnapshot } from '../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-930, also preserves 903: transcript events cannot override server
// lifecycle metadata, including when the old start survives a status update.
it('keeps the header and Stop active through generation ends and settles from server status', async () => {
  const initial = useChatStore.getState()
  useChatStore.setState({ getActiveSessions: vi.fn().mockResolvedValue([]) })
  const store = useChatStore.getState()
  const tabId = await store.createChatTab('Example', { mode: 'multi-agent', agentProfileId: 'crew' }, 'turn-status-chat')
  const runtime: RuntimeSnapshot = {
    session_id: 'turn-status-chat', generation: 1, revision: 1, phase: 'running',
    foreground_turn: { busy: true, has_cancel: true, can_steer: true, synthetic: false },
    background_live: false, terminal_busy: false, waiting_for_user: false,
    started_at: '2026-01-01T10:00:00Z', observed_at: '2026-01-01T10:01:00Z',
  }
  // Same projection and tab setters used by ChatArea's SSE/REST handler.
  const status = (snapshot: RuntimeSnapshot) => {
    const activity = sessionStreamingState({ runtime_state: snapshot })
    store.setTabStreaming(tabId, activity.isStreaming)
    store.setTabCompleted(tabId, activity.isCompleted)
    store.setTabHasRunningBgAgents(tabId, activity.hasRunningBgAgents)
  }
  function Probe() {
    const activity = useChatRuntimeActivity(tabId)
    const canStop = useTabTurnActive(tabId, 'foreground')
    return <div>{activity.state}:{String(canStop)}</div>
  }
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    status(runtime)
    await act(async () => root.render(<Probe />))
    expect(host.textContent).toBe('running:true')
    for (const [index, type] of ['user_message', 'llm_generation_end', 'agent_end', 'streaming_end', 'tool_call_start'].entries()) {
      await act(async () => {
        store.addTabEvent(runtime.session_id, { id: String(index), type, session_id: runtime.session_id,
          timestamp: `2026-01-01T10:00:0${index}Z`, execution_kind: 'main_agent' })
      })
      expect(host.textContent).toBe('running:true')
    }
    for (const phase of ['completed', 'failed', 'canceled'] as const) {
      await act(async () => status({ ...runtime, phase, foreground_turn: { ...runtime.foreground_turn, busy: false, has_cancel: false, can_steer: false } }))
      expect(host.textContent).toBe('ready:false')
    }
    // New-send feedback stays immediate, even before the next server update.
    await act(async () => { store.setTabCompleted(tabId, false); store.setTabStreaming(tabId, true) })
    expect(host.textContent).toBe('running:true')
    const waiting = { ...runtime, phase: 'waiting' as const, waiting_for_user: true }
    await act(async () => {
      useChatStore.setState({ activeSessionsCache: [{ session_id: runtime.session_id, runtime_state: waiting } as ActiveSessionInfo] })
      status(waiting)
    })
    expect(host.textContent).toBe('waiting:false')
    await act(async () => {
      const background = { ...runtime, foreground_turn: { ...runtime.foreground_turn, busy: false, has_cancel: false, can_steer: false }, background_live: true }
      useChatStore.setState({ activeSessionsCache: [{ session_id: runtime.session_id, runtime_state: background } as ActiveSessionInfo] })
      status(background)
    })
    expect(host.textContent).toBe('running:false')
  } finally {
    await act(async () => root.unmount())
    useChatStore.setState(initial, true)
  }
})
