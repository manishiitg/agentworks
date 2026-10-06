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

async function renderTab(name: string, displayName?: string, productSurface?: ProductSurface) {
  const container = document.createElement("div");
  document.body.append(container);
  const tab = { tabId: "tab-1", name } as ChatTab;
  await act(async () => createRoot(container).render(
    <AgentWorksChatTabItem tab={tab} isActive={false} canClose isBlank={false} displayName={displayName} productSurface={productSurface} onTabClick={() => {}} onCloseTab={() => {}} />,
  ));
  return container;
}

it("shows the full chat name on hover when the tab truncates it", async () => {
  const host = await renderTab("RTS Flow Tester — sprint regression checks");
  const label = Array.from(host.querySelectorAll("span")).find(el => el.textContent === "RTS Flow Tester — sprint regression checks");
  expect(label?.getAttribute("title")).toBe("RTS Flow Tester — sprint regression checks");
});

it.each(['agentworks', 'relays', 'work', 'code', 'mcp-gateway'] as ProductSurface[])('shows the %s product mark while retaining the status dot and name', async surface => {
  const host = await renderTab('sales-outreach', undefined, surface);
  expect(host.querySelector('[data-product-icon]')?.getAttribute('data-product-icon')).toBe(surface);
  expect(host.querySelector('[aria-label="Ready"]')).not.toBeNull();
  expect(host.textContent).toContain('sales-outreach');
});

it("shows the stored name on hover when a shorter display name is shown", async () => {
  const host = await renderTab("Daily Notion status for the web team", "Daily Notion");
  const label = Array.from(host.querySelectorAll("span")).find(el => el.textContent === "Daily Notion");
  expect(label?.getAttribute("title")).toBe("Daily Notion status for the web team");
});

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
  expect((await render({ isStreaming: true }))?.getAttribute("aria-label")).toBe("Needs your input");
  useChatStore.setState({ activeSessionsCache: [] });

  expect((await render({}))?.getAttribute("aria-label")).toBe("Ready");
});
