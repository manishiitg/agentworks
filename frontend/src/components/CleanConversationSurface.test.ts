import { describe, expect, it } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { buildCleanConversationItems, buildProductionActivityItems, buildProductionActivityTurns, cleanConversationActivity } from '../utils/cleanConversation'
import { buildAskAIMessage } from '../utils/askAIMessage'

function event(id: string, type: string, data: Record<string, unknown>, extra: Partial<PollingEvent> = {}): PollingEvent {
  return {
    id,
    type,
    timestamp: '2026-08-07T12:00:00.000Z',
    data: { type, data } as PollingEvent['data'],
    ...extra,
  }
}

describe('buildCleanConversationItems', () => {
  it('shows native options and settles the same coding-agent choice', () => {
    const requested = event('q1', 'coding_agent_question', {
      provider: 'muse-cli', kind: 'requested', prompt_id: 'prompt-1',
      questions: [{ id: 'scope', header: 'Scope', question: 'Choose a scope', options: [{ label: 'One', description: 'Small' }, { label: 'Two', description: 'Wide' }] }],
    })
    const pending = buildCleanConversationItems([requested])
    expect(pending[0].codingAgentQuestion).toEqual(expect.objectContaining({ provider: 'muse-cli', state: 'pending', promptId: 'prompt-1' }))
    const settled = buildCleanConversationItems([requested, event('q2', 'coding_agent_question', {
      provider: 'muse-cli', kind: 'settled', prompt_id: 'prompt-1', outcome: 'answered', answers: [{ id: 'scope', selected_labels: ['Two'] }],
    })])
    expect(settled).toHaveLength(1)
    expect(settled[0].codingAgentQuestion).toEqual(expect.objectContaining({ state: 'answered', answers: [{ id: 'scope', selectedLabels: ['Two'] }] }))
  })

  it('preserves Claude multi-select questions in the shared choice contract', () => {
    const items = buildCleanConversationItems([event('claude-q', 'coding_agent_question', {
      provider: 'claude-code', kind: 'requested', prompt_id: 'toolu_123',
      questions: [{ id: 'question-1', header: 'Scope', question: 'Which areas?', multi_select: true, options: [{ label: 'API' }, { label: 'UI' }] }],
    })])
    expect(items[0].codingAgentQuestion).toEqual(expect.objectContaining({
      provider: 'claude-code', promptId: 'toolu_123',
      questions: [expect.objectContaining({ multiSelect: true })],
    }))
  })

  it('keeps earlier Muse question events readable after the shared event change', () => {
    const items = buildCleanConversationItems([
      event('old-request', 'coding_agent_question', { provider: 'muse-cli', kind: 'user_input_prompt_requested', prompt_id: 'old-prompt', questions: [{ id: 'color', question: 'Color?', options: [{ label: 'Blue' }] }] }),
      event('old-settle', 'coding_agent_question', { provider: 'muse-cli', kind: 'user_input_prompt_settled', prompt_id: 'old-prompt', outcome: 'answered', answers: [{ id: 'color', selected_label: 'Blue' }] }),
    ])
    expect(items[0].codingAgentQuestion?.answers).toEqual([{ id: 'color', selectedLabels: ['Blue'] }])
  })
  it('keeps Muse background updates after the foreground answer in journal order', () => {
    const items = buildCleanConversationItems([
      event('task-start', 'coding_agent_background_task', { kind: 'task_backgrounded', task_id: 'task-a' }),
      event('answer', 'unified_completion', { final_result: 'I started the job.' }),
      event('task-status', 'coding_agent_background_task', { kind: 'status', task_id: 'task-a', message: 'still working' }),
      event('task-done', 'coding_agent_background_task', { kind: 'completed', task_id: 'task-a' }),
    ])
    expect(items.map((item) => `${item.role}:${item.content}`)).toEqual([
      'background:Background task task-a started',
      'assistant:I started the job.',
      'background:Background task task-a status: still working',
      'background:Background task task-a completed',
    ])
  })

  it('classifies provider restart context as continuity instead of a user message', () => {
    const content = '[AGENTWORKS CONVERSATION CONTINUITY]\nRead the complete 423-message conversation archive.'
    expect(buildCleanConversationItems([
      event('continuity', 'user_message', { content }),
    ])).toEqual([
      expect.objectContaining({ role: 'continuity', content }),
    ])
  })

  it('keeps the human conversation, surfaces auto-notifications, and removes other internal runtime messages', () => {
    const items = buildCleanConversationItems([
      event('user-1', 'user_message', {
        content: 'Make a launch video\n\nPrevious workflow-builder conversation file: Chats/internal.md',
      }),
      event('notice', 'user_message', { content: '[AUTO-NOTIFICATION] internal runtime update' }),
      event('child', 'unified_completion', { final_result: 'Worker internals' }, { execution_kind: 'background_agent' }),
      event('done', 'unified_completion', { final_result: 'Your launch video is ready.' }, { execution_kind: 'main_agent' }),
    ])

    expect(items).toEqual([
      expect.objectContaining({ role: 'user', content: 'Make a launch video' }),
      expect.objectContaining({ role: 'notification', content: 'internal runtime update' }),
      expect.objectContaining({ role: 'assistant', content: 'Your launch video is ready.' }),
    ])
  })

  it('collapses Ask AI blocks to their plain-words request', () => {
    const items = buildCleanConversationItems([
      event('askai', 'user_message', {
        content: buildAskAIMessage({
          view: 'Dashboard',
          summary: 'Help me understand my results page.',
          instructions: 'First read the guide with read_skill(skills=[{"name":"builder-reference"}]).',
        }),
      }),
    ])

    expect(items).toEqual([
      expect.objectContaining({ role: 'user', content: 'Ask AI · Dashboard — Help me understand my results page.' }),
    ])
    expect(items[0].content).not.toContain('read_skill')
  })

  it('deduplicates echoed user messages and repeated final answers', () => {
    const items = buildCleanConversationItems([
      event('user-local', 'user_message', { content: 'Create it' }),
      event('user-echo', 'user_message', { content: 'Create it' }),
      event('done-1', 'conversation_end', { result: 'Finished.' }),
      event('done-2', 'unified_completion', { final_result: 'Finished.' }),
    ])

    expect(items.map((item) => `${item.role}:${item.content}`)).toEqual([
      'user:Create it',
      'assistant:Finished.',
    ])
  })

  it('renders assistant replies restored from the shared durable transcript', () => {
    const items = buildCleanConversationItems([
      event('restored-user', 'user_message', { content: 'Show the finished video' }),
      event('restored-answer', 'llm_generation_end', { content: 'The finished video is ready to preview.' }),
    ])

    expect(items.map((item) => `${item.role}:${item.content}`)).toEqual([
      'user:Show the finished video',
      'assistant:The finished video is ready to preview.',
    ])
  })

  it('keeps intermediate transcript updates visually distinct from the final answer', () => {
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Compare prod and staging' }),
      event('progress', 'llm_generation_end', {
        content: 'Checking the last seven days in both environments.',
        restored_intermediate_update: true,
      }),
      event('done', 'unified_completion', { final_result: 'Prod has 72 sessions and staging has 45.' }),
    ])

    expect(items.map((item) => `${item.role}:${item.content}`)).toEqual([
      'user:Compare prod and staging',
      'progress:Checking the last seven days in both environments.',
      'assistant:Prod has 72 sessions and staging has 45.',
    ])
    expect(items[1]).toEqual(expect.objectContaining({ assistantUpdate: true }))
    expect(items[2]).not.toHaveProperty('assistantUpdate')
  })

  it('renders structured provider narration as progress, not a final assistant answer', () => {
    const items = buildCleanConversationItems([
      event('update', 'conversation_thinking', {
        thinking: 'I am checking the latest production totals.',
        metadata: { presentation: 'assistant_update' },
      }),
    ])

    expect(items).toEqual([
      expect.objectContaining({
        role: 'progress',
        content: 'I am checking the latest production totals.',
        assistantUpdate: true,
      }),
    ])
  })

  it('keeps structured reasoning separate from the final answer', () => {
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Create the teaser' }),
      event('thinking', 'conversation_thinking', { thinking: 'I am checking the supplied assets before choosing the production path.' }),
      event('done', 'conversation_end', { result: 'The teaser plan is ready.' }),
    ])

    expect(items.map((item) => `${item.role}:${item.content}`)).toEqual([
      'user:Create the teaser',
      'reasoning:I am checking the supplied assets before choosing the production path.',
      'assistant:The teaser plan is ready.',
    ])
  })

  it('attaches native structured token usage to the completed assistant answer', () => {
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Create the teaser' }),
      event('done', 'unified_completion', { final_result: 'The teaser is ready.' }),
      event('usage', 'token_usage', {
        context: 'conversation_total',
        prompt_tokens: 1234,
        completion_tokens: 56,
        generation_info: { cumulative_cache_tokens: 789 },
      }),
    ])

    expect(items.at(-1)).toEqual(expect.objectContaining({
      role: 'assistant',
      usage: { inputTokens: 1234, outputTokens: 56, cacheTokens: 789, isEstimated: false },
    }))
  })

  it('removes delayed backend echoes without hiding an intentional repeated prompt', () => {
    const items = buildCleanConversationItems([
      event('user-message-local-1', 'user_message', { content: 'Check the report' }),
      event('user-message-local-steer', 'user_message', { content: 'Include the dimensions' }),
      event('backend-echo-1', 'user_message', { content: 'Check the report' }),
      event('backend-echo-steer', 'user_message', { content: 'Include the dimensions' }),
      event('user-message-local-2', 'user_message', { content: 'Check the report' }),
      event('backend-echo-2', 'user_message', { content: 'Check the report' }),
    ])

    expect(items.map((item) => item.content)).toEqual([
      'Check the report',
      'Include the dimensions',
      'Check the report',
    ])
  })

  it('turns cancellation into a simple product-facing message', () => {
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Create it' }),
      event('cancel', 'context_cancelled', {}),
      event('cancel-echo', 'context_cancelled', {}),
    ])

    expect(items).toHaveLength(2)
    expect(items.at(-1)).toEqual(expect.objectContaining({
      role: 'error',
      content: 'The current response was cancelled.',
    }))
  })

  it('turns an agent quota failure into an actionable product error', () => {
    const raw = 'all LLMs failed (primary + 0 fallbacks): Claude Code hit your weekly limit [quota_exhausted]'
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Create the video' }),
      event('failure', 'agent_error', { error: raw, code: 'quota_exhausted' }),
    ])

    expect(items.at(-1)).toEqual(expect.objectContaining({
      role: 'error',
      content: expect.stringContaining('usage limit has been reached'),
      failure: expect.objectContaining({
        code: 'quota_exhausted',
        title: 'Claude Code usage limit reached',
        retryable: true,
        technicalDetails: raw,
      }),
    }))
  })

  it('shows the Muse reset time and upgrade action instead of a network failure', () => {
    const raw = `all LLMs failed (primary + 0 fallbacks): muse-cli/muse-spark-1.3-contributor [quota_exhausted]: Usage limit reached · /upgrade
(https://accountscenter.meta.com/muse_code/?ep=xgrade) for increased limits, or
wait for usage to reset at Sep 14 at 5:30 AM`
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Run the workflow' }),
      event('failure', 'agent_error', { error: raw, code: 'quota_exhausted', provider: 'muse-cli' }),
    ])

    expect(items.at(-1)).toEqual(expect.objectContaining({
      role: 'error',
      content: expect.stringContaining('Retry after Sep 14 at 5:30 AM.'),
      failure: expect.objectContaining({
        code: 'quota_exhausted',
        title: 'Muse usage limit reached',
        actionLabel: 'Upgrade Muse',
        actionUrl: 'https://accountscenter.meta.com/muse_code/?ep=xgrade',
      }),
    }))
  })

  it('does not present a failed completion as an assistant answer', () => {
    const raw = 'all LLMs failed (primary + 0 fallbacks): provider unavailable'
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Hello' }),
      event('done', 'unified_completion', { final_result: raw }),
    ])

    expect(items.at(-1)).toEqual(expect.objectContaining({
      role: 'error',
      failure: expect.objectContaining({ code: 'provider_unavailable', retryable: true }),
    }))
    expect(items.some((item) => item.role === 'assistant')).toBe(false)
  })

  it('deduplicates equivalent failure carriers from one turn', () => {
    const raw = 'all LLMs failed (primary + 0 fallbacks): Claude Code hit your weekly limit [quota_exhausted]'
    const items = buildCleanConversationItems([
      event('user', 'user_message', { content: 'Create it' }),
      event('failure', 'agent_error', { error: raw }),
      event('done', 'unified_completion', { final_result: raw }),
    ])

    expect(items.filter((item) => item.role === 'error')).toHaveLength(1)
  })

  it('keeps technical configuration errors behind structured failure metadata', () => {
    const raw = 'Claude Code requires the MCP bridge: mcpbridge binary not found in PATH (set MCP_BRIDGE_BINARY)'
    const items = buildCleanConversationItems([
      event('failure', 'conversation_error', { error: raw }),
    ])

    expect(items).toEqual([
      expect.objectContaining({
        role: 'error',
        content: 'A required server configuration is missing. Ask an administrator to check this product deployment.',
        failure: expect.objectContaining({
          code: 'configuration_error',
          title: 'This product is not configured correctly',
          technicalDetails: raw,
        }),
      }),
    ])
  })

  it('uses the server tmux diagnostic as Technical details for a failed submission', () => {
    const technicalDetails = 'Request failed with status code 409\ntmux: mlp-pi-cli-test\nTerminal snapshot:\nπ • ✅ api-bridge_execute_shell_command'
    const items = buildCleanConversationItems([
      event('failure', 'conversation_error', {
        error: 'Live input unavailable: context deadline exceeded',
        code: 'live_input_unavailable',
        provider: 'pi-cli',
        technical_details: technicalDetails,
      }),
    ])

    expect(items).toEqual([
      expect.objectContaining({
        role: 'error',
        failure: expect.objectContaining({
          title: 'The response could not be completed',
          provider: 'pi-cli',
          technicalDetails,
        }),
      }),
    ])
  })

  it('redacts credentials from technical failure details', () => {
    const items = buildCleanConversationItems([
      event('failure', 'agent_error', {
        error: 'all LLMs failed: invalid token sk-secretvalue123456 and API_KEY=also-secret',
      }),
    ])

    expect(items[0].failure?.technicalDetails).not.toContain('sk-secretvalue123456')
    expect(items[0].failure?.technicalDetails).not.toContain('also-secret')
    expect(items[0].failure?.technicalDetails).toContain('[redacted]')
  })
})

describe('cleanConversationActivity', () => {
  it('uses friendly workflow and presentation labels instead of tool names', () => {
    expect(cleanConversationActivity([
      event('workflow', 'tool_call_start', { tool_name: 'run_full_workflow' }),
    ], 'Creating')).toBe('Running the production workflow')

    expect(cleanConversationActivity([
      event('video', 'tool_call_start', { tool_name: 'show_video' }),
    ], 'Creating')).toBe('Preparing the finished video')
  })

  it('turns low-level QA tools into product progress', () => {
    expect(cleanConversationActivity([
      event('skill', 'tool_call_start', { tool_name: 'read_skill' }),
    ], 'Creating')).toBe('Preparing the production checklist')

    expect(cleanConversationActivity([
      event('image', 'tool_call_start', { tool_name: 'read_image' }),
    ], 'Creating')).toBe('Reviewing the video frames')

    expect(cleanConversationActivity([
      event('shell', 'tool_call_start', { tool_name: 'execute_shell_command' }),
    ], 'Creating')).toBe('Checking the video output')
  })
})

describe('buildProductionActivityItems', () => {
  it('pairs live tool calls and exposes product-safe status details', () => {
    const items = buildProductionActivityItems([
      event('user', 'user_message', { content: 'Make the video' }),
      event('tool-start', 'tool_call_start', { tool_call_id: 'call-1', tool_name: 'execute_shell_command' }),
      event('tool-end', 'tool_call_end', { tool_call_id: 'call-1', tool_name: 'execute_shell_command' }),
    ])

    expect(items).toEqual([
      expect.objectContaining({ title: 'execute_shell_command', detail: 'Tool call', status: 'complete', kind: 'tool' }),
    ])
  })

  it('collapses wrapper and provider lifecycle events into one finished activity', () => {
    const items = buildProductionActivityItems([
      event('user', 'user_message', { content: 'Check the video' }),
      event('wrapper-start', 'tool_call_start', { tool_call_id: 'wrapper', tool_name: 'exec' }),
      event('wrapper-error', 'tool_call_error', { tool_call_id: 'wrapper-error', tool_name: 'exec' }),
      event('provider-error', 'tool_call_error', { tool_call_id: 'provider-error', tool_name: 'execute_shell_command' }),
      event('provider-end', 'tool_call_end', { tool_call_id: 'provider', tool_name: 'execute_shell_command' }),
    ])

    expect(items).toEqual([
      expect.objectContaining({ title: 'execute_shell_command', detail: 'Tool call', status: 'complete', kind: 'tool' }),
    ])
  })

  it('keeps workflow tools literal but gives them workflow context', () => {
    const items = buildProductionActivityItems([
      event('user', 'user_message', { content: 'Run the full workflow' }),
      event('workflow', 'tool_call_start', { tool_call_id: 'workflow-1', tool_name: 'run_full_workflow' }),
    ])

    expect(items).toEqual([
      expect.objectContaining({ title: 'run_full_workflow', detail: 'Workflow: running the full production workflow', status: 'running' }),
    ])
  })

  it('keeps raw tool details for developer inspection while redacting credentials', () => {
    const items = buildProductionActivityItems([
      event('user', 'user_message', { content: 'Inspect the tool' }),
      event('tool-start', 'tool_call_start', {
        tool_call_id: 'tool-1', tool_name: 'get_api_spec',
        tool_params: { arguments: '{"tool_name":"run_full_workflow","token":"should-not-show"}' },
      }),
      event('tool-end', 'tool_call_end', {
        tool_call_id: 'tool-1', tool_name: 'get_api_spec', result: 'Bearer super-secret-token',
      }),
    ])

    expect(items.at(-1)).toEqual(expect.objectContaining({
      title: 'get_api_spec',
      arguments: '{"tool_name":"run_full_workflow","token":"[redacted]"}',
      result: 'Bearer [redacted]',
    }))
  })

  it('keeps distinct same-named Cursor calls with their own inputs', () => {
    const items = buildProductionActivityItems([
      event('user', 'user_message', { content: 'Inspect both files' }),
      event('first-start', 'tool_call_start', {
        tool_call_id: 'call-1', tool_name: 'execute_shell_command',
        tool_params: { arguments: '{"command":"first"}' },
      }),
      event('first-end', 'tool_call_end', { tool_call_id: 'call-1', tool_name: 'execute_shell_command', result: 'one' }),
      event('second-start', 'tool_call_start', {
        tool_call_id: 'call-2', tool_name: 'execute_shell_command',
        tool_params: { arguments: '{"command":"second"}' },
      }),
      event('second-end', 'tool_call_end', { tool_call_id: 'call-2', tool_name: 'execute_shell_command', result: 'two' }),
    ])

    expect(items).toHaveLength(2)
    expect(items.map(item => item.arguments)).toEqual(['{"command":"first"}', '{"command":"second"}'])
    expect(items.map(item => item.result)).toEqual(['one', 'two'])
  })

  it('shows the underlying Cursor MCP tool instead of its CallMcpTool wrapper', () => {
    const items = buildProductionActivityItems([
      event('user', 'user_message', { content: 'Inspect the project' }),
      event('cursor-tool', 'tool_call_start', {
        tool_call_id: 'cursor-1',
        tool_name: 'CallMcpTool',
        tool_params: { arguments: {
          serverIdentifier: 'api-bridge',
          toolName: 'get_api_spec',
          args: { tool_name: 'show_video' },
        } },
      }),
    ])

    expect(items).toEqual([
      expect.objectContaining({
        title: 'get_api_spec',
        arguments: '{"tool_name":"show_video"}',
      }),
    ])
  })

  it('shows the selected workflow route without exposing routing internals', () => {
    const items = buildProductionActivityItems([
      event('route', 'routing_evaluated', {
        routing_response: { selected_route_id: 'infographic' },
        routes: [{ route_id: 'infographic', route_name: 'Product infographic' }],
      }),
    ])

    expect(items.at(-1)).toEqual(expect.objectContaining({
      title: 'Choose production path',
      detail: 'Product infographic',
      status: 'complete',
    }))
  })
})

describe('buildProductionActivityTurns', () => {
  it('keeps an earlier turn\'s tools after the user sends another message', () => {
    // The reported bug: activity was computed for the newest turn only, so
    // sending a message erased the visible record of the work just done.
    const turns = buildProductionActivityTurns([
      event('user-1', 'user_message', { content: 'Make the video' }),
      event('t1-start', 'tool_call_start', { tool_call_id: 'call-1', tool_name: 'execute_shell_command' }),
      event('t1-end', 'tool_call_end', { tool_call_id: 'call-1', tool_name: 'execute_shell_command' }),
      event('user-2', 'user_message', { content: 'Now make it shorter' }),
      event('t2-start', 'tool_call_start', { tool_call_id: 'call-2', tool_name: 'show_video' }),
      event('t2-end', 'tool_call_end', { tool_call_id: 'call-2', tool_name: 'show_video' }),
    ])

    expect(turns).toHaveLength(2)
    expect(turns[0].anchorId).toBe('user-1')
    expect(turns[0].items.map((item) => item.title)).toEqual(['execute_shell_command'])
    expect(turns[1].anchorId).toBe('user-2')
    expect(turns[1].items).toHaveLength(1)
  })

  it('keeps auto-notification work in the turn that triggered it', () => {
    // Auto-notifications arrive as user_message events but nobody typed them,
    // so they must not split a turn -- doing so would strand the work under a
    // message the user never sent.
    const turns = buildProductionActivityTurns([
      event('user-1', 'user_message', { content: 'Run the workflow' }),
      event('t1-start', 'tool_call_start', { tool_call_id: 'call-1', tool_name: 'run_full_workflow' }),
      event('t1-end', 'tool_call_end', { tool_call_id: 'call-1', tool_name: 'run_full_workflow' }),
      event('auto', 'user_message', { content: '[AUTO-NOTIFICATION] step done' }),
      event('t2-start', 'tool_call_start', { tool_call_id: 'call-2', tool_name: 'show_video' }),
      event('t2-end', 'tool_call_end', { tool_call_id: 'call-2', tool_name: 'show_video' }),
    ])

    expect(turns).toHaveLength(1)
    expect(turns[0].anchorId).toBe('user-1')
    expect(turns[0].items).toHaveLength(2)
  })

  it('returns no turns when nothing observable happened', () => {
    expect(buildProductionActivityTurns([
      event('user-1', 'user_message', { content: 'Hello' }),
    ])).toEqual([])
  })
})
