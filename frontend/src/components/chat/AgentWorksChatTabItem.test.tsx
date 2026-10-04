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
