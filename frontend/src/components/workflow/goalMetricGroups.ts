import type { GoalMetric } from "../../services/api-types";

export function goalMetricGroups(metrics: GoalMetric[]) {
  const primaries = metrics.filter((m) => m.role === "primary");
  const groups: Array<{
    id: string;
    name: string;
    primaries: Array<{ metric: GoalMetric; supporting: GoalMetric[] }>;
  }> = [];
  const assigned = new Set<string>();
  for (const primary of primaries) {
    const id = primary.goal_id || "";
    let group = groups.find((g) => g.id === id);
    if (!group) {
      group = {
        id,
        name: primary.goal_name || "Workflow goals",
        primaries: [],
      };
      groups.push(group);
    }
    const supporting = metrics.filter(
      (m) =>
        m.role === "supporting" &&
        (m.supports?.length
          ? m.supports.includes(primary.id)
          : primaries.length === 1),
    );
    supporting.forEach((m) => assigned.add(m.id));
    group.primaries.push({ metric: primary, supporting });
  }
  return {
    groups,
    unassigned: metrics.filter(
      (m) => m.role === "supporting" && !assigned.has(m.id),
    ),
  };
}

export function supportingMetricLabel(metric: GoalMetric) {
  const labels: Record<string, string> = {
    breakdown: "Breakdown",
    diagnostic: "Diagnostic measurement",
    guardrail: "Guardrail",
  };
  return labels[metric.support_kind || ""] || "Supporting metric";
}

export function metricDimensions(metric: GoalMetric) {
  return Object.entries(metric.dimensions || {})
    .map(([key, value]) => `${key}: ${value}`)
    .join(" · ");
}
