// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";

vi.mock("./bots/useWorkflowBots", () => ({
  useWorkflowBots: () => ({
    setup: null,
    setSetup: () => {},
    workflowId: "workflow-1",
    workflowRoutes: [],
    routeError: null,
    waRoutingError: null,
  }),
}));

vi.mock("./bots/AddChannel", () => ({
  ChannelRow: ({ kind }: { kind: string }) => <div data-testid={`channel-${kind}`}>{kind}</div>,
}));

vi.mock("./bots/SlackSetup", () => ({
  SlackSetup: () => <div data-testid="slack-settings">settings</div>,
}));
vi.mock("../integrations/SlackAccountsPanel", () => ({ default: () => <div data-testid="sending-account">Sending account permissions</div> }));

// The real button pulls the chat stores into the module graph, which trips a
// circular-import TDZ under SSR transform; the tabs don't depend on it.
vi.mock("./AskAIButton", () => ({
  AskAIButton: ({ label }: { label?: string }) => <button type="button">{label ?? "Ask AI"}</button>,
}));

vi.mock("../../hooks/useCanWriteWorkflow", () => ({
  READ_ONLY_TITLE: "read-only",
  useCanWriteWorkflow: () => true,
}));

import WorkflowBotsPanel from "./WorkflowBotsPanel";

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
});

afterEach(() => {
  document.body.innerHTML = "";
});

async function renderPanel(fixedChannel?: "slack" | "whatsapp", channelOnly = false) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () =>
    root.render(
      <TooltipProvider>
        <WorkflowBotsPanel workspacePath="/workflows/demo" fixedChannel={fixedChannel} channelOnly={channelOnly} />
      </TooltipProvider>,
    ),
  );
  return container;
}

it("shows Slack and WhatsApp as separate tabs with Slack first", async () => {
  const host = await renderPanel();
  const tabs = Array.from(host.querySelectorAll('[role="tab"]'));
  expect(tabs.map(tab => tab.textContent)).toEqual(["Slack", "WhatsApp"]);
  expect(tabs[0].getAttribute("aria-selected")).toBe("true");
  expect(host.querySelector('[data-testid="slack-settings"]')).not.toBeNull();
  expect(host.querySelector('[data-testid="channel-whatsapp"]')).toBeNull();
});

it("switches the channel card when the WhatsApp tab is picked", async () => {
  const host = await renderPanel();
  const tabs = Array.from(host.querySelectorAll('[role="tab"]'));
  await act(async () => {
    tabs[1].dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  expect(tabs[1].getAttribute("aria-selected")).toBe("true");
  expect(host.querySelector('[data-testid="channel-whatsapp"]')).not.toBeNull();
  expect(host.querySelector('[data-testid="slack-settings"]')).toBeNull();
});

it("pins to one channel with no tab row when fixedChannel is set", async () => {
  const host = await renderPanel("whatsapp");
  expect(host.querySelector('[role="tablist"]')).toBeNull();
  expect(host.querySelector('[data-testid="channel-whatsapp"]')).not.toBeNull();
  expect(host.querySelector('[data-testid="channel-slack"]')).toBeNull();
});

it("gives Slack its own one-question tab with no channel row", async () => {
  const slack = await renderPanel("slack");
  expect(slack.querySelector('[data-testid="slack-settings"]')).not.toBeNull();
  expect(slack.querySelector('[data-testid="channel-slack"]')).toBeNull();
  const whatsapp = await renderPanel("whatsapp");
  expect(whatsapp.querySelector('[data-testid="slack-settings"]')).toBeNull();
});

it("shows only incoming bot setup in Use & share while preserving the legacy mixed host", async () => {
  const legacy = await renderPanel("slack");
  expect(legacy.querySelector('[data-testid="sending-account"]')).not.toBeNull();
  const incoming = await renderPanel("slack", true);
  expect(incoming.querySelector('[data-testid="sending-account"]')).toBeNull();
  expect(incoming.querySelector('[data-testid="slack-settings"]')).not.toBeNull();
  expect(incoming.querySelector('details')).toBeNull();
});
