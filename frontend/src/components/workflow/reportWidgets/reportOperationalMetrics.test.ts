import type { CostSummary } from "../../../services/api-types";
// @vitest-environment happy-dom
import { expect, it, vi } from "vitest";
import {
  getReportCosts,
  renderReportCosts,
} from "./reportOperationalMetrics";
import { installReportHost, withReportBootstrap } from "./reportHostRuntime";
import type { ReportDataApi } from "./reportEmbedContext";
import type {
  CostAggregate,
} from "../../../services/api-types";
const aggregate = (cost: number): CostAggregate => ({
  total_cost_usd: cost,
  call_count: 3,
  prompt_tokens: 100,
  completion_tokens: 30,
  reasoning_tokens: 0,
  cache_read_tokens: 0,
  cache_write_tokens: 0,
});
function api(): ReportDataApi {
  return {
    workspacePath: "Workflow/test",
    getCosts: vi.fn(async () => ({
      success: true,
      runs: [],
      phase_daily_costs: [],
      scoped_costs: {
        total: aggregate(100),
        by_date: { "2026-09-11": aggregate(2), "2026-09-10": aggregate(3) },
        by_scope: { pulse: aggregate(100) },
        by_model: { model: aggregate(5) },
      },
      history: {
        days: 30,
        window_from: "2026-08-13",
        window_to: "2026-09-11",
        has_more: true,
        next_before: "2026-08-13",
      },
    })),
  } as unknown as ReportDataApi;
}
function host() {
  const node = document.createElement("section");
  document.body.replaceChildren(node);
  return node;
}
it("distinguishes all-time totals from paged daily totals and preserves cost cursor", async () => {
  const data = api(),
    costs = await getReportCosts(data, { days: 7, before: "2026-09-12" });
  expect(costs.summary!.total.total_cost_usd).toBe(100);
  expect(costs.window_total_usd).toBe(5);
  expect(costs.history!.next_before).toBe("2026-08-13");
  expect(data.getCosts).toHaveBeenCalledWith({ days: 7, before: "2026-09-12" });
  const container = host();
  await renderReportCosts(document, data, container);
  expect(container.shadowRoot!.textContent).toContain("All-time recorded cost");
  expect(container.shadowRoot!.textContent).toContain("$100.00");
  expect(container.shadowRoot!.textContent).toContain("$5.00");
  expect(container.shadowRoot!.querySelector("svg")).not.toBeNull();
});
it("rejects invalid date windows before fetching and distinguishes zero from unavailable", async () => {
  const data = api();
  for (const options of [
    { days: 0 },
    { days: 91 },
    { days: 1.5 },
    { before: "2026-02-30" },
  ])
    await expect(getReportCosts(data, options)).rejects.toThrow();
  expect(data.getCosts).not.toHaveBeenCalled();
  data.getCosts = async () => ({
    success: true,
    runs: [],
    phase_daily_costs: [],
  });
  expect((await getReportCosts(data)).window_total_usd).toBeUndefined();
  const container = host();
  await renderReportCosts(document, data, container);
  expect(container.shadowRoot!.textContent).toContain(
    "Cost ledger unavailable",
  );
});
it("surfaces failures and does not overwrite newer refreshes", async () => {
  const data = api(),
    container = host();
  let release!: (
    v: Awaited<ReturnType<NonNullable<ReportDataApi["getCosts"]>>>,
  ) => void;
  data.getCosts = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  const pending = renderReportCosts(document, data, container);
  await renderReportCosts(document, api(), container);
  release({ success: true, runs: [], phase_daily_costs: [] });
  await pending;
  expect(container.shadowRoot!.textContent).toContain("$100.00");
  data.getCosts = async () => {
    throw new Error("Offline");
  };
  await expect(renderReportCosts(document, data, container)).rejects.toThrow(
    "Offline",
  );
  expect(container.shadowRoot!.textContent).toContain("Costs unavailable");
  expect(container.getAttribute("aria-busy")).toBe("false");
});
it("replays report methods called before host injection", async () => {
  const frame = document.createElement("iframe");
  document.body.replaceChildren(frame);
  const doc = frame.contentDocument!,
    win = frame.contentWindow!;
  doc.body.innerHTML = '<section id="cost"></section>';
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
    report.getCosts({ days: 7 }),
    report.renderCosts("#cost"),
  ];
  installReportHost(frame, {
    dataApi: api(),
    title: "Test",
    theme: "light",
    tokenSource: null,
    dispatchData: true,
  });
  await Promise.all(calls);
  expect(doc.querySelector("#cost")!.shadowRoot!.textContent).toContain(
    "$100.00",
  );
});

it("renders normalized input/output and explains missing usage in cost reports", async () => {
  const data = api()
  const summary: CostSummary = {
    total: { prompt_tokens: 100, completion_tokens: 10, reasoning_tokens: 0, cache_read_tokens: 80, cache_write_tokens: 0, call_count: 1, total_cost_usd: 0, input_tokens: 100, unpriced_call_count: 1, missing_usage_call_count: 1 },
    by_date: {}, by_model: {}, by_scope: {},
  }
  data.getCosts = async () => ({ success: true, runs: [], phase_daily_costs: [], scoped_costs: summary })
  const container = host()
  await renderReportCosts(document, data, container)
  expect(container.shadowRoot!.textContent).toContain("100 input · 10 output")
  expect(container.shadowRoot!.textContent).toContain("Not priced")
  expect(container.shadowRoot!.textContent).toContain("1 call did not report tokens or cost")
})
