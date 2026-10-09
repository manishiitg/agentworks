// @vitest-environment happy-dom
import { expect, it, vi } from "vitest";
import { renderReportActivity, renderReportTable } from "./reportComposition";
import { installReportHost, withReportBootstrap } from "./reportHostRuntime";
import type { ReportDataApi } from "./reportEmbedContext";

function api(
  rows: Record<string, unknown>[] = [],
  tables: string[] = [],
): ReportDataApi {
  return {
    workspacePath: "Workflow/test",
    query: vi.fn(async (sql: string) => {
      if (sql.includes("sqlite_master"))
        return tables.map((name) => ({ name }));
      return rows;
    }),
    renderMarkdown: (md: string) => `<p>${md}</p>`,
  } as unknown as ReportDataApi;
}
function host() {
  const node = document.createElement("section");
  document.body.replaceChildren(node);
  return node;
}

it("renders query rows as a themed table with an empty state", async () => {
  const data = api([
    { name: "Acme", leads: 12 },
    { name: "Beta", leads: 3 },
  ]);
  const container = host();
  const rows = await renderReportTable(document, data.query, container, {
    query: "SELECT name, leads FROM leads WHERE status = ?",
    params: ["open"],
  });
  expect(rows).toHaveLength(2);
  expect(data.query).toHaveBeenCalledWith("SELECT name, leads FROM leads WHERE status = ?", ["open"]);
  const root = container.shadowRoot!;
  expect(root.querySelectorAll("tbody tr")).toHaveLength(2);
  expect(root.textContent).toContain("Acme");
  expect(root.textContent).toContain("2 rows");
  expect(
    root.querySelector("th.num button, th.num"),
  ).not.toBeNull();

  const empty = host();
  await renderReportTable(document, api([]).query, empty, {
    query: "SELECT 1 WHERE 0",
  });
  expect(empty.shadowRoot!.textContent).toContain("No rows returned");
});

it("rejects a missing query and surfaces failures without clobbering refreshes", async () => {
  const container = host();
  await expect(
    renderReportTable(document, api().query, container, {
      query: "",
    }),
  ).rejects.toThrow("options.query");
  await expect(
    renderReportTable(document, api().query, container, undefined as never),
  ).rejects.toThrow("options.query");

  const data = api();
  let release!: (v: Record<string, unknown>[]) => void;
  data.query = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  const pending = renderReportTable(document, data.query, container, {
    query: "SELECT 1",
  });
  await renderReportTable(
    document,
    api([{ name: "fresh" }]).query,
    container,
    { query: "SELECT 2" },
  );
  release([{ name: "stale" }]);
  await pending;
  expect(container.shadowRoot!.textContent).toContain("fresh");
  expect(container.shadowRoot!.textContent).not.toContain("stale");

  data.query = async () => {
    throw new Error("Offline");
  };
  await expect(
    renderReportTable(document, data.query, container, { query: "SELECT 1" }),
  ).rejects.toThrow("Offline");
  expect(container.shadowRoot!.textContent).toContain("Table unavailable");
  expect(container.getAttribute("aria-busy")).toBe("false");
});

it("filters and sorts client-side when asked", async () => {
  const data = api([
    { name: "Acme", leads: 12 },
    { name: "Beta", leads: 3 },
    { name: "Gamma", leads: 30 },
  ]);
  const container = host();
  await renderReportTable(document, data.query, container, {
    query: "SELECT name, leads FROM leads",
    searchable: true,
    sortable: true,
  });
  const root = container.shadowRoot!;
  const box = root.querySelector("input[type=search]") as HTMLInputElement;
  expect(box).not.toBeNull();
  box.value = "acm";
  box.dispatchEvent(new Event("input", { bubbles: true }));
  expect(root.querySelectorAll("tbody tr")).toHaveLength(1);
  expect(root.textContent).toContain("1 of 3 rows");
  box.value = "";
  box.dispatchEvent(new Event("input", { bubbles: true }));
  expect(root.querySelectorAll("tbody tr")).toHaveLength(3);

  const headers = root.querySelectorAll("thead th");
  const leadsBtn = headers[1].querySelector("button") as HTMLButtonElement;
  leadsBtn.click();
  let first = root.querySelector("tbody tr td:last-child")!.textContent;
  expect(first).toBe("3");
  expect(headers[1].getAttribute("aria-sort")).toBe("ascending");
  leadsBtn.click();
  first = root.querySelector("tbody tr td:last-child")!.textContent;
  expect(first).toBe("30");
  expect(headers[1].getAttribute("aria-sort")).toBe("descending");
});

it("renders activity grouped by route with markdown summaries", async () => {
  const data = api(
    [
      {
        id: 1,
        notification_kind: "run_summary",
        title: "Morning run",
        status: "success",
        summary_text: "All routes **green**",
        message: "",
        fields_json: "[]",
        sections_json: "[]",
        route_summaries_json: JSON.stringify([
          {
            routing_step_id: "s1",
            route_id: "r1",
            label: "Outreach",
            title: "Sent 5 bids",
            status: "success",
            message: "Placed *5* bids",
            fields: [],
            sections: [],
          },
        ]),
        created_at: "2026-09-20T08:00:00Z",
      },
    ],
    ["org_dashboard_notifications"],
  );
  const container = host();
  const rows = await renderReportActivity(document, data, container);
  expect(rows).toHaveLength(1);
  const root = container.shadowRoot!;
  expect(root.textContent).toContain("Morning run");
  expect(root.textContent).toContain("Run summary");
  expect(root.textContent).toContain("Shared workflow update");
  expect(root.textContent).toContain("Outreach");
  expect(root.textContent).toContain("Sent 5 bids");
  expect(root.querySelector(".md")).not.toBeNull();
});

it("falls back to the execution log and then to a setup message", async () => {
  const logs = api(
    [
      {
        name: "Daily outreach",
        status: "complete",
        result: "Sent 4 bids. See db/notes.md for detail.",
        completed_at: "2026-09-19T08:00:00Z",
        updated_at: "2026-09-19T08:00:00Z",
      },
    ],
    ["background_agent_log"],
  );
  const container = host();
  await renderReportActivity(document, logs, container);
  expect(container.shadowRoot!.textContent).toContain("Daily outreach");
  expect(container.shadowRoot!.textContent).toContain("Sent 4 bids.");

  const fresh = host();
  await renderReportActivity(document, api([], []), fresh);
  expect(fresh.shadowRoot!.textContent).toContain("No activity yet");
});

it("validates the activity limit and surfaces failures", async () => {
  const container = host();
  for (const limit of [0, 101, 1.5]) {
    await expect(
      renderReportActivity(document, api([], []), container, { limit }),
    ).rejects.toThrow("options.limit");
  }
  const data = api([], ["org_dashboard_notifications"]);
  // sqlite_master says the notifications table exists, the detail read fails.
  data.query = async (sql: string) => {
    if (sql.includes("sqlite_master"))
      return [{ name: "org_dashboard_notifications" }];
    throw new Error("Offline");
  };
  await expect(
    renderReportActivity(document, data, container),
  ).rejects.toThrow("Offline");
  expect(container.shadowRoot!.textContent).toContain("Activity unavailable");
  expect(container.getAttribute("aria-busy")).toBe("false");
});

it("replays composition widgets called before host injection", async () => {
  const frame = document.createElement("iframe");
  document.body.replaceChildren(frame);
  const doc = frame.contentDocument!,
    win = frame.contentWindow!;
  doc.body.innerHTML = '<section id="t"></section><section id="a"></section>';
  const stub = withReportBootstrap("").match(
    /<script>([\s\S]*?)<\/script>/,
  )![1];
  new Function("window", "document", stub)(win, doc);
  const report = (
    win as unknown as {
      report: Record<string, (...args: unknown[]) => Promise<unknown>>;
    }
  ).report;
  const calls = [
    report.renderTable("#t", { query: "SELECT 1" }),
    report.renderActivity("#a"),
  ];
  installReportHost(frame, {
    dataApi: api([{ one: 1 }], ["org_dashboard_notifications"]),
    title: "Test",
    theme: "light",
    tokenSource: null,
    dispatchData: true,
  });
  await Promise.all(calls);
  expect(
    doc.querySelector("#t")!.shadowRoot!.querySelectorAll("tbody tr"),
  ).toHaveLength(1);
  expect(doc.querySelector("#a")!.shadowRoot!.textContent).toContain(
    "Run summary",
  );
});
