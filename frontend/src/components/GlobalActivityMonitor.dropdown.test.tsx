// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import type { ActiveSessionInfo } from '../services/api-types'

const getHeaderSummary = vi.hoisted(() => vi.fn())
vi.mock('../services/api', () => ({
  agentApi: { getHeaderSummary },
  getApiBaseUrl: () => '',
  getAuthToken: () => null,
}))

const openGlobalActivitySession = vi.hoisted(() => vi.fn(async () => {}))
const openGlobalTab = vi.hoisted(() => vi.fn(() => true))
vi.mock('../utils/globalProductNavigation', async importOriginal => {
  const actual = await importOriginal<typeof import('../utils/globalProductNavigation')>()
  return { ...actual, openGlobalActivitySession, openGlobalTab }
})

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

import { useChatStore } from '../stores/useChatStore'
import { ProductTopBar } from './workspace/ProductTopBar'
import { GlobalActivityMonitor } from './GlobalActivityMonitor'

const now = () => new Date().toISOString()

function session(overrides: Partial<ActiveSessionInfo>): ActiveSessionInfo {
  return {
    session_id: 'session-1',
    observer_id: '',
    agent_mode: 'workflow',
    status: 'running',
    display_status: 'busy',
    created_at: now(),
    last_activity: now(),
    ...overrides,
  } as ActiveSessionInfo
}

const workflowSession = (overrides: Partial<ActiveSessionInfo> = {}) => session({
  session_id: 'wf-session',
  agent_mode: 'workflow',
  workflow_name: 'ICICI Bank Parsing',
  workspace_path: 'Workflow/icici-bank-parsing',
  ...overrides,
})

const crewTrigger = (overrides: Partial<ActiveSessionInfo> = {}) => session({
  session_id: 'product-trigger-1',
  agent_mode: 'multi-agent',
  workspace_path: 'Chats/Work/projects/news-monitor',
  triggered_by: 'cron',
  title: 'News Monitor · news monitor trigger',
  ...overrides,
})

function renderMonitor() {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  return { host, root }
}

function click(element: Element | null) {
  expect(element).not.toBeNull()
  act(() => {
    element!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

beforeEach(() => {
  getHeaderSummary.mockResolvedValue({ active_sessions: [], schedule_summary: null })
})

afterEach(() => {
  useChatStore.setState({ chatTabs: {}, activeTabId: null, activeSessionsCache: [], activeSessionsCacheTimestamp: null })
  vi.clearAllMocks()
})

describe('global activity monitor dropdown', () => {
  it('renders nothing when nothing is active', async () => {
    const { host, root } = renderMonitor()
    try {
      await act(async () => root.render(<GlobalActivityMonitor />))
      expect(host.textContent).toBe('')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('shows one button with the count and full names in the panel', async () => {
    getHeaderSummary.mockResolvedValue({ active_sessions: [workflowSession(), crewTrigger()], schedule_summary: null })
    const { host, root } = renderMonitor()
    try {
      await act(async () => root.render(<GlobalActivityMonitor />))
      const button = host.querySelector('button[data-testid="tour-active-work-switcher"]')
      expect(button?.textContent).toContain('2 running')
      expect(document.body.querySelector('[role="menu"]')).toBeNull()

      click(button)
      const menu = document.body.querySelector('[role="menu"]')
      expect(menu?.textContent).toContain('ICICI Bank Parsing')
      expect(menu?.textContent).toContain('News Monitor')
      expect(menu?.parentElement).toBe(document.body)
      expect(host.contains(menu)).toBe(false)
      act(() => {
        menu!.dispatchEvent(new Event('pointerdown', { bubbles: true }))
      })
      expect(document.body.querySelector('[role="menu"]')).toBe(menu)
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('keeps active session switching available from the sidebar monitor', async () => {
    getHeaderSummary.mockResolvedValue({ active_sessions: [workflowSession(), crewTrigger()], schedule_summary: null })
    const { host, root } = renderMonitor()
    try {
      await act(async () => root.render(<ProductTopBar sidebar><GlobalActivityMonitor /></ProductTopBar>))
      const trigger = host.querySelector('button[data-testid="tour-active-work-switcher"]')!
      expect(trigger.getAttribute('title')).toBe('Active work · 2 running')
      expect(trigger.querySelector('svg')).not.toBeNull()
      click(trigger)
      expect(document.body.querySelector('[role="menu"]')?.textContent).toContain('News Monitor')
      click(document.body.querySelector('[role="menuitem"]'))
      expect(openGlobalActivitySession).toHaveBeenCalledWith(
        expect.objectContaining({ session_id: 'wf-session' }),
        expect.objectContaining({ source: 'global-activity-monitor' }),
      )
      expect(document.body.querySelector('[role="menu"]')).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('switches to the clicked row and closes the panel', async () => {
    const sessions = [workflowSession(), crewTrigger()]
    getHeaderSummary.mockResolvedValue({ active_sessions: sessions, schedule_summary: null })
    const { host, root } = renderMonitor()
    try {
      await act(async () => root.render(<GlobalActivityMonitor />))
      click(host.querySelector('button[data-testid="tour-active-work-switcher"]'))
      const rows = document.body.querySelectorAll('[role="menuitem"]')
      expect(rows.length).toBe(2)

      click(rows[0])
      expect(openGlobalActivitySession).toHaveBeenCalledTimes(1)
      expect(openGlobalActivitySession).toHaveBeenCalledWith(
        expect.objectContaining({ session_id: 'wf-session' }),
        expect.objectContaining({ source: 'global-activity-monitor' }),
      )
      expect(document.body.querySelector('[role="menu"]')).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('flags sessions waiting for input on the button', async () => {
    getHeaderSummary.mockResolvedValue({
      active_sessions: [workflowSession({ needs_user_input: true, waiting_message: 'Approve the plan' })],
      schedule_summary: null,
    })
    const { host, root } = renderMonitor()
    try {
      await act(async () => root.render(<GlobalActivityMonitor />))
      expect(host.querySelector('button[data-testid="tour-active-work-switcher"]')?.textContent).toContain('1 needs input')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('closes the panel on outside click and Escape', async () => {
    getHeaderSummary.mockResolvedValue({ active_sessions: [workflowSession()], schedule_summary: null })
    const { host, root } = renderMonitor()
    try {
      await act(async () => root.render(<GlobalActivityMonitor />))
      click(host.querySelector('button[data-testid="tour-active-work-switcher"]'))
      expect(document.body.querySelector('[role="menu"]')).not.toBeNull()

      act(() => {
        document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
      })
      expect(document.body.querySelector('[role="menu"]')).toBeNull()

      click(host.querySelector('button[data-testid="tour-active-work-switcher"]'))
      act(() => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      })
      expect(document.body.querySelector('[role="menu"]')).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
