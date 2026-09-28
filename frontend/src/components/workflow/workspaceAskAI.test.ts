import { describe, expect, it } from "vitest";
import {
  getIdentityTabAskAIMessage,
  getIntegrationTabAskAIMessage,
  type IdentityTabId,
  type IntegrationTabId,
} from "./workspaceAskAI";

describe("integration tab Ask AI messages", () => {
  const tabs: IntegrationTabId[] = ["apps", "skills", "slack", "whatsapp", "gmail"];

  it("marks every tab message with its tab label", () => {
    for (const tab of tabs) {
      expect(getIntegrationTabAskAIMessage(tab)).toContain("[ASK-AI");
    }
    expect(getIntegrationTabAskAIMessage("apps")).toContain("Integrations · MCPs");
    expect(getIntegrationTabAskAIMessage("skills")).toContain("Integrations · Skills");
    expect(getIntegrationTabAskAIMessage("slack")).toContain("Integrations · Slack");
    expect(getIntegrationTabAskAIMessage("whatsapp")).toContain("Integrations · WhatsApp");
    expect(getIntegrationTabAskAIMessage("gmail")).toContain("Integrations · Gmail");
  });

  it("keeps the slack routing guide in the hidden builder instructions", () => {
    const message = getIntegrationTabAskAIMessage("slack");
    expect(message).toContain("connect Slack");
    expect(message).toContain("slack-bot-routing.md");
  });
});

describe("identity tab Ask AI messages", () => {
  const tabs: IdentityTabId[] = ["general", "secrets", "folders", "llm", "upgrades"];

  it("marks every tab message with its tab label", () => {
    for (const tab of tabs) {
      expect(getIdentityTabAskAIMessage(tab)).toContain("[ASK-AI");
    }
    expect(getIdentityTabAskAIMessage("general")).toContain("Identity · General");
    expect(getIdentityTabAskAIMessage("secrets")).toContain("Identity · Secrets");
    expect(getIdentityTabAskAIMessage("folders")).toContain("Identity · Connected work");
    expect(getIdentityTabAskAIMessage("llm")).toContain("Identity · Models");
    expect(getIdentityTabAskAIMessage("upgrades")).toContain("Identity · Upgrades");
  });

  it("keeps the soul file path in the hidden builder instructions", () => {
    const message = getIdentityTabAskAIMessage("general");
    expect(message).toContain("soul/soul.md");
  });
});
