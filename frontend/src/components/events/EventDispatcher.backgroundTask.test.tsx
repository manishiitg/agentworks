// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { EventDispatcher } from './EventDispatcher'
import { summarizeBackgroundTaskMessage } from '../../utils/cleanConversation'
import type { PollingEvent } from '../../services/api-types'

// Muse's background shell polling loop showed as boxed cards holding the raw tool payload (Relay chat on server B, 2026-10-04).
const rawOutput = JSON.stringify({
  chunk_id: 'exec-11-1',
  command: 'for i in $(seq 1 24); do\n META=$(cat project/runs/iteration-0/default/run_metadata.json 2>/dev/null)\n sleep 10\ndone; echo DONE',
  description: 'Poll test run until completion',
  execution_state: 'background_running',
})

const event = (kind: string, message?: string) => ({
  type: 'coding_agent_background_task',
  timestamp: '2026-10-04T10:00:00Z',
  data: { data: { kind, task_id: '01a1032e-aaaa', ...(message ? { message } : {}) } },
}) as unknown as PollingEvent

let container: HTMLDivElement | null = null
afterEach(() => { container?.remove(); container = null })

async function render(e: PollingEvent): Promise<{ text: string, html: string }> {
  container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => { root.render(<EventDispatcher event={e} />) })
  const out = { text: container.textContent || '', html: container.innerHTML }
  await act(async () => { root.unmount() })
  return out
}

describe('background task events', () => {
  it('summarises a raw tool payload by its description', () => {
    expect(summarizeBackgroundTaskMessage(rawOutput)).toBe('Poll test run until completion')
    expect(summarizeBackgroundTaskMessage('terminal process running')).toBe('terminal process running')
    expect(summarizeBackgroundTaskMessage('')).toBe('')
    expect(summarizeBackgroundTaskMessage('x'.repeat(300)).length).toBe(140)
    expect(summarizeBackgroundTaskMessage('{not json\nsecond line')).toBe('{not json')
  })

  it('shows one readable line and keeps the full payload behind a click', async () => {
    const { text, html } = await render(event('output', rawOutput))
    expect(text).toContain('Background task 01a1032e · output')
    expect(text).toContain('Poll test run until completion')
    expect(html).toContain('<details')
    // the raw command is inside the closed details, not in the visible line
    const visible = html.replace(/<details[\s\S]*<\/details>/, '')
    expect(visible).not.toContain('seq 1 24')
  })

  it('has no details for a short message and none for a bare lifecycle event', async () => {
    expect((await render(event('status', 'terminal process running'))).html).not.toContain('<details')
    expect((await render(event('completed'))).html).not.toContain('<details')
  })
})
