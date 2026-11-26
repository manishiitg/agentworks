// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { AccountSlackOverview } from './AccountSlackOverview'

const state = vi.hoisted(() => ({ get: vi.fn(), personal: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: state.get } }))
vi.mock('./PersonalAccountsPanel', () => ({ default: (props: unknown) => { state.personal(props); return <div>Personal accounts</div> } }))

const host = document.createElement('div')
const root = createRoot(host)
afterEach(() => {
  act(() => root.render(null))
  state.get.mockReset()
  state.personal.mockClear()
})

it('shows Slack bot identity and authorized project usage alongside Slack as me on servers', async () => {
  state.get.mockResolvedValue({
    data: {
      bots: [{
        id: 'bot-one', can_manage: true, name: 'Saved helper', configured: true, enabled: true,
        scope_label: 'Crew · Example',
        identity: { name: 'Review helper', workspace: 'Example workspace', verified: true },
        used_in: [{ label: 'Example project', kind: 'work' }],
      }],
    },
  })
  await act(async () => root.render(<AccountSlackOverview />))
  expect(state.personal.mock.calls.some(([props]) => props.heading === 'Slack as me')).toBe(true)
  expect(host.querySelector('section[aria-label="Slack bots"]')).toBeTruthy()
  expect(host.textContent).toContain('Review helper')
  expect(host.textContent).toContain('Example workspace')
  expect(host.textContent).toContain('Example project')
})

it('hides Slack bots in local mode while keeping Slack as me', async () => {
  const runtimeWindow = window as Window & { __APP_RUNTIME_CONFIG__?: { deploymentMode?: string } }
  const previous = runtimeWindow.__APP_RUNTIME_CONFIG__
  runtimeWindow.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'local' }
  try {
    await act(async () => root.render(<AccountSlackOverview />))
    expect(state.personal.mock.calls.some(([props]) => props.heading === 'Slack as me')).toBe(true)
    expect(host.querySelector('section[aria-label="Slack bots"]')).toBeNull()
    expect(state.get).not.toHaveBeenCalled()
  } finally {
    runtimeWindow.__APP_RUNTIME_CONFIG__ = previous
  }
})
