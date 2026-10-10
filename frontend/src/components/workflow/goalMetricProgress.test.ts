import { describe, expect, it } from "vitest";
import { goalMetricProgress } from "./goalMetricProgress";
import type {
  GoalMetric,
  PulseGoalObservation,
} from "../../services/api-types";
const metric: GoalMetric = {
  id: "followers",
  criterion_id: "audience",
  name: "Followers",
  role: "primary",
  unit: "followers",
  direction: "increase",
  definition: "Observed followers",
  source: "daily_metrics",
  window: "instant",
  route: "",
  environment: "",
  collection_frequency: "daily",
  freshness_hours: 48,
  target: 300,
};
const observation = (
  extra: Partial<PulseGoalObservation>,
): PulseGoalObservation => ({
  observation_id: "1",
  criterion_id: "audience",
  metric: "followers",
  run_id: "run-1",
  unit: "followers",
  value: 290,
  evidence: ["source"],
  observed_at: "2026-09-09T00:00:00Z",
  ...extra,
});
const now = Date.parse("2026-09-10T00:00:00Z");
describe("goal progress", () => {
  it("retains period history without inventing comparisons across unknown windows or gaps", () => {
    const daily = { ...metric, window: "daily" };
    const before = observation({ value: 0, observed_at: "2026-09-09T00:00:00Z", window_start: "2026-09-08T00:00:00Z", window_end: "2026-09-09T00:00:00Z" });
    const after = observation({ value: 4, observed_at: "2026-09-10T00:00:00Z", window_start: "2026-09-09T00:00:00Z", window_end: "2026-09-10T00:00:00Z" });
    expect(goalMetricProgress(daily, [before, after], now).delta).toBe(4);
    expect(goalMetricProgress(daily, [observation({ value: 0 }), after], now).delta).toBeUndefined();
    const partial = { ...after, window_start: "2026-09-09T12:00:00Z" };
    expect(goalMetricProgress(daily, [before, partial], now).delta).toBeUndefined();
    const unavailable = observation({ value: undefined, status: "unavailable", observed_at: "2026-09-09T12:00:00Z" });
    const p = goalMetricProgress(daily, [before, unavailable, after], now);
    expect(p.history).toHaveLength(3);
    expect(p.delta).toBeUndefined();
    expect(p.continuous).toBe(false);
  });
  it("compares matching metrics only and retains genuine zero", () => {
    const result = goalMetricProgress(
      metric,
      [
        observation({ value: 0, observed_at: "2026-09-08T00:00:00Z" }),
        observation({}),
        observation({ unit: "percent", value: 99 }),
        observation({ environment: "test", value: 9000 }),
      ],
      now,
    );
    expect(result.numeric).toHaveLength(2);
    expect(result.delta).toBe(290);
    expect(result.current).toBe(290);
  });
  it("does not replace a failed latest collection with an old success", () => {
    const p = goalMetricProgress(
      metric,
      [
        observation({ value: 400 }),
        observation({
          observation_id: "2",
          observed_at: "2026-09-10T00:00:00Z",
          value: undefined,
          status: "blocked",
        }),
      ],
      now,
    );
    expect(p.current).toBeUndefined();
    expect(p.state).toBe("Measurement unavailable");
    expect(p.targetMet).toBe(false);
  });
  it("shows missing setup, baseline collection and stale data honestly", () => {
    expect(goalMetricProgress(metric, [], now).state).toBe(
      "Measurement setup needed",
    );
    expect(goalMetricProgress(metric, [observation({})], now).state).toBe(
      "Baseline collecting",
    );
    expect(
      goalMetricProgress(
        metric,
        [observation({ value: 400 })],
        now + 72 * 3600000,
      ).state,
    ).toBe("Measurement stale");
  });
});
