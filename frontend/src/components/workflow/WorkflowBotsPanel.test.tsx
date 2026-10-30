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

async function renderPanel(fixedChannel?: "slack" | "whatsapp") {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () =>
    root.render(
      <TooltipProvider>
        <WorkflowBotsPanel workspacePath="/workflows/demo" fixedChannel={fixedChannel} />
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

it("uses the same Slack page in all hosts without a credential form", async () => {
 const host = await renderPanel("slack")
 expect(host.querySelector('[data-testid="slack-settings"]')).not.toBeNull()
 expect(host.querySelector('[data-testid="sending-account"]')).toBeNull()
 expect(host.querySelector('input[type="password"]')).toBeNull()
});

it('guides paired self-chat without a manual link code or slug form', async () => {
  const { ChannelRow } = await vi.importActual<typeof import('./bots/AddChannel')>('./bots/AddChannel')
  const { WhatsAppSetup } = await import('./bots/WhatsAppSetup')
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const bots = { workflowId: 'example', waReady: true, waStatusLabel: 'Connected', waStatus: { enabled: true, paired: true, connected: true }, slackOriginal: {}, myRoutes: [], addError: {} } as never
  try {
    await act(async () => root.render(<><ChannelRow bots={bots} kind="whatsapp" manageRoutes /><WhatsAppSetup bots={bots} /></>))
    expect(host.querySelector('input')).toBeNull()
    expect([...host.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Add')).toBe(false)
    expect(host.querySelectorAll('ol li')).toHaveLength(3)
    expect(host.textContent).toContain('Message yourself')
    expect(host.textContent).toContain('@list')
    expect(host.textContent).not.toContain('Step 1: link a DM')
  } finally { await act(async () => root.unmount()); host.remove() }
})
