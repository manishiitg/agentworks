// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { EventDispatcher } from './EventDispatcher'
import type { PollingEvent } from '../../services/api-types'

// A Muse multiple-choice question rendered as "Unknown Event Type: coding_agent_question" with the raw JSON in the
// detailed chat view (Excellence 2026-10-03). These are the two events from that report, trimmed.
const requested = {
  type: 'coding_agent_question',
  timestamp: '2026-10-03T16:01:38.668877623+02:00',
  data: {
    data: {
      provider: 'muse-cli', kind: 'requested', prompt_id: '01a10212-07ec-76a2-a627-fed5febfbd56',
      questions: [
        { id: 'fields', header: 'Fields', question: 'Which invoice fields should the Relay return as JSON?', options: [{ label: 'Standard set (Recommended)' }, { label: 'Minimal total only' }] },
        { id: 'pdf_type', header: 'PDF type', question: 'Are your PDFs digital text PDFs or scanned images needing OCR?', options: [{ label: 'Digital text (Recommended)' }, { label: 'Scanned images' }] },
      ],
    },
  },
} as unknown as PollingEvent

const settled = {
  type: 'coding_agent_question',
  timestamp: '2026-10-03T16:02:58.668857806+02:00',
  data: {
    data: {
      provider: 'muse-cli', kind: 'settled', outcome: 'answered', prompt_id: '01a10212-07ec-76a2-a627-fed5febfbd56',
      answers: [{ id: 'fields', selected_labels: ['Standard set (Recommended)'] }, { id: 'pdf_type', selected_labels: ['Digital text (Recommended)'] }],
    },
  },
} as unknown as PollingEvent

let container: HTMLDivElement | null = null
afterEach(() => { container?.remove(); container = null })

async function render(event: PollingEvent): Promise<string> {
  container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => { root.render(<EventDispatcher event={event} />) })
  const text = container.textContent || ''
  await act(async () => { root.unmount() })
  return text
}

describe('coding_agent_question in the detailed view', () => {
  it('shows what the agent asked, not the raw event', async () => {
    const text = await render(requested)
    expect(text).not.toContain('Unknown Event Type')
    expect(text).toContain('Muse asked')
    expect(text).toContain('Which invoice fields should the Relay return as JSON?')
    expect(text).toContain('Standard set (Recommended)')
  })

  it('shows what was chosen once answered', async () => {
    const text = await render(settled)
    expect(text).not.toContain('Unknown Event Type')
    expect(text).toContain("Muse's question answered")
    expect(text).toContain('fields: Standard set (Recommended)')
  })
})
