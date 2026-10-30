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

it.each(['knowledgebase', 'database'] as const)('opens the targeted %s page with no duplicate tabs', async tab => {
  storeState.workspaceViewTarget = { view: "knowledge", target: tab, token: 1 };
  const host = await renderView();
  expect(host.querySelector(`[data-testid="${tab}"]`)).not.toBeNull();
  expect(host.querySelector(`[data-testid="${tab === 'database' ? 'knowledgebase' : 'database'}"]`)).toBeNull();
  expect(host.querySelector('[role="tablist"]')).toBeNull();
  expect(host.querySelector('[aria-label^="Walkthrough:"]')).toBeNull();
  expect(host.textContent).not.toContain('Learnings');
});
