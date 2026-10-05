// @vitest-environment happy-dom
import { act } from 'react'
import type { ReactElement } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import { ProviderChangeNotice, QueuedProviderLabel } from './ProviderChangeNotice'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

function text(element: ReactElement): string {
  const host = document.createElement('div')
  const root = createRoot(host)
  act(() => root.render(element))
  const out = host.textContent || ''
  act(() => root.unmount())
  return out
}

describe('provider change notices', () => {
  it('shows the picker notice only while a turn runs on a different provider', () => {
    expect(text(<ProviderChangeNotice turnRunning runningProvider="claude-code" selectedProvider="codex-cli" />))
      .toBe('Applies from your next message. The current turn finishes on Claude.')
    expect(text(<ProviderChangeNotice turnRunning={false} runningProvider="claude-code" selectedProvider="codex-cli" />)).toBe('')
    expect(text(<ProviderChangeNotice turnRunning runningProvider="claude-code" selectedProvider="claude-code" />)).toBe('')
  })

  it('labels a queued send with its new provider, and drops the label once it is no longer queued', () => {
    expect(text(<QueuedProviderLabel metadata={{ delivery_status: 'queued_for_turn', pending_provider: 'codex-cli' }} />))
      .toBe('then runs on Codex')
    expect(text(<QueuedProviderLabel metadata={{ delivery_status: 'queued_for_turn', confirmation: 'confirmed', pending_provider: 'codex-cli' }} />)).toBe('')
  })
})
