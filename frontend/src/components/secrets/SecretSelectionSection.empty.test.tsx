// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { SecretSelectionSection } from "./SecretSelectionSection";
vi.mock("../integrations/OpenVaultButton", () => ({
  OpenVaultButton: () => null,
}));
vi.mock("../../hooks/useCanWriteWorkflow", () => ({
  useCanWriteWorkflow: () => true,
}));
vi.mock("../../api/secrets", () => ({
  secretsApi: {
    listWorkflowSecrets: vi
      .fn()
      .mockResolvedValue([{ name: "LOCAL_KEY", encrypted_value: "sealed" }]),
    getGlobalSecrets: vi.fn().mockRejectedValue(new Error("Vault unavailable")),
  },
}));
it("keeps project secrets available during a Vault outage", async () => {
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  await act(async () =>
    root.render(
      <SecretSelectionSection
        workflowPath="Workflow/test"
        selectedSecrets={[]}
        onSecretChange={() => {}}
      />,
    ),
  );
  expect(host.textContent).toContain("LOCAL_KEY");
  expect(host.querySelector('[role="alert"]')?.textContent).toContain(
    "Vault unavailable",
  );
  await act(async () => root.unmount());
  host.remove();
});
