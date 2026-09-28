#!/usr/bin/env python3
"""Check exact signup-to-paid counts and a pending experiment handoff."""

import json
import sys
from datetime import date, datetime
from pathlib import Path


def required(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def count(value, label, *, positive=False):
    if isinstance(value, bool) or not isinstance(value, int) or value < (1 if positive else 0):
        raise ValueError(f"{label} must be a {'positive' if positive else 'nonnegative'} integer")
    return value


def signed(value, label):
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{label} must be an integer")
    return value


def instant(value, label):
    required(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def day(value, label):
    required(value, label)
    try:
        return date.fromisoformat(value)
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO date") from exc


def dated_sources(value, observed):
    if not isinstance(value, list) or not value:
        raise ValueError("dated source_refs are required")
    refs = {}
    for item in value:
        if not isinstance(item, dict):
            raise ValueError("source_ref must be an object")
        key = required(item.get("id"), "source id")
        required(item.get("uri"), "source uri")
        if key in refs or instant(item.get("observed_at"), "source observed_at") > observed:
            raise ValueError("source IDs must be unique and no later than artifact")
        refs[key] = item
    return refs


def rate_bps(numerator, denominator):
    return (numerator * 10000 + denominator // 2) // denominator


def validate_observation(observation):
    if not isinstance(observation, dict) or observation.get("artifact_type") != "funnel-observation/v1":
        raise ValueError("expected funnel-observation/v1")
    for key in ("artifact_id", "product_id", "tenant_id", "cohort_id", "timezone", "identity_rule", "exclusion_rule", "metric_id", "owner", "next_check"):
        required(observation.get(key), key)
    if observation["metric_id"] != "paid-per-eligible-v2":
        raise ValueError("v1 metric must be paid per eligible account")
    observed = instant(observation.get("observed_at"), "observed_at")
    refs = dated_sources(observation.get("source_refs"), observed)
    state = observation.get("comparison_state")
    if state not in ("comparable", "baseline_first"):
        raise ValueError("comparison_state must be comparable or baseline_first")
    current = (day(observation.get("current_start"), "current_start"), day(observation.get("current_end"), "current_end"))
    if current[0] > current[1] or current[1] > observed.date():
        raise ValueError("current funnel window must be complete")
    if state == "comparable":
        baseline = (day(observation.get("baseline_start"), "baseline_start"), day(observation.get("baseline_end"), "baseline_end"))
        if baseline[0] > baseline[1] or baseline[1] >= current[0] or baseline[1] - baseline[0] != current[1] - current[0]:
            raise ValueError("funnel windows must be ordered and equally long")
        periods = (("baseline", baseline[1]), ("current", current[1]))
    else:
        if any(observation.get(key) is not None for key in ("baseline_start", "baseline_end", "baseline")):
            raise ValueError("baseline_first must not claim a prior period")
        periods = (("current", current[1]),)
    stages = observation.get("stages")
    stage_ids = ("eligible", "signup", "activated", "paid")
    if not isinstance(stages, list) or len(stages) != len(stage_ids) or tuple(stage.get("id") if isinstance(stage, dict) else None for stage in stages) != stage_ids:
        raise ValueError("funnel needs ordered eligible, signup, activated, paid stages")
    for stage in stages:
        required(stage.get("definition"), "stage definition")
        required(stage.get("version"), "stage version")
    minimum = count(observation.get("minimum_identity_coverage_bps"), "minimum_identity_coverage_bps", positive=True)
    if minimum > 10000:
        raise ValueError("minimum identity coverage cannot exceed 100%")
    source_ids = []
    for period_name, period_end in periods:
        period = observation.get(period_name)
        if not isinstance(period, dict) or not isinstance(period.get("counts"), dict) or set(period["counts"]) != set(stage_ids):
            raise ValueError(f"{period_name} needs every stage count")
        values = [count(period["counts"][stage], f"{period_name}.{stage}", positive=stage == "eligible") for stage in stage_ids]
        if any(left < right for left, right in zip(values, values[1:])):
            raise ValueError(f"{period_name} stage counts must be monotone")
        matched = count(period.get("matched_identity_users"), f"{period_name}.matched_identity_users")
        if matched < values[-1] or matched > values[0] or rate_bps(matched, values[0]) < minimum:
            raise ValueError(f"{period_name} identity coverage is insufficient or inconsistent")
        for ref_name in ("event_source_ref", "paid_source_ref"):
            ref = required(period.get(ref_name), ref_name)
            if ref not in refs:
                raise ValueError(f"{period_name} lacks {ref_name} evidence")
            source_ids.append(ref)
        cutoff = instant(period.get("paid_state_cutoff"), "paid_state_cutoff")
        if cutoff.date() < period_end or cutoff > observed:
            raise ValueError("paid state cutoff must cover the full cohort window")
    if len(set(source_ids)) != len(source_ids):
        raise ValueError("period event and paid sources need distinct references")
    expected = {
        "baseline_paid_rate_bps": rate_bps(observation["baseline"]["counts"]["paid"], observation["baseline"]["counts"]["eligible"]) if state == "comparable" else None,
        "current_paid_rate_bps": rate_bps(observation["current"]["counts"]["paid"], observation["current"]["counts"]["eligible"]),
        "baseline_activation_per_signup_bps": rate_bps(observation["baseline"]["counts"]["activated"], observation["baseline"]["counts"]["signup"]) if state == "comparable" and observation["baseline"]["counts"]["signup"] else None,
        "current_activation_per_signup_bps": rate_bps(observation["current"]["counts"]["activated"], observation["current"]["counts"]["signup"]) if observation["current"]["counts"]["signup"] else None,
    }
    for key, value in expected.items():
        if value is None:
            if observation.get(key) is not None:
                raise ValueError(f"{key} needs a denominator")
        elif count(observation.get(key), key) != value:
            raise ValueError(f"{key} does not reconcile")
    if state == "comparable":
        if signed(observation.get("change_bps"), "change_bps") != expected["current_paid_rate_bps"] - expected["baseline_paid_rate_bps"]:
            raise ValueError("paid rate change does not reconcile")
    elif observation.get("change_bps") is not None:
        raise ValueError("baseline_first cannot claim a rate change")
    coverage_full = all(observation[name]["matched_identity_users"] == observation[name]["counts"]["eligible"] for name, _ in periods)
    if observation.get("quality_state") != ("complete" if coverage_full else "partial_coverage"):
        raise ValueError("quality_state must disclose partial identity coverage")
    if not isinstance(observation.get("limitations"), list) or not observation["limitations"]:
        raise ValueError("limitations are required")
    return observed


def validate_plan(observation, plan):
    observed = validate_observation(observation)
    if not isinstance(plan, dict) or plan.get("artifact_type") != "funnel-experiment-plan/v1":
        raise ValueError("expected funnel-experiment-plan/v1")
    for key in ("product_id", "tenant_id", "cohort_id", "metric_id", "timezone"):
        if plan.get(key) != observation[key]:
            raise ValueError(f"plan {key} differs from observation")
    if plan.get("source_observation_artifact_id") != observation["artifact_id"]:
        raise ValueError("plan cites a different observation")
    if plan.get("source_comparison_state") != observation["comparison_state"]:
        raise ValueError("plan comparison state differs from observation")
    for key in ("artifact_id", "hypothesis", "proposed_change", "assignment_unit", "eligible_population", "primary_metric", "guardrail_metric", "stop_rule", "owner", "next_check"):
        required(plan.get(key), key)
    if plan["primary_metric"] != observation["metric_id"] or plan["assignment_unit"] != "account" or plan["guardrail_metric"] == "none":
        raise ValueError("plan metric, account assignment or guardrail is invalid")
    if count(plan.get("baseline_rate_bps"), "baseline_rate_bps") != observation["current_paid_rate_bps"]:
        raise ValueError("plan baseline must use observed current paid rate")
    if observation["comparison_state"] == "baseline_first":
        if plan.get("observed_change_bps") is not None:
            raise ValueError("baseline-first plan cannot claim an observed change")
    elif signed(plan.get("observed_change_bps"), "observed_change_bps") != observation["change_bps"]:
        raise ValueError("plan observed change differs from observation")
    plan_observed = instant(plan.get("observed_at"), "observed_at")
    refs = dated_sources(plan.get("source_refs"), plan_observed)
    if plan_observed < observed or not any(ref["uri"] == "artifact:" + observation["artifact_id"] for ref in refs.values()):
        raise ValueError("plan needs the exact prior observation")
    if len(refs) < 2:
        raise ValueError("plan needs owner policy evidence")
    if plan.get("sample_plan_state") != "needs_power_review" or plan.get("minimum_sample_per_variant") is not None:
        raise ValueError("sample plan remains pending a power review")
    if plan.get("decision_state") != "pending_owner_review" or plan.get("launch_state") != "not_launched":
        raise ValueError("plan is pending owner review and cannot claim launch or a winner")


def main(argv):
    if not argv or argv[0] not in ("observation", "plan") or len(argv) != (2 if argv[0] == "observation" else 3):
        raise SystemExit("usage: validate_handoff.py observation <observation.json> | plan <observation.json> <plan.json>")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        (validate_observation if argv[0] == "observation" else validate_plan)(*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid funnel handoff: {exc}") from exc
    print(f"valid funnel {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
