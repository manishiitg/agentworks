import { describe, expect, it } from 'vitest'
import type { ReportHumanInput } from '../services/api-types'
import type { ChatTab } from '../stores/useChatStore'
import {
  buildReportHumanInputChatMessage,
  buildReportHumanInputDelegatedActionMessage,
} from './reportHumanInputChat'
import { selectWorkspacePaneWorkflowTab } from './workspacePaneChat'

function tab(tabId: string, overrides: Partial<ChatTab> = {}): ChatTab {
  return {
    tabId,
    name: tabId,
    sessionId: `${tabId}-session`,
    isStreaming: false,
    isCompleted: false,
    hasRunningBgAgents: false,
    isSyntheticTurn: false,
    canSteer: false,
    hideToolCalls: true,
    viewMode: 'terminal',
    config: {
      inputText: '',
      useCodeExecutionMode: false,
      selectedServers: [],
      selectedSkills: [],
      selectedSecrets: [],
      llmConfig: { provider: 'codex-cli', model_id: 'gpt-5.6-sol' },
      fileContext: [],
      workflowContext: [],
      queuedMessages: [],
    },
    createdAt: 1,
    lastViewedEventCount: 0,
    lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', presetQueryId: 'workflow-one' },
    ...overrides,
  }
}

describe('Autopilot decision chat routing', () => {
  it('keeps a running schedule separate and chooses the interactive workflow chat', () => {
    const schedule = tab('schedule', {
      isStreaming: true,
      metadata: {
        mode: 'workflow',
        presetQueryId: 'workflow-one',
        isViewOnly: true,
        isScheduledRun: true,
      },
    })
    const chat = tab('chat', {
      metadata: { mode: 'workflow', presetQueryId: 'workflow-one', phaseId: 'workflow-builder' },
    })

    expect(selectWorkspacePaneWorkflowTab(
      { schedule, chat, pulse: tab('pulse', { metadata: { mode: 'workflow', presetQueryId: 'workflow-one', phaseId: 'workflow-builder', isExecutionRun: true } }) },
      'workflow-one',
    )?.tabId).toBe('chat')
  })

  it('prefers an idle interactive chat instead of stranding a pane action behind a running turn', () => {
    const idle = tab('idle', { createdAt: 20 })
    const running = tab('running', { isStreaming: true, createdAt: 10 })

    expect(selectWorkspacePaneWorkflowTab(
      { idle, running },
      'workflow-one',
      'idle',
    )?.tabId).toBe('idle')
  })

  it('includes decision context while making clear that chat must not answer it implicitly', () => {
    const input = {
      id: 'decision-one',
      workspace_path: 'Workflow/example',
      source: 'pulse',
      status: 'pending',
      priority: 'medium',
      question: 'How should I finish the job?',
      context: 'The previous cleanup did not reach the limit.',
      options: [{ id: 'archive', title: 'Archive older entries', description: 'Nothing is lost.' }],
      allow_free_text: true,
      evidence: 'builder/improve.html',
      created_at: '2026-08-04T08:22:00Z',
      updated_at: '2026-08-04T08:22:00Z',
    } as ReportHumanInput

    const message = buildReportHumanInputChatMessage(input, 'Workflow/example', 'Why is archiving safer?')

    expect(message).toContain('Do not submit, dismiss, or mark the decision handled yet')
    expect(message).toContain('call answer_human_input_request')
    expect(message).toContain('Decision ID: decision-one')
    expect(message).toContain('How should I finish the job?')
    expect(message).toContain('Archive older entries [option_id=archive] — Nothing is lost.')
    expect(message).toContain('My question:\nWhy is archiving safer?')
  })

  it('makes an explicit delegated action authorize a choice without allowing an invented unsafe action', () => {
    const input = {
      id: 'decision-one',
      workspace_path: 'Workflow/example',
      source: 'pulse',
      status: 'pending',
      priority: 'medium',
      question: 'How should I finish the job?',
      context: 'The previous cleanup did not reach the limit.',
      options: [{ id: 'archive', title: 'Archive older entries', description: 'Nothing is lost.' }],
      allow_free_text: true,
      evidence: 'builder/improve.html',
      created_at: '2026-08-04T08:22:00Z',
      updated_at: '2026-08-04T08:22:00Z',
    } as ReportHumanInput

    const message = buildReportHumanInputDelegatedActionMessage(input, 'Workflow/example')

    expect(message).toContain('I delegate this pending Autopilot decision to you.')
    expect(message).toContain('choose the best supported option')
    expect(message).toContain('do not invent one or take an unsafe action')
    expect(message).toContain('call answer_human_input_request')
    expect(message).toContain('apply the answer as the tool result describes')
    expect(message).toContain('Archive older entries [option_id=archive] — Nothing is lost.')
  })

  it('names the reviewer that created the decision', () => {
    const input = {
      id: 'ops-decision-one', workspace_path: 'Workflow/example', source: 'ops_review', status: 'pending',
      priority: 'medium', question: 'Replace the fixed orchestrator?', options: [], allow_free_text: true,
      created_at: '2026-08-19T08:22:00Z', updated_at: '2026-08-19T08:22:00Z',
    } as ReportHumanInput

    expect(buildReportHumanInputDelegatedActionMessage(input, 'Workflow/example'))
      .toContain('pending Technical Review decision')
  })
})
