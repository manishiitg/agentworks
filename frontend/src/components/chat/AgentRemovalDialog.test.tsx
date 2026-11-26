// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

const previewAgentRemoval = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ agentApi: { previewAgentRemoval } }))

import { AgentRemovalDialog, requestAgentRemoval } from './AgentRemovalDialog'
import type { ChatTab } from '../../stores/useChatStore'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function makeTab(): ChatTab {
  return {
    tabId: 'tab-1',
    name: 'Plugin test',
    metadata: {
      agentProfileId: 'profile-1',
      agentProfileProjectId: 'proj-1',
      agentProfileConversationKey: 'proj-1:chat:1',
    },
  } as ChatTab
}

describe('AgentRemovalDialog', () => {
  it('shows Enter hint on Remove agent and confirms on Enter when ready', async () => {
    previewAgentRemoval.mockResolvedValueOnce({
      confirmation: 'tok-123',
      effects: ['Delete this agent'],
      dependencies: [],
      destinations: [],
    })
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<AgentRemovalDialog projectId="proj-1" />))
      let resolved: unknown = 'pending'
      await act(async () => {
        void requestAgentRemoval(makeTab()).then(value => { resolved = value })
      })
      const dialog = document.body.querySelector('[role="dialog"]')!
      expect(dialog).toBeTruthy()
      const removeButton = [...dialog.querySelectorAll('button')].find(button => button.textContent?.includes('Remove agent'))!
      expect(removeButton.textContent).toContain('Enter ↵')
      expect(removeButton.disabled).toBe(false)

      await act(async () => {
        dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      })
      expect(resolved).toEqual({ confirmation: 'tok-123', choices: {} })
      expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('does not submit on Enter when dependencies still require a choice, and cancels on Escape', async () => {
    previewAgentRemoval.mockResolvedValueOnce({
      confirmation: 'tok-456',
      effects: ['Delete this agent'],
      dependencies: [{ id: 'dep-1', name: 'Daily run', kind: 'schedule', can_reassign: false }],
      destinations: [],
    })
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<AgentRemovalDialog projectId="proj-1" />))
      let resolved: unknown = 'pending'
      await act(async () => {
        void requestAgentRemoval(makeTab()).then(value => { resolved = value })
      })
      const dialog = document.body.querySelector('[role="dialog"]')!
      await act(async () => {
        dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      })
      expect(resolved).toBe('pending')

      await act(async () => {
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      })
      expect(resolved).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
