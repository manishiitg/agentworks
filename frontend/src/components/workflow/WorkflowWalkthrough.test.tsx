// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import WorkflowWalkthrough from './WorkflowWalkthrough'
import { dismissWorkflowWalkthrough, isWorkflowWalkthroughDismissed } from '../../utils/onboarding'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const targets: HTMLElement[] = []
const addTarget = (name: string, width = 200) => {
  const element = document.createElement('button')
  element.dataset.tour = name
  element.getBoundingClientRect = () => ({
    x: 20, y: 20, left: 20, top: 20, right: 20 + width, bottom: 50,
    width, height: 30, toJSON: () => ({}),
  }) as DOMRect
  document.body.append(element)
  targets.push(element)
  return element
}

afterEach(() => {
  targets.splice(0).forEach(target => target.remove())
})

describe('Context walkthroughs', () => {
  it('remembers each screen guide independently', () => {
    const surfaces = ['overview', 'empty-automation', 'automation', 'empty-crew', 'crew'] as const
    const originalStorage = Object.getOwnPropertyDescriptor(window, 'localStorage')
    const values = new Map<string, string>()
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => { values.set(key, value) },
      },
    })
    try {
      values.set('agentworks_overview_walkthrough_v2_dismissed', 'true')
      values.set('agentworks_crew_walkthrough_v2_dismissed', 'true')
      expect(isWorkflowWalkthroughDismissed('overview')).toBe(false)
      expect(isWorkflowWalkthroughDismissed('crew')).toBe(false)
      dismissWorkflowWalkthrough('empty-crew')
      expect(isWorkflowWalkthroughDismissed('empty-crew')).toBe(true)
      for (const surface of surfaces.filter(surface => surface !== 'empty-crew')) {
        expect(isWorkflowWalkthroughDismissed(surface)).toBe(false)
      }
    } finally {
      if (originalStorage) Object.defineProperty(window, 'localStorage', originalStorage)
      else Reflect.deleteProperty(window, 'localStorage')
    }
  })

  it('guides first-run users through the controls visible on Activity', async () => {
    addTarget('activity-feed')
    addTarget('workflow-add-edit')
    addTarget('global-activity')
    addTarget('global-schedules')
    addTarget('global-providers')
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="overview" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.textContent).toContain('1 of 6')
      expect(dialog.textContent).toContain('What are Goals for?')
      expect(dialog.textContent).toContain('repeatable work with a measurable result')
      expect(dialog.textContent).toContain('Example:')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Open or create an automation')
      for (const title of ['Activity', 'Your Activity home', 'Schedules', 'Providers']) {
        await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
        expect(dialog.textContent).toContain(title)
      }
      expect(dialog.querySelector('[data-testid="workflow-walkthrough-done"]')).not.toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it.each(['overview', 'empty-automation', 'empty-crew'] as const)('explains when to use each product from $surface', async (surface) => {
    const switcher = addTarget('product-switcher')
    switcher.setAttribute('aria-label', 'Switch product')
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface={surface} onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Choose a workspace')
      expect(dialog.textContent).toContain('repeatable work with a success metric')
      expect(dialog.textContent).toContain('specialist teammate that remembers an ongoing project')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('remembers the Code guides on their own, apart from Crew', () => {
    const originalStorage = Object.getOwnPropertyDescriptor(window, 'localStorage')
    const values = new Map<string, string>()
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) } },
    })
    try {
      dismissWorkflowWalkthrough('code')
      expect(isWorkflowWalkthroughDismissed('code')).toBe(true)
      for (const other of ['empty-code', 'crew', 'empty-crew', 'overview'] as const) {
        expect(isWorkflowWalkthroughDismissed(other)).toBe(false)
      }
    } finally {
      if (originalStorage) Object.defineProperty(window, 'localStorage', originalStorage)
      else Reflect.deleteProperty(window, 'localStorage')
    }
  })

  it('walks a first-time Code user through the controls they can see, skipping admin-only ones', async () => {
    for (const tour of ['crew-empty-state', 'crew-create', 'global-providers']) addTarget(tour)
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="empty-code" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.textContent).toContain('Code')
      expect(dialog.textContent).toContain('What is Code for?')
      expect(dialog.textContent).toContain('private workspace')
      const titles = ['Your workspaces start here', 'Create a workspace', 'Providers']
      for (const title of titles) {
        await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
        expect(dialog.textContent).toContain(title)
      }
      // No MCP or Users icon on this screen (not an admin), so the guide ends here.
      expect(dialog.querySelector('[data-testid="workflow-walkthrough-done"]')).not.toBeNull()
      expect(dialog.textContent).not.toContain('Add your team')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('shows an admin the MCP and Users steps in the Code getting-started guide', async () => {
    for (const tour of ['crew-empty-state', 'crew-create', 'global-providers', 'global-mcp', 'global-users']) addTarget(tour)
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="empty-code" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      for (const title of ['Your workspaces start here', 'Create a workspace', 'Providers', 'Connect an AI agent', 'Add your team']) {
        await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
        expect(dialog.textContent).toContain(title)
      }
      expect(dialog.querySelector('[data-testid="workflow-walkthrough-done"]')).not.toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('guides the open Code workspace with its own words, not Crew’s', async () => {
    for (const tour of ['crew-chat', 'chat-input-box', 'chat-send-controls', 'work-tools', 'crew-workspace']) addTarget(tour)
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="code" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.textContent).toContain('Your private workspace')
      expect(dialog.textContent).not.toContain('Crew')
      for (const title of ['Work together in chat', 'Describe the work', 'Attach and send', 'Workspace tools']) {
        await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
        expect(dialog.textContent).toContain(title)
        expect(dialog.textContent).not.toContain('Crew')
      }
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('offers a usable exit while the workspace is loading', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="overview" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.textContent).toContain('What are Goals for?')
      expect(dialog.textContent).toContain('1 of 1')
      expect(dialog.querySelector('[data-testid="workflow-walkthrough-done"]')).not.toBeNull()
      expect(dialog.querySelector('[data-testid="workflow-walkthrough-next"]')).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('moves to a visible step when the page changes under the tour', async () => {
    vi.useFakeTimers()
    const selector = addTarget('workflow-add-edit')
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="overview" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Open or create an automation')
      selector.remove()
      addTarget('global-activity')
      await act(async () => vi.advanceTimersByTime(300))
      expect(dialog.textContent).toContain('Activity')
      expect(dialog.textContent).toContain('2 of 2')
    } finally {
      await act(async () => root.unmount())
      host.remove()
      vi.useRealTimers()
    }
  })

  it('starts a separate guide when an automation workspace opens', async () => {
    addTarget('workflow-add-edit')
    addTarget('workflow-chat-pane')
    addTarget('chat-input-box')
    addTarget('chat-send-controls')
    addTarget('workflow-dashboard')
    addTarget('workflow-views')
    addTarget('workflow-operations')
    addTarget('workflow-setup')
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="automation" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.getAttribute('aria-label')).toBe('Automation workspace walkthrough')
      expect(dialog.textContent).toContain('From goal to running automation')
      expect(dialog.textContent).toContain('1 of 9')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Current automation')
      expect(dialog.textContent).toContain('2 of 9')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Build in chat')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it.each([
    { surface: 'empty-automation' as const, target: 'automation-empty-state', intro: 'Choose a goal', title: 'Start with an automation', label: 'Empty automation walkthrough' },
    { surface: 'empty-crew' as const, target: 'crew-empty-state', intro: 'What is Crew for?', title: 'Your Crew starts here', label: 'Empty Crew walkthrough' },
    { surface: 'crew' as const, target: 'crew-selector', intro: 'A teammate that remembers this project', title: 'Current Crew member', label: 'Crew workspace walkthrough' },
  ])('explains $surface before showing its controls', async ({ surface, target, intro, title, label }) => {
    addTarget(target)
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface={surface} onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.getAttribute('aria-label')).toBe(label)
      expect(dialog.textContent).toContain(intro)
      expect(dialog.textContent).toContain('Example:')
      expect(dialog.textContent).toContain('1 of 2')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain(title)
      expect(dialog.textContent).toContain('2 of 2')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('skips Crew tools that are absent from a shared or collapsed workspace', async () => {
    addTarget('crew-selector')
    addTarget('crew-chat')
    addTarget('work-tools')
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="crew" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.textContent).toContain('1 of 4')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Current Crew member')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Work together in chat')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Workspace tools')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('skips chat steps when the Crew pane is too narrow to use', async () => {
    addTarget('crew-selector')
    const chat = addTarget('crew-chat', 56)
    const input = addTarget('chat-input-box')
    chat.append(input)
    addTarget('crew-workspace')
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="crew" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      expect(dialog.textContent).toContain('1 of 3')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Current Crew member')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Workspace pane')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('resets progress when moving from Activity into an automation', async () => {
    addTarget('workflow-add-edit')
    addTarget('global-activity')
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="overview" onClose={() => {}} />))
      const dialog = document.querySelector('[data-testid="workflow-walkthrough-dialog"]')!
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Open or create an automation')
      await act(async () => (dialog.querySelector('[data-testid="workflow-walkthrough-next"]') as HTMLButtonElement).click())
      expect(dialog.textContent).toContain('Activity')
      addTarget('workflow-chat-pane')
      await act(async () => root.render(<WorkflowWalkthrough isOpen surface="automation" openToken={1} onClose={() => {}} />))
      expect(dialog.textContent).toContain('From goal to running automation')
      expect(dialog.textContent).toContain('1 of 3')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
