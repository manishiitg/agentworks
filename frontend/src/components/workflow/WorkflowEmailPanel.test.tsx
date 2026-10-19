// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ notifications: vi.fn(), settings: { gmailConnections: [] } }))
vi.mock('./bots/useWorkflowBots', () => ({ useWorkflowBots: () => mocks.settings }))
vi.mock('./bots/GmailNotifications', () => ({ GmailNotifications: (props: unknown) => { mocks.notifications(props); return null } }))
import WorkflowEmailPanel from './WorkflowEmailPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('uses the common account surface without a per-product Google sign-in form', async () => {
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    for (const path of ['Crew/support', 'Workflow/support', 'Workflow/relays/support']) {
      await act(async () => root.render(<WorkflowEmailPanel workspacePath={path} />))
      expect(mocks.notifications).toHaveBeenLastCalledWith(expect.objectContaining({ bots: mocks.settings, workspacePath: path }))
      expect(mocks.notifications.mock.lastCall?.[0]).not.toHaveProperty('platformConnect')
    }
  } finally { act(() => root.unmount()) }
})
