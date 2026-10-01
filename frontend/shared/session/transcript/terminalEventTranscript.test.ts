import { describe, it, expect } from 'vitest'
import { pairToolCalls, runActivity, toolErrorContextByEventID, selectTerminalEvents, buildTranscriptItems, collapseCompletedLifecycleStarts, internalTranscriptMessageTitle, isExecutionPromptTranscriptMessage, isInternalTranscriptMessage, shouldCollapseTranscriptUserMessage, type TranscriptItem } from './terminalEventTranscript'
import type { PollingEvent, TerminalSnapshot } from '../types'

// The rail is the hierarchy: every agent and sub-agent owns its own terminal.
// selectTerminalEvents is the contract between the rail and the transcript, and
// it fails SILENTLY when wrong — you get another agent's conversation rendered
// under this terminal, which looks like working software. These pin that,
// against the SAME owner-key derivation the (now-removed) tree used, not a
// narrower reimplementation of it.

function evt(partial: Partial<PollingEvent> & { id: string; session_id: string }): PollingEvent {
  return {
    type: 'agent_message',
    timestamp: '2026-07-25T10:00:00Z',
    ...partial,
  } as PollingEvent
}

function terminal(partial: Partial<TerminalSnapshot> & { session_id: string; owner_id: string }): TerminalSnapshot {
  return {
    terminal_id: `${partial.session_id}:${partial.owner_id}`,
    content: '',
    rows: [],
    chunk_index: 0,
    active: true,
    status: 'running',
    created_at: '',
    updated_at: '',
    ...partial,
  } as TerminalSnapshot
}

describe('internal transcript messages', () => {
  it('keeps automation notifications out of the person-authored chat stream', () => {
    const notification = evt({
      id: 'notice', session_id: 's1', type: 'user_message',
      data: { content: "[AUTO-NOTIFICATION] Agent 'Research prospects' completed — status=completed" } as never,
    })
    const person = evt({
      id: 'person', session_id: 's1', type: 'user_message',
      data: { content: 'Please review the results.' } as never,
    })

    expect(isInternalTranscriptMessage(notification)).toBe(true)
    expect(internalTranscriptMessageTitle(notification)).toBe('Research prospects · completed')
    expect(isInternalTranscriptMessage(person)).toBe(false)
  })
})

describe('execution prompt transcript messages', () => {
  it('distinguishes workflow task input from a human chat message', () => {
    const task = evt({
      id: 'task', session_id: 's1', type: 'user_message',
      data: { data: { content: 'Read the fixtures.', metadata: { source: 'execution_prompt' } } } as never,
    })
    const legacyTask = evt({
      id: 'legacy-task', session_id: 's1', type: 'user_message',
      data: { data: { content: 'Read the fixtures.', turn: 0, metadata: { current_step_id: 'collect-summary' } } } as never,
    })
    const human = evt({
      id: 'human', session_id: 's1', type: 'user_message',
      data: { data: { content: 'Please run the step.' } } as never,
    })

    expect(isExecutionPromptTranscriptMessage(task)).toBe(true)
    expect(isExecutionPromptTranscriptMessage(legacyTask)).toBe(true)
    expect(isExecutionPromptTranscriptMessage(human)).toBe(false)
  })
})

describe('long user messages', () => {
  it('collapses long task briefs but leaves ordinary chat messages alone', () => {
    expect(shouldCollapseTranscriptUserMessage('Can you check this?')).toBe(false)
    expect(shouldCollapseTranscriptUserMessage(Array.from({ length: 7 }, (_, index) => `line ${index}`).join('\n'))).toBe(true)
    expect(shouldCollapseTranscriptUserMessage('x'.repeat(481))).toBe(true)
  })
})

describe('selectTerminalEvents — owned terminal (workflow step, message-sequence item, ...)', () => {

  it('matches an event whose execution_id equals the terminal owner id', () => {
    const t = terminal({ session_id: 's1', owner_id: 'exec-1' })
    const events = [
      evt({ id: 'a', session_id: 's1', execution_id: 'exec-1' }),
      evt({ id: 'b', session_id: 's1', execution_id: 'exec-2' }),
    ]
    expect(selectTerminalEvents(events, t).map(e => e.id)).toEqual(['a'])
  })

  it('matches via the workflow-step composite id AND its short step-id alias', () => {
    // This is exactly the case that broke in production: a message-sequence
    // item terminal (owner_id = "msgseq-dummy-sequence-1-d-1-1784986266486292000")
    // whose agent events carry the PARENT step's execution id, not the item's
    // own id. getOwnedTerminalOwnerKeys derives a short alias from a
    // workflow-step composite id; matching on that is what makes this terminal
    // show its real conversation instead of falling back to an empty pane.
    const t = terminal({ session_id: 's1', owner_id: 'dummy-sequence-1' })
    const events = [
      evt({
        id: 'a', session_id: 's1',
        data: { data: { execution_id: 'workflow-step:exec-dummy-sequence-1-123:dummy-sequence-1' } } as never,
      }),
    ]
    expect(selectTerminalEvents(events, t).map(e => e.id)).toEqual(['a'])
  })

  it('does not leak a sibling sub-agent conversation into this terminal', () => {
    const t = terminal({ session_id: 's1', owner_id: 'exec-1' })
    const events = [
      evt({ id: 'mine', session_id: 's1', execution_id: 'exec-1' }),
      evt({ id: 'sibling', session_id: 's1', execution_id: 'exec-2' }),
    ]
    expect(selectTerminalEvents(events, t).map(e => e.id)).toEqual(['mine'])
  })

  it('keeps child workflow-step events out of their full-workflow parent transcript', () => {
    const parent = terminal({
      session_id: 's1',
      owner_id: 'workflow-full-1',
      execution_id: 'workflow-full-1',
      execution_kind: 'sub_agent',
    })
    const child = terminal({
      session_id: 's1',
      owner_id: 'workflow-step:workflow-full-1:eval-quality',
      execution_id: 'workflow-full-1',
      execution_kind: 'workflow_step',
      step_id: 'eval-quality',
    })
    const events = [
      evt({ id: 'parent', session_id: 's1', execution_id: 'workflow-full-1' }),
      evt({
        id: 'child',
        session_id: 's1',
        execution_id: 'workflow-full-1',
        data: {
          execution_id: 'workflow-full-1',
          metadata: {
            execution_id: 'workflow-step:workflow-full-1:eval-quality',
          },
        } as never,
      }),
    ]

    expect(selectTerminalEvents(events, parent, [parent, child]).map(e => e.id)).toEqual(['parent'])
    expect(selectTerminalEvents(events, child, [parent, child]).map(e => e.id)).toEqual(['child'])
  })

  it('matches via delegation_id / background_agent_id / agent_id, not just execution_id', () => {
    const t = terminal({ session_id: 's1', owner_id: 'delegation-9' })
    const events = [
      evt({ id: 'a', session_id: 's1', data: { delegation_id: 'delegation-9' } as never }),
    ]
    expect(selectTerminalEvents(events, t).map(e => e.id)).toEqual(['a'])
  })

  it('sorts chronologically so out-of-order arrivals do not read as scrambled', () => {
    const t = terminal({ session_id: 's1', owner_id: 'exec-1' })
    const events = [
      evt({ id: 'late', session_id: 's1', execution_id: 'exec-1', timestamp: '2026-07-25T10:00:05Z' }),
      evt({ id: 'early', session_id: 's1', execution_id: 'exec-1', timestamp: '2026-07-25T10:00:01Z' }),
    ]
    expect(selectTerminalEvents(events, t).map(e => e.id)).toEqual(['early', 'late'])
  })

  it('uses durable sequence before misleading timestamps', () => {
    const t = terminal({ session_id: 's1', owner_id: 'exec-1' })
    const events = [
      // Fixture uses tool_call_end: the rail hides lifecycle banners
      // (background_agent_completed included) since the 2026-09-22 minimal-rail
      // change, so a lifecycle type can no longer exercise ordering here.
      evt({
        id: 'completion', session_id: 's1', execution_id: 'exec-1',
        sequence: 12, timestamp: '2026-07-25T10:00:01Z', type: 'tool_call_end',
      }),
      evt({
        id: 'task-work', session_id: 's1', execution_id: 'exec-1',
        sequence: 11, timestamp: '2026-07-25T10:00:05Z', type: 'llm_generation_end',
      }),
    ]

    expect(selectTerminalEvents(events, t).map(e => e.id)).toEqual(['task-work', 'completion'])
  })

  it('returns the product main transcript when no terminal is supplied', () => {
    const events = [evt({ id: 'a', session_id: 's1', execution_id: 'exec-1' })]
    expect(selectTerminalEvents(events, terminal({ session_id: 's1', owner_id: '' }))).toEqual([])
    expect(selectTerminalEvents(events, null).map(event => event.id)).toEqual(['a'])
    expect(selectTerminalEvents(undefined, terminal({ session_id: 's1', owner_id: 'exec-1' }))).toEqual([])
  })

  it('keeps explicit child step output out of the product main transcript', () => {
    const events = [
      evt({ id: 'main', session_id: 's1', execution_id: 'main:s1', execution_kind: 'main_agent', type: 'llm_generation_end' }),
      evt({
        id: 'step', session_id: 's1', execution_id: 'workflow-step:run-1:review-measure', execution_kind: 'workflow_step',
        type: 'pre_validation_failed', data: { data: { metadata: { current_step_id: 'review-measure' } } } as never,
      }),
    ]
    expect(selectTerminalEvents(events, null).map(event => event.id)).toEqual(['main'])
  })
})

describe('selectTerminalEvents — main agent', () => {
  it.each([null, terminal({ session_id: 's1', owner_id: 'main:s1', execution_kind: 'main_agent' })])(
    'keeps pre-restart replies before new work when the server sequence restarts',
    (main) => {
      const oldReply = evt({
        id: 'old-queue-update', session_id: 's1', type: 'llm_generation_end',
        timestamp: '2026-09-05T22:58:37+05:30', sequence: 2400,
        data: { data: { content: 'That queue notification is from the old workflow.' } } as never,
      })
      const currentReply = evt({
        id: 'new-status', session_id: 's1', type: 'unified_completion',
        timestamp: '2026-09-06T00:17:35+05:30', sequence: 448,
        data: { data: { final_result: 'Still running in its final validation repair pass.' } } as never,
      })
      const currentTool = evt({
        id: 'new-tool', session_id: 's1', type: 'tool_call_end',
        timestamp: '2026-09-06T00:18:00+05:30', sequence: 460,
      })
      // Replayed history may arrive after live events and keep its old sequence.
      const ordered = selectTerminalEvents([currentReply, currentTool, oldReply], main)
      expect(ordered.map(event => event.id)).toEqual(['old-queue-update', 'new-status', 'new-tool'])
    },
  )

  it('keeps an optimistic user message once it is tagged with the active session', () => {
    const main = terminal({
      session_id: 's-live',
      owner_id: 'main:s-live',
      execution_kind: 'main_agent',
    })
    const message = evt({
      id: 'user-message-local',
      session_id: 's-live',
      type: 'user_message',
      data: { data: { content: 'Please continue.' } } as never,
    })

    expect(selectTerminalEvents([message], main)).toEqual([message])
  })
})

describe('selectTerminalEvents — main-agent terminal', () => {
  it('excludes events claimed by a sibling owned terminal when the sibling list is given', () => {
    const main = terminal({ session_id: 's1', owner_id: 'main:s1', execution_kind: 'main_agent' })
    const sub = terminal({ session_id: 's1', owner_id: 'exec-1' })
    const events = [
      evt({ id: 'mainEvt', session_id: 's1' }),
      evt({ id: 'subEvt', session_id: 's1', execution_id: 'exec-1' }),
    ]
    expect(selectTerminalEvents(events, main, [main, sub]).map(e => e.id)).toEqual(['mainEvt'])
  })

  it('is permissive (shows everything in-session) when no sibling list is supplied', () => {
    // Documented fallback: hiding a live turn because the sibling list was not
    // threaded through somewhere is worse than occasionally over-showing.
    const main = terminal({ session_id: 's1', owner_id: 'main:s1', execution_kind: 'main_agent' })
    const events = [
      evt({ id: 'mainEvt', session_id: 's1' }),
      evt({ id: 'subEvt', session_id: 's1', execution_id: 'exec-1' }),
    ]
    expect(selectTerminalEvents(events, main).map(e => e.id).sort()).toEqual(['mainEvt', 'subEvt'])
  })

  it('never shows another session entirely, sibling list or not', () => {
    const main = terminal({ session_id: 's1', owner_id: 'main:s1', execution_kind: 'main_agent' })
    const events = [evt({ id: 'other-session', session_id: 's2' })]
    expect(selectTerminalEvents(events, main)).toEqual([])
  })
})

describe('buildTranscriptItems', () => {
  it('collapses an adjacent durable echo of a frontend user message', () => {
    const durable = evt({
      id: 'restored-s1-4-user_message', session_id: 's1', type: 'user_message',
      data: { data: { content: 'check global secrets' } } as never,
    })
    const optimistic = evt({
      id: 'user-message-local-secret-check', session_id: 's1', type: 'user_message',
      data: { data: { content: 'check global secrets', metadata: {
        source: 'coding_agent_live_input',
        delivery_status: 'sent_to_cli',
        message_id: 'server-secret-check',
      } } } as never,
    })

    expect(buildTranscriptItems([durable, optimistic]).map(item => item.key)).toEqual([
      'user-message-local-secret-check',
    ])
    expect(buildTranscriptItems([optimistic, durable]).map(item => item.key)).toEqual([
      'user-message-local-secret-check',
    ])
  })

  it('keeps intentional repeated user messages and older identical turns', () => {
    const first = evt({
      id: 'user-message-local-1', session_id: 's1', type: 'user_message',
      data: { data: { content: 'check cli' } } as never,
    })
    const second = evt({
      id: 'user-message-local-2', session_id: 's1', type: 'user_message',
      data: { data: { content: 'check cli' } } as never,
    })
    const reply = evt({
      id: 'reply-1', session_id: 's1', type: 'llm_generation_end',
      data: { data: { content: 'CLI is available.' } } as never,
    })
    const laterDurable = evt({
      id: 'restored-s1-8-user_message', session_id: 's1', type: 'user_message',
      data: { data: { content: 'check cli' } } as never,
    })

    expect(buildTranscriptItems([first, second]).map(item => item.key)).toEqual([
      'user-message-local-1', 'user-message-local-2',
    ])
    expect(buildTranscriptItems([first, reply, laterDurable]).map(item => item.key)).toEqual([
      'user-message-local-1', 'reply-1', 'restored-s1-8-user_message',
    ])
  })

  it('replaces a finished background-agent start card with its one completion card', () => {
    const events = [
      evt({
        id: 'started', session_id: 's1', type: 'background_agent_started',
        data: { data: { agent_id: 'math-probe', name: 'Math Solver Probe' } } as never,
      }),
      evt({
        id: 'completed', session_id: 's1', type: 'background_agent_completed',
        data: { data: { agent_id: 'math-probe', name: 'Math Solver Probe', status: 'completed' } } as never,
      }),
    ]

    expect(collapseCompletedLifecycleStarts(events).map(event => event.id)).toEqual(['completed'])
    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['completed'])
  })

  it('keeps the background-agent start card until the agent finishes', () => {
    const events = [
      evt({
        id: 'started', session_id: 's1', type: 'background_agent_started',
        data: { data: { agent_id: 'math-probe', name: 'Math Solver Probe' } } as never,
      }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['started'])
  })

  it('collapses generic and duplicate agent starts into the richest lifecycle card', () => {
    const events = [
      evt({
        id: 'background-start', session_id: 's1', execution_id: 'review-cost-1',
        type: 'background_agent_started',
        data: { data: { agent_id: 'review-cost-1', name: 'Review Cost Review' } } as never,
      }),
      evt({
        id: 'sparse-agent-start', session_id: 's1', execution_id: 'review-cost-1',
        type: 'orchestrator_agent_start',
        data: { data: { agent_name: 'Review Cost Review', agent_type: 'workshop-background-task' } } as never,
      }),
      evt({
        id: 'rich-agent-start', session_id: 's1', execution_id: 'review-cost-1',
        type: 'orchestrator_agent_start',
        data: {
          data: {
            agent_name: 'Review Cost Review',
            agent_type: 'workshop-background-task',
            provider: 'claude-code',
            model_id: 'claude-opus-5',
          },
        } as never,
      }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['rich-agent-start'])
  })

  it('collapses a message-sequence background banner into its richer step card', () => {
    const events = [
      evt({
        id: 'sequence-background-start', session_id: 's1',
        execution_id: 'workflow-step:exec-check-cdp:check-cdp',
        type: 'background_agent_started',
        data: {
          data: {
            agent_id: 'msgseq-check-cdp-execute-and-verify-1785661200000000000',
            name: 'Message sequence item > Check CDP connection / Execute and verify (User message)',
          },
        } as never,
      }),
      evt({
        id: 'rich-sequence-step', session_id: 's1',
        execution_id: 'workflow-step:exec-check-cdp:check-cdp',
        type: 'orchestrator_agent_start',
        data: {
          data: {
            agent_name: 'message-sequence-check-cdp-execute-and-verify',
            agent_type: 'workshop-step-execution',
            provider: 'codex-cli',
            metadata: {
              agent_id: 'msgseq-check-cdp-execute-and-verify-1785661200000000000',
              message_sequence_item: true,
            },
          },
        } as never,
      }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['rich-sequence-step'])
  })

  it('drops generic message-sequence running and completed banners', () => {
    const events = [
      evt({
        id: 'sequence-running', session_id: 's1',
        type: 'background_agent_started',
        data: {
          data: {
            agent_id: 'msgseq-search-prove-and-repair-1',
            name: 'Message sequence item > Search / Prove and repair (User message)',
          },
        } as never,
      }),
      evt({
        id: 'real-step', session_id: 's1',
        type: 'orchestrator_agent_start',
        data: {
          data: {
            agent_name: 'message-sequence-search-prove-and-repair',
            agent_type: 'workshop-step-execution',
            metadata: { message_sequence_item: true },
          },
        } as never,
      }),
      evt({
        id: 'sequence-completed', session_id: 's1',
        type: 'background_agent_completed',
        data: {
          data: {
            agent_id: 'msgseq-search-final-gate-2',
            name: 'Message sequence item > Search / Final gate (Prevalidation)',
            status: 'completed',
          },
        } as never,
      }),
      evt({ id: 'prevalidation', session_id: 's1', type: 'pre_validation_completed' }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['real-step', 'prevalidation'])
  })

  it('keeps standalone background lifecycle cards', () => {
    const events = [
      evt({
        id: 'review-started', session_id: 's1',
        type: 'background_agent_started',
        data: { data: { agent_id: 'review-plan-1', name: 'Review workflow plan' } } as never,
      }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['review-started'])
  })

  it('replaces all aliased start events with one completion event', () => {
    const events = [
      evt({
        id: 'background-start', session_id: 's1', execution_id: 'review-cost-1',
        type: 'background_agent_started',
        data: { data: { agent_id: 'review-cost-1', name: 'Review Cost Review' } } as never,
      }),
      evt({
        id: 'agent-start', session_id: 's1', execution_id: 'review-cost-1',
        type: 'orchestrator_agent_start',
        data: { data: { agent_name: 'Review Cost Review', provider: 'claude-code' } } as never,
      }),
      evt({
        id: 'agent-end', session_id: 's1', execution_id: 'review-cost-1',
        type: 'orchestrator_agent_end',
        data: { data: { agent_name: 'Review Cost Review', success: true } } as never,
      }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['agent-end'])
  })

  it('does not collapse different sequence items sharing one execution', () => {
    const events = [
      evt({
        id: 'first', session_id: 's1', execution_id: 'sequence-1',
        type: 'orchestrator_agent_start',
        data: { data: { agent_name: 'Load Inputs' } } as never,
      }),
      evt({
        id: 'second', session_id: 's1', execution_id: 'sequence-1',
        type: 'orchestrator_agent_start',
        data: { data: { agent_name: 'Validate Result' } } as never,
      }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['first', 'second'])
  })

  it('does not let one completed agent hide a different live sibling', () => {
    const events = [
      evt({ id: 'a-start', session_id: 's1', type: 'background_agent_started', data: { data: { agent_id: 'a', name: 'A' } } as never }),
      evt({ id: 'b-start', session_id: 's1', type: 'background_agent_started', data: { data: { agent_id: 'b', name: 'B' } } as never }),
      evt({ id: 'a-end', session_id: 's1', type: 'background_agent_completed', data: { data: { agent_id: 'a', name: 'A' } } as never }),
    ]

    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['b-start', 'a-end'])
  })

  it('folds a consecutive tool run into one collapsible group', () => {
    const items = buildTranscriptItems([
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
      evt({ id: 't1', session_id: 's1', type: 'tool_call_start' }),
      evt({ id: 't2', session_id: 's1', type: 'tool_call_end' }),
      evt({ id: 't3', session_id: 's1', type: 'tool_call_start' }),
      evt({ id: 't4', session_id: 's1', type: 'tool_call_end' }),
      evt({ id: 'm2', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.kind)).toEqual(['event', 'tools', 'event'])
    expect((items[1] as Extract<TranscriptItem, { kind: 'tools' }>).toolCount).toBe(2)
  })

  it('folds a tool end whose start was not retained into the batch instead of a standalone card', () => {
    const items = buildTranscriptItems([
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
      evt({ id: 't2', session_id: 's1', type: 'tool_call_end', data: { data: { tool_call_id: 'call-9', tool_name: 'execute_shell_command' } } }),
      evt({ id: 'm2', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.kind)).toEqual(['event', 'tools', 'event'])
    expect((items[1] as Extract<TranscriptItem, { kind: 'tools' }>).toolCount).toBe(1)
  })

  it('folds an id-less tool end with no start into the batch too', () => {
    const items = buildTranscriptItems([
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
      evt({ id: 't2', session_id: 's1', type: 'tool_call_end', data: { data: { tool_name: 'execute_shell_command' } } }),
      evt({ id: 'm2', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.kind)).toEqual(['event', 'tools', 'event'])
    expect((items[1] as Extract<TranscriptItem, { kind: 'tools' }>).toolCount).toBe(1)
  })

  it('gives a step run its own row instead of folding it into a tool chip', () => {
    const items = buildTranscriptItems([
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
      evt({ id: 'r1', session_id: 's1', type: 'tool_call_start', data: { data: { tool_call_id: 'c1', tool_name: 'execute_step', tool_params: { arguments: '{"step_id":"step-fetch","group_name":"default"}' } } } }),
      evt({ id: 'm2', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.kind)).toEqual(['event', 'event', 'event'])
    expect(runActivity(items[1].kind === 'event' ? items[1].event : evt({ id: 'x', session_id: 's1' }))).toEqual({
      label: 'Running step', target: 'step-fetch · default', state: 'started',
    })
  })

  it('says a run started and never that it finished', () => {
    // execute_step returns "started in background" the moment it launches, so
    // its end event knows nothing about the outcome — it must not draw a row.
    const end = evt({ id: 'r2', session_id: 's1', type: 'tool_call_end', data: { data: { tool_call_id: 'c1', tool_name: 'execute_step' } } })
    expect(runActivity(end)).toBeNull()
    const items = buildTranscriptItems([
      evt({ id: 'r1', session_id: 's1', type: 'tool_call_start', data: { data: { tool_call_id: 'c1', tool_name: 'execute_step', tool_params: { arguments: '{"step_id":"step-fetch"}' } } } }),
      end,
    ])
    expect(items).toHaveLength(1)
  })

  it('omits token usage because cost and context have dedicated UI', () => {
    const items = buildTranscriptItems([
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
      evt({ id: 'u1', session_id: 's1', type: 'token_usage' }),
      evt({ id: 'm2', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.kind)).toEqual(['event', 'event'])
    expect(items.map(i => i.key)).toEqual(['m1', 'm2'])
  })

  it('omits repeated provider status lines because the terminal footer owns them', () => {
    const items = buildTranscriptItems([
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
      evt({ id: 's1', session_id: 's1', type: 'status_line' }),
      evt({ id: 's2', session_id: 's1', type: 'status_line' }),
      evt({ id: 'm2', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.key)).toEqual(['m1', 'm2'])
  })

  it('keeps a presentation update available for the product activity renderer', () => {
    const items = buildTranscriptItems([
      evt({ id: 'message', session_id: 's1', type: 'agent_message' }),
      evt({
        id: 'shown-video',
        session_id: 's1',
        type: 'presentation_updated',
        data: { data: { kind: 'media.video', title: 'Shot 1 — Hook (Preview)' } } as never,
      }),
      evt({ id: 'answer', session_id: 's1', type: 'agent_message' }),
    ])

    expect(items.filter(item => item.kind === 'event').map(item => (item as Extract<TranscriptItem, { kind: 'event' }>).event.id))
      .toEqual(['message', 'shown-video', 'answer'])
  })

  it('does not show token usage beside collapsed tool calls', () => {
    const items = buildTranscriptItems([
      evt({ id: 't1', session_id: 's1', type: 'tool_call_start' }),
      evt({ id: 't2', session_id: 's1', type: 'tool_call_end' }),
      evt({ id: 'u1', session_id: 's1', type: 'token_usage' }),
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.kind)).toEqual(['tools', 'event'])
    expect(items.map(i => i.key)).toEqual(['t1', 'm1'])
  })

  it('does not split a batch when delegation events interleave', () => {
    const items = buildTranscriptItems([
      evt({ id: 't1', session_id: 's1', type: 'tool_call_start' }),
      evt({ id: 'd1', session_id: 's1', type: 'delegation_start' }),
      evt({ id: 'd2', session_id: 's1', type: 'delegation_end' }),
      evt({ id: 't2', session_id: 's1', type: 'tool_call_end' }),
    ])
    expect(items.map(i => i.kind)).toEqual(['tools'])
  })

  it('preserves message order around tool groups', () => {
    const items = buildTranscriptItems([
      evt({ id: 'm1', session_id: 's1', type: 'agent_message' }),
      evt({ id: 't1', session_id: 's1', type: 'tool_call_start' }),
      evt({ id: 'm2', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.key)).toEqual(['m1', 't1', 'm2'])
  })

  it('handles an empty list', () => {
    expect(buildTranscriptItems([])).toEqual([])
  })
})

describe('buildTranscriptItems — duplicate execution_prompt suppression', () => {
  it('drops an execution_prompt user_message when the preceding orchestrator_agent_start already carries the same content', () => {
    const items = buildTranscriptItems([
      evt({
        id: 'start', session_id: 's1', type: 'orchestrator_agent_start',
        data: { data: { user_message: 'Do the thing' } } as never,
      }),
      evt({
        id: 'prompt', session_id: 's1', type: 'user_message',
        data: { data: { content: 'Do the thing', metadata: { source: 'execution_prompt' } } } as never,
      }),
      evt({ id: 'reply', session_id: 's1', type: 'agent_message' }),
    ])
    expect(items.map(i => i.key)).toEqual(['start', 'reply'])
  })

  it('drops an execution_prompt user_message when the preceding background_agent_started already carries the instruction', () => {
    const items = buildTranscriptItems([
      evt({
        id: 'start', session_id: 's1', type: 'background_agent_started',
        data: { data: { instruction: 'Count the folders' } } as never,
      }),
      evt({
        id: 'prompt', session_id: 's1', type: 'user_message',
        data: { data: { content: 'Count the folders', metadata: { source: 'execution_prompt' } } } as never,
      }),
    ])
    expect(items.map(i => i.key)).toEqual(['start'])
  })

  it('keeps the execution_prompt message when the preceding start event has no content to duplicate', () => {
    // Some execution kinds' own start event never carries the kickoff text —
    // dropping here would silently lose the only copy. Never guess from the tag alone.
    const items = buildTranscriptItems([
      evt({
        id: 'start', session_id: 's1', type: 'orchestrator_agent_start',
        data: { data: { user_message: '' } } as never,
      }),
      evt({
        id: 'prompt', session_id: 's1', type: 'user_message',
        data: { data: { content: 'Only copy of this text', metadata: { source: 'execution_prompt' } } } as never,
      }),
    ])
    expect(items.map(i => i.key)).toEqual(['start', 'prompt'])
  })

  it('keeps an execution_prompt message with no preceding lifecycle-start event at all', () => {
    const items = buildTranscriptItems([
      evt({
        id: 'prompt', session_id: 's1', type: 'user_message',
        data: { data: { content: 'Kickoff', metadata: { source: 'execution_prompt' } } } as never,
      }),
    ])
    expect(items.map(i => i.key)).toEqual(['prompt'])
  })

  it('never touches an [AUTO-NOTIFICATION] user_message even after a content-bearing start event', () => {
    const items = buildTranscriptItems([
      evt({
        id: 'start', session_id: 's1', type: 'orchestrator_agent_start',
        data: { data: { user_message: 'Do the thing' } } as never,
      }),
      evt({
        id: 'notice', session_id: 's1', type: 'user_message',
        data: { data: { content: '[AUTO-NOTIFICATION] Agent X completed' } } as never,
      }),
    ])
    expect(items.map(i => i.key)).toEqual(['start', 'notice'])
  })
})

describe('pairToolCalls', () => {
  const tool = (id: string, type: string, extra: Record<string, unknown> = {}) =>
    evt({ id, session_id: 's1', type, data: { data: { tool_call_id: 'call-1', ...extra } } as never })

  it('collapses a start/end pair into ONE row instead of two', () => {
    const pairs = pairToolCalls([
      tool('a', 'tool_call_start', { tool_name: 'ToolSearch' }),
      tool('b', 'tool_call_end', { tool_name: 'ToolSearch', duration: 1_200_000_000 }),
    ])
    expect(pairs).toHaveLength(1)
    expect(pairs[0].name).toBe('ToolSearch')
    expect(pairs[0].status).toBe('ok')
    // Nanoseconds, as Go's time.Duration sends them — 1.2s, not 1200ms.
    expect(pairs[0].durationNs).toBe(1_200_000_000)
    expect(pairs[0].events).toHaveLength(2)
  })

  it('strips the mcp__server__tool wire name and keeps the server separately', () => {
    const pairs = pairToolCalls([
      tool('a', 'tool_call_start', { tool_name: 'mcp__api-bridge__agent_browser' }),
    ])
    expect(pairs[0].name).toBe('agent_browser')
    expect(pairs[0].server).toBe('api-bridge')
  })

  it('marks a call still running when only the start has arrived', () => {
    expect(pairToolCalls([tool('a', 'tool_call_start', { tool_name: 'x' })])[0].status).toBe('running')
  })

  it('an error anywhere in the pair wins over a later success', () => {
    const pairs = pairToolCalls([
      tool('a', 'tool_call_start', { tool_name: 'x' }),
      tool('b', 'tool_call_error', { tool_name: 'x' }),
      tool('c', 'tool_call_end', { tool_name: 'x' }),
    ])
    expect(pairs).toHaveLength(1)
    expect(pairs[0].status).toBe('error')
  })

  it('keeps interleaved calls separate rather than merging them', () => {
    const a = (id: string, type: string, name: string) =>
      evt({ id, session_id: 's1', type, data: { data: { tool_call_id: 'A', tool_name: name } } as never })
    const b = (id: string, type: string, name: string) =>
      evt({ id, session_id: 's1', type, data: { data: { tool_call_id: 'B', tool_name: name } } as never })
    const pairs = pairToolCalls([
      a('1', 'tool_call_start', 'first'),
      b('2', 'tool_call_start', 'second'),
      a('3', 'tool_call_end', 'first'),
      b('4', 'tool_call_end', 'second'),
    ])
    expect(pairs.map(p => p.name)).toEqual(['first', 'second'])
    expect(pairs.every(p => p.status === 'ok')).toBe(true)
  })

  it('never drops an event that has no tool_call_id to pair on', () => {
    const orphan = evt({ id: 'o', session_id: 's1', type: 'tool_call_end', data: { data: { tool_name: 'lonely' } } as never })
    const pairs = pairToolCalls([orphan])
    expect(pairs).toHaveLength(1)
    expect(pairs[0].name).toBe('lonely')
  })
})

describe('pairToolCalls — args and result surfaced on the pair', () => {
  it('takes arguments from the START event and the result from the END event', () => {
    // This split is the whole reason a per-event card was misleading: neither
    // event alone can render a complete tool call.
    const start = evt({
      id: 'a', session_id: 's1', type: 'tool_call_start',
      data: { data: { tool_call_id: 'c1', tool_name: 'agent_browser', tool_params: { arguments: '{"action":"status"}' } } } as never,
    })
    const end = evt({
      id: 'b', session_id: 's1', type: 'tool_call_end',
      data: { data: { tool_call_id: 'c1', tool_name: 'agent_browser', result: 'CDP connected', duration: 1_200_000_000 } } as never,
    })
    const [pair] = pairToolCalls([start, end])
    expect(pair.args).toBe('{"action":"status"}')
    expect(pair.result).toBe('CDP connected')
    expect(pair.durationNs).toBe(1_200_000_000)
    expect(pair.status).toBe('ok')
  })

  it('unwraps Cursor CallMcpTool into the actual MCP tool and nested arguments', () => {
    const [pair] = pairToolCalls([
      evt({
        id: 'cursor-start', session_id: 's1', type: 'tool_call_start',
        data: { data: {
          tool_call_id: 'cursor-1',
          tool_name: 'CallMcpTool',
          tool_params: { arguments: {
            server: 'api-bridge',
            toolName: 'execute_shell_command',
            arguments: { command: 'pwd' },
          } },
        } } as never,
      }),
    ])

    expect(pair.name).toBe('execute_shell_command')
    expect(pair.server).toBe('api-bridge')
    expect(pair.args).toBe('{"command":"pwd"}')
  })

  it('unwraps Cursor structured CallMcpTool variants with bridge identifiers', () => {
    const [pair] = pairToolCalls([
      evt({
        id: 'cursor-structured-start', session_id: 's1', type: 'tool_call_start',
        data: { data: {
          tool_call_id: 'cursor-structured-1',
          tool_name: 'call_mcp_tool',
          tool_params: { arguments: {
            providerIdentifier: 'api-bridge',
            serverIdentifier: 'api-bridge',
            toolName: 'execute_shell_command',
            args: { command: 'pwd' },
          } },
        } } as never,
      }),
    ])

    expect(pair.name).toBe('execute_shell_command')
    expect(pair.server).toBe('api-bridge')
    expect(pair.args).toBe('{"command":"pwd"}')
  })

  it('drops an orphan Cursor CallMcpTool event with no resolvable capability', () => {
    const pairs = pairToolCalls([
      evt({
        id: 'cursor-orphan', session_id: 's1', type: 'tool_call_end',
        data: { data: { tool_call_id: 'cursor-orphan-1', tool_name: 'CallMcpTool' } } as never,
      }),
    ])

    expect(pairs).toEqual([])
  })

  it('leaves args/result undefined when the provider sent neither', () => {
    const [pair] = pairToolCalls([
      evt({ id: 'a', session_id: 's1', type: 'tool_call_start', data: { data: { tool_call_id: 'c1', tool_name: 'x' } } as never }),
    ])
    expect(pair.args).toBeUndefined()
    expect(pair.result).toBeUndefined()
  })

  it('recovers empty provider-start arguments from the structured coding transcript', () => {
    const start = evt({
      id: 'start', session_id: 's1', type: 'tool_call_start',
      data: { data: { tool_call_id: 'toolu_1', tool_name: 'execute_shell_command', tool_params: { arguments: '' } } } as never,
    })
    const end = evt({
      id: 'end', session_id: 's1', type: 'tool_call_end',
      data: { data: { tool_call_id: 'toolu_1', tool_name: 'execute_shell_command', result: 'ok' } } as never,
    })
    const completion = evt({
      id: 'completion', session_id: 's1', type: 'llm_generation_end',
      data: { data: { metadata: { generation_info: { coding_provider_intermediate_messages: { messages: [{
        Role: 'ai', Parts: [{ ID: 'toolu_1', FunctionCall: { Name: 'mcp__api-bridge__execute_shell_command', Arguments: '{"command":"pwd"}' } }],
      }] } } } } } as never,
    })
    const [pair] = pairToolCalls([start, end, completion])
    expect(pair.args).toBe('{"command":"pwd"}')
    expect(pair.result).toBe('ok')
  })

  it('recovers Cursor arguments from a compound provider id and lowercase restored fields', () => {
    const start = evt({
      id: 'start', session_id: 's1', type: 'tool_call_start',
      data: { data: { tool_call_id: 'fc_cursor_1', tool_name: 'execute_shell_command', tool_params: { arguments: '' } } } as never,
    })
    const completion = evt({
      id: 'completion', session_id: 's1', type: 'llm_generation_end',
      data: { data: { generation_info: { coding_provider_intermediate_messages: { messages: [{
        parts: [{ id: 'call-platform-1\nfc_cursor_1', function_call: { name: 'mcp__api-bridge__execute_shell_command', arguments: { command: 'pwd' } } }],
      }] } } } } as never,
    })
    const [pair] = pairToolCalls([start, completion])
    expect(pair.args).toBe('{"command":"pwd"}')
  })

  it('keeps direct CLI arguments and error output when the canonical fields are absent', () => {
    const start = evt({
      id: 'a', session_id: 's1', type: 'tool_call_start',
      data: { data: { tool_call_id: 'c1', tool_name: 'execute_shell_command', input: { command: 'pwd' } } } as never,
    })
    const failure = evt({
      id: 'b', session_id: 's1', type: 'tool_call_error',
      data: { data: { tool_call_id: 'c1', tool_name: 'execute_shell_command', error: 'Operation not permitted' } } as never,
    })

    const [pair] = pairToolCalls([start, failure])
    expect(pair.args).toBe('{\n  "command": "pwd"\n}')
    expect(pair.result).toBe('Operation not permitted')
    expect(pair.status).toBe('error')
  })
})

describe('toolErrorContextByEventID', () => {
  it('joins a tool error to the exact start arguments by tool_call_id', () => {
    const start = evt({
      id: 'start', session_id: 's1', type: 'tool_call_start',
      data: { data: { tool_call_id: 'call-7', tool_name: 'execute_shell_command', tool_params: { arguments: '{"command":"/bin/ps"}' } } } as never,
    })
    const other = evt({
      id: 'other', session_id: 's1', type: 'tool_call_start',
      data: { data: { tool_call_id: 'call-8', tool_name: 'other_tool', tool_params: { arguments: '{"wrong":true}' } } } as never,
    })
    const failure = evt({
      id: 'failure', session_id: 's1', type: 'tool_call_error',
      data: { data: { tool_call_id: 'call-7', tool_name: 'execute_shell_command', error: 'Operation not permitted' } } as never,
    })

    expect(toolErrorContextByEventID([start, other, failure]).get('failure')).toEqual({
      name: 'execute_shell_command',
      server: undefined,
      args: '{"command":"/bin/ps"}',
    })
  })
})

describe('final answer visibility (CDP step regression)', () => {
  const ev = (id: string, type: string, content?: string): any => ({
    id,
    type,
    data: { data: { ...(content !== undefined ? { content } : {}), tool_name: 'agent_browser' } },
  })

  // A browser/CDP step ends on a tool call and then answers. The answer event
  // must not be swallowed by the adjacent tool batch: nothing else in the
  // transcript renders that text (EventDispatcher has no streaming_chunk case).
  it('keeps a content-carrying llm_generation_end out of an adjacent tool batch', () => {
    const items = buildTranscriptItems([
      ev('t1', 'tool_call_start'),
      ev('t2', 'tool_call_end'),
      ev('answer', 'llm_generation_end', 'Snapshot captured. The page shows 3 results.'),
    ])

    const rendered = items.find((i) => i.kind === 'event' && (i as any).event.id === 'answer')
    expect(rendered).toBeDefined()
  })

  // An empty one is still just noise closing a generation, and should stay
  // absorbed rather than splitting the batch into two collapsed groups.
  it('still absorbs an empty llm_generation_end into the tool batch', () => {
    const items = buildTranscriptItems([
      ev('t1', 'tool_call_start'),
      ev('t2', 'tool_call_end'),
      ev('empty', 'llm_generation_end', '   '),
    ])

    expect(items.some((i) => i.kind === 'event' && (i as any).event.id === 'empty')).toBe(false)
    expect(items.some((i) => i.kind === 'tools')).toBe(true)
  })
})

describe('answer shown exactly once', () => {
  it.each(['unified_completion', 'agent_end'])('keeps identical %s answers to separate messages in a retained main execution', completionType => {
    const events = [1, 2, 3].flatMap(turn => [
      evt({ id: `user-${turn}`, type: 'user_message', data: { data: { content: 'Which model are you using?' } } as never }),
      evt({ id: `gen-${turn}`, type: 'llm_generation_end', execution_id: 'main:s1', data: { data: { content: 'GPT-6 (Codex).' } } as never }),
      evt({ id: `done-${turn}`, type: completionType, execution_id: 'main:s1', data: { data: { final_result: 'GPT-6 (Codex).', status: 'completed' } } as never }),
    ])
    expect(buildTranscriptItems(events).map(item => item.key)).toEqual([
      'user-1', 'done-1', 'user-2', 'done-2', 'user-3', 'done-3',
    ])
  })

  it('keeps an answer whose completion is missing when another turn repeats it', () => {
    const events = [
      evt({ id: 'user-1', type: 'user_message', data: { data: { content: 'Hello' } } as never }),
      evt({ id: 'gen-1', type: 'llm_generation_end', data: { data: { content: 'Hello there.' } } as never }),
      evt({ id: 'user-2', type: 'user_message', data: { data: { content: 'Hello again' } } as never }),
      evt({ id: 'done-2', type: 'unified_completion', data: { data: { final_result: 'Hello there.', status: 'completed' } } as never }),
    ]
    expect(buildTranscriptItems(events).map(item => item.key)).toEqual(['user-1', 'gen-1', 'user-2', 'done-2'])
  })

  const ANSWER =
    'Confirmed CDP is available: status reported the endpoint reachable, and a live snapshot succeeded.'
  const gen = (content: string): any => ({
    id: 'gen', type: 'llm_generation_end', data: { data: { content } },
  })
  const completion = (final_result: string): any => ({
    id: 'done', type: 'unified_completion', data: { data: { final_result, status: 'completed' } },
  })

  it('drops the generation-end copy when a completion card repeats it', () => {
    const items = buildTranscriptItems([gen(ANSWER), completion(ANSWER)])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toContain('done')
    expect(ids).not.toContain('gen')
  })

  it('drops an exact repeated answer even when it is short', () => {
    const shortAnswer = 'Hi! Ready when you are.'
    const items = buildTranscriptItems([gen(shortAnswer), completion(shortAnswer)])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toEqual(['done'])
  })

  it('does not fuzzy-dedupe a different short answer', () => {
    const items = buildTranscriptItems([gen('Ready when you are.'), completion('Hi! Ready when you are.')])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toEqual(['gen', 'done'])
  })

  it('still keeps generation-end when no completion card carries the answer', () => {
    const items = buildTranscriptItems([gen(ANSWER)])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toContain('gen')
  })

  it('keeps both when the completion card carries a different answer', () => {
    const items = buildTranscriptItems([gen(ANSWER), completion('Something else entirely happened here.')])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toContain('gen')
    expect(ids).toContain('done')
  })

  // Two completion-card types (e.g. agent_end and unified_completion) can both
  // fire for the same turn and both carry the identical final_result -- the
  // reader saw the same "Agent · Response" block rendered twice in a row.
  const agentEnd = (final_result: string): any => ({
    id: 'agent-end', type: 'agent_end', data: { data: { final_result, status: 'completed' } },
  })

  it('drops a second completion card that repeats the same answer as an earlier one', () => {
    const items = buildTranscriptItems([agentEnd(ANSWER), completion(ANSWER)])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toEqual(['agent-end'])
  })

  it('keeps two completion cards when their answers genuinely differ', () => {
    const items = buildTranscriptItems([agentEnd(ANSWER), completion('A distinct second answer, not a repeat.')])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toEqual(['agent-end', 'done'])
  })
})

describe('full-run container rows', () => {
  const ev = (id: string, type: string, execution_kind?: string): any => ({
    id, type, execution_kind, data: { data: { name: 'full-run [Toptal Bid / iteration-0]' } },
  })

  // A full run has no conversation of its own -- the backend already keeps it
  // out of the rail for that reason. Its lifecycle card just restated the panel
  // header before the first real event.
  it('drops full_run lifecycle rows from the transcript', () => {
    const items = buildTranscriptItems([
      ev('fullrun', 'background_agent_started', 'full_run'),
      ev('routing', 'orchestrator_agent_start', 'orchestrator'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).not.toContain('fullrun')
    expect(ids).toContain('routing')
  })

  it('keeps non-lifecycle events even when tagged full_run', () => {
    const items = buildTranscriptItems([ev('msg', 'user_message', 'full_run')])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toContain('msg')
  })

  it('drops legacy full-run completion and delivery diagnostics that lost their kind', () => {
    const items = buildTranscriptItems([
      {
        ...ev('done', 'background_agent_completed'),
        execution_id: 'workflow-full-job-search-1',
        data: { data: { agent_id: 'workflow-full-job-search-1', name: 'Full Workflow Execution' } },
      },
      {
        ...ev('steered', 'auto_notification_steered'),
        execution_id: 'workflow-full-job-search-1',
        data: { data: { agent_id: 'workflow-full-job-search-1', name: 'full-run [job-search / iteration-0]' } },
      },
    ])

    expect(items).toEqual([])
  })
})

describe('lifecycle alias dedupe', () => {
  const start = (id: string, type: string, name: string, execution_id = 'pulse-review-2026-07-27-bug'): any => ({
    id, type, execution_id, data: { data: { name, provider: 'claude-code', model_id: 'opus' } },
  })

  // Real production names for ONE reviewer: the server and the delegated agent
  // decorate the same execution differently, so the agent rendered twice.
  it('collapses background/orchestrator starts for the same execution', () => {
    const items = buildTranscriptItems([
      start('bg', 'background_agent_started', 'Pulse reviewer: pulse 2026 07 27 bug review'),
      start('orch', 'orchestrator_agent_start', 'Background: Pulse reviewer - pulse-2026-07-27-bug-review'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toHaveLength(1)
    expect(ids).toContain('orch') // the richer payload wins
  })

  // Siblings that legitimately share one execution id must stay apart. An eval
  // step emits several orchestrator starts under the same id; repeats WITHIN a
  // family are the signal that these are distinct agents, not aliases.
  it('keeps repeated starts from the same family distinct', () => {
    const items = buildTranscriptItems([
      start('v2', 'orchestrator_agent_start', 'step-1-execution-evaluate-search-route-val-2', 'workflow-step:eval'),
      start('v3', 'orchestrator_agent_start', 'step-1-execution-evaluate-search-route-val-3', 'workflow-step:eval'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toHaveLength(2)
  })

  // Mixed families where one family repeats: the repeats are real agents, so
  // nothing may collapse on execution id alone.
  it('does not alias-collapse when a family repeats on that execution', () => {
    const items = buildTranscriptItems([
      start('a', 'agent_start', 'eval', 'workflow-step:eval'),
      start('v2', 'orchestrator_agent_start', 'route-val-2', 'workflow-step:eval'),
      start('v3', 'orchestrator_agent_start', 'route-val-3', 'workflow-step:eval'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toContain('v2')
    expect(ids).toContain('v3')
  })

  // Different executions are never merged, however similar the names.
  it('never merges across executions', () => {
    const items = buildTranscriptItems([
      start('bug', 'background_agent_started', 'Pulse reviewer: pulse bug review', 'exec-bug'),
      start('art', 'background_agent_started', 'Pulse reviewer: pulse artifact review', 'exec-artifact'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toHaveLength(2)
  })
})

describe('one completion card per execution', () => {
  const ev = (id: string, type: string, name: string, execution_id = 'pulse-review-eval-health'): any => ({
    id, type, execution_id, data: { data: { name, result: 'done' } },
  })

  // A finished reviewer reported "Agent Completed" and "Pulse Reviewer: …
  // completed (2m46s)" as separate cards for one agent.
  it('keeps only the last completion when several families report the same execution', () => {
    const items = buildTranscriptItems([
      ev('start', 'background_agent_started', 'Pulse reviewer: pulse 2026 07 27 eval health'),
      ev('agentEnd', 'agent_end', 'Pulse reviewer: pulse 2026 07 27 eval health'),
      ev('bgDone', 'background_agent_completed', 'Pulse reviewer: pulse 2026 07 27 eval health'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toEqual(['bgDone'])
  })

  // A retried step emits several orchestrator_agent_end events for one piece of
  // work; rendering each is noise, not information.
  it('collapses repeated completions from the same family', () => {
    const items = buildTranscriptItems([
      ev('e1', 'orchestrator_agent_end', 'verification', 'workflow-step:verification'),
      ev('e2', 'orchestrator_agent_end', 'verification', 'workflow-step:verification'),
      ev('e3', 'orchestrator_agent_end', 'verification', 'workflow-step:verification'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toEqual(['e3'])
  })

  // A failure reported after an end is the outcome that actually held.
  it('lets a later failure supersede an earlier completion', () => {
    const items = buildTranscriptItems([
      ev('ok', 'orchestrator_agent_end', 'verification', 'workflow-step:verification'),
      ev('failed', 'orchestrator_agent_error', 'verification', 'workflow-step:verification'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toEqual(['failed'])
  })

  // Distinct executions each keep their own completion.
  it('never merges completions across executions', () => {
    const items = buildTranscriptItems([
      ev('a', 'background_agent_completed', 'Reviewer A', 'exec-a'),
      ev('b', 'background_agent_completed', 'Reviewer B', 'exec-b'),
    ])
    const ids = items.filter(i => i.kind === 'event').map(i => (i as any).event.id)
    expect(ids).toHaveLength(2)
  })
})

describe('formatted view for a tmux terminal', () => {
  // The formatted-view toggle on a raw tmux pane rests on one assumption: the
  // same run is ALSO available as structured events. Event selection must not
  // care about transport, or the toggle would open an empty transcript on
  // exactly the terminals it exists for.
  const tmuxMainAgent: any = {
    terminal_id: 'main:sess-1',
    session_id: 'sess-1',
    execution_kind: 'main_agent',
    step_transport: 'tmux',
    tmux_session: 'mcp-agent-20260728',
  }

  const ev = (id: string, type: string, data: Record<string, unknown>): any => ({
    id, type, session_id: 'sess-1', execution_id: 'main:sess-1',
    execution_kind: 'main_agent', data: { data },
  })

  it('selects the run’s events for a tmux main agent, not just structured ones', () => {
    const selected = selectTerminalEvents(
      [
        ev('t1', 'tool_call_start', { tool_name: 'execute_shell_command', tool_params: { arguments: '{"command":"ls"}' } }),
        ev('t2', 'tool_call_end', { tool_name: 'execute_shell_command', result: 'a.txt' }),
        ev('answer', 'llm_generation_end', { content: 'Listed the directory: one file.' }),
      ],
      tmuxMainAgent,
      [tmuxMainAgent],
    )
    expect(selected).toHaveLength(3)
  })

  it('builds a transcript carrying the tool call and the reply the pane wraps badly', () => {
    const items = buildTranscriptItems([
      ev('t1', 'tool_call_start', { tool_name: 'execute_shell_command', tool_params: { arguments: '{"command":"ls"}' } }),
      ev('t2', 'tool_call_end', { tool_name: 'execute_shell_command', result: 'a.txt' }),
      ev('answer', 'llm_generation_end', { content: 'Listed the directory: one file.' }),
    ])

    expect(items.some(i => i.kind === 'tools')).toBe(true)
    const answer = items.find(i => i.kind === 'event' && (i as any).event.id === 'answer')
    expect(answer).toBeDefined()
  })

  it('does not render persisted streaming packets as unknown-event cards', () => {
    const items = buildTranscriptItems([
      ev('stream-start', 'streaming_start', {}),
      ev('stream-chunk', 'streaming_chunk', { content: 'partial transport packet' }),
      ev('stream-end', 'streaming_end', {}),
      ev('answer', 'llm_generation_end', { content: 'Durable final answer.' }),
    ])

    expect(items.map(item => item.kind === 'event' ? (item as any).event.id : item.key)).toEqual(['answer'])
  })

  it('does not render live cache diagnostics as unknown-event cards', () => {
    const items = buildTranscriptItems([
      ev('cache', 'cache_event', { operation: 'hit', server_name: 'github' }),
      ev('answer', 'llm_generation_end', { content: 'Durable final answer.' }),
    ])

    expect(items.map(item => item.kind === 'event' ? (item as any).event.id : item.key)).toEqual(['answer'])
  })

  it('does not render tracer-only MCP connection lifecycle as unknown-event cards', () => {
    const items = buildTranscriptItems([
      ev('conn-start', 'mcp_server_connection_start', { server_name: 'github' }),
      ev('conn-end', 'mcp_server_connection_end', { server_name: 'github' }),
      ev('answer', 'llm_generation_end', { content: 'Durable final answer.' }),
    ])

    expect(items.map(item => item.kind === 'event' ? (item as any).event.id : item.key)).toEqual(['answer'])
  })

  it('keeps the human conversation and removes provider setup diagnostics', () => {
    const items = buildTranscriptItems([
      ev('user', 'user_message', { content: 'Is the schedule fixed?' }),
      ev('prompt', 'system_prompt', { content: 'internal system prompt' }),
      ev('turn', 'conversation_turn', { last_message: 'Is the schedule fixed?' }),
      ev('start', 'llm_generation_start', { model_id: 'claude-opus-5' }),
      ev('retry', 'llm_generation_with_retry', { max_retries: 5 }),
      ev('answer', 'llm_generation_end', { content: 'Yes, it is fixed.' }),
    ])

    expect(items.map(item => item.kind === 'event' ? (item as any).event.id : item.key)).toEqual(['user', 'answer'])
  })

  it('drops an empty successful agent-end trace after the reply', () => {
    const items = buildTranscriptItems([
      ev('answer', 'llm_generation_end', { content: 'Done.' }),
      ev('end', 'agent_end', { success: true, agent_type: 'simple' }),
    ])

    expect(items.map(item => item.kind === 'event' ? (item as any).event.id : item.key)).toEqual(['answer'])
  })

  it('drops an empty retained-turn completion marker', () => {
    const items = buildTranscriptItems([
      ev('answer', 'llm_generation_end', { content: 'Done.' }),
      ev('settled', 'unified_completion', { source: 'tmux_stream' }),
    ])

    expect(items.map(item => item.kind === 'event' ? (item as any).event.id : item.key)).toEqual(['answer'])
  })
})

describe('a terminal does not repeat its own name as an opening card', () => {
  const t = terminal({ session_id: 's1', owner_id: 'exec-1', execution_id: 'exec-1' })
  const ev = (id: string, type: string, execution_id: string, name = 'Review Artifact Drift Review') =>
    evt({ id, type, session_id: 's1', execution_id, data: { data: { name } } as never })

  // The panel header already shows this agent's name ("Review artifact drift
  // review · Sub-agent · updated now"). Its own start card then repeated it
  // verbatim as the very first thing in the transcript -- the same class of
  // bug as the earlier "Full Run" container row, just for a plain sub-agent.
  it('drops the terminal\'s own start card', () => {
    const events = selectTerminalEvents(
      [ev('start', 'background_agent_started', 'exec-1'), ev('msg', 'user_message', 'exec-1')],
      t,
    )
    expect(events.map(e => e.id)).toEqual(['msg'])
  })

  // Minimal rail (2026-09-22): lifecycle banners are hidden from the rail
  // even when they carry content -- the rail shows user/assistant/tool rows
  // plus content-bearing gates, errors, and results.
  it('drops the terminal\'s own start card even when it contains the task', () => {
    const start = evt({
      id: 'start', type: 'background_agent_started', session_id: 's1', execution_id: 'exec-1',
      data: { data: { name: 'Review Artifact Drift Review', instruction: 'Inspect the report and verify every claim.' } } as never,
    })
    const events = selectTerminalEvents([start, ev('done', 'background_agent_completed', 'exec-1')], t)

    expect(events.map(e => e.id)).toEqual([])
  })

  it('keeps a child execution failure visible in the rail', () => {
    const failure = evt({
      id: 'failed', type: 'orchestrator_agent_error', session_id: 's1', execution_id: 'exec-1',
      data: { data: { error: 'routing step failed: no route matched' } } as never,
    })
    const events = selectTerminalEvents([ev('start', 'background_agent_started', 'exec-1'), failure], t)

    expect(events.map(e => e.id)).toEqual(['failed'])
  })

  it('drops the terminal\'s own completion card', () => {
    const events = selectTerminalEvents(
      [ev('start', 'background_agent_started', 'exec-1'), ev('done', 'background_agent_completed', 'exec-1')],
      t,
    )
    expect(events.map(e => e.id)).toEqual([])
  })

  it('drops a child agent\'s start card from the main-agent transcript', () => {
    const mainTerminal = terminal({
      session_id: 's1', owner_id: '', execution_id: 'main:s1',
      execution_kind: 'main_agent',
    } as never)
    const events = selectTerminalEvents(
      [
        evt({ id: 'own-start', type: 'agent_start', session_id: 's1', execution_id: 'main:s1' }),
        evt({ id: 'child', type: 'background_agent_started', session_id: 's1', execution_id: 'exec-2' }),
      ],
      mainTerminal,
    )
    expect(events.map(e => e.id)).toEqual([])
  })
})

describe('main agent start card', () => {
  it('never shows the main agent\'s own Agent Started card, matched end or not', () => {
    const mk = (type: string, extra: Record<string, unknown> = {}) => ({
      id: `${type}-${Math.random()}`, type, session_id: 's1', execution_kind: 'main_agent', execution_id: 'main:s1',
      data: { type, data: { agent_type: 'simple', ...extra } },
    }) as unknown as PollingEvent
    const start = mk('agent_start', { model: 'claude-sonnet-5-5', provider: 'claude-code' })
    const sub = { ...mk('agent_start', { agent_name: 'reviewer' }), execution_kind: 'delegation', execution_id: 'delegation-1' } as PollingEvent
    const kept = collapseCompletedLifecycleStarts([start, sub, mk('unified_completion', { final_result: 'done' })])
    expect(kept).not.toContain(start)
    expect(kept).toContain(sub)
  })
})

describe('product side-channel events', () => {
  it('keeps product_interaction out of the transcript', () => {
    const ev = { id: 'pi-1', type: 'product_interaction', session_id: 's1', execution_kind: 'main_agent', execution_id: 'main:s1',
      data: { type: 'product_interaction', data: { product: 'sparkquill', kind: 'suggestions', payload: { actions: [] } } } } as unknown as PollingEvent
    expect(selectTerminalEvents([ev], null, [])).toEqual([])
  })

  it('keeps legacy Work state events out of restored transcripts', () => {
    const identity = { id: 'identity-1', type: 'work_identity_updated', session_id: 's1', data: {} } as unknown as PollingEvent
    const references = { id: 'refs-1', type: 'work_workflow_references_updated', session_id: 's1', data: {} } as unknown as PollingEvent
    expect(selectTerminalEvents([identity, references], null, [])).toEqual([])
  })
})

// RTS rtslatency, Cursor over tmux: the confirmation of a steered question and
// its whole one-chunk reply arrived in one batch. The backend dates the
// question no later than the reply it held (equal time, journal order breaks
// the tie); the reply must render after the question as the newest row.
describe('steered question and one-chunk reply in one batch', () => {
  const sessionId = 'aa7480bf'
  const main = { execution_id: `main:${sessionId}`, execution_kind: 'main_agent', session_id: sessionId }
  const reply = 'No. The plan was **not** updated for crews.'
  const batch: PollingEvent[] = [
    evt({ ...main, id: 'user-message-2fcd5415', type: 'user_message', timestamp: '2026-09-25T12:11:15.900Z', data: { content: 'did you update the plan' } }),
    evt({ ...main, id: 'user:2fcd5415', sequence: 514, type: 'user_message', timestamp: '2026-09-25T12:11:20.963898417Z', data: { data: { content: 'did you update the plan', role: 'user' } } }),
    evt({ ...main, id: 'observer:chunk', sequence: 515, type: 'streaming_chunk', timestamp: '2026-09-25T12:11:20.963898417Z', data: { type: 'streaming_chunk', data: { content: reply, chunk_index: 1, is_tool_call: false, source: 'transcript' } } }),
    evt({ ...main, id: 'observer:completion', sequence: 516, type: 'unified_completion', timestamp: '2026-09-25T12:11:20.963927538Z', data: { type: 'unified_completion', data: { final_result: reply, status: 'completed' } } }),
  ]

  it('renders the reply after its question, as the newest row', () => {
    const items = buildTranscriptItems(selectTerminalEvents(batch, null))
    const rows = items.map(item => item.kind === 'event' ? item.event.type : item.kind)
    expect(rows).toEqual(['user_message', 'unified_completion'])
  })
})
