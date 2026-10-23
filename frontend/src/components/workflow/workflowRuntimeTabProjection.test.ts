import { describe, expect, it } from 'vitest'
import type { RunningWorkflowInfo } from '../../services/api-types'
import type { ChatTab } from '../../stores/useChatStore'
import { convertObservedWorkflowTabToInteractive } from './workflowChatTabConversion'
import {
  reconcileWorkflowRuntimeTab,
  shouldCatchUpRunningWorkflowTranscript,
  shouldDisplayWorkflowTab,
  staleWorkflowTabIds,
  workflowTabDisplayName,
  workflowRuntimeTabProjection,
} from './workflowRuntimeTabProjection'

function runtime(overrides: Partial<RunningWorkflowInfo>): RunningWorkflowInfo {
  return {
    session_id: 'session-1',
    status: 'running',
    ...overrides,
  } as RunningWorkflowInfo
}

describe('workflowRuntimeTabProjection', () => {
  it('projects a running schedule beside Chat without auto-activating it', () => {
    const projected = workflowRuntimeTabProjection(runtime({
      session_id: 'schedule-manual--daily_123',
      triggered_by: 'cron',
      title: 'Daily execution',
    }), 'workflow-social')

    expect(projected).toEqual({
      name: 'Daily execution',
      metadata: {
        mode: 'workflow',
        presetQueryId: 'workflow-social',
        isViewOnly: true,
        isScheduledRun: true,
        scheduledJobName: 'Daily execution',
      },
      autoActivate: false,
    })
  })

  it('falls back to the generic "Schedule" label when no job name is available', () => {
    const projected = workflowRuntimeTabProjection(runtime({
      session_id: 'schedule-manual--unnamed_1',
      triggered_by: 'cron',
    }), 'workflow-social')

    expect(projected?.name).toBe('Schedule')
    expect(projected?.metadata.scheduledJobName).toBe('Schedule')
  })

  it('labels the toolbar one-off Autopilot lane as Manual Autopilot', () => {
    const projected = workflowRuntimeTabProjection(runtime({
      session_id: 'schedule-manual--manual-p_1788000820583762000',
      triggered_by: 'workflow_builder',
      title: 'Workflow Builder',
      phase_name: 'Workflow Builder',
    }), 'workflow-linkedin')

    expect(projected).toMatchObject({
      name: 'Manual Autopilot',
      metadata: {
        isViewOnly: true,
        isScheduledRun: true,
        scheduledJobName: 'Manual Autopilot',
      },
      autoActivate: false,
    })
  })

  it('keeps an interactive builder run eligible for automatic selection', () => {
    const projected = workflowRuntimeTabProjection(runtime({
      phase_id: 'workflow-builder',
      phase_name: 'Workflow Builder',
      triggered_by: 'user',
    }), 'workflow-social')

    expect(projected?.name).toBe('Automation Builder')
    expect(projected?.metadata.isViewOnly).toBeUndefined()
    expect(projected?.autoActivate).toBe(true)
  })

  it('does not auto-project bot lanes', () => {
    expect(workflowRuntimeTabProjection(runtime({
      session_id: 'bot-slack-123',
      triggered_by: 'bot-slack',
    }), 'workflow-social')).toBeNull()
  })

  it('does not turn a converted Schedule back into a read-only tab on later reconciliation ticks', () => {
    const observed = {
      tabId: 'schedule-tab',
      name: 'Schedule',
      sessionId: 'schedule-manual--daily_123',
      isStreaming: true,
      isCompleted: false,
      hasRunningBgAgents: false,
      isSyntheticTurn: false,
      canSteer: true,
      hideToolCalls: false,
      viewMode: 'tree',
      config: {} as ChatTab['config'],
      createdAt: 1,
      lastAccessedAt: 1,
      lastViewedEventCount: 0,
      lastViewedEventCounts: { micro: 0 },
      metadata: {
        mode: 'workflow' as const,
        presetQueryId: 'workflow-social',
        isViewOnly: true,
        isScheduledRun: true,
        scheduledJobName: 'Daily execution',
      },
    } satisfies ChatTab
    const projection = workflowRuntimeTabProjection(runtime({
      session_id: observed.sessionId,
      triggered_by: 'cron',
      title: 'Daily execution',
    }), 'workflow-social')

    expect(projection).not.toBeNull()
    const converted = convertObservedWorkflowTabToInteractive(observed)
    const firstTick = reconcileWorkflowRuntimeTab(converted, projection!)
    const secondTick = reconcileWorkflowRuntimeTab(firstTick, projection!)

    expect(secondTick.tabId).toBe(observed.tabId)
    expect(secondTick.sessionId).toBe(observed.sessionId)
    expect(secondTick.name).toBe('Automation Builder')
    expect(secondTick.metadata).toMatchObject({
      phaseId: 'workflow-builder',
      phaseName: 'Automation Builder',
      isViewOnly: false,
      isScheduledRun: false,
      userInteractiveContinuation: true,
    })
    expect(secondTick.metadata?.scheduledJobName).toBeUndefined()
  })

  it('continues refreshing ordinary Schedule tabs from runtime state', () => {
    const tab = {
      tabId: 'schedule-tab',
      name: 'Old name',
      sessionId: 'schedule-manual--daily_123',
      metadata: { mode: 'workflow' as const, presetQueryId: 'old-preset' },
    } as ChatTab
    const projection = workflowRuntimeTabProjection(runtime({
      session_id: 'schedule-manual--daily_123',
      triggered_by: 'cron',
      title: 'Daily execution',
    }), 'workflow-social')!

    const reconciled = reconcileWorkflowRuntimeTab(tab, projection)

    expect(reconciled.name).toBe('Daily execution')
    expect(reconciled.metadata).toMatchObject({
      presetQueryId: 'workflow-social',
      isViewOnly: true,
      isScheduledRun: true,
      scheduledJobName: 'Daily execution',
    })
  })

  it('preserves Builder identity when an active-session refresh omits phase_id', () => {
    const tab = {
      tabId: 'builder-chat',
      name: 'Automation Builder',
      sessionId: 'builder-session',
      metadata: {
        mode: 'workflow' as const,
        phaseId: 'workflow-builder',
        phaseName: 'Automation Builder',
        presetQueryId: 'workflow-social',
      },
    } as ChatTab
    const projection = workflowRuntimeTabProjection(runtime({
      session_id: 'builder-session',
      workspace_path: 'Workflow/social-media',
      // Retained live-input turns can be present in active sessions without
      // phase_id/phase_name even though the existing tab owns that identity.
    }), 'workflow-social')!

    const reconciled = reconcileWorkflowRuntimeTab(tab, projection)

    expect(reconciled.name).toBe('Automation Builder')
    expect(reconciled.metadata).toMatchObject({
      mode: 'workflow',
      phaseId: 'workflow-builder',
      phaseName: 'Automation Builder',
      presetQueryId: 'workflow-social',
    })
  })
})

describe('workflowTabDisplayName', () => {
  it('labels a tab-pinned Run conversation distinctly', () => {
    expect(workflowTabDisplayName({
      name: 'Automation Builder',
      sessionId: 'run-chat-session',
      metadata: { mode: 'workflow', phaseId: 'workflow-builder', workshopMode: 'run' },
    })).toBe('Run chat')
  })

  it('renders both current and persisted legacy Builder conversations as Chat', () => {
    for (const name of ['Automation Builder', 'Workflow Builder']) {
      expect(workflowTabDisplayName({
        name,
        sessionId: 'chat-session',
        metadata: { mode: 'workflow', phaseId: 'workflow-builder' },
      })).toBe('Chat')
    }
  })

  it('preserves names for schedule and run lanes', () => {
    expect(workflowTabDisplayName({
      name: 'full-run [default / iteration-0]',
      sessionId: 'schedule-cron--daily_1',
      metadata: { mode: 'workflow', isScheduledRun: true },
    })).toBe('full-run [default / iteration-0]')
  })

  it('repairs a persisted one-off Autopilot tab whose old title was Workflow Builder', () => {
    expect(workflowTabDisplayName({
      name: 'Workflow Builder',
      sessionId: 'schedule-manual--manual-p_1788000820583762000',
      metadata: { mode: 'workflow', isViewOnly: true, isScheduledRun: true },
    })).toBe('Manual Autopilot')
  })
})

describe('shouldCatchUpRunningWorkflowTranscript', () => {
  it('loads already-emitted events when a running tab appears in formatted mode', () => {
    expect(shouldCatchUpRunningWorkflowTranscript('formatted', 0)).toBe(true)
  })

  it('leaves a hydrated transcript and terminal restoration alone', () => {
    expect(shouldCatchUpRunningWorkflowTranscript('formatted', 1)).toBe(false)
    expect(shouldCatchUpRunningWorkflowTranscript('terminal', 0)).toBe(false)
  })
})

describe('shouldDisplayWorkflowTab', () => {
  const scheduleTab = (overrides: Partial<ChatTab>): ChatTab => ({
    tabId: 'schedule-tab', name: 'Daily execution', sessionId: 'schedule-session',
    isStreaming: false, isCompleted: false, hasRunningBgAgents: false, isSyntheticTurn: false,
    canSteer: false, hideToolCalls: true, viewMode: 'terminal', config: {} as ChatTab['config'],
    createdAt: 1, lastAccessedAt: 1, lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', presetQueryId: 'workflow-1', isViewOnly: true, isScheduledRun: true },
    ...overrides,
  })

  // Explicit product decision: a tab, once opened, is closed only by the
  // user -- never auto-hidden because its run finished or lost focus. This
  // was previously conditional (visible only while active/streaming/running
  // background agents); reverted on purpose, accepting that finished
  // Schedule tabs accumulate until manually closed.
  it('keeps a LIVE Schedule visible after the user switches to Chat', () => {
    expect(shouldDisplayWorkflowTab(scheduleTab({ isStreaming: true }), 'chat-tab')).toBe(true)
  })

  it('keeps a Schedule whose background agents are still running', () => {
    expect(shouldDisplayWorkflowTab(scheduleTab({ hasRunningBgAgents: true }), 'chat-tab')).toBe(true)
  })

  it('keeps the Schedule you are actually looking at, finished or not', () => {
    expect(shouldDisplayWorkflowTab(scheduleTab({}), 'schedule-tab')).toBe(true)
  })

  it('keeps a finished Schedule you are not looking at -- user closes it, not auto-hide', () => {
    expect(shouldDisplayWorkflowTab(scheduleTab({}), 'chat-tab')).toBe(true)
  })
})

describe('staleWorkflowTabIds', () => {
  const NOW = 1_000_000_000_000
  const hoursAgo = (hours: number) => NOW - hours * 60 * 60 * 1000

  const tab = (overrides: Partial<ChatTab>): ChatTab => ({
    tabId: 'tab-1', name: 'Daily execution', sessionId: 'session-1',
    isStreaming: false, isCompleted: false, hasRunningBgAgents: false, isSyntheticTurn: false,
    canSteer: false, hideToolCalls: true, viewMode: 'terminal', config: {} as ChatTab['config'],
    createdAt: hoursAgo(8), lastAccessedAt: hoursAgo(8), lastViewedEventCount: 0,
    lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', presetQueryId: 'workflow-1' },
    ...overrides,
  })

  it('closes a workflow tab left untouched past the six-hour threshold', () => {
    expect(staleWorkflowTabIds([tab({})], 'other-tab', NOW)).toEqual(['tab-1'])
  })

  it('keeps a tab that was viewed within the threshold', () => {
    const recent = tab({ lastAccessedAt: hoursAgo(5) })
    expect(staleWorkflowTabIds([recent], 'other-tab', NOW)).toEqual([])
  })

  it('measures idleness from the last view, not from when the tab was created', () => {
    const oldButRecentlyUsed = tab({ createdAt: hoursAgo(30), lastAccessedAt: hoursAgo(1) })
    expect(staleWorkflowTabIds([oldButRecentlyUsed], 'other-tab', NOW)).toEqual([])
  })

  it('never closes the tab the user is looking at, however old', () => {
    expect(staleWorkflowTabIds([tab({ lastAccessedAt: hoursAgo(72) })], 'tab-1', NOW)).toEqual([])
  })

  it('never closes a tab whose run is still streaming', () => {
    expect(staleWorkflowTabIds([tab({ isStreaming: true })], 'other-tab', NOW)).toEqual([])
  })

  it('never closes a tab whose background agents are still running', () => {
    expect(staleWorkflowTabIds([tab({ hasRunningBgAgents: true })], 'other-tab', NOW)).toEqual([])
  })

  it('leaves non-workflow tabs alone', () => {
    const chatTab = tab({ metadata: { mode: 'multi-agent' } })
    expect(staleWorkflowTabIds([chatTab], 'other-tab', NOW)).toEqual([])
  })

  it('falls back to createdAt when a tab has never been switched to', () => {
    const neverViewed = tab({ lastAccessedAt: undefined, createdAt: hoursAgo(7) })
    expect(staleWorkflowTabIds([neverViewed], 'other-tab', NOW)).toEqual(['tab-1'])
  })
})

// reusableScheduleTabId moved to utils/workflowTabResolution.ts; its tests
// live in utils/workflowTabResolution.test.ts.
