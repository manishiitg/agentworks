// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { TooltipProvider } from "../../ui/tooltip";
import type { SlackConnection, SlackUsableBot } from "../../../services/api-types";
import type { WorkflowRoute } from "./types";

vi.mock("../../admin/SlackAdminPanel", () => ({
  SharedSlackBotSettings: () => <div data-testid="shared-bot-settings" />,
}));

const api = vi.hoisted(() => ({
  getSlackTargetSettings: vi.fn(),
  updateSlackTargetSettings: vi.fn(),
  listSlackBotChannels: vi.fn(),
  placeSlackBotChannel: vi.fn(),
  removeSlackBotChannelRoute: vi.fn(),
  attachSlackBotTarget: vi.fn(),
  detachSlackBotTarget: vi.fn(),
  addSlackTargetChannel: vi.fn(),
  removeSlackTargetChannel: vi.fn(),
  dryRunSlackTarget: vi.fn(),
}));
vi.mock("../../../services/api", () => ({ agentApi: api }));

vi.mock("../../../hooks/useCanWriteWorkflow", () => ({
  READ_ONLY_TITLE: "read-only",
  useCanWriteWorkflow: () => true,
}));

import { SlackSetup } from "./SlackSetup";

const sharedBot: SlackConnection = { id: "slack_001", display_name: "AgentWorks", enabled: true, configured: true, is_default: true };
const ownBot: SlackConnection = { id: "slack_own", display_name: "Support bot", enabled: true, configured: true, is_default: false, workspace_path: "Workflow/support" };

const alphaBot: SlackUsableBot = { id: "slack_alpha", display_name: "Alpha bot", enabled: true, configured: true, workspace_path: "Workflow/alpha", owner_label: "Alpha", channel_routes: [] };

function makeBots(overrides: {
  own?: SlackConnection | null; routes?: WorkflowRoute[]; canManageSlackDefault?: boolean; shared?: SlackConnection | null;
  myBots?: SlackUsableBot[]; newMyBotChannel?: string; addMyBotChannel?: (id: string) => Promise<void>;
} = {}) {
  const noop = () => {};
  const asyncNoop = async () => null;
  return {
    readOnly: false,
    workflowId: "wf-support",
    slackOriginal: { enabled: true, bot_mode: true, connections: [...(overrides.shared === null ? [] : [overrides.shared ?? sharedBot]), ...(overrides.own ? [overrides.own] : [])] },
    loadSlack: asyncNoop,
    canManageSlackDefault: overrides.canManageSlackDefault ?? false,
    slackLoading: false,
    slackError: null,
    slackSuccess: null,
    canManageWorkflowSlack: true,
    hasProfileTarget: false,
    slackSelection: { own: overrides.own ?? null, effective: overrides.own ?? sharedBot, selectionId: overrides.own?.id ?? "" },
    slackConnName: "", setSlackConnName: noop, slackConnBot: "", setSlackConnBot: noop, slackConnApp: "", setSlackConnApp: noop,
    slackConnEnabled: true, setSlackConnEnabled: noop, slackConnSaving: false, slackConnTesting: false, slackConnTestResult: null,
    slackConnConfirmDelete: false, slackConnHasChanges: false,
    saveWorkflowSlackConnection: asyncNoop, testWorkflowSlackConnection: async () => {}, removeWorkflowSlackConnection: async () => false,
    workflowRoutes: overrides.routes ?? [], routeError: null,
    newSlackChannel: "", setNewSlackChannel: noop, addSlackRoute: noop,
    routeSaving: null, myRoutes: overrides.routes ?? [], addError: {}, setAddError: noop,
    expandedChip: null, setExpandedChip: noop, removeRoute: async () => {}, updateRoute: async () => {},
    myOtherBots: overrides.myBots ?? [],
    myBotRoutesHere: (bot: SlackUsableBot) => bot.channel_routes.filter(route => route.workspace_path === "Workflow/support"),
    newMyBotChannel: overrides.newMyBotChannel ?? "", setNewMyBotChannel: noop,
    myBotSaving: null, myBotError: null, setMyBotError: noop,
    addMyBotChannel: overrides.addMyBotChannel ?? (async () => {}), removeMyBotChannel: async () => {},
    slackTarget: { workspace_path: "Workflow/support" }, reloadMyBots: async () => {},
  } as unknown as React.ComponentProps<typeof SlackSetup>["bots"];
}

const noPlatformBot = { slug: "support", label: "Support", platform_bot: false, platform_available: false, product_allowed: true, can_manage: true, dm_only: false, channels: [] };

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  for (const fn of Object.values(api)) fn.mockReset();
  api.getSlackTargetSettings.mockResolvedValue(noPlatformBot);
  api.listSlackBotChannels.mockResolvedValue({ bot_name: "", slug: "support", channels: [
    { id: "C0QA0000001", name: "qa-team", answers: false, is_default: false, removable: false, targets: [] },
  ] });
  api.placeSlackBotChannel.mockResolvedValue({ channel_name: "qa-team" });
});

afterEach(() => {
  document.body.innerHTML = "";
});

async function render(bots: React.ComponentProps<typeof SlackSetup>["bots"]) {
  const container = document.createElement("div");
  document.body.append(container);
  await act(async () => createRoot(container).render(<TooltipProvider><SlackSetup bots={bots} /></TooltipProvider>));
  await act(async () => {});
  return container;
}

function radio(host: HTMLElement, label: RegExp) {
  const option = Array.from(host.querySelectorAll("label")).find(el => label.test(el.textContent || ""));
  return option?.querySelector('input[type="radio"]') as HTMLInputElement;
}

it("asks which bot answers and defaults a fresh workflow to its own bot", async () => {
  const host = await render(makeBots());
  expect(host.textContent).toContain("Which bot answers");
  expect(radio(host, /Its own bot/).checked).toBe(true);
  expect(host.textContent).toContain("Save bot");
  expect(host.textContent).not.toContain("Add channel");
});

it("shows where to get each Slack token next to its field", async () => {
  const host = await render(makeBots());
  const botField = host.querySelector<HTMLInputElement>('input[placeholder="xoxb-..."]')?.parentElement?.parentElement;
  const appField = host.querySelector<HTMLInputElement>('input[placeholder="xapp-..."]')?.parentElement?.parentElement;
  expect(botField?.textContent).toContain("OAuth & Permissions → OAuth Tokens");
  expect(botField?.textContent).toContain("Bot User OAuth Token");
  expect(appField?.textContent).toContain("Basic Information → App-Level Tokens");
  expect(appField?.textContent).toContain("connections:write");
  expect(botField?.querySelector('a')?.getAttribute('href')).toBe('https://api.slack.com/apps');
  expect(appField?.querySelector('a')?.getAttribute('href')).toBe('https://api.slack.com/apps');
  expect(host.querySelector('summary')?.textContent).toContain('Where to get Slack tokens');
});

it("shows required scopes, optional scopes, and bot events without opening setup details", async () => {
  const host = await render(makeBots());
  const checklist = host.querySelector('[aria-label="Slack permissions and events"]')!;
  const required = Array.from(checklist.querySelectorAll('ul code')).map(item => item.textContent);
  expect(required).toEqual([
    'app_mentions:read', 'channels:history', 'groups:history', 'channels:read', 'groups:read',
    'chat:write', 'reactions:write', 'users:read', 'users:read.email',
  ]);
  expect(checklist.textContent).toContain('Optional bot scopes: files:read');
  expect(checklist.textContent).toContain('chat:write.public');
  expect(checklist.textContent).toContain('connections:write');
  expect(checklist.textContent).toContain('app_mention message.channels message.groups');
  expect(checklist.textContent).toContain('Reinstall the Slack app after changing scopes');
});

it("shows the answering bot, how to reach this workflow by slug, and its channels by name", async () => {
  const host = await render(makeBots({ own: ownBot }));
  expect(radio(host, /Its own bot/)).toBeUndefined();
  expect(host.textContent).toContain("Support bot");
  expect(host.textContent).toContain("Ready");
  expect(host.textContent).toContain("Change");
  expect(host.textContent).toContain("@Support bot support your question");
  expect(host.textContent).toContain("support your question");
  expect(host.textContent).toContain("#qa-team");
  expect(host.textContent).toContain("/invite @Support bot");
  expect(host.textContent).not.toContain("Save bot");
  expect(host.textContent).not.toContain("@AgentWorks");
  expect(api.listSlackBotChannels).toHaveBeenCalledWith("slack_own", { workspace_path: "Workflow/support" });
});

it("opens on the shared bot when the workflow already has channels", async () => {
  const routes: WorkflowRoute[] = [{ kind: "slack", key: "C0123456789", current_target: true }];
  const host = await render(makeBots({ routes }));
  expect(radio(host, /Shared bot/).checked).toBe(true);
  expect(host.textContent).toContain("Add channel");
  expect(host.textContent).toContain("C0123456789");
});

it("warns that an own bot keeps answering when the shared bot is picked", async () => {
  const host = await render(makeBots({ own: ownBot }));
  const change = Array.from(host.querySelectorAll("button")).find(button => button.textContent === "Change")!;
  await act(async () => { change.click(); });
  await act(async () => {
    radio(host, /Shared bot/).click();
  });
  expect(host.textContent).toContain("still answers wherever it's invited");
  expect(host.textContent).toContain("Remove own bot");
});

it("keeps shared bot settings for admins only", async () => {
  const routes: WorkflowRoute[] = [{ kind: "slack", key: "C0123456789", current_target: true }];
  const member = await render(makeBots({ routes }));
  expect(member.querySelector('[data-testid="shared-bot-settings"]')).toBeNull();
  const admin = await render(makeBots({ routes, canManageSlackDefault: true }));
  expect(admin.querySelector('[data-testid="shared-bot-settings"]')).not.toBeNull();
});

it("offers a ready-made Slack app manifest named after the workflow", async () => {
  const bots = { ...makeBots(), slackAppDefaultName: "Support Desk" } as React.ComponentProps<typeof SlackSetup>["bots"];
  const host = await render(bots);
  const section = host.querySelector('[aria-label="Create from manifest"]')!;
  const manifest = JSON.parse(section.querySelector('pre')!.textContent || "");
  expect(manifest.display_information.name).toBe("Support Desk");
  expect(manifest.settings.socket_mode_enabled).toBe(true);
  expect(section.textContent).toContain("From a manifest");
  expect(section.textContent).toContain("Copy");
  const nameInput = section.querySelector<HTMLInputElement>("#slack-manifest-name")!;
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
    setter.call(nameInput, "Ops Bot");
    nameInput.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(JSON.parse(section.querySelector('pre')!.textContent || "").display_information.name).toBe("Ops Bot");
});

it("offers three answers: its own bot, one of my bots, and the shared bot", async () => {
  const host = await render(makeBots());
  const options = Array.from(host.querySelectorAll('input[name="slack-mode"]')).map(input => input.closest("label")?.textContent || "");
  expect(options).toHaveLength(3);
  expect(options[0]).toContain("Its own bot");
  expect(options[1]).toContain("One of my bots");
  expect(options[2]).toContain("Shared bot");
});

it("says ask an admin only for the platform shared bot", async () => {
  const host = await render(makeBots({ shared: null }));
  await act(async () => { radio(host, /One of my bots/).click(); });
  expect(host.textContent).not.toContain("ask an admin");
  expect(host.textContent).toContain("You have no other bots yet");
  expect(host.textContent).toContain("Give another workflow or crew its own bot first");
  // No shared bot is configured, so it is not offered at all.
  expect(radio(host, /Shared bot/)).toBeUndefined();
});

it("uses one of my bots and adds a channel picked by name", async () => {
  const host = await render(makeBots({ myBots: [alphaBot] }));
  await act(async () => { radio(host, /One of my bots/).click(); });
  expect(host.textContent).toContain("Alpha bot");
  expect(host.textContent).toContain("Set up for Alpha");
  expect(host.textContent).not.toContain("ask an admin");
  const use = Array.from(host.querySelectorAll("button")).find(button => button.textContent?.includes("Use this bot"))!;
  await act(async () => { use.click(); });
  await act(async () => {});
  expect(host.textContent).toContain("@Alpha bot support your question");
  const add = Array.from(host.querySelectorAll("button")).find(button => button.textContent?.includes("Add channel"))!;
  await act(async () => { add.click(); });
  expect(api.placeSlackBotChannel).toHaveBeenCalledWith("slack_alpha", "C0QA0000001", { workspace_path: "Workflow/support" }, true);
});

it("answers through one of my bots when it already answers here", async () => {
  const sharing = { ...alphaBot, channel_routes: [
    { channel_id: "C0111111111", workspace_path: "Workflow/support", label: "Support" },
  ] };
  api.listSlackBotChannels.mockResolvedValue({ bot_name: "Alpha bot", slug: "support", channels: [
    { id: "C0111111111", name: "support-desk", answers: true, is_default: false, removable: true, targets: [{ channel_id: "C0111111111", workspace_path: "Workflow/alpha", slug: "alpha", is_default: true }, { channel_id: "C0111111111", workspace_path: "Workflow/support", slug: "support", is_default: false }] },
  ] });
  const host = await render(makeBots({ myBots: [sharing] }));
  expect(host.textContent).toContain("Alpha bot");
  expect(host.textContent).toContain("support-desk");
  expect(host.textContent).toContain("also alpha");
  expect(host.textContent).toContain("Answer here without the slug");
});

it("never offers the platform bot on a server without one", async () => {
  const host = await render(makeBots({ shared: null }));
  expect(host.textContent).not.toContain("AgentWorks bot");
  expect(host.textContent).not.toContain("@AgentWorks");
  expect(host.textContent).not.toContain("Answer through @");
});

it("shows a save error once, beside Save bot, while the bot form is open", async () => {
  const message = "Slack token already in use: this Slack app is already connected as 'Bot-2'";
  const bots = { ...makeBots(), slackError: message } as React.ComponentProps<typeof SlackSetup>["bots"];
  const host = await render(bots);
  expect(host.textContent?.split(message).length).toBe(2);
  const saveButton = Array.from(host.querySelectorAll("button")).find(b => /Save bot/.test(b.textContent || ""));
  const formText = saveButton?.closest("section, div.space-y-4, form")?.textContent || "";
  expect(formText).toContain(message);
});

it("offers the shared bot only when an admin has configured one", async () => {
  const none = await render(makeBots({ shared: null }));
  expect(none.textContent).not.toContain("Shared bot");
  document.body.innerHTML = "";

  const unconfigured = await render(makeBots({ shared: { ...sharedBot, configured: false } }));
  expect(unconfigured.textContent).not.toContain("Shared bot");
  document.body.innerHTML = "";

  const configured = await render(makeBots());
  expect(configured.textContent).toContain("Shared bot");
});

it("keeps the shared bot visible while this target has shared-bot channels", async () => {
  const routes = [{ kind: "slack", channel_id: "C0123456789", workspace_path: "Workflow/support" }] as unknown as WorkflowRoute[];
  const host = await render(makeBots({ shared: null, routes }));
  expect(host.textContent).toContain("Shared bot");
});
