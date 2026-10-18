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
    expect(getIntegrationTabAskAIMessage("slack")).toContain("Connections · Slack");
    expect(getIntegrationTabAskAIMessage("whatsapp")).toContain("Integrations · WhatsApp");
    expect(getIntegrationTabAskAIMessage("gmail")).toContain("Integrations · Gmail");
  });

  it("keeps sending accounts separate from incoming Slack app routing", () => {
    const message = getIntegrationTabAskAIMessage("slack");
    expect(message).toContain("Slack account");
    expect(message).toContain("Use & share");
    expect(message).toContain("account send/read rules");
    expect(message).not.toContain("slack-bot-routing.md");
  });
});

describe("identity tab Ask AI messages", () => {
  const tabs: IdentityTabId[] = ["general", "secrets", "llm", "upgrades"];

  it("marks every tab message with its tab label", () => {
    for (const tab of tabs) {
      expect(getIdentityTabAskAIMessage(tab)).toContain("[ASK-AI");
    }
    expect(getIdentityTabAskAIMessage("general")).toContain("Identity · General");
    expect(getIdentityTabAskAIMessage("secrets")).toContain("Identity · Secrets");
    expect(getIdentityTabAskAIMessage("llm")).toContain("Identity · Models");
    expect(getIdentityTabAskAIMessage("upgrades")).toContain("Identity · Upgrades");
  });

  it("keeps the soul file path in the hidden builder instructions", () => {
    const message = getIdentityTabAskAIMessage("general");
    expect(message).toContain("soul/soul.md");
  });
});
