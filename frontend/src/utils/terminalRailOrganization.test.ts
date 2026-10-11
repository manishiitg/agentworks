import { describe, expect, it } from 'vitest'
import type { TerminalSnapshot } from '../services/api-types'
import {
  canonicalTerminalRailSelection,
  hiddenSelectedTerminalRailGroup,
  organizeTerminalRail,
  terminalRailLogicalKey,
  terminalRailTitle,
  terminalRailVisualKind,
} from './terminalRailOrganization'

const terminal = (id: string, overrides: Partial<TerminalSnapshot> = {}): TerminalSnapshot => ({
  terminal_id: id,
  session_id: 'session-1',
  content: '',
  rows: [],
  chunk_index: 1,
  active: false,
  state: 'completed',
  status: { provider_label: 'Claude Code' },
  created_at: '2026-07-14T00:00:00Z',
  updated_at: '2026-07-14T00:01:00Z',
  ...overrides,
})

const organize = (terminals: TerminalSnapshot[]) => organizeTerminalRail(terminals, {
  getState: item => item.state || (item.active ? 'running' : 'completed'),
  isMainAgent: item => item.execution_kind === 'main_agent',
})

describe('terminal rail organization', () => {
  it('keeps the main agent out of logical task groups', () => {
    const groups = organize([
      terminal('main', { execution_kind: 'main_agent' }),
      terminal('step', { execution_kind: 'workflow_step', step_id: 'collect-price', step_name: 'Collect Price' }),
    ])

    expect(groups).toHaveLength(1)
    expect(groups[0].title).toBe('Collect Price')
  })

  it('collapses repeated attempts of the same workflow step', () => {
    const first = terminal('attempt-1', {
      execution_kind: 'workflow_step',
      step_id: 'collect-insider',
      step_name: 'Collect Insider Activity',
      step_attempt: 1,
    })
    const second = terminal('attempt-2', {
      execution_kind: 'workflow_step',
      step_id: 'collect-insider',
      step_name: 'Collect Insider Activity',
      step_attempt: 2,
      updated_at: '2026-07-14T00:02:00Z',
    })

    const groups = organize([first, second])

    expect(groups).toHaveLength(1)
    expect(groups[0].terminals.map(item => item.terminal_id)).toEqual(['attempt-2', 'attempt-1'])
    expect(groups[0].representative.terminal_id).toBe('attempt-2')
  })

  it('keeps a still-running earlier attempt as the group representative', () => {
    const running = terminal('attempt-1', {
      active: true,
      state: 'running',
      execution_kind: 'workflow_step',
      step_id: 'collect-social',
      step_name: 'Collect Social Momentum',
      step_attempt: 1,
    })
    const completed = terminal('attempt-2', {
      execution_kind: 'workflow_step',
      step_id: 'collect-social',
      step_name: 'Collect Social Momentum',
      step_attempt: 2,
      updated_at: '2026-07-14T00:02:00Z',
    })

    const groups = organize([running, completed])

    expect(groups[0].representative.terminal_id).toBe('attempt-1')
    expect(groups[0].terminals).toHaveLength(2)
    expect(groups[0].section).toBe('active')
  })

  it('promotes a restarted attempt of a stopped step into Active', () => {
    const stopped = terminal('stopped-attempt', {
      active: false,
      state: 'failed',
      execution_kind: 'workflow_step',
      step_id: 'survey-app-and-refresh-knowledge',
      step_name: 'Survey App and Refresh Knowledge',
      step_attempt: 1,
    })
    const restarted = terminal('restarted-attempt', {
      active: true,
      state: 'running',
      execution_kind: 'workflow_step',
      step_id: 'survey-app-and-refresh-knowledge',
      step_name: 'Survey App and Refresh Knowledge',
      step_attempt: 2,
      updated_at: '2026-07-14T00:02:00Z',
    })

    const groups = organize([stopped, restarted])

    expect(groups).toHaveLength(1)
    expect(groups[0].section).toBe('active')
    expect(groups[0].representative.terminal_id).toBe('restarted-attempt')
  })

  it('groups agent turns under their owning step', () => {
    const first = terminal('turn-1', {
      step_type: 'agent',
      step_id: 'agent-load',
      parent_step_id: 'score-and-plan',
      agent_name: 'agent-load',
    })
    const second = terminal('turn-2', {
      step_type: 'agent',
      step_id: 'agent-validate',
      parent_step_id: 'score-and-plan',
      agent_name: 'agent-validate',
    })

    expect(terminalRailLogicalKey(first)).toBe(terminalRailLogicalKey(second))
    expect(terminalRailTitle(first)).toBe('Score and plan sequence')
    expect(organize([first, second])).toHaveLength(1)
  })

  it('keeps sibling agents separate under the same manager', () => {
    const calc = terminal('calc', {
      step_type: 'agent',
      step_id: 'calc-task',
      parent_step_id: 'nested-manager',
      agent_name: 'agent-calc-task',
      step_name: 'Nested Calc Task',
    })
    const word = terminal('word', {
      step_type: 'agent',
      step_id: 'word-task',
      parent_step_id: 'nested-manager',
      agent_name: 'agent-word-task',
      step_name: 'Nested Word Task',
    })

    const groups = organize([calc, word])

    expect(groups).toHaveLength(2)
    expect(groups.map(group => group.title)).toEqual(['Nested Calc Task', 'Nested Word Task'])
  })

  it('collapses direct and orchestrated executions of the same logical child step', () => {
    const direct = terminal('direct-calc', {
      execution_kind: 'workflow_step',
      step_type: 'regular',
      step_execution_mode: 'agentic',
      step_id: 'calc-task',
      parent_step_id: 'nested-manager',
      step_name: 'Nested Calc Task',
    })
    const orchestrated = terminal('orchestrated-calc', {
      execution_kind: 'agent',
      step_type: 'agent',
      step_id: 'calc-task',
      parent_step_id: 'exec-nested-manager-123',
      step_name: 'Nested Calc Task',
      updated_at: '2026-07-14T00:02:00Z',
    })

    const groups = organize([direct, orchestrated])

    expect(groups).toHaveLength(1)
    expect(groups[0].terminals).toHaveLength(2)
    expect(groups[0].representative.terminal_id).toBe('orchestrated-calc')
  })

  it('assigns a distinct visual kind to every supported step category', () => {
    expect(terminalRailVisualKind(terminal('orchestrator', {
      execution_kind: 'orchestrator',
    }))).toBe('orchestrator')
    expect(terminalRailVisualKind(terminal('legacy-orchestrator', {
      execution_kind: 'workflow_step',
      step_type: 'todo_task',
    }))).toBe('orchestrator')
    expect(terminalRailVisualKind(terminal('session-1:workflow-full-run-1', {
      execution_kind: 'sub_agent',
      execution_id: 'workflow-full-run-1',
      agent_name: 'Full Workflow Execution',
    }))).toBe('orchestrator')
    expect(terminalRailVisualKind(terminal('sub-agent', {
      execution_kind: 'sub_agent',
    }))).toBe('sub-agent')
    expect(terminalRailVisualKind(terminal('sequence', {
      execution_kind: 'agent',
      step_type: 'agent',
    }))).toBe('agent')
    expect(terminalRailVisualKind(terminal('legacy-sequence', {
      execution_kind: 'sub_agent',
      step_type: 'regular',
      agent_name: 'agent-word-task',
    }))).toBe('agent')
    expect(terminalRailVisualKind(terminal('regular-compat-sequence', {
      execution_kind: 'workflow_step',
      step_type: 'regular',
      step_execution_mode: 'agentic',
      step_id: 'prepare-test-fixtures',
      parent_step_id: 'main_agent:session-1',
    }))).toBe('agent')
    expect(terminalRailVisualKind(terminal('nested-sequence', {
      execution_kind: 'agent',
      step_type: 'agent',
      step_id: 'child-sequence',
      parent_step_id: 'nested-manager',
    }))).toBe('sub-agent')
    expect(terminalRailVisualKind(terminal('standalone-sequence-turn', {
      execution_kind: 'agent',
      step_type: 'agent',
      step_id: 'sequence-type-probe',
      parent_step_id: 'sequence-type-probe',
    }))).toBe('agent')
    expect(terminalRailVisualKind(terminal('route', {
      execution_kind: 'router',
      step_type: 'routing',
    }))).toBe('routing')
    expect(terminalRailVisualKind(terminal('script', {
      execution_kind: 'scripted_step',
      step_execution_mode: 'scripted',
    }))).toBe('scripted')
    expect(terminalRailVisualKind(terminal('evaluation', {
      execution_kind: 'background_agent',
      step_id: 'eval-engagement-actions',
      step_name: 'Engagement actions met minimums',
      parent_step_id: 'step-workflow-router',
    }))).toBe('evaluation')
    expect(terminalRailVisualKind(terminal('reviewer', {
      execution_kind: 'background_agent',
      agent_name: 'Pulse review: bug review',
      step_id: 'pulse-review-bug-review',
    }))).toBe('reviewer')
    expect(terminalRailVisualKind(terminal('tmux'))).toBe('terminal')
  })

  it('merges a predefined-route lifecycle wrapper into the real child transcript', () => {
    const wrapper = terminal('session-1:todo-sub-word-task-sub-word-task-todo-id-123', {
      active: true,
      state: 'running',
      execution_kind: 'background_agent',
      execution_id: 'todo-sub-word-task-sub-word-task-todo-id-123',
      step_id: 'sub-word-task-todo-id',
      step_name: 'Nested Word Probe (Sub Word Task Todo ID)',
      parent_step_id: 'nested-manager',
      updated_at: '2026-07-14T00:03:00Z',
    })
    const transcript = terminal('session-1:workflow-step:todo-sub-word-task-sub-word-task-todo-id-123:word-task', {
      state: 'completed',
      execution_kind: 'workflow_step',
      execution_id: 'workflow-step:exec-word-task-123:word-task',
      step_id: 'word-task',
      step_name: 'Nested Word Task',
      parent_step_id: 'nested-manager',
      updated_at: '2026-07-14T00:02:00Z',
    })

    const groups = organize([wrapper, transcript])

    expect(groups).toHaveLength(1)
    expect(groups[0].title).toBe('Nested Word Task')
    expect(groups[0].representative.terminal_id).toBe(transcript.terminal_id)
    expect(groups[0].section).toBe('workflow')
    expect(groups[0].terminals).toHaveLength(1)
    expect(groups[0].members).toHaveLength(2)
  })

  it('does not merge similarly named agents from different sessions', () => {
    const wrapper = terminal('session-1:todo-sub-word-task-sub-word-task-todo-id-123', {
      execution_id: 'todo-sub-word-task-sub-word-task-todo-id-123',
      step_id: 'sub-word-task-todo-id',
      session_id: 'session-1',
    })
    const transcript = terminal('session-2:workflow-step:exec-word-task-123:word-task', {
      execution_kind: 'workflow_step',
      step_id: 'word-task',
      step_name: 'Nested Word Task',
      session_id: 'session-2',
    })

    expect(organize([wrapper, transcript])).toHaveLength(2)
  })

  it('merges an evaluation lifecycle record into its real transcript', () => {
    const transcript = terminal('session-1:workflow-step:workflow-full-1:eval-engagement-actions', {
      execution_kind: 'background_agent',
      execution_id: 'workflow-full-1',
      step_id: 'eval-engagement-actions',
      step_name: 'Engagement actions met minimums',
      agent_name: 'step-1-execution-engagement-actions-met-minimums',
      display_title: 'linkedin -> step-1-execution-engagement-actions-met-minimums',
    })
    const lifecycle = terminal('session-1:workflow-full-1-step-0-attempt-1', {
      execution_kind: 'background_agent',
      execution_id: 'workflow-full-1-step-0-attempt-1',
      agent_name: 'step-1-execution-engagement-actions-met-minimums',
      display_title: 'linkedin -> step-1-execution-engagement-actions-met-minimums',
      step_id: undefined,
      updated_at: '2026-07-14T00:02:00Z',
    })
    const fullWorkflow = terminal('session-1:workflow-full-1', {
      execution_kind: 'background_agent',
      execution_id: 'workflow-full-1',
      agent_name: 'Full Workflow Execution',
      display_title: 'linkedin -> Full Workflow Execution',
      step_id: undefined,
    })

    const groups = organize([lifecycle, transcript, fullWorkflow])

    expect(groups).toHaveLength(1)
    const evaluation = groups.find(group => group.key === 'step:eval-engagement-actions')
    expect(evaluation?.title).toBe('Engagement actions met minimums')
    expect(evaluation?.representative.terminal_id).toBe(transcript.terminal_id)
    expect(evaluation?.terminals).toHaveLength(1)
    expect(evaluation?.members).toHaveLength(2)
    expect(groups.some(group => group.title === 'Full Workflow Execution')).toBe(false)
  })

  it('merges a differently named background lifecycle root into its one real transcript', () => {
    const lifecycle = terminal('session-1:review-plan-12345', {
      session_id: 'session-1',
      owner_id: 'review-plan-12345',
      execution_id: 'review-plan-12345',
      execution_kind: 'sub_agent',
      agent_name: 'Review Workflow Plan',
      display_title: 'Review Workflow Plan',
      step_id: undefined,
    })
    const transcript = terminal('session-1:workflow-step:review-plan-12345:review-plan', {
      session_id: 'session-1',
      owner_id: 'workflow-step:review-plan-12345:review-plan',
      execution_id: 'review-plan-12345',
      execution_kind: 'workflow_step',
      step_id: 'review-plan',
      step_name: 'Review plan',
      agent_name: 'Review Plan Agent',
    })

    const groups = organize([lifecycle, transcript])

    expect(groups).toHaveLength(1)
    expect(groups[0].title).toBe('Review plan')
    expect(groups[0].representative.terminal_id).toBe(transcript.terminal_id)
    expect(groups[0].terminals).toEqual([transcript])
    expect(groups[0].members).toHaveLength(2)
    expect(canonicalTerminalRailSelection(groups, lifecycle)).toBe(transcript)
    expect(canonicalTerminalRailSelection(groups, transcript)).toBe(transcript)
  })

  it('keeps a lifecycle root when it owns several concrete child transcripts', () => {
    const lifecycle = terminal('session-1:review-bundle-1', {
      session_id: 'session-1',
      owner_id: 'review-bundle-1',
      execution_id: 'review-bundle-1',
      execution_kind: 'sub_agent',
      agent_name: 'Review bundle',
      step_id: undefined,
    })
    const children = ['plan', 'artifacts'].map(stepID => terminal(
      `session-1:workflow-step:review-bundle-1:${stepID}`,
      {
        session_id: 'session-1',
        owner_id: `workflow-step:review-bundle-1:${stepID}`,
        execution_id: 'review-bundle-1',
        execution_kind: 'workflow_step',
        step_id: stepID,
        step_name: `Review ${stepID}`,
      },
    ))

    const groups = organize([lifecycle, ...children])

    expect(groups).toHaveLength(3)
    expect(groups.some(group => group.title === 'Review bundle')).toBe(true)
  })

  it('groups Pulse reviewer retries while excluding raw turn companions from attempt counts', () => {
    const firstRoot = terminal('session-1:pulse-review-bug-1', {
      execution_kind: 'background_agent',
      execution_id: 'pulse-review-bug-1',
      agent_name: 'Pulse reviewer: pulse review bug_review 2026 07 26',
      display_title: 'linkedin -> Pulse reviewer: pulse review bug_review 2026 07 26',
    })
    const firstTurn = terminal('session-1:pulse-review-bug-1:turn-1', {
      execution_kind: 'background_agent',
      execution_id: 'pulse-review-bug-1',
      display_title: 'linkedin -> Pulse review pulse review bug review 2026 07 26 123',
    })
    const retryRoot = terminal('session-1:pulse-review-bug-2', {
      active: true,
      state: 'running',
      execution_kind: 'background_agent',
      execution_id: 'pulse-review-bug-2',
      display_title: 'linkedin -> Pulse review pulse review bug review 2026 07 26 456',
      updated_at: '2026-07-14T00:03:00Z',
    })
    const retryTurn = terminal('session-1:pulse-review-bug-2:turn-2', {
      active: true,
      state: 'running',
      execution_kind: 'background_agent',
      execution_id: 'pulse-review-bug-2',
      display_title: 'linkedin -> Pulse review pulse review bug review 2026 07 26 456',
      updated_at: '2026-07-14T00:03:00Z',
    })

    const groups = organize([firstRoot, firstTurn, retryRoot, retryTurn])

    expect(groups).toHaveLength(1)
    expect(groups[0].title).toBe('Bug review')
    expect(groups[0].section).toBe('active')
    expect(groups[0].terminals).toHaveLength(2)
    expect(groups[0].members).toHaveLength(4)
    expect(groups[0].representative.terminal_id).toBe(retryRoot.terminal_id)
  })

  it('uses the owning Pulse module when a nested reviewer has the wrong child label', () => {
    const lifecycle = terminal('session-1:pulse-review-pulse-review-eval-health-2026-07-26-123', {
      execution_kind: 'background_agent',
      execution_id: 'pulse-review-pulse-review-eval-health-2026-07-26-123',
      agent_name: 'Pulse review: evaluation health',
      display_title: 'linkedin -> Pulse review: evaluation health',
      updated_at: '2026-07-14T00:03:00Z',
    })
    const transcript = terminal(
      'session-1:workflow-step:pulse-review-pulse-review-eval-health-2026-07-26-123:pulse-reviewer-pulse-review-bug-review-456',
      {
        execution_kind: 'background_agent',
        execution_id: 'pulse-reviewer-pulse-review-bug-review-456',
        owner_id: 'workflow-step:pulse-review-pulse-review-eval-health-2026-07-26-123:pulse-reviewer-pulse-review-bug-review-456',
        parent_step_id: 'pulse-review-pulse-review-eval-health-2026-07-26-123',
        step_id: 'pulse-reviewer-pulse-review-bug-review-456',
        agent_name: 'Pulse reviewer: pulse review bug review 2026 07 26',
        display_title: 'linkedin -> Pulse reviewer: pulse review bug review 2026 07 26',
        content: 'Evaluation findings',
        rows: [{ kind: 'assistant', text: 'Evaluation findings' }],
        updated_at: '2026-07-14T00:02:00Z',
      },
    )

    expect(terminalRailTitle(transcript)).toBe('Evaluation health')
    expect(terminalRailVisualKind(transcript)).toBe('evaluation')

    const groups = organize([lifecycle, transcript])

    expect(groups).toHaveLength(1)
    expect(groups[0].title).toBe('Evaluation health')
    expect(groups[0].representative.terminal_id).toBe(transcript.terminal_id)
    expect(groups[0].terminals).toEqual([transcript])
    expect(groups[0].members).toHaveLength(2)
  })

  it('does not merge an ambiguous step-less lifecycle record', () => {
    const lifecycle = terminal('session-1:lifecycle', {
      execution_kind: 'background_agent',
      agent_name: 'Repeated evaluator',
      step_id: undefined,
    })
    const first = terminal('session-1:workflow-step:first', {
      execution_kind: 'background_agent',
      step_id: 'first',
      agent_name: 'Repeated evaluator',
    })
    const second = terminal('session-1:workflow-step:second', {
      execution_kind: 'background_agent',
      step_id: 'second',
      agent_name: 'Repeated evaluator',
    })

    expect(organize([lifecycle, first, second])).toHaveLength(3)
  })

  it('presents the LinkedIn workflow run as nine agents instead of eleven runtime records', () => {
    const workflowSteps = [
      ['step-resolve-mode-route', 'Resolve Run Mode -> Route'],
      ['step-engagement-scan', 'LinkedIn Engagement: Scan for Targets'],
      ['step-engagement-comment', 'Post Comments'],
      ['step-engagement-connect', 'Send Connection Requests'],
      ['step-engagement-reply-own', 'Reply to Comments'],
      ['step-end', 'Workflow Complete'],
    ].map(([stepID, stepName], index) => terminal(`session-1:workflow-step:workflow-full-1:${stepID}`, {
      execution_kind: 'workflow_step',
      step_type: 'regular',
      step_id: stepID,
      step_name: stepName,
      step_index: index,
    }))
    const evaluationPairs = [
      ['eval-engagement-actions', 'Engagement actions met minimums', 'step-1-execution-engagement-actions-met-minimums'],
      ['eval-db-schema-integrity', 'DB schema + merge-rule integrity', 'step-2-execution-db-schema-merge-rule-integrity'],
    ].flatMap(([stepID, stepName, agentName], index) => [
      terminal(`session-1:workflow-step:workflow-full-1:${stepID}`, {
        execution_kind: 'background_agent',
        execution_id: 'workflow-full-1',
        step_id: stepID,
        step_name: stepName,
        agent_name: agentName,
        step_index: 6 + index,
      }),
      terminal(`session-1:workflow-full-1-step-${index}-attempt-1`, {
        execution_kind: 'background_agent',
        execution_id: `workflow-full-1-step-${index}-attempt-1`,
        agent_name: agentName,
        step_id: undefined,
      }),
    ])
    const fullWorkflow = terminal('session-1:workflow-full-1', {
      execution_kind: 'background_agent',
      execution_id: 'workflow-full-1',
      agent_name: 'Full Workflow Execution',
      step_id: undefined,
    })

    const groups = organize([...workflowSteps, ...evaluationPairs, fullWorkflow])

    expect(groups).toHaveLength(8)
    expect(groups.map(group => group.title)).toEqual(expect.arrayContaining([
      'Resolve Run Mode > Route',
      'LinkedIn Engagement: Scan for Targets',
      'Post Comments',
      'Send Connection Requests',
      'Reply to Comments',
      'Workflow Complete',
      'Engagement actions met minimums',
      'DB schema + merge rule integrity',
    ]))
    expect(groups.filter(group => group.title === 'Engagement actions met minimums')).toHaveLength(1)
    expect(groups.filter(group => group.title === 'DB schema + merge rule integrity')).toHaveLength(1)
    expect(groups.some(group => group.title === 'Full Workflow Execution')).toBe(false)
  })

  it('puts live, failed, workflow, and reviewer tasks in distinct sections', () => {
    const groups = organize([
      terminal('running', { active: true, state: 'running', step_id: 'collect-price', step_name: 'Collect Price' }),
      terminal('failed', { state: 'failed', step_id: 'delivery', step_name: 'Deliver Briefing' }),
      terminal('done', { step_id: 'score', step_name: 'Score Ideas' }),
      terminal('review', { execution_kind: 'background_agent', agent_name: 'Evaluation Health Reviewer' }),
      terminal('underscore-review', { execution_kind: 'background_agent', agent_name: 'learning_health' }),
    ])

    expect(Object.fromEntries(groups.map(group => [group.title, group.section]))).toEqual({
      'Collect Price': 'active',
      'Deliver Briefing': 'attention',
      'Score Ideas': 'workflow',
      'Evaluation Health Reviewer': 'review',
      'Learning health': 'review',
    })
  })

  it('identifies only the selected completed child hidden by the active filter', () => {
    const active = terminal('active', {
      active: true,
      state: 'running',
      step_id: 'collect-price',
      step_name: 'Collect Price',
    })
    const selectedDone = terminal('selected-done', {
      step_id: 'score',
      step_name: 'Score Ideas',
      tmux_session: 'tmux-score',
    })
    const otherDone = terminal('other-done', {
      step_id: 'deliver',
      step_name: 'Deliver Briefing',
    })
    const groups = organize([active, selectedDone, otherDone])
    const visible = groups.filter(group => group.section === 'active')

    expect(hiddenSelectedTerminalRailGroup(groups, visible, selectedDone)?.title).toBe('Score Ideas')
    expect(hiddenSelectedTerminalRailGroup(groups, visible, otherDone)?.title).toBe('Deliver Briefing')
    expect(hiddenSelectedTerminalRailGroup(groups, groups, selectedDone)).toBeNull()
  })
})
