import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../ui/MarkdownRenderer', () => ({
  MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div>,
}))
vi.mock('../../ui/CsvRenderer', () => ({
  CsvRenderer: ({ content }: { content: string }) => <div>{content}</div>,
}))
vi.mock('../../ui/CircularProgress', () => ({
  CircularProgress: () => null,
}))
vi.mock('../../ui/tooltip', () => ({
  TooltipProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))
vi.mock('./ToolCallSpecialRender', () => ({
  WorkspaceToolCallEndDisplay: () => null,
  CodeExecutionToolCallEndDisplay: () => null,
  WebSearchToolCallDisplay: () => null,
}))

import type { ToolCallEndEvent } from '../../../generated/events'
import { ToolCallEndEventDisplay } from './ToolCallEndEvent'

describe('ToolCallEndEventDisplay', () => {
  it('marks a bridge failure red on the generic custom-tool path', () => {
    const event: ToolCallEndEvent = {
      tool_name: 'record_pulse_result',
      result: JSON.stringify({
        stdout: 'ERROR: tool execution failed: layer=custom_tool_handler tool=record_pulse_result: invalid payload',
        stderr: '',
        exit_code: 0,
      }),
    }

    const markup = renderToStaticMarkup(<ToolCallEndEventDisplay event={event} />)
    expect(markup).toContain('Tool Call Failed')
    expect(markup).toContain('border-red-300')
  })

  // PLAT-141's settleOpenToolCalls fabricates an end event when a tool call
  // never reported one, so its UI chip stops spinning. On those events
  // `duration` is open-to-settle time, NOT tool runtime — the backend log says
  // so verbatim ("open-to-settle (NOT tool runtime)") — but the UI rendered it
  // under a plain "Duration" label anyway. Observed 2026-08-19: a Codex chat
  // session settling 14 of 14 unreported calls, every row stamped the same
  // second, reading as though tools had taken up to 6.3 minutes.
  it('does not present a synthetic settle as a measured runtime', () => {
    const event: ToolCallEndEvent = {
      tool_name: 'mcp',
      turn: 0,
      duration: 378_000_000_000, // 6.3m in ns
      synthetic_settle: true,
    }

    const markup = renderToStaticMarkup(<ToolCallEndEventDisplay event={event} />)
    expect(markup).toContain('no result reported')
    expect(markup).toContain('not tool runtime')
    // The bare "Duration:" label is the specific thing that misled — it must
    // not appear on an event whose duration was never a measurement of runtime.
    expect(markup).not.toContain('Duration:')
    // Turn is always 0 on these (the fabricated event carries none), so
    // showing it invites a wrong inference about ordering.
    expect(markup).not.toContain('Turn:')
    // Neither green (asserts a success nobody observed) nor red (asserts a
    // failure that did not happen).
    expect(markup).not.toContain('border-green-200')
    expect(markup).not.toContain('border-red-300')
  })

  // The regression guard for the change above: a genuine provider-reported end
  // must be completely unaffected — same label, same real Duration, same green.
  it('leaves a genuinely reported end event untouched', () => {
    // Same tool name as the synthetic case above, so `synthetic_settle` is the
    // only variable between the two. (A name like execute_shell_command would
    // route to a specialised display instead of the generic one under test.)
    const event: ToolCallEndEvent = {
      tool_name: 'mcp',
      turn: 3,
      duration: 1_500_000_000, // 1.5s
      result: 'ok',
    }

    const markup = renderToStaticMarkup(<ToolCallEndEventDisplay event={event} />)
    expect(markup).toContain('Tool Call End')
    expect(markup).toContain('Duration:')
    expect(markup).toContain('Turn: 3')
    expect(markup).not.toContain('not tool runtime')
  })
})
