// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const storeState = { workspaceViewTarget: null as null | { view: string; target: string; token: number } };
const openWorkspaceView = vi.fn();
const refreshWorkspaceView = vi.fn();

vi.mock("../../stores/useWorkflowStore", () => ({
  useWorkflowStore: Object.assign(
    (selector: (state: unknown) => unknown) => selector(storeState),
    { getState: () => ({ openWorkspaceView, refreshWorkspaceView }) },
  ),
}));
vi.mock("./LearningsView", () => ({
  default: ({ hideHeader }: { hideHeader?: boolean }) => (
    <div data-testid="learnings" data-hide-header={String(Boolean(hideHeader))}>Learnings content</div>
  ),
}));
vi.mock("./KnowledgebaseView", () => ({
  default: ({ hideHeader }: { hideHeader?: boolean }) => (
    <div data-testid="knowledgebase" data-hide-header={String(Boolean(hideHeader))}>Knowledgebase content</div>
  ),
}));
vi.mock("./DatabaseView", () => ({
  default: ({ hideHeader }: { hideHeader?: boolean }) => (
    <div data-testid="database" data-hide-header={String(Boolean(hideHeader))}>Database content</div>
  ),
}));
vi.mock("./AskAIButton", () => ({
  AskAIButton: ({ message }: { message: string }) => <button type="button" data-testid="ask-ai" data-message={message}>Ask AI</button>,
}));

import KnowledgeView from "./KnowledgeView";

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  storeState.workspaceViewTarget = null;
  vi.clearAllMocks();
});

afterEach(() => {
  document.body.innerHTML = "";
});

async function renderView() {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () =>
    root.render(<KnowledgeView workspacePath="/tmp/workflow-test" plan={null} />),
  );
  return container;
}

function tabByName(host: HTMLElement, name: string) {
  const tabs = Array.from(host.querySelectorAll('[role="tab"]'));
  const tab = tabs.find(candidate => candidate.textContent === name);
  if (!tab) throw new Error(`missing tab ${name}`);
  return tab as HTMLElement;
}

async function openWalkthrough(host: HTMLElement) {
  const guide = host.querySelector('button[aria-label^="Walkthrough:"]') as HTMLElement;
  await act(async () => {
    guide.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
}

function popupAskMessage(host: HTMLElement) {
  return host.querySelector('[role="dialog"] [data-testid="ask-ai"]')?.getAttribute("data-message");
}

it("renders one header with three tabs and learnings first", async () => {
  const host = await renderView();
  expect(host.querySelector("h2")?.textContent).toBe("Knowledge");
  expect(Array.from(host.querySelectorAll('[role="tab"]')).map(tab => tab.textContent))
    .toEqual(["Learnings", "Knowledgebase", "Database"]);
  expect(tabByName(host, "Learnings").getAttribute("aria-selected")).toBe("true");
  // Embedded views hide their own headers; only the active tab's content shows.
  expect(host.querySelector('[data-testid="learnings"]')?.getAttribute("data-hide-header")).toBe("true");
  expect(host.querySelector('[data-testid="knowledgebase"]')).toBeNull();
  expect(host.querySelector('[data-testid="database"]')).toBeNull();
  expect(host.querySelector('[aria-label="Refresh Learnings"]')).not.toBeNull();
  expect(host.querySelector('[data-testid="ask-ai"]')).toBeNull();
  await openWalkthrough(host);
  expect(popupAskMessage(host)).toContain("Knowledge · Learnings");
});

it("switches content, Ask AI, and refresh per tab", async () => {
  const host = await renderView();
  await act(async () => {
    tabByName(host, "Database").dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  expect(tabByName(host, "Database").getAttribute("aria-selected")).toBe("true");
  expect(host.querySelector('[data-testid="database"]')).not.toBeNull();
  expect(host.querySelector('[data-testid="learnings"]')).toBeNull();
  expect(host.querySelector('[aria-label="Refresh Database"]')).not.toBeNull();
  await openWalkthrough(host);
  expect(popupAskMessage(host)).toContain("Knowledge · Database");
  expect(openWorkspaceView).toHaveBeenCalledWith("knowledge", "database");
});

it("opens on the targeted tab", async () => {
  storeState.workspaceViewTarget = { view: "knowledge", target: "knowledgebase", token: 1 };
  const host = await renderView();
  expect(tabByName(host, "Knowledgebase").getAttribute("aria-selected")).toBe("true");
  expect(host.querySelector('[data-testid="knowledgebase"]')).not.toBeNull();
});
