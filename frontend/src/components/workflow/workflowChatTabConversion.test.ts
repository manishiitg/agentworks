import { describe, expect, it } from 'vitest'
import type { ChatTab } from '../../stores/useChatStore'
import { userInteractiveContinuationFlag } from '../../utils/chatSubmitHelpers'
import {
  convertObservedWorkflowTabToInteractive,
  isMisclassifiedRestoredWorkflowChat,
} from './workflowChatTabConversion'

function observedTab(overrides: Partial<ChatTab> = {}): ChatTab {
  return {
    tabId: 'tab-scheduled',
    sessionId: 'schedule-cron--42eca39a_1785810615371091000',
    name: 'Daily Pulse',
    isStreaming: false,
    isCompleted: true,
    hasRunningBgAgents: false,
    isSyntheticTurn: false,
    canSteer: false,
    hideToolCalls: false,
    viewMode: 'tree',
    config: {} as ChatTab['config'],
    createdAt: 1,
    lastViewedEventCount: 0,
    lastViewedEventCounts: {} as ChatTab['lastViewedEventCounts'],
    metadata: {
      mode: 'workflow',
      phaseId: 'workflow-builder',
      phaseName: 'Daily Pulse',
      presetQueryId: 'latency-report',
      isViewOnly: true,
      isScheduledRun: true,
      scheduledJobName: 'Daily Pulse',
      readOnlyRestoredAt: 123,
    },
    ...overrides,
  }
}

describe('convertObservedWorkflowTabToInteractive', () => {
  it('keeps the scheduled conversation session instead of forking it', () => {
    const original = observedTab()
    const converted = convertObservedWorkflowTabToInteractive(original)

    expect(converted.sessionId).toBe(original.sessionId)
    expect(converted.tabId).toBe(original.tabId)
    expect(converted.name).toBe('Automation Builder')
    expect(converted.metadata).toMatchObject({
      mode: 'workflow',
      phaseId: 'workflow-builder',
      phaseName: 'Automation Builder',
      presetQueryId: 'latency-report',
      isViewOnly: false,
      isScheduledRun: false,
      isBotRun: false,
      userInteractiveContinuation: true,
    })
    expect(converted.metadata?.scheduledJobName).toBeUndefined()
    expect(converted.metadata?.readOnlyRestoredAt).toBeUndefined()
    expect(userInteractiveContinuationFlag(converted)).toBe(true)
    expect(original.metadata?.isViewOnly).toBe(true)
  })

  it('keeps a bot conversation session and clears bot-only metadata', () => {
    const original = observedTab({
      sessionId: 'bot-whatsapp--abc',
      metadata: {
        mode: 'workflow',
        isViewOnly: true,
        isBotRun: true,
        botPlatform: 'WhatsApp',
      },
    })
    const converted = convertObservedWorkflowTabToInteractive(original)

    expect(converted.sessionId).toBe('bot-whatsapp--abc')
    expect(converted.metadata?.isBotRun).toBe(false)
    expect(converted.metadata?.botPlatform).toBeUndefined()
  })
})

describe('isMisclassifiedRestoredWorkflowChat', () => {
  it('recognizes an interactive restore that retained schedule metadata', () => {
    expect(isMisclassifiedRestoredWorkflowChat(observedTab({
      config: { restoredConversationPath: 'chat_history/session.json' } as ChatTab['config'],
    }))).toBe(true)
  })

  it('does not reinterpret a legitimate read-only schedule transcript', () => {
    expect(isMisclassifiedRestoredWorkflowChat(observedTab())).toBe(false)
  })
})
