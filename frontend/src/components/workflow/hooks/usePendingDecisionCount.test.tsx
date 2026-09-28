// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type {
  ReportHumanInput,
  ReportHumanInputsResponse,
} from "../../../services/api-types";
vi.mock("../../../services/api", () => ({
  agentApi: { listReportHumanInputs: vi.fn() },
}));
import { agentApi } from "../../../services/api";
import { usePendingDecisionCount, usePendingDecisionState } from "./usePendingDecisionCount";
import {
  WORKFLOW_DECISIONS_REFRESH_EVENT,
  WORKFLOW_LOG_REFRESH_EVENT,
} from "../workflowEvents";
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
function Count({ workspace }: { workspace: string }) {
  return <span>{usePendingDecisionCount(workspace)}</span>;
}
function DecisionState({ workspace }: { workspace: string }) {
  const state = usePendingDecisionState(workspace);
  return <span>{`${state.loaded}:${state.count}`}</span>;
}
const input = (status: string, workspace = "Workflow/a") =>
  ({ id: status, status, workspace_path: workspace }) as ReportHumanInput;
it("counts only pending decisions and clears after an answer refresh", async () => {
  const container = document.createElement("div"),
    root = createRoot(container);
  const fetch = vi.mocked(agentApi.listReportHumanInputs);
  fetch.mockResolvedValue({
    success: true,
    inputs: [
      input("pending"),
      input("answered"),
      input("claimed"),
      input("pending", "Workflow/other"),
    ],
  });
  try {
    await act(async () => root.render(<Count workspace="Workflow/a" />));
    expect(container.textContent).toBe("1");
    expect(fetch).toHaveBeenLastCalledWith("Workflow/a", "pending");
    fetch.mockResolvedValue({ success: true, inputs: [input("answered")] });
    await act(async () => {
      window.dispatchEvent(
        new CustomEvent(WORKFLOW_DECISIONS_REFRESH_EVENT, {
          detail: { workspacePath: "Workflow/a" },
        }),
      );
    });
    expect(container.textContent).toBe("0");
  } finally {
    await act(async () => root.unmount());
  }
});
it("does not leak an old workflow count or apply an out-of-order response", async () => {
  const container = document.createElement("div"),
    root = createRoot(container),
    fetch = vi.mocked(agentApi.listReportHumanInputs);
  let resolveOld!: (r: ReportHumanInputsResponse) => void;
  fetch.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveOld = resolve;
      }),
  );
  try {
    await act(async () => root.render(<Count workspace="Workflow/a" />));
    fetch.mockResolvedValue({ success: true, inputs: [] });
    await act(async () => root.render(<Count workspace="Workflow/b" />));
    await act(async () =>
      resolveOld({ success: true, inputs: [input("pending")] }),
    );
    expect(container.textContent).toBe("0");
    let resolveSlow!: (r: ReportHumanInputsResponse) => void;
    fetch.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveSlow = resolve;
        }),
    );
    await act(async () => {
      window.dispatchEvent(new Event(WORKFLOW_LOG_REFRESH_EVENT));
    });
    fetch.mockResolvedValue({
      success: true,
      inputs: [input("pending", "Workflow/b")],
    });
    await act(async () => {
      window.dispatchEvent(new Event(WORKFLOW_LOG_REFRESH_EVENT));
    });
    expect(container.textContent).toBe("1");
    await act(async () => resolveSlow({ success: true, inputs: [] }));
    expect(container.textContent).toBe("1");
  } finally {
    await act(async () => root.unmount());
  }
});
it("reports the first workflow decision load before choosing a default view", async () => {
  const container = document.createElement("div"), root = createRoot(container);
  const fetch = vi.mocked(agentApi.listReportHumanInputs);
  let resolve!: (response: ReportHumanInputsResponse) => void;
  fetch.mockImplementationOnce(() => new Promise(response => { resolve = response; }));
  try {
    await act(async () => root.render(<DecisionState workspace="Workflow/entry" />));
    expect(container.textContent).toBe("false:0");
    await act(async () => resolve({ success: true, inputs: [input("pending", "Workflow/entry")] }));
    expect(container.textContent).toBe("true:1");
  } finally {
    await act(async () => root.unmount());
  }
});
