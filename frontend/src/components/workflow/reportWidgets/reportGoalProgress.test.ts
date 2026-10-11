// @vitest-environment happy-dom
import { describe, expect, it, vi } from "vitest";
import type { GoalMetric } from "../../../services/api-types";
import {
  getReportGoalMetrics,
  renderReportGoalProgress,
} from "./reportGoalProgress";
import { installReportHost, withReportBootstrap } from "./reportHostRuntime";
import type { ReportDataApi } from "./reportEmbedContext";
const metric: GoalMetric = {
  id: "latency's-p95",
  criterion_id: "speed",
  name: "Response latency",
  role: "primary",
  unit: "ms",
  direction: "decrease",
  definition: "95th percentile response time",
  source: "latency_samples",
  window: "Past 24 hours",
  route: "learner",
  environment: "production",
  collection_frequency: "daily",
  freshness_hours: 48,
  target: 300,
};
function fixture(status = "ok", value: number | null = 400) {
  const now = Date.now();
  return vi.fn(async (sql: string): Promise<Record<string, unknown>[]> => {
    if (sql.includes("sqlite_master"))
      return [
        { name: "workflow_goal_metrics" },
        { name: "pulse_goal_observations" },
      ];
    if (sql.includes("definition_json"))
      return [
        { metric_id: metric.id, definition_json: JSON.stringify(metric) },
      ];
    return [
      {
        observation_id: "2",
        metric: metric.id,
        criterion_id: "speed",
        unit: "ms",
        route: "learner",
        environment: "production",
        run_id: "run-2",
        value,
        status,
        observed_at: new Date(now).toISOString(),
        window_start: new Date(now - 86400000).toISOString(),
        window_end: new Date(now).toISOString(),
        evidence_json: '["db/sample.json"]',
      },
      {
        observation_id: "1",
        metric: metric.id,
        criterion_id: "speed",
        unit: "ms",
        route: "learner",
        environment: "production",
        run_id: "run-1",
        value: 600,
        status: "ok",
        observed_at: new Date(now - 86400000).toISOString(),
        window_start: new Date(now - 2 * 86400000).toISOString(),
        window_end: new Date(now - 86400000).toISOString(),
        evidence_json: '["db/previous-sample.json"]',
      },
    ];
  });
}
function container() {
  const host = document.createElement("section");
  document.body.replaceChildren(host);
  return host;
}
describe("report goal metrics", () => {
  it("uses scoped bounded queries and the shared progress calculation", async () => {
    const query = fixture(),
      data = await getReportGoalMetrics(query);
    expect(data.progress[0]).toMatchObject({
      current: 400,
      delta: -200,
      state: "Tracking progress",
      targetMet: false,
    });
    expect(data.observations[0].evidence).toEqual(["db/sample.json"]);
    const sql = query.mock.calls.at(-1)![0];
    for (const part of [
      "metric = 'latency''s-p95'",
      "criterion_id = 'speed'",
      "unit = 'ms'",
      "COALESCE(route, '') = 'learner'",
      "COALESCE(environment, '') = 'production'",
      "window_start, window_end",
      "LIMIT 120",
    ])
      expect(sql).toContain(part);
  });
  it("distinguishes missing setup from failed data access", async () => {
    expect(await getReportGoalMetrics(async () => [])).toEqual({
      metrics: [],
      observations: [],
      progress: [],
    });
    await expect(
      getReportGoalMetrics(async () => {
        throw new Error("Access denied");
      }),
    ).rejects.toThrow("Access denied");
  });
  it("renders values, trend, evidence and replaces content on refresh", async () => {
    const host = container();
    await renderReportGoalProgress(document, fixture(), host);
    expect(host.shadowRoot!.textContent).toContain("400");
    expect(host.shadowRoot!.textContent).toContain("db/sample.json");
    expect(host.shadowRoot!.querySelectorAll("svg")).toHaveLength(1);
    const css = host.shadowRoot!.querySelector("style")!.textContent;
    expect(css).toContain("@container (max-width:800px)");
    expect(css).toContain("min-height:44px");
    await renderReportGoalProgress(document, fixture("error", null), host);
    expect(host.shadowRoot!.querySelectorAll("article")).toHaveLength(1);
    expect(host.shadowRoot!.querySelector(".value")!.textContent).toBe("—ms");
    expect(host.shadowRoot!.textContent).toContain("Measurement unavailable");
    expect(host.getAttribute("aria-busy")).toBe("false");
  });
  it("escapes metric text and surfaces failed loading", async () => {
    const host = container(),
      query = fixture(),
      base = query.getMockImplementation()!;
    query.mockImplementation(async (sql) =>
      sql.includes("definition_json")
        ? [
            {
              metric_id: metric.id,
              definition_json: JSON.stringify({
                ...metric,
                name: "<img src=x onerror=alert(1)>",
              }),
            },
          ]
        : base(sql),
    );
    await renderReportGoalProgress(document, query, host);
    expect(host.shadowRoot!.querySelector("img")).toBeNull();
    expect(host.shadowRoot!.querySelector("h3")!.textContent).toContain("<img");
    await expect(
      renderReportGoalProgress(
        document,
        async () => {
          throw new Error("Offline");
        },
        host,
      ),
    ).rejects.toThrow("Offline");
    expect(host.shadowRoot!.textContent).toContain("Goal progress unavailable");
    expect(host.getAttribute("aria-busy")).toBe("false");
  });
  it("does not let a slow old refresh overwrite a newer result", async () => {
    const host = container();
    let release!: (rows: Record<string, unknown>[]) => void;
    const slow = renderReportGoalProgress(
      document,
      () =>
        new Promise((resolve) => {
          release = resolve;
        }),
      host,
    );
    await renderReportGoalProgress(document, fixture(), host);
    release([]);
    await slow;
    expect(host.shadowRoot!.querySelectorAll("article")).toHaveLength(1);
  });
  it("queues both helpers before injection and replays them through the real host", async () => {
    const frame = document.createElement("iframe");
    document.body.replaceChildren(frame);
    const win = frame.contentWindow!,
      doc = frame.contentDocument!;
    doc.body.innerHTML = '<section id="goals"></section>';
    const stub = withReportBootstrap("<div></div>").match(
      /<script>([\s\S]*?)<\/script>/,
    )![1];
    new Function("window", "document", stub)(win, doc);
    const api = (
      win as unknown as {
        report: {
          getGoalMetrics: () => ReturnType<typeof getReportGoalMetrics>;
          renderGoalProgress: (selector: string) => Promise<unknown>;
        };
      }
    ).report;
    const pendingData = api.getGoalMetrics(),
      pendingRender = api.renderGoalProgress("#goals");
    const dataApi = {
      workspacePath: "Workflow/test",
      query: fixture(),
    } as unknown as ReportDataApi;
    installReportHost(frame, {
      dataApi,
      theme: "dark",
      tokenSource: null,
      title: "Goal progress",
      dispatchData: true,
    });
    expect((await pendingData).metrics).toHaveLength(1);
    await pendingRender;
    expect(
      doc.querySelector("#goals")!.shadowRoot!.querySelector("h3")!.textContent,
    ).toBe("Response latency");
  });
});

it("renders supporting slices beneath their primary and preserves independent goals", async () => {
  const primary = {
    ...metric,
    goal_id: "performance",
    goal_name: "Responsiveness",
  };
  const cost = {
    ...metric,
    id: "cost",
    name: "AWS cost",
    goal_id: "spend",
    goal_name: "Cost control",
  };
  const english = {
    ...metric,
    id: "english",
    name: "English latency",
    role: "supporting",
    supports: [primary.id],
    support_kind: "breakdown",
    dimensions: { language: "English" },
  };
  const host = container();
  await renderReportGoalProgress(
    document,
    async (sql) =>
      sql.includes("sqlite_master")
        ? [{ name: "workflow_goal_metrics" }]
        : [english, cost, primary].map((m) => ({
            metric_id: m.id,
            definition_json: JSON.stringify(m),
          })),
    host,
  );
  const speed = host.shadowRoot!.querySelector(
    '[aria-label="Response latency and supporting measurements"]',
  )!;
  expect(speed.querySelectorAll("article")).toHaveLength(2);
  expect(speed.textContent).toContain("Breakdown");
  expect(speed.textContent).toContain("language: English");
  const spend = host.shadowRoot!.querySelector(
    '[aria-label="AWS cost and supporting measurements"]',
  )!;
  expect(spend.querySelectorAll("article")).toHaveLength(1);
  expect(spend.textContent).not.toContain("English latency");
});
