// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

vi.mock("./PulseWorkspace", () => ({
  PulseWorkspace: ({ onTabChange }: { onTabChange: (tab: 'for_you' | 'platform') => void }) => <div data-testid="pulse-workspace">
    <button type="button" onClick={() => onTabChange('for_you')}>For you</button>
    <button type="button" onClick={() => onTabChange('platform')}>Platform health</button>
  </div>,
}));
vi.mock("./GoalLeadPanel", () => ({ GoalLeadPanel: () => null }));
vi.mock("./SoulViewer", () => ({
  WORKFLOW_SOUL_REFRESH_EVENT: "test-soul-refresh",
}));

import PulseView from "./PulseView";

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
});

afterEach(() => {
  document.body.innerHTML = "";
});

async function renderView(monitorOn: boolean, hasSoul = true) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () =>
    root.render(
      <PulseView
        workspacePath="/tmp/workflow-test"
        monitorOn={monitorOn}
        hasSoul={hasSoul}
        runSetup={{ schedules: [{ name: 'Daily', type: 'cron', enabled: true, cron_expression: '0 9 * * *', after_run: { backup: true, publish: false, notify: true } }], manual_after_run: { backup: false, publish: false, notify: false } }}
        monitorSaving={false}
        onToggleMonitor={() => {}}
        disabledReviewModules={[]}
        reviewModuleSaving={null}
        onToggleReviewModule={() => {}}
        moduleStates={[]}
        planDriftDue={false}
        planDriftDueItems={[]}
        planDriftDueError={null}
        finalCommandStates={[]}
        reviewFocuses={[]}
        reviewFocusSelections={[]}
        statusError={null}
        statusLoading={false}
        overview={{ recorded: 0, total: 8, latest: "" }}
        onRefresh={() => {}}
        headerAction={<button type="button" data-testid="ask-ai">Ask AI</button>}
      />,
    ),
  );
  return container;
}

it.each([true, false])("shows the standard refresh with monitor %s", async (monitorOn) => {
  const host = await renderView(monitorOn);
  const refresh = host.querySelector('[aria-label="Refresh Pulse status"]');
  expect(refresh).not.toBeNull();
  expect(refresh?.className).toContain("h-8 w-8");
  // Ask AI left, refresh right.
  const askIndex = host.innerHTML.indexOf('data-testid="ask-ai"');
  const refreshIndex = host.innerHTML.indexOf('aria-label="Refresh Pulse status"');
  expect(askIndex).toBeGreaterThanOrEqual(0);
  expect(askIndex).toBeLessThan(refreshIndex);
});

it('updates the header walkthrough when the Pulse tab changes', async () => {
  const host = await renderView(true);
  await act(async () => (host.querySelector('[aria-label="Walkthrough: Pulse · For you"]') as HTMLButtonElement).click());
  expect(host.querySelector('[role="dialog"]')?.textContent).toContain('progress toward this automation’s goal');

  await act(async () => (host.querySelector('[data-testid="pulse-workspace"] button:last-child') as HTMLButtonElement).click());
  expect(host.querySelector('[aria-label="Walkthrough: Pulse · Platform health"]')).not.toBeNull();
  expect(host.querySelector('[role="dialog"]')?.textContent).toContain('Workflow Review, Technical, and Architecture');
});

// Owner 2026-10-08: Pulse off (or on without a goal in soul.md) means the owner
// manages the workflow, so the tab shows what still runs, not Pulse's panels.
it.each([[false, true], [true, false]])('shows the owner-managed card when Pulse on=%s and soul.md=%s', async (monitorOn, hasSoul) => {
  const host = await renderView(monitorOn, hasSoul);
  const card = host.querySelector('[data-testid="pulse-off-card"]');
  expect(card?.textContent).toContain('Today you manage this workflow');
  expect(card?.textContent).toContain('after: backup, notify');
  expect(host.querySelector('[data-testid="pulse-workspace"]')).toBeNull();
});
