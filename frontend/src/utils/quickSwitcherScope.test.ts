import { describe, expect, it } from 'vitest'
import type { ActiveSessionInfo } from '../services/api-types'
import type { ChatTab } from '../stores/useChatStore'
import { isAgentWorksSwitcherTab, scopeQuickSwitcherToAgentWorks } from './quickSwitcherScope'

const tab = (id: string, profile?: string, sessionId: string | null = id): ChatTab => ({
  tabId: id, name: id, sessionId, metadata: { mode: 'multi-agent', agentProfileId: profile },
} as ChatTab)
const session = (id: string): ActiveSessionInfo => ({
  session_id: id, observer_id: '', agent_mode: 'multi-agent', status: 'running', created_at: '', last_activity: '',
})

describe('AgentWorks quick switcher scope', () => {
  it('keeps ordinary chats and workflows, excluding product profiles even before a session exists', () => {
    expect(isAgentWorksSwitcherTab(tab('ordinary'))).toBe(true)
    expect(isAgentWorksSwitcherTab(tab('explicit', 'agentworks'))).toBe(true)
    expect(isAgentWorksSwitcherTab({ sessionId: 'workflow-run', metadata: { mode: 'workflow' } })).toBe(true)
    for (const profile of ['video-studio', 'sparkquill', 'sparkquill-child', 'future-product']) {
      expect(isAgentWorksSwitcherTab(tab('new', profile, null))).toBe(false)
    }
  })

  it('removes history video without allowing its active session to reappear', () => {
    const tabs = {
      ordinary: tab('ordinary'),
      historyVideo: tab('history video', 'video-studio', 'legacy-opaque-id'),
      newVideo: tab('new-video', 'video-studio', 'video-studio:project:new-video'),
    }
    const result = scopeQuickSwitcherToAgentWorks(tabs, [
      session('ordinary'), session('legacy-opaque-id'), session('video-studio:project:new-video'), session('schedule-workflow-run'),
    ])
    expect(Object.keys(result.tabs)).toEqual(['ordinary'])
    expect(result.sessions.map(s => s.session_id)).toEqual(['ordinary', 'schedule-workflow-run'])
    expect(Object.keys(tabs)).toHaveLength(3)
  })

  it('recognizes product sessions without local tabs or with stale generic metadata', () => {
    const result = scopeQuickSwitcherToAgentWorks({
      stale: tab('stale', 'agentworks', 'video-studio:project:old'),
      registry: tab('registry', undefined, 'product-uuid'),
    }, [session('product-orphan'), session('future-product:project:one'), session('normal-orphan')])
    expect(Object.keys(result.tabs)).toEqual([])
    expect(result.sessions.map(s => s.session_id)).toEqual(['normal-orphan'])
  })
})
