import { expect, it } from "vitest";
import type { GoalMetric } from "../../services/api-types";
import { goalMetricGroups } from "./goalMetricGroups";
const primary = (id: string, goal_id = "performance"): GoalMetric => ({
  id,
  goal_id,
  goal_name: goal_id,
  criterion_id: id,
  name: id,
  role: "primary",
  unit: "ms",
  direction: "decrease",
  definition: "p50",
  source: "samples",
  window: "daily",
  route: "",
  environment: "dev",
  collection_frequency: "daily",
  freshness_hours: 48,
});
it("groups multiple primaries by goal and shares diagnostics only with linked primaries", () => {
  const p50 = primary("p50"),
    p95 = primary("p95"),
    cost = primary("cost", "cost");
  const diagnostic: GoalMetric = {
    ...primary("llm"),
    role: "supporting",
    supports: ["p50", "p95"],
    support_kind: "diagnostic",
  };
  const result = goalMetricGroups([diagnostic, cost, p50, p95]);
  expect(result.groups).toHaveLength(2);
  expect(
    result.groups
      .find((g) => g.id === "performance")!
      .primaries.map((p) => p.supporting.map((s) => s.id)),
  ).toEqual([["llm"], ["llm"]]);
  expect(
    result.groups.find((g) => g.id === "cost")!.primaries[0].supporting,
  ).toEqual([]);
  expect(result.unassigned).toEqual([]);
});
it("resolves legacy single-primary support but keeps ambiguous measurements visible", () => {
  const p50 = primary("p50"),
    support: GoalMetric = { ...primary("language"), role: "supporting" };
  expect(
    goalMetricGroups([support, p50]).groups[0].primaries[0].supporting,
  ).toEqual([support]);
  expect(goalMetricGroups([support, p50, primary("p95")]).unassigned).toEqual([
    support,
  ]);
});
