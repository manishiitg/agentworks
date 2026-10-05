// @vitest-environment happy-dom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { KnowledgebaseSources } from "./KnowledgebaseSources";
import KnowledgebaseView from "./KnowledgebaseView";
import WorkflowFolderAccessView from "./WorkflowFolderAccessView";
import { workflowManifestApi, agentApi } from "../../services/api";
import { useCanWriteWorkflow } from "../../hooks/useCanWriteWorkflow";
vi.mock("../../services/api", () => ({
  workflowManifestApi: {
    getKnowledgebaseSources: vi.fn(),
    getWorkflowManifest: vi.fn(),
    listWorkflowManifests: vi.fn(),
    updateWorkflowManifest: vi.fn(),
    readKnowledgebaseSource: vi.fn(),
  },
  agentApi: { getPlannerFileContent: vi.fn() },
}));
vi.mock("../../hooks/useCanWriteWorkflow", () => ({
  useCanWriteWorkflow: vi.fn(() => true),
}));
vi.mock("../ui/MarkdownRenderer", () => ({
  MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div>,
}));
// The folders view banners Ask AI; stub the button so these tests don't pull
// the chat/LLM store chain through the services/api mock.
vi.mock("./AskAIButton", () => ({
  AskAIButton: ({ label }: { label?: string }) => <button type="button">{label ?? "Ask AI"}</button>,
}));
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const cleanups: Array<() => void> = [];
afterEach(() => {
  cleanups.splice(0).forEach((f) => f());
  vi.clearAllMocks();
  vi.mocked(useCanWriteWorkflow).mockReturnValue(true);
});
async function mount(node: React.ReactNode) {
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  await act(async () => root.render(node));
  cleanups.push(() => {
    act(() => root.unmount());
    host.remove();
  });
  return host;
}
const source = {
  workflow_id: "source",
  alias: "rts",
  access: "read" as const,
  label: "RTS",
  available: true,
};
it("shows several sources and detaches only the selected attachment", async () => {
  const other = {
    ...source,
    workflow_id: "security",
    alias: "security",
    label: "Security",
    available: false,
    reason: "Source unavailable",
  };
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockResolvedValue({
    success: true,
    sources: [source, other],
  });
  vi.mocked(workflowManifestApi.updateWorkflowManifest).mockResolvedValue({
    success: true,
  });
  const select = vi.fn();
  const host = await mount(
    <KnowledgebaseSources
      workspacePath="Workflow/consumer"
      selected="rts"
      onSelect={select}
      variant="folders"
    />,
  );
  expect(
    host.querySelectorAll('ul[aria-label="Legacy knowledge sources"] > li'),
  ).toHaveLength(2);
  expect(host.textContent).toContain("Source unavailable");
  await act(async () => {
    Array.from(host.querySelectorAll("button"))
      .find((b) => b.textContent === "Detach")!
      .click();
  });
  expect(workflowManifestApi.updateWorkflowManifest).toHaveBeenCalledWith({
    workspace_path: "Workflow/consumer",
    knowledgebase_sources: [
      { workflow_id: "security", alias: "security", access: "read" },
    ],
  });
  expect(select).toHaveBeenCalledWith("");
});
it("keeps management controls unavailable to readers", async () => {
  vi.mocked(useCanWriteWorkflow).mockReturnValue(false);
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockResolvedValue({
    success: true,
    sources: [source],
  });
  const host = await mount(
    <KnowledgebaseSources
      workspacePath="Workflow/consumer"
      selected="rts"
      onSelect={() => {}}
    />,
  );
  expect(host.textContent).not.toContain("Detach");
  expect(host.textContent).not.toContain("Attach knowledge");
  const options = host.querySelectorAll('[role="group"] button');
  expect(options).toHaveLength(2);
  expect(options[0].textContent).toContain("Local knowledge");
  expect(options[0].getAttribute("aria-pressed")).toBe("false");
  expect(options[1].textContent).toContain("RTS");
  expect(options[1].getAttribute("aria-pressed")).toBe("true");
});
it("reads shared notes through the consumer-scoped API and surfaces failed collection", async () => {
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockResolvedValue({
    success: true,
    sources: [source],
  });
  vi.mocked(agentApi.getPlannerFileContent).mockResolvedValue({
    success: true,
    data: { content: '{"topics":[]}' },
  } as never);
  vi.mocked(workflowManifestApi.readKnowledgebaseSource).mockImplementation(
    async (_ws, _alias, path) => ({
      success: true,
      content:
        path === "notes/_index.json"
          ? '{"topics":[{"id":"architecture","file":"architecture.md"}]}'
          : path === "notes/architecture.md"
            ? "Verified shared architecture"
            : "{}",
    }),
  );
  const host = await mount(
    <KnowledgebaseView workspacePath="Workflow/consumer" />,
  );
  const pickSource = (label: string) =>
    Array.from(host.querySelectorAll('[role="group"] button')).find((b) =>
      b.textContent?.includes(label),
    )! as HTMLElement;
  await act(async () => {
    pickSource("RTS").click();
  });
  expect(workflowManifestApi.readKnowledgebaseSource).toHaveBeenCalledWith(
    "Workflow/consumer",
    "rts",
    "notes/_index.json",
  );
  await act(async () => {
    Array.from(host.querySelectorAll("button"))
      .find((b) => b.textContent?.includes("architecture"))!
      .click();
  });
  expect(host.textContent).toContain("Verified shared architecture");
  vi.mocked(workflowManifestApi.readKnowledgebaseSource).mockRejectedValue(
    new Error("Source permission revoked"),
  );
  await act(async () => {
    pickSource("Local knowledge").click();
  });
  // Re-selecting always remounts the source, discarding cached notes.
  await act(async () => {
    pickSource("RTS").click();
  });
  expect(host.textContent).toContain("Source permission revoked");
});

it("offers no manual attach in the folders variant; adding goes through Ask AI", async () => {
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockResolvedValue({
    success: true,
    sources: [source],
  });
  const host = await mount(
    <KnowledgebaseSources
      workspacePath="Workflow/consumer"
      variant="folders"
    />,
  );
  expect(host.textContent).not.toContain("Attach knowledge");
  expect(host.querySelector("form")).toBeNull();
  expect(host.textContent).toContain("RTS");
  expect(
    host.querySelector('button[aria-label="Detach RTS knowledge base"]'),
  ).not.toBeNull();
});

it("shows shared KB access in Attached folders and refreshes the KB view after detach", async () => {
  let sources = [{ ...source, workspace_path: "Workflow/rts" }];
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockImplementation(
    async () => ({ success: true, sources }),
  );
  vi.mocked(workflowManifestApi.getWorkflowManifest).mockResolvedValue({
    success: true,
    manifest: { folder_access: [] },
  } as never);
  vi.mocked(workflowManifestApi.updateWorkflowManifest).mockImplementation(
    async () => {
      sources = [];
      return { success: true };
    },
  );
  const select = vi.fn();
  const host = await mount(
    <>
      <WorkflowFolderAccessView workspacePath="Workflow/consumer" />
      <KnowledgebaseSources
        workspacePath="Workflow/consumer"
        selected="rts"
        onSelect={select}
      />
    </>,
  );
  const panels = host.querySelectorAll(
    'section[aria-label="Knowledge sources"]',
  );
  expect(panels).toHaveLength(2);
  expect(panels[0].textContent).toContain("1 attached");
  expect(panels[0].textContent).toContain("Workflow/rts/knowledgebase/");
  expect(panels[0].textContent).toContain("$WORKFLOW_KB_RTS");
  expect(panels[0].textContent).toContain("Read only");
  expect(panels[1].querySelector("ul")).toBeNull();
  expect(panels[1].textContent).not.toContain("Attach knowledge");
  expect(panels[1].textContent).not.toContain("Detach");
  expect(panels[1].textContent).toContain("Integrations → Tools & secrets → Brain");
  expect(panels[1].querySelectorAll('[role="group"] button')).toHaveLength(2);
  expect(panels[0].querySelector("select")).toBeNull();
  await act(async () => {
    (
      panels[0].querySelector(
        'button[aria-label="Detach RTS knowledge base"]',
      ) as HTMLButtonElement
    ).click();
  });
  expect(workflowManifestApi.updateWorkflowManifest).toHaveBeenCalledWith({
    workspace_path: "Workflow/consumer",
    knowledgebase_sources: [],
  });
  expect(panels[0].textContent).toContain(
    "No legacy knowledge sources attached.",
  );
  expect(panels[1].querySelectorAll('[role="group"] button')).toHaveLength(1);
  expect(select).toHaveBeenCalledWith("");
});

it("shows unavailable sources to readers without offering write or detach controls", async () => {
  vi.mocked(useCanWriteWorkflow).mockReturnValue(false);
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockResolvedValue({
    success: true,
    sources: [
      { ...source, available: false, reason: "Source permission revoked" },
    ],
  });
  const host = await mount(
    <KnowledgebaseSources
      workspacePath="Workflow/consumer"
      variant="folders"
    />,
  );
  expect(host.textContent).toContain("Unavailable · Source permission revoked");
  expect(host.textContent).toContain("Read only");
  expect(host.textContent).toContain("Shell when available:");
  expect(host.querySelectorAll("button, select")).toHaveLength(0);
});

it("keeps the connected work view free of manual attach forms", async () => {
  vi.mocked(workflowManifestApi.getWorkflowManifest).mockResolvedValue({
    success: true,
    manifest: {},
  } as never);
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockResolvedValue({
    success: true,
    sources: [],
  });
  const host = await mount(
    <WorkflowFolderAccessView workspacePath="Workflow/consumer" />,
  );
  expect(host.textContent).not.toContain("Attach knowledge");
  expect(host.textContent).toContain("Nothing attached.");
  expect(host.querySelector("form")).toBeNull();
  expect(host.textContent).not.toContain("External folders");
});

it("keeps the header above the source picker with Ask AI left of refresh", async () => {
  vi.mocked(workflowManifestApi.getKnowledgebaseSources).mockResolvedValue({
    success: true,
    sources: [source],
  });
  vi.mocked(agentApi.getPlannerFileContent).mockResolvedValue({
    success: true,
    data: { content: '{"topics":[]}' },
  } as never);
  const host = await mount(
    <KnowledgebaseView
      workspacePath="Workflow/consumer"
      headerAction={<button type="button" data-testid="ask-ai">Ask AI</button>}
    />,
  );
  const html = host.innerHTML;
  const titleIndex = html.indexOf(">Knowledgebase<");
  const pickerIndex = html.indexOf('aria-label="Knowledge sources"');
  const askIndex = html.indexOf('data-testid="ask-ai"');
  const refreshIndex = html.indexOf('aria-label="Refresh knowledgebase"');
  expect(titleIndex).toBeGreaterThanOrEqual(0);
  expect(pickerIndex).toBeGreaterThan(titleIndex);
  expect(askIndex).toBeGreaterThanOrEqual(0);
  expect(refreshIndex).toBeGreaterThan(askIndex);
});
