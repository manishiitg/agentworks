// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'

const { apiGet, apiPost } = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}))

vi.mock('../../services/api', () => ({
  default: { get: apiGet, post: apiPost },
}))
vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: (selector: (s: { user?: { is_admin?: boolean } }) => unknown) => selector({ user: { is_admin: true } }),
}))
vi.mock('./SlackSignInSetup', () => ({
  SlackSignInSetup: () => null,
}))

import SlackAccountsPanel from './SlackAccountsPanel'

beforeEach(() => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  apiGet.mockReset()
  apiPost.mockReset()
})

it('shows Slack workspace name, handle, and domain when available and falls back to label otherwise', async () => {
  apiGet.mockResolvedValue({
    data: {
      can_connect: true,
      sign_in_ready: true,
      accounts: [{
        id: 'c-1234abcd',
        owner: 'default',
        label: 'My Slack account',
        team_name: 'Acme Workspace',
        team_domain: 'acme-team.slack.com',
        user_name: 'alex',
        mine: true,
        selected: true,
        connected: true,
        can_add: false,
        can_detach: true,
        can_manage: true,
        can_reconnect: true,
      }],
    },
  })

  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => { root.render(<SlackAccountsPanel workspacePath="Crew/demo" />) })

  expect(host.textContent).toContain('Acme Workspace')
  expect(host.textContent).toContain('@alex')
  expect(host.textContent).toContain('acme-team.slack.com · Connected · Used by this Project')

  // Falls back cleanly when identity metadata isn't present yet.
  apiGet.mockResolvedValue({
    data: {
      can_connect: true,
      sign_in_ready: true,
      accounts: [{
        id: 'c-5678ef01',
        owner: 'default',
        label: 'My Slack account',
        mine: true,
        selected: false,
        connected: false,
        can_add: true,
        can_detach: false,
        can_manage: true,
        can_reconnect: true,
      }],
    },
  })

  const host2 = document.createElement('div')
  document.body.appendChild(host2)
  const root2 = createRoot(host2)
  await act(async () => { root2.render(<SlackAccountsPanel workspacePath="Crew/demo" />) })

  expect(host2.textContent).toContain('My Slack account')
  expect(host2.textContent).toContain('Needs sign-in · Not used by this Project')
  act(() => { root.unmount(); root2.unmount() })
})
