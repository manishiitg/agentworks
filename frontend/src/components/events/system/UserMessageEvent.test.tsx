import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { buildAskAIMessage } from '../../../utils/askAIMessage'
import { UserMessageEventDisplay } from './UserMessageEvent'

describe('UserMessageEventDisplay', () => {
  it('renders generated structured input as a compact task instead of a human message', () => {
    const html = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: '## Orchestrator Instructions\nInternal generated prompt',
          role: 'user',
          turn: 0,
          metadata: {
            source: 'execution_prompt',
            step_name: 'Nested Word Task',
          },
        }}
      />,
    )

    expect(html).toContain('data-testid="terminal-execution-prompt"')
    expect(html).toContain('Nested Word Task')
    expect(html).toContain('Instructions')
    expect(html).not.toContain('No message content')
    // The instructions ARE the task: rendering the card with them collapsed
    // left a header with nothing under it, so the details element must open by
    // default and the prompt text must be present without interaction.
    expect(html).toContain('<details open')
    expect(html).toContain('Internal generated prompt')
  })

  it('keeps live user input visually distinct from execution prompts', () => {
    const html = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'Please check this result',
          role: 'user',
          metadata: { source: 'coding_agent_live_input' },
        }}
      />,
    )

    expect(html).toContain('Please check this result')
    expect(html).not.toContain('terminal-execution-prompt')
  })

  it('recognizes persisted step-scoped prompts created before source metadata existed', () => {
    const html = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'Legacy generated executor prompt',
          role: 'user',
          turn: 0,
          metadata: {
            current_step_id: 'nested-word-task',
            step_name: 'Nested Word Task',
          },
        }}
      />,
    )

    expect(html).toContain('terminal-execution-prompt')
    expect(html).toContain('Nested Word Task')
  })

  it('shows a single tick after the fast ack and a double tick after durable confirmation', () => {
    const fast = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'steer into the turn',
          role: 'user',
          metadata: { source: 'coding_agent_live_input', delivery_status: 'sent_to_cli', provider: 'codex-cli', message_id: 'steer-1', confirmation: 'fast' },
        }}
      />,
    )
    expect(fast).toContain('data-testid="delivery-tick"')
    expect(fast).toContain('data-state="fast"')
    expect(fast).toContain('<svg')

    const confirmed = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'steer into the turn',
          role: 'user',
          metadata: { source: 'coding_agent_live_input', delivery_status: 'sent_to_cli', provider: 'codex-cli', message_id: 'steer-1', confirmation: 'confirmed', latency_ms: 1200 },
        }}
      />,
    )
    expect(confirmed).toContain('data-state="confirmed"')
    expect(confirmed).toContain('<svg')
    expect(confirmed).toContain('Confirmed in codex-cli CLI record in 1.2s')
  })

  it('shows queued and failed ticks without touching rows that were never live-delivered', () => {
    const queuedTurn = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'run after the schedule',
          role: 'user',
          metadata: { source: 'coding_agent_live_input', confirmation: 'queued', delivery_status: 'queued_for_turn' },
        }}
      />,
    )
    expect(queuedTurn).toContain('data-state="queued"')
    expect(queuedTurn).toContain('Queued behind the active conversation turn')

    const unflushed = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'steer into the turn',
          role: 'user',
          metadata: { source: 'coding_agent_live_input', confirmation: 'accepted_but_unflushed', provider: 'codex-cli' },
        }}
      />,
    )
    expect(unflushed).toContain('data-state="unflushed"')
    expect(unflushed).toContain('Sent to codex-cli CLI; not confirmed yet')

    const failed = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'steer into the turn',
          role: 'user',
          metadata: { source: 'coding_agent_live_input', confirmation: 'failed' },
        }}
      />,
    )
    expect(failed).toContain('data-state="failed"')

    const sending = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: 'steer into the turn',
          role: 'user',
          metadata: { source: 'coding_agent_live_input', delivery_status: 'sending' },
        }}
      />,
    )
    expect(sending).not.toContain('delivery-tick')

    const plain = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{ content: 'plain question', role: 'user', timestamp: new Date().toISOString() }}
      />,
    )
    expect(plain).not.toContain('delivery-tick')
  })

  it('shows the Ask AI request with builder instructions behind a toggle', () => {
    const html = renderToStaticMarkup(
      <UserMessageEventDisplay
        event={{
          content: buildAskAIMessage({
            view: 'Dashboard',
            summary: 'Help me understand my results page.',
            instructions: 'First read the guide with read_skill(skills=[{"name":"builder-reference"}]).',
          }),
          role: 'user',
        }}
      />,
    )

    expect(html).toContain('Ask AI')
    expect(html).toContain('Ask AI · Dashboard — Help me understand my results page.')
    expect(html).toContain('Show detail')
    expect(html).not.toContain('read_skill')
  })
})
