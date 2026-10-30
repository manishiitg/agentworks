// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChatTab } from "../../stores/useChatStore";

vi.mock("../../stores/useAuthStore", () => ({
  useAuthStore: (select: (state: { user: null; isMultiUserMode: boolean }) => unknown) => select({ user: null, isMultiUserMode: false }),
}));
vi.mock("../../utils/workflowPermissions", () => ({ isWorkflowReadOnly: () => false }));

import { AgentWorksChatTabItem } from "./AgentWorksChatTabItem";
import type { ProductSurface } from '../../products/productSurfaceConfig';

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
});

afterEach(() => {
  document.body.innerHTML = "";
});

async function renderTab(name: string, displayName?: string, productSurface?: ProductSurface, metadata?: ChatTab['metadata']) {
  const container = document.createElement("div");
  document.body.append(container);
  const tab = { tabId: "tab-1", name, metadata } as ChatTab;
  await act(async () => createRoot(container).render(
    <AgentWorksChatTabItem tab={tab} isActive={false} canClose isBlank={false} displayName={displayName} productSurface={productSurface} onTabClick={() => {}} onCloseTab={() => {}} />,
  ));
  return container;
}

it("shows the full chat name on hover when the tab truncates it", async () => {
  const host = await renderTab("project-a Flow Tester — sprint regression checks");
  const label = Array.from(host.querySelectorAll("span")).find(el => el.textContent === "project-a Flow Tester — sprint regression checks");
  expect(label?.getAttribute("title")).toBe("project-a Flow Tester — sprint regression checks");
});

it.each(['agentworks', 'relays', 'work', 'mcp-gateway'] as ProductSurface[])('shows the %s product mark and workspace name without an idle status dot', async surface => {
  const host = await renderTab('sales-outreach', undefined, surface);
  expect(host.querySelector('[data-product-icon]')?.getAttribute('data-product-icon')).toBe(surface);
  expect(host.querySelector('[aria-label="Ready"]')).toBeNull();
  expect(host.textContent).toContain('sales-outreach');
  expect(host.textContent).not.toContain('Ready');
});

it("shows the stored name on hover when a shorter display name is shown", async () => {
  const host = await renderTab("Daily Notion status for the web team", "Daily Notion");
  const label = Array.from(host.querySelectorAll("span")).find(el => el.textContent === "Daily Notion");
  expect(label?.getAttribute("title")).toBe("Daily Notion status for the web team");
});

it('keeps generated run prompts out of the visible tab and its tooltip without changing the saved name', async () => {
  const original = 'WORKFLOW REVIEW BEFORE A RUN. workspace_path="Workflow/trading", pulse_run_id="one"'
  const host = await renderTab(original)
  expect(host.textContent).toContain('Scheduled review')
  expect(host.textContent).not.toContain('workspace_path=')
  expect(host.querySelector('[title="Scheduled review"]')).not.toBeNull()
})

it('warns before stopping a chat, while view tabs close without stopping confirmation', async () => {
  const onClose = vi.fn()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const tab = { tabId: 'close-chat', name: 'Reports', sessionId: 'chat-session' } as ChatTab
  const render = async (stops: boolean) => act(async () => root.render(
    <AgentWorksChatTabItem tab={tab} isActive canClose isBlank={false} closeStopsSession={stops} onTabClick={() => {}} onCloseTab={onClose} />,
  ))
  try {
    await render(true)
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="Close Reports"]')!.click() })
    expect(onClose).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('Closing this chat stops its background runs.')
    const button = (name: string) => Array.from(document.body.querySelectorAll('button')).find(el => el.textContent === name)!
    await act(async () => { button('Cancel').click() })
    expect(onClose).not.toHaveBeenCalled()
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="Close Reports"]')!.click() })
    await act(async () => { button('Close chat').click() })
    expect(onClose).toHaveBeenCalledWith('close-chat')
    onClose.mockClear()
    await render(false)
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="Close Reports"]')!.click() })
    expect(onClose).toHaveBeenCalledWith('close-chat')
    expect(document.body.textContent).not.toContain('Closing this chat stops its background runs.')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

// Owner, 2026-10-06: the state must read at a glance on a tab you are not
// looking at: a spinner while working, amber when it waits for you.
it("shows working, waiting and idle on an unfocused tab", async () => {
  const { useChatStore } = await import("../../stores/useChatStore");
  const render = async (tab: Partial<ChatTab>) => {
    const container = document.createElement("div");
    document.body.append(container);
    await act(async () => createRoot(container).render(
      <AgentWorksChatTabItem tab={{ tabId: "tab-1", name: "task2", sessionId: "s1", ...tab } as ChatTab} isActive={false} canClose isBlank={false} onTabClick={() => {}} onCloseTab={() => {}} />
    ));
    return container.querySelector('[role="img"]');
  };
  const working = await render({ isStreaming: true });
  expect(working?.getAttribute("aria-label")).toBe("Working");
  expect(working?.tagName.toLowerCase()).toBe("svg");

  useChatStore.setState({ activeSessionsCache: [{ session_id: "s1", needs_user_input: true }] as never });
  expect((await render({ isStreaming: true }))?.getAttribute("aria-label")).toBe("Needs you");
  useChatStore.setState({ activeSessionsCache: [] });

  expect((await render({}))?.getAttribute("aria-label")).toBe("Ready");
});

it('makes the workspace name prominent beside its product icon, with the product named on hover', async () => {
  const host = await renderTab('Company CA', undefined, 'work')
  expect(host.textContent).not.toContain('Crew/Company CA')
  expect(host.querySelector('[data-product-icon]')?.getAttribute('title')).toBe('Crew')
  expect(host.querySelector('[data-product-icon]')?.getAttribute('aria-label')).toBe('Crew')
  const name = [...host.querySelectorAll('span')].find(node => node.textContent === 'Company CA')!
  expect(name.className).toContain('font-semibold')
  expect(name.closest('[role="button"]')?.className).toContain('text-sm')
})

it('does not duplicate foreground work on the workspace title while preserving each chat spinner', async () => {
  const { useChatStore } = await import('../../stores/useChatStore')
  const original = useChatStore.getState()
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container)
  const tab = { tabId: 'project-main', name: 'Main', sessionId: 'main', isStreaming: true } as ChatTab
  try {
    useChatStore.setState({ chatTabs: { [tab.tabId]: tab }, activeSessionsCache: [] })
    await act(async () => root.render(<AgentWorksChatTabItem tab={tab} isActive canClose={false} isBlank={false} productSurface="work" workspaceActivity={null} onTabClick={() => {}} onCloseTab={() => {}} />))
    expect(container.querySelector('[aria-label="Working"]')).toBeNull()
    await act(async () => root.render(<AgentWorksChatTabItem tab={tab} isActive canClose={false} isBlank={false} productSurface="work" workspaceActivity="running" onTabClick={() => {}} onCloseTab={() => {}} />))
    expect(container.querySelector('[aria-label="Working"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()); container.remove(); useChatStore.setState(original, true) }
})

it.each(['agentworks', 'relays', 'work', 'mcp-gateway'] as ProductSurface[])('keeps %s completion state in the dot and accessible label without repeating Done', async surface => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    const tab = { tabId: 'completion-dot', name: 'Reports', hasUnreadCompletion: true } as ChatTab
    await act(async () => root.render(<AgentWorksChatTabItem tab={tab} isActive={false} canClose={false} isBlank={false} productSurface={surface} onTabClick={() => {}} onCloseTab={() => {}} />))
    expect(host.querySelector('[aria-label="Completed — open to mark as seen"]')).not.toBeNull()
    expect(host.querySelector('[title="Reports"]')?.textContent).toBe('Reports')
    expect(host.textContent).not.toContain('Done')
  } finally { await act(async () => root.unmount()); host.remove() }
})

it.each([undefined, 'agentworks', 'relays', 'work', 'mcp-gateway'] as (ProductSurface | undefined)[])('reserves the same status space through blank, idle, running, waiting and completion for %s tabs', async surface => {
  const { useChatStore } = await import('../../stores/useChatStore')
  const original = useChatStore.getState()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const tab = { tabId: 'status-layout', name: 'Reports', sessionId: 'status-session' } as ChatTab
  vi.useFakeTimers()
  try {
    useChatStore.setState({ chatTabs: {}, activeSessionsCache: [] })
    const render = async (overrides: Partial<ChatTab> = {}, blank = false) => {
      await act(async () => root.render(<AgentWorksChatTabItem tab={{ ...tab, ...overrides }} isActive={false} canClose={false} isBlank={blank} productSurface={surface} onTabClick={() => {}} onCloseTab={() => {}} />))
      await act(async () => { vi.advanceTimersByTime(1200) })
    }
    await render({}, true)
    const slot = host.querySelector('[data-chat-status-slot]')!
    const slotClasses = slot.className
    expect(slotClasses).toContain('w-3')
    expect(slotClasses).toContain('shrink-0')
    expect(slot.childElementCount).toBe(0)

    const expectSlot = (label: string | null) => {
      expect(host.querySelector('[data-chat-status-slot]')).toBe(slot)
      expect(slot.className).toBe(slotClasses)
      expect(slot.querySelector('[role="img"]')?.getAttribute('aria-label') ?? null).toBe(label)
      expect(host.querySelector('[title="Reports"]')?.textContent).toBe('Reports')
      expect(host.textContent).not.toContain('Done')
    }
    await render()
    expectSlot(surface ? null : 'Ready')
    await render({ isStreaming: true })
    expectSlot('Working')
    await act(async () => { useChatStore.setState({ activeSessionsCache: [{ session_id: tab.sessionId, needs_user_input: true }] as never }) })
    await render()
    expectSlot('Needs you')
    // The server's failed phase (a turn with no response) shows Failed until the next turn.
    await act(async () => { useChatStore.setState({ activeSessionsCache: [{ session_id: tab.sessionId, runtime_state: { phase: 'failed' } }] as never }) })
    await render()
    expectSlot('Failed')
    await act(async () => { useChatStore.setState({ activeSessionsCache: [] }) })
    await render({ hasUnreadCompletion: true })
    expectSlot('Completed — open to mark as seen')
    await render()
    expectSlot(surface ? null : 'Ready')
  } finally {
    await act(async () => root.unmount())
    vi.useRealTimers()
    host.remove()
    useChatStore.setState(original, true)
  }
})

it('announces a tab changing to working and waiting without repeating idle labels on mount', async () => {
  const { useChatStore } = await import('../../stores/useChatStore')
  const original = useChatStore.getState()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  vi.useFakeTimers()
  try {
    useChatStore.setState({ chatTabs: {}, activeSessionsCache: [] })
    const tab = { tabId: 'announced-tab', name: 'Reports', sessionId: 'announced-session' } as ChatTab
    const render = async (streaming = false) => act(async () => root.render(<AgentWorksChatTabItem tab={{ ...tab, isStreaming: streaming }} isActive={false} canClose={false} isBlank={false} productSurface="work" onTabClick={() => {}} onCloseTab={() => {}} />))
    await render()
    const status = host.querySelector('[role="status"]')!
    expect(status.textContent).toBe('')
    await render(true)
    expect(status.textContent).toBe('Reports: Working')
    await act(async () => { useChatStore.setState({ activeSessionsCache: [{ session_id: tab.sessionId, needs_user_input: true }] as never }) })
    await act(async () => { vi.advanceTimersByTime(1200) })
    expect(status.textContent).toBe('Reports: Needs you')
    await act(async () => { vi.advanceTimersByTime(15_000) })
    expect(status.textContent).toBe('Reports: Needs you')
  } finally {
    await act(async () => root.unmount())
    vi.useRealTimers()
    host.remove()
    useChatStore.setState(original, true)
  }
})


it.each([true, false])('keeps full owner identity in tooltips without per-tab ownership badges (%s)', async yours => {
  const owner = 'alex.taylor@example.com'
  const host = await renderTab(`${owner} · Builder`, undefined, 'work', {
    agentProfileId: 'work', agentProfileTabOwnerID: 'owner-one', agentProfileTabOwnerName: owner, agentProfileTabCanWrite: yours,
  })
  expect(host.textContent).toContain('Main')
  expect(host.textContent).not.toContain(owner)
  expect(host.textContent).not.toContain('You')
  expect(host.querySelector(`[aria-label="Tab owned by ${owner}"]`)).toBeNull()
  expect(host.querySelector(`[aria-label="Your tab · ${owner}"]`)).toBeNull()
  expect(host.querySelector('span[title]')?.getAttribute('title')).toContain(owner)
})
