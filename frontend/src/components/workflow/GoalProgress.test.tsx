// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, it, vi } from "vitest";
import { GoalProgress } from "./GoalProgress";
vi.mock("./AskAIButton", () => ({
  AskAIButton: ({ label }: { label: string }) => <button>{label}</button>,
}));
it("leads with the primary metric, a real value, target and observation date", () => {
  const html = renderToStaticMarkup(
    <GoalProgress
      workspacePath="Workflow/example"
      impact={{
        interventions: [],
        assessments: [],
        metrics: [
          {
            id: "followers",
            criterion_id: "audience",
            name: "Followers",
            role: "primary",
            unit: "followers",
            direction: "increase",
            definition: "Total followers",
            source: "daily_metrics",
            window: "instant",
            route: "",
            environment: "",
            collection_frequency: "daily",
            freshness_hours: 48,
            target: 1000,
          },
        ],
        observations: [
          {
            observation_id: "1",
            criterion_id: "audience",
            metric: "followers",
            unit: "followers",
            run_id: "run-1",
            value: 290,
            evidence: ["source"],
            observed_at: "2026-09-09T00:00:00Z",
          },
        ],
      }}
    />,
  );
  expect(html).toContain("Primary metric");
  expect(html).toContain("290");
  expect(html).toContain("1,000");
  expect(html).toContain("Last observed");
  expect(html).toContain("Measurement details and history");
});
it("shows setup without inventing a score for legacy workflows", () => {
  const html = renderToStaticMarkup(
    <GoalProgress
      workspacePath="Workflow/example"
      impact={{ interventions: [], observations: [], assessments: [] }}
    />,
  );
  expect(html).toContain("Measurement setup needed");
  expect(html).toContain("Set up goals &amp; numbers");
  expect(html).not.toContain("0%");
});

it("reveals only the supporting metrics for the primary metric the user opens", async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  const metric = (id: string, name: string, role: "primary" | "supporting", supports?: string[]) => ({
    id,
    criterion_id: id,
    name,
    role,
    supports,
    unit: "ms",
    direction: "decrease" as const,
    definition: `${name} definition`,
    source: "metrics",
    window: "daily",
    route: "",
    environment: "",
    collection_frequency: "daily",
    freshness_hours: 48,
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  try {
    await act(async () => root.render(<GoalProgress workspacePath="Workflow/example" impact={{
      interventions: [], assessments: [], observations: [], metrics: [
        metric("latency", "End-to-end latency", "primary"),
        metric("ttft", "Time to first token", "supporting", ["latency"]),
        metric("quality", "Answer quality", "primary"),
        metric("coverage", "Evaluation coverage", "supporting", ["quality"]),
      ],
    }} />));

    expect(container.textContent).toContain("End-to-end latency");
    expect(container.textContent).toContain("Answer quality");
    expect(container.textContent).not.toContain("Time to first token");
    expect(container.textContent).not.toContain("Evaluation coverage");

    const latencyToggle = [...container.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.getAttribute("aria-controls") === "supporting-metrics-latency")!;
    expect(latencyToggle.getAttribute("aria-expanded")).toBe("false");
    await act(async () => latencyToggle.click());
    expect(latencyToggle.getAttribute("aria-expanded")).toBe("true");
    expect(container.textContent).toContain("Time to first token");
    expect(container.textContent).not.toContain("Evaluation coverage");

    await act(async () => latencyToggle.click());
    expect(container.textContent).not.toContain("Time to first token");
  } finally {
    await act(async () => root.unmount());
    container.remove();
  }
});
