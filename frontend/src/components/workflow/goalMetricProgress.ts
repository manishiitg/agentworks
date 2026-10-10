import type {
  GoalMetric,
  PulseGoalObservation,
} from "../../services/api-types";

// An environment/window/unit change must never silently become a trend.
export function goalMetricProgress(
  metric: GoalMetric,
  observations: PulseGoalObservation[],
  now = Date.now(),
) {
  const history = observations
    .filter(
      (o) =>
        o.metric === metric.id &&
        o.criterion_id === metric.criterion_id &&
        o.unit === metric.unit &&
        (o.route || "") === (metric.route || "") &&
        (o.environment || "") === (metric.environment || "") &&
        Number.isFinite(Date.parse(o.observed_at)) &&
        Date.parse(o.observed_at) <= now,
    )
    .sort((a, b) => Date.parse(a.observed_at) - Date.parse(b.observed_at) || Date.parse(a.recorded_at || a.observed_at) - Date.parse(b.recorded_at || b.observed_at));
  const latest = history.at(-1);
  const isNumeric = (o: PulseGoalObservation) =>
    ["", "ok", "met", "not_met", "in_progress"].includes(o.status || "") &&
    o.evidence?.some((e) => e.trim()) &&
    typeof o.value === "number" && Number.isFinite(o.value);
  const numeric = history.filter(isNumeric);
  const current = latest && isNumeric(latest) ? latest.value : undefined;
  const previous = latest ? history.filter((o) => Date.parse(o.observed_at) < Date.parse(latest.observed_at)).at(-1) : undefined;
  const comparable = (before: PulseGoalObservation, after: PulseGoalObservation) => {
    if (!isNumeric(before) || !isNumeric(after) || Date.parse(before.observed_at) >= Date.parse(after.observed_at)) return false;
    if (!before.window_start && !before.window_end && !after.window_start && !after.window_end)
      return ["instant", "snapshot", "point_in_time"].includes(metric.window.trim().toLowerCase());
    const ps = Date.parse(before.window_start || ""), pe = Date.parse(before.window_end || "");
    const cs = Date.parse(after.window_start || ""), ce = Date.parse(after.window_end || "");
    return [ps, pe, cs, ce].every(Number.isFinite) && pe > ps && ce > cs && pe - ps === ce - cs && pe <= cs && pe <= Date.parse(before.observed_at) && ce <= Date.parse(after.observed_at);
  };
  const delta = latest && previous && comparable(previous, latest) ? current! - previous.value! : undefined;
  const continuous = history.length > 1 && history.every((o, i) => i === 0 || comparable(history[i-1], o));
  const stale =
    !!latest &&
    now - Date.parse(latest.observed_at) > metric.freshness_hours * 3600000;
  const targetMet =
    current !== undefined &&
    metric.target !== undefined &&
    !stale &&
    (metric.direction === "increase"
      ? current >= metric.target
      : metric.direction === "decrease"
        ? current <= metric.target
        : current === metric.target);
  const state = !latest
    ? "Measurement setup needed"
    : current === undefined
      ? "Measurement unavailable"
      : stale
        ? "Measurement stale"
        : targetMet
          ? "Target met"
          : delta === undefined
            ? "Baseline collecting"
            : "Tracking progress";
  return { history, numeric, latest, current, delta, stale, targetMet, state, continuous };
}
