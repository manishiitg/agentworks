import { describe, expect, it } from 'vitest'
import type { ChatHistorySession } from '../services/api-types'
import type { ChatTab } from '../stores/useChatStore'
import {
  findLatestRestorableWorkflowChatSession,
  resolveLatestWorkflowChatAction,
} from './latestWorkflowChat'

function session(overrides: Partial<ChatHistorySession> & { session_id: string }): ChatHistorySession {
  return { message_count: 5, ...overrides }
}

function tab(overrides: { tabId: string; sessionId?: string | null; mode?: 'workflow' | 'multi-agent' }): ChatTab {
  return {
    tabId: overrides.tabId,
    sessionId: overrides.sessionId ?? null,
    metadata: { mode: overrides.mode ?? 'workflow' },
  } as unknown as ChatTab
}

describe('findLatestRestorableWorkflowChatSession', () => {
  it('picks the first resumable restorable chat', () => {
    const sessions = [
      session({ session_id: 'newest-blocked', can_resume: false }),
      session({ session_id: 'schedule-cron--1' }),
      session({ session_id: 'newest-ok' }),
      session({ session_id: 'older-ok' }),
    ]
    expect(findLatestRestorableWorkflowChatSession(sessions)?.session_id).toBe('newest-ok')
  })

  it('skips bot prefixes, foreign modes, and empty chats', () => {
    const sessions = [
      session({ session_id: 'bot-slack--1', message_count: 5 }),
      session({ session_id: 'other-mode', agent_mode: 'terminal', message_count: 5 }),
      session({ session_id: 'empty', message_count: 0, preview_messages: [] }),
      session({ session_id: 'fallback', query: 'hello' }),
    ]
    expect(findLatestRestorableWorkflowChatSession(sessions)?.session_id).toBe('fallback')
  })

  it('returns undefined when nothing is pickable', () => {
    expect(findLatestRestorableWorkflowChatSession(undefined)).toBeUndefined()
    expect(findLatestRestorableWorkflowChatSession([])).toBeUndefined()
    expect(findLatestRestorableWorkflowChatSession([
      session({ session_id: 'blocked', can_resume: false }),
    ])).toBeUndefined()
  })
})

describe('resolveLatestWorkflowChatAction', () => {
  const newest = session({ session_id: 'newest' })
  const older = session({ session_id: 'older' })
  const sessions = [newest, older]

  it('keeps the active tab when it already shows the newest chat', () => {
    const tabs = { t1: tab({ tabId: 't1', sessionId: 'newest' }) }
    expect(resolveLatestWorkflowChatAction({ sessions, tabs, activeTabId: 't1' }))
      .toEqual({ action: 'keep-active' })
  })

  it('activates the existing tab when the newest chat already has one', () => {
    const tabs = {
      t1: tab({ tabId: 't1', sessionId: 'older' }),
      t2: tab({ tabId: 't2', sessionId: 'newest' }),
    }
    expect(resolveLatestWorkflowChatAction({ sessions, tabs, activeTabId: 't1' }))
      .toEqual({ action: 'activate-tab', tabId: 't2' })
  })

  it('restores the newest chat when it has no tab yet', () => {
    const tabs = { t1: tab({ tabId: 't1', sessionId: 'older' }) }
    expect(resolveLatestWorkflowChatAction({ sessions, tabs, activeTabId: 't1' }))
      .toEqual({ action: 'restore-latest', session: newest, sessionId: 'newest' })
  })

  it('keeps runs, schedules, bots, fresh, and foreign tabs untouched', () => {
    const runTabs = { t1: tab({ tabId: 't1', sessionId: 'schedule-cron--9' }) }
    expect(resolveLatestWorkflowChatAction({ sessions, tabs: runTabs, activeTabId: 't1' }))
      .toEqual({ action: 'keep-active' })
    const freshTabs = { t1: tab({ tabId: 't1', sessionId: null }) }
    expect(resolveLatestWorkflowChatAction({ sessions, tabs: freshTabs, activeTabId: 't1' }))
      .toEqual({ action: 'keep-active' })
    const foreignTabs = { t1: tab({ tabId: 't1', sessionId: 'another-workflow-session' }) }
    expect(resolveLatestWorkflowChatAction({ sessions, tabs: foreignTabs, activeTabId: 't1' }))
      .toEqual({ action: 'keep-active' })
  })

  it('keeps the active tab when there is no newest chat', () => {
    const tabs = { t1: tab({ tabId: 't1', sessionId: 'older' }) }
    expect(resolveLatestWorkflowChatAction({ sessions: [], tabs, activeTabId: 't1' }))
      .toEqual({ action: 'keep-active' })
    expect(resolveLatestWorkflowChatAction({ sessions: undefined, tabs, activeTabId: 't1' }))
      .toEqual({ action: 'keep-active' })
  })
})

describe('explicit server workspace return', () => {
  it('distinguishes a current validated return from stale local tab metadata', async () => {
    const { isRecentWorkspaceReturnTab } = await import('./workflowTabOwnership')
    const now = Date.now()
    const returned = { ...tab({ tabId: 'returned', sessionId: 'older' }), metadata: { mode: 'workflow' as const, presetQueryId: 'finance', workspaceReturnAt: now } }
    expect(isRecentWorkspaceReturnTab(returned, 'finance', now)).toBe(true)
    expect(isRecentWorkspaceReturnTab(returned, 'other', now)).toBe(false)
    expect(isRecentWorkspaceReturnTab(returned, 'finance', now + 60001)).toBe(false)
    expect(isRecentWorkspaceReturnTab(returned, 'finance', now - 1)).toBe(false)
  })
})
