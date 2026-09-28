// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

const storeState = { workspaceViewTarget: null as null | { view: string; target: string; token: number } }
const openWorkspaceView = vi.fn()

vi.mock('../../stores/useWorkflowStore', () => ({
  useWorkflowStore: Object.assign(
    (selector: (state: unknown) => unknown) => selector(storeState),
    { getState: () => ({ openWorkspaceView }) },
  ),
}))
const { schedulesPanelProps } = vi.hoisted(() => ({ schedulesPanelProps: { current: null as null | Record<string, unknown> } }))
vi.mock('../scheduler/WorkflowScheduleRunsPanel', () => ({
  default: (props: Record<string, unknown>) => {
    schedulesPanelProps.current = props
    return <div data-testid="schedules" data-hide-header={String(Boolean(props.hideHeader))} data-refresh-token={String(props.refreshToken ?? 0)}>Schedule content</div>
  },
}))
const { triggersPanelProps } = vi.hoisted(() => ({ triggersPanelProps: { current: null as null | Record<string, unknown> } }))
vi.mock('../workflow/ProductAPITriggersView', () => ({ default: (props: Record<string, unknown>) => {
  triggersPanelProps.current = props
  return <div data-testid="triggers" data-hide-header={String(Boolean(props.hideHeader))} data-refresh-token={String(props.refreshToken ?? 0)}>Trigger content</div>
} }))
vi.mock('../workflow/WorkflowAPITriggersView', () => ({ default: (props: Record<string, unknown>) => {
  triggersPanelProps.current = props
  return <div data-testid="workflow-triggers" data-hide-header={String(Boolean(props.hideHeader))} data-refresh-token={String(props.refreshToken ?? 0)}>Workflow trigger content</div>
} }))
vi.mock('./CrewFunctionsView', () => ({ default: () => <div data-testid="functions">Function content</div> }))
vi.mock('./TriggerDeliveryHistoryPanel', () => ({ TriggerDeliveryHistoryPanel: () => <div data-testid="delivery-history" /> }))
const { askAIProps } = vi.hoisted(() => ({ askAIProps: { current: null as null | Record<string, unknown> } }))
vi.mock('../workflow/AskAIButton', () => ({
  AskAIButton: (props: Record<string, unknown>) => {
    askAIProps.current = props
    return <button type="button" data-testid="ask-ai" data-message={String(props.message)}>Ask AI</button>
  },
}))

import { AutomationHubPanel } from './AutomationHubPanel'

const { chatProps } = vi.hoisted(() => ({ chatProps: { current: null as null | Record<string, unknown> } }))
function ChatProbe(props: Record<string, unknown>) {
  chatProps.current = props
  return <div data-testid="chats">Chat history</div>
}

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('AutomationHubPanel', () => {
  it('keeps schedules, triggers, bots, and chats in one responsive panel opening on schedules', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    await act(async () => root.render(<AutomationHubPanel
      entityType="product"
      workspacePath="Work/projects/release"
      productTriggerScope={{ profileId: 'work', projectId: 'release' }}
      chatContent={<div data-testid="chats">Chat history</div>}
      botContent={<div data-testid="bots">Bot routes</div>}
    />))

    try {
      expect(host.textContent).toContain('Automation')
      const panel = host.querySelector('[data-testid="automation-hub-panel"]')
      expect(panel?.className).toContain('w-full')
      expect(panel?.className).toContain('flex-1')
      expect(panel?.className).toContain('min-w-0')
      const tabs = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Automation center"] [role="tab"]'))
      expect(tabs.map(tab => tab.textContent)).toEqual(['Schedules', 'Webhooks', 'Functions', 'Bots', 'Chats'])
      expect(host.querySelector('[data-testid="schedules"]')?.getAttribute('data-hide-header')).toBe('true')

      await act(async () => { tabs[4]!.click(); await Promise.resolve() })
      expect(host.querySelector('[data-testid="chats"]')).not.toBeNull()
      await act(async () => { tabs[0]!.click(); await Promise.resolve() })
      expect(host.querySelector('[data-testid="schedules"]')?.getAttribute('data-hide-header')).toBe('true')
      await act(async () => { tabs[1]!.click(); await Promise.resolve() })
      expect(host.querySelector('[data-testid="triggers"]')).not.toBeNull()
      await act(async () => { tabs[2]!.click(); await Promise.resolve() })
      expect(host.querySelector('[data-testid="functions"]')).not.toBeNull()
      expect(openWorkspaceView).toHaveBeenLastCalledWith('workshop', 'functions')
      await act(async () => { tabs[3]!.click(); await Promise.resolve() })
      expect(host.querySelector('[data-testid="bots"]')).not.toBeNull()
      expect(openWorkspaceView).toHaveBeenLastCalledWith('workshop', 'bots')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})

describe('AutomationHubPanel Ask AI', () => {
  async function mountHub(props: Partial<React.ComponentProps<typeof AutomationHubPanel>> & { entityType: 'workflow' | 'product' }) {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    await act(async () => root.render(<AutomationHubPanel
      workspacePath="Workflow/one"
      {...props}
    />))
    return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
  }

  async function openWalkthrough(host: HTMLElement) {
    await act(async () => {
      (host.querySelector('button[aria-label^="Walkthrough:"]') as HTMLButtonElement).click()
      await Promise.resolve()
    })
  }

  function popupAskMessage(host: HTMLElement) {
    return host.querySelector('[role="dialog"] [data-testid="ask-ai"]')?.getAttribute('data-message')
  }

  it('renders one popup Ask AI whose message follows the active tab', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'workflow',
      workflowScope: { workspacePath: 'Workflow/one' },
      chatContent: <div>Chats</div>,
    })
    try {
      const tabs = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Automation center"] [role="tab"]'))
      expect(host.querySelector('[data-testid="ask-ai"]')).toBeNull()
      await openWalkthrough(host)
      expect(host.querySelectorAll('[data-testid="ask-ai"]').length).toBe(1)
      expect(popupAskMessage(host)).toContain('set up or change a schedule')
      await act(async () => { tabs.find(tab => tab.textContent === 'Chats')!.click(); await Promise.resolve() })
      expect(host.querySelectorAll('[data-testid="ask-ai"]').length).toBe(1)
      expect(popupAskMessage(host)).toContain('past chats and automatic jobs')
      await act(async () => { tabs.find(tab => tab.textContent === 'Webhooks')!.click(); await Promise.resolve() })
      expect(popupAskMessage(host)).toContain('set up or change a webhook')
      expect(host.querySelector('[data-testid="schedules"] [data-testid="ask-ai"]')).toBeNull()
    } finally {
      await unmount()
    }
  })

  it('renders explicit per-tab messages with the provided send', async () => {
    const onAskAI = vi.fn()
    const { host, unmount } = await mountHub({
      entityType: 'product',
      productTriggerScope: { profileId: 'work', projectId: 'p1' },
      chatContent: <div>Chats</div>,
      askAIMessages: { schedules: 'Crew schedules help', chats: 'Crew chats help' },
      onAskAI,
    })
    try {
      const tabs = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Automation center"] [role="tab"]'))
      await openWalkthrough(host)
      expect(popupAskMessage(host)).toBe('Crew schedules help')
      expect(askAIProps.current?.onAsk).toBe(onAskAI)
      await act(async () => { tabs.find(tab => tab.textContent === 'Chats')!.click(); await Promise.resolve() })
      expect(popupAskMessage(host)).toBe('Crew chats help')
    } finally {
      await unmount()
    }
  })

  it('renders no Ask AI for product hubs without messages', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'product',
      productTriggerScope: { profileId: 'work', projectId: 'p1' },
      chatContent: <div>Chats</div>,
    })
    try {
      expect(host.querySelector('[data-testid="ask-ai"]')).toBeNull()
      await openWalkthrough(host)
      expect(host.querySelector('[role="dialog"] [data-testid="ask-ai"]')).toBeNull()
    } finally {
      await unmount()
    }
  })

  it('offers schedules refresh beside the header pair on the Schedules tab', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'workflow',
      workflowScope: { workspacePath: 'Workflow/one' },
      chatContent: <div>Chats</div>,
    })
    try {
      const tabs = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Automation center"] [role="tab"]'))
      const refresh = () => host.querySelector('button[aria-label="Refresh schedules"]')
      expect(refresh()).not.toBeNull()
      expect(host.querySelector('[data-testid="schedules"]')?.getAttribute('data-refresh-token')).toBe('0')
      await act(async () => { refresh()!.dispatchEvent(new MouseEvent('click', { bubbles: true })); await Promise.resolve() })
      expect(host.querySelector('[data-testid="schedules"]')?.getAttribute('data-refresh-token')).toBe('1')
      await act(async () => { tabs.find(tab => tab.textContent === 'Chats')!.click(); await Promise.resolve() })
      expect(refresh()).toBeNull()
    } finally {
      await unmount()
    }
  })

  it('shows reported schedule status below the header on the Schedules tab', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'workflow',
      workflowScope: { workspacePath: 'Workflow/one' },
      chatContent: <div>Chats</div>,
    })
    try {
      const onStatus = schedulesPanelProps.current?.onStatus as ((status: object) => void) | undefined
      expect(onStatus).toBeTypeOf('function')
      await act(async () => {
        onStatus!({
          isWorkflowScoped: true,
          isLoading: false,
          isSchedulerPaused: false,
          summary: { total: 3, lastRunAt: null, enabled: 3, running: 0 },
          workflowScheduleSummary: { workflows: 1, running: 0, fullyPaused: 0, partlyPaused: 0 },
        })
        await Promise.resolve()
      })
      expect(host.textContent).toContain('3 schedules')
      expect(host.textContent).toContain('3 active')
    } finally {
      await unmount()
    }
  })

  it('embeds triggers without their header and refreshes them from the hub', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'product',
      productTriggerScope: { profileId: 'work', projectId: 'p1' },
      chatContent: <div>Chats</div>,
    })
    try {
      const tabs = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Automation center"] [role="tab"]'))
      const refresh = () => host.querySelector('button[aria-label="Refresh webhooks"]')
      expect(refresh()).toBeNull()
      await act(async () => { tabs.find(tab => tab.textContent === 'Webhooks')!.click(); await Promise.resolve() })
      expect(host.querySelector('[data-testid="triggers"]')?.getAttribute('data-hide-header')).toBe('true')
      expect(host.querySelector('[data-testid="triggers"]')?.getAttribute('data-refresh-token')).toBe('0')
      expect(refresh()).not.toBeNull()
      await act(async () => { refresh()!.dispatchEvent(new MouseEvent('click', { bubbles: true })); await Promise.resolve() })
      expect(host.querySelector('[data-testid="triggers"]')?.getAttribute('data-refresh-token')).toBe('1')
    } finally {
      await unmount()
    }
  })

  it('shows reported trigger counts beside the title on the Triggers tab', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'workflow',
      workflowScope: { workspacePath: 'Workflow/one' },
      chatContent: <div>Chats</div>,
    })
    try {
      const tabs = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Automation center"] [role="tab"]'))
      await act(async () => { tabs.find(tab => tab.textContent === 'Webhooks')!.click(); await Promise.resolve() })
      expect(host.querySelector('[data-testid="workflow-triggers"]')?.getAttribute('data-hide-header')).toBe('true')
      const onCounts = triggersPanelProps.current?.onCounts as ((counts: { active: number; paused: number }) => void) | undefined
      expect(onCounts).toBeTypeOf('function')
      await act(async () => {
        onCounts!({ active: 2, paused: 1 })
        await Promise.resolve()
      })
      expect(host.textContent).toContain('2 active · 1 paused')
    } finally {
      await unmount()
    }
  })

  it('refreshes chats from the hub header inside a full-height scrolling slot', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'workflow',
      workflowScope: { workspacePath: 'Workflow/one' },
      chatContent: <ChatProbe />,
    })
    try {
      const tabs = Array.from(host.querySelectorAll<HTMLButtonElement>('[aria-label="Automation center"] [role="tab"]'))
      const refresh = () => host.querySelector('button[aria-label="Refresh chats"]')
      expect(refresh()).toBeNull()
      await act(async () => { tabs.find(tab => tab.textContent === 'Chats')!.click(); await Promise.resolve() })
      expect(chatProps.current?.refreshToken).toBe(0)
      const slot = host.querySelector('[data-testid="automation-hub-chats"]')
      expect(slot?.className).toContain('flex')
      expect(slot?.className).toContain('h-full')
      expect(slot?.className).toContain('min-h-0')
      expect(slot?.className).toContain('flex-col')
      expect(refresh()).not.toBeNull()
      await act(async () => { refresh()!.dispatchEvent(new MouseEvent('click', { bubbles: true })); await Promise.resolve() })
      expect(chatProps.current?.refreshToken).toBe(1)
    } finally {
      await unmount()
    }
  })

  it('places the walkthrough left of the per-tab refresh button', async () => {
    const { host, unmount } = await mountHub({
      entityType: 'workflow',
      workflowScope: { workspacePath: 'Workflow/one' },
      chatContent: <div>Chats</div>,
    })
    try {
      const buttons = Array.from(host.querySelectorAll('header button'))
      expect(buttons.some(button => button.getAttribute('data-testid') === 'ask-ai')).toBe(false)
      const walkthroughIndex = buttons.findIndex(button => (button.getAttribute('aria-label') || '').startsWith('Walkthrough'))
      const refreshIndex = buttons.findIndex(button => (button.getAttribute('aria-label') || '').startsWith('Refresh'))
      expect(walkthroughIndex).toBeGreaterThanOrEqual(0)
      expect(refreshIndex).toBeGreaterThanOrEqual(0)
      expect(walkthroughIndex).toBeLessThan(refreshIndex)
    } finally {
      await unmount()
    }
  })
})
