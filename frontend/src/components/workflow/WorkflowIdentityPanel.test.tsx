// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const { manifestState, presetState, workflowState } = vi.hoisted(() => ({
  manifestState: {
    workflows: [
      { workspace_path: "/workflows/demo", manifest: { label: "Demo", icon: "🤖" } },
    ],
    updateWorkflow: vi.fn(async () => {}),
    deleteWorkflow: vi.fn(async () => {}),
    refreshWorkflows: vi.fn(async () => {}),
  },
  presetState: {
    clearActivePreset: vi.fn(),
    deleteWorkflow: vi.fn(async () => {}),
    activePresetIds: { workflow: null as string | null },
    refreshPresets: vi.fn(async () => {}),
  },
  workflowState: {
    setShowWorkspacePane: vi.fn(),
  },
}));

vi.mock("../../stores/useWorkflowManifestStore", () => ({
  useWorkflowManifestStore: Object.assign(
    (selector: (state: never) => unknown) => selector(manifestState as never),
    { getState: () => manifestState },
  ),
}));

vi.mock("../../stores/useGlobalPresetStore", () => ({
  useGlobalPresetStore: { getState: () => presetState },
}));

vi.mock("../../stores/useWorkflowStore", () => ({
  useWorkflowStore: { getState: () => workflowState },
}));

vi.mock("../../hooks/useCanWriteWorkflow", () => ({
  READ_ONLY_TITLE: "read-only",
  useCanWriteWorkflow: () => true,
}));


vi.mock("./SoulViewer", () => ({
  SoulViewer: () => <div data-testid="soul">soul contents</div>,
}));

import WorkflowIdentityPanel from "./WorkflowIdentityPanel";

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.clearAllMocks();
});

afterEach(() => {
  document.body.innerHTML = "";
});

async function renderPanel() {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(<WorkflowIdentityPanel workspacePath="/workflows/demo" />));
  return container;
}

function saveButton(host: Element) {
  return Array.from(host.querySelectorAll("button")).find(button => button.textContent === "Save")!;
}

it("renders name, icon, purpose, and delete with save disabled when pristine", async () => {
  const host = await renderPanel();
  const name = host.querySelector('input[placeholder="e.g. Support helper"]') as HTMLInputElement;
  expect(name.value).toBe("Demo");
  expect(host.querySelector('[data-testid="soul"]')).not.toBeNull();
  expect(host.textContent).toContain("Delete workflow");
  expect(saveButton(host).disabled).toBe(true);
});

it("offers a custom icon upload next to the emoji field", async () => {
  const host = await renderPanel();
  expect(host.querySelector('input[aria-label="Workflow icon"]')).not.toBeNull();
  expect(host.querySelector('input[aria-label="Upload icon image"]')).not.toBeNull();
  const upload = Array.from(host.querySelectorAll("button")).find(button => button.textContent === "Upload");
  expect(upload).not.toBeUndefined();
});

it("saves the renamed workflow", async () => {
  const host = await renderPanel();
  const name = host.querySelector('input[placeholder="e.g. Support helper"]') as HTMLInputElement;
  await act(async () => {
    name.focus();
    // React tracks the value; set it the way user input does.
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!;
    setter.call(name, "Renamed");
    name.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(saveButton(host).disabled).toBe(false);
  await act(async () => {
    saveButton(host).dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await act(async () => {});
  expect(manifestState.updateWorkflow).toHaveBeenCalledWith("/workflows/demo", { label: "Renamed" });
});

it("deletes through the confirm dialog and closes the pane", async () => {
  const host = await renderPanel();
  const trigger = Array.from(host.querySelectorAll("button")).find(button =>
    button.textContent?.includes("Delete workflow"),
  )!;
  await act(async () => {
    trigger.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  const confirm = Array.from(document.body.querySelectorAll("button")).find(button =>
    button.textContent === "Delete workflow" && !host.contains(button),
  ) as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);
  const confirmation = document.body.querySelector('input[aria-label="Confirmation text"]') as HTMLInputElement;
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!;
    setter.call(confirmation, "Demo");
    confirmation.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(confirm.disabled).toBe(false);
  await act(async () => {
    confirm.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await act(async () => {});
  expect(presetState.deleteWorkflow).toHaveBeenCalledWith("/workflows/demo");
  expect(manifestState.refreshWorkflows).not.toHaveBeenCalled();
  expect(presetState.refreshPresets).not.toHaveBeenCalled();
  expect(workflowState.setShowWorkspacePane).toHaveBeenCalledWith(false);
});

it("dismisses confirmation before a slow delete and preserves a newer workspace selection", async () => {
  let finish!: () => void;
  presetState.deleteWorkflow.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; }));
  const host = await renderPanel();
  const trigger = Array.from(host.querySelectorAll("button")).find(button => button.textContent?.includes("Delete workflow"))!;
  await act(async () => trigger.click());
  const confirmation = document.body.querySelector('input[aria-label="Confirmation text"]') as HTMLInputElement;
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(confirmation, "Demo");
    confirmation.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const confirm = Array.from(document.body.querySelectorAll("button")).find(button => button.textContent === "Delete workflow" && !host.contains(button))!;
  await act(async () => confirm.click());
  expect(document.body.querySelector('input[aria-label="Confirmation text"]')).toBeNull();
  expect(host.textContent).toContain("Deleting…");
  expect(workflowState.setShowWorkspacePane).not.toHaveBeenCalled();
  presetState.activePresetIds.workflow = "another-workflow";
  await act(async () => finish());
  expect(workflowState.setShowWorkspacePane).not.toHaveBeenCalled();
  presetState.activePresetIds.workflow = null;
});
