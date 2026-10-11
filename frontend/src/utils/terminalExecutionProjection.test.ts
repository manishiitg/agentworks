import { describe, expect, it } from 'vitest'
import type {
  SessionExecutionTreeNode,
  SessionExecutionTreeResponse,
  TerminalSnapshot,
} from '../services/api-types'
import { projectExecutionTreeTerminals } from './terminalExecutionProjection'

const node = (
  executionID: string,
  overrides: Partial<SessionExecutionTreeNode> = {},
): SessionExecutionTreeNode => ({
  execution_id: executionID,
  session_id: 'session-1',
  kind: 'background_agent',
  name: executionID,
  status: 'running',
  started_at: '2026-08-04T16:00:00Z',
  ...overrides,
})

const tree = (children: SessionExecutionTreeNode[]): SessionExecutionTreeResponse => ({
  session_id: 'session-1',
  root: node('session:session-1', {
    kind: 'session_root',
    name: 'Session',
    children,
  }),
  summary: {
    session_id: 'session-1',
    session_status: 'running',
    display_status: 'busy',
    is_session_busy: true,
    running_count: 1,
    completed_count: 1,
    failed_count: 0,
    canceled_count: 0,
    has_running_main_agent: false,
    has_running_background_agents: true,
    has_running_tracked_executions: true,
  },
})

const terminal = (overrides: Partial<TerminalSnapshot> = {}): TerminalSnapshot => ({
  terminal_id: 'session-1:child-1',
  session_id: 'session-1',
  execution_id: 'child-1',
  execution_kind: 'background_agent',
  agent_name: 'Trending radar',
  content: '',
  rows: [],
  chunk_index: 1,
  active: false,
  state: 'completed',
  status: {},
  created_at: '2026-08-04T16:00:00Z',
  updated_at: '2026-08-04T16:00:01Z',
  ...overrides,
})

describe('live execution-tree terminal projection', () => {
  it('shows a running asynchronous child when no terminal exists yet', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('main:session-1', {
        kind: 'main_agent',
        status: 'completed',
        name: 'Route execution',
      }),
      node('child-1', {
        parent_execution_id: 'main:session-1',
        source: 'background_agent_registry',
        name: 'Execute trending radar',
      }),
    ]))

    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      execution_id: 'child-1',
      parent_execution_id: 'main:session-1',
      agent_name: 'Execute trending radar',
      active: true,
      state: 'running',
      execution_tree_placeholder: true,
    })
  })

  it('does not invent a terminal for event-stream activity', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('main:session-1', {
        kind: 'main_agent',
        status: 'completed',
        name: 'Main agent',
      }),
      node('tool-call-1', {
        parent_execution_id: 'main:session-1',
        source: 'event_stream',
        kind: 'tool',
        name: 'Call get_workflow_command_guidance',
      }),
    ]))

    expect(result).toEqual([])
  })

  it('allows event-stream activity to enrich an already-published terminal', () => {
    const retained = terminal()
    const result = projectExecutionTreeTerminals([retained], tree([
      node('child-1', {
        parent_execution_id: 'main:session-1',
        source: 'event_stream',
        name: 'Background review',
      }),
    ]))

    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      terminal_id: retained.terminal_id,
      active: true,
      state: 'running',
    })
    expect(result[0].execution_tree_placeholder).toBeUndefined()
  })

  it('replaces the placeholder identity with the real terminal without duplicating it', () => {
    const retained = terminal()
    const result = projectExecutionTreeTerminals([retained], tree([
      node('child-1', {
        parent_execution_id: 'orchestrator-1',
        name: 'Execute trending radar',
      }),
    ]))

    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      terminal_id: retained.terminal_id,
      parent_execution_id: 'orchestrator-1',
      active: true,
      state: 'running',
    })
    expect(result[0].execution_tree_placeholder).toBeUndefined()
  })

  it('uses the published parent step terminal for a live agent item', () => {
    const publishedStepTerminal = terminal({
      terminal_id: 'session-1:workflow-step:exec-discover-1:discover',
      execution_id: 'workflow-step:exec-discover-1:discover',
      owner_id: 'workflow-step:exec-discover-1:discover',
      active: true,
      state: 'running',
    })

    const result = projectExecutionTreeTerminals([publishedStepTerminal], tree([
      node('msgseq-discover-audit-123', {
        parent_execution_id: 'exec-discover-1',
        kind: 'agent_item',
        name: 'Audit candidate pool',
      }),
    ]))

    // The item shares its structured agent and event stream with the parent
    // workflow-step terminal. A second placeholder would have no terminal or
    // event endpoint to load.
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      terminal_id: publishedStepTerminal.terminal_id,
      execution_id: publishedStepTerminal.execution_id,
      active: true,
      state: 'running',
    })
    expect(result[0].execution_tree_placeholder).toBeUndefined()
  })

  it('does not create retained UI rows for completed children', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('child-1', { status: 'completed', completed_at: '2026-08-04T16:01:00Z' }),
    ]))

    expect(result).toEqual([])
  })

  // PLAT-107 acceptance 2: the rail must contain NO extra row for a self-parent
  // Finalizer — not merely a relabelled one. A phantom "Asynchronous child"
  // still hides real progress and makes a healthy run look stalled, which is
  // the reported defect. A self-parent edge means a sequential main turn was
  // misprojected, so it collapses into the main conversation entirely.
  it('discards a self-parent main turn instead of leaving a phantom rail row', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('pulse-finalizer-1', {
        parent_execution_id: 'pulse-finalizer-1',
        name: 'PULSE FINALIZER',
      }),
    ]))

    expect(result).toEqual([])
  })

  // Acceptance 6: malformed self-parent input is ignored even alongside a
  // genuine child, which must still be projected.
  it('ignores a self-parent node without suppressing a real sibling child', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('main:session-1', { kind: 'main_agent', name: 'Route execution' }),
      node('pulse-finalizer-1', {
        parent_execution_id: 'pulse-finalizer-1',
        name: 'PULSE FINALIZER',
      }),
      node('child-1', { parent_execution_id: 'main:session-1', name: 'Trending radar' }),
    ]))

    expect(result).toHaveLength(1)
    expect(result[0].execution_id).toBe('child-1')
    expect(result.some(t => t.display_meta?.includes('PULSE FINALIZER'))).toBe(false)
  })

  // PLAT-107: sequential Pulse messages are main-agent turns in the existing
  // conversation. They must never be projected as child terminals.
  it('keeps a sequential main-agent turn out of the rail', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('main:session-1', { kind: 'main_agent', name: 'PULSE FINALIZER' }),
    ]))

    expect(result).toEqual([])
  })

  // PLAT-107: kind alone was insufficient. An unclassified node with no
  // distinct parent used to fall through to a `background_agent` placeholder.
  it('does not invent a child for an unclassified node with no distinct parent', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('turn-000', { kind: '', name: 'PULSE FINALIZER' }),
    ]))

    expect(result).toEqual([])
  })

  it('still projects a genuine child that has a distinct parent', () => {
    const result = projectExecutionTreeTerminals([], tree([
      node('main:session-1', { kind: 'main_agent', name: 'Route execution' }),
      node('child-1', { parent_execution_id: 'main:session-1', name: 'Trending radar' }),
    ]))

    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      execution_id: 'child-1',
      display_meta: 'Child of Route execution',
      execution_tree_placeholder: true,
    })
  })
})
