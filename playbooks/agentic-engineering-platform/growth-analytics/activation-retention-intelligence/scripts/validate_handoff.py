#!/usr/bin/env python3
"""Check maturity-aware SaaS cohort observations and pending experiment handoffs."""

import json
import sys
from datetime import date, datetime, timedelta
from pathlib import Path


def required(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def integer(value, label, *, positive=False):
    if isinstance(value, bool) or not isinstance(value, int) or value < (1 if positive else 0):
        raise ValueError(f"{label} must be a {'positive' if positive else 'nonnegative'} integer")
    return value


def signed(value, label):
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{label} must be an integer")
    return value


def timestamp(value, label):
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


def rate_bps(numerator, denominator):
    return (numerator * 10000 + denominator // 2) // denominator


def sources(items, as_of):
    if not isinstance(items, list) or not items:
        raise ValueError("dated source_refs are required")
    refs = {}
    for item in items:
        if not isinstance(item, dict):
            raise ValueError("source_ref must be an object")
        key = required(item.get("id"), "source id")
        required(item.get("uri"), "source uri")
        if key in refs or timestamp(item.get("observed_at"), "source observed_at") > as_of:
            raise ValueError("source IDs must be unique and not later than artifact")
        refs[key] = item
    return refs


def validate_observation(observation):
    if not isinstance(observation, dict) or observation.get("artifact_type") != "cohort-retention-observation/v1":
        raise ValueError("expected cohort-retention-observation/v1")
    for key in ("artifact_id", "product_id", "tenant_id", "policy_id", "activation_rule", "retention_rule", "timezone", "owner", "next_check"):
        required(observation.get(key), key)
    as_of = timestamp(observation.get("as_of"), "as_of")
    refs = sources(observation.get("source_refs"), as_of)
    maturity_days = integer(observation.get("maturity_days"), "maturity_days", positive=True)
    if maturity_days != 30:
        raise ValueError("v1 retention maturity must be day 30")
    minimum = integer(observation.get("minimum_identity_coverage_bps"), "minimum_identity_coverage_bps", positive=True)
    if minimum > 10000:
        raise ValueError("minimum coverage cannot exceed 100%")
    state = observation.get("comparison_state")
    if state not in ("comparable", "baseline_first", "pending_maturity"):
        raise ValueError("comparison_state is invalid")
    cohorts = observation.get("cohorts")
    if not isinstance(cohorts, list) or len(cohorts) != (2 if state == "comparable" else 1):
        raise ValueError("comparison needs two cohorts; baseline or pending needs one")
    ids = set()
    source_ids = set()
    windows = []
    for cohort in cohorts:
        if not isinstance(cohort, dict):
            raise ValueError("cohort must be an object")
        cohort_id = required(cohort.get("cohort_id"), "cohort_id")
        if cohort_id in ids:
            raise ValueError("cohort IDs must be distinct")
        ids.add(cohort_id)
        start, end = day(cohort.get("signup_start"), "signup_start"), day(cohort.get("signup_end"), "signup_end")
        if start > end or end > as_of.date():
            raise ValueError("cohort signup window is invalid")
        maturity = day(cohort.get("maturity_date"), "maturity_date")
        if maturity != end + timedelta(days=maturity_days):
            raise ValueError("maturity date must follow the final signup by 30 days")
        windows.append((start, end))
        eligible = integer(cohort.get("eligible_accounts"), "eligible_accounts", positive=True)
        matched = integer(cohort.get("matched_identity_accounts"), "matched_identity_accounts")
        activated = integer(cohort.get("activated_accounts"), "activated_accounts")
        if matched > eligible or activated > eligible or activated > matched or rate_bps(matched, eligible) < minimum:
            raise ValueError("cohort identity coverage or activation count is inconsistent")
        if integer(cohort.get("identity_coverage_bps"), "identity_coverage_bps") != rate_bps(matched, eligible):
            raise ValueError("identity coverage rate does not reconcile")
        if integer(cohort.get("activation_rate_bps"), "activation_rate_bps") != rate_bps(activated, eligible):
            raise ValueError("activation rate does not reconcile")
        for key in ("product_source_ref", "billing_source_ref"):
            ref = required(cohort.get(key), key)
            if ref not in refs or ref in source_ids:
                raise ValueError("each cohort needs distinct product and billing evidence")
            source_ids.add(ref)
        if state == "pending_maturity":
            if as_of.date() >= maturity or cohort.get("state") != "pending_maturity" or cohort.get("retained_accounts") is not None or cohort.get("retention_rate_bps") is not None or cohort.get("unmatured_accounts") != eligible:
                raise ValueError("immature cohort cannot claim retention or churn")
        else:
            if as_of.date() < maturity or cohort.get("state") != "mature" or cohort.get("unmatured_accounts") != 0:
                raise ValueError("mature cohort needs a closed outcome window")
            retained = integer(cohort.get("retained_accounts"), "retained_accounts")
            if retained > eligible or retained > matched or integer(cohort.get("retention_rate_bps"), "retention_rate_bps") != rate_bps(retained, eligible):
                raise ValueError("retention count or rate does not reconcile")
    if state == "comparable":
        if windows[0][1] >= windows[1][0] or windows[0][1] - windows[0][0] != windows[1][1] - windows[1][0]:
            raise ValueError("cohort windows must be ordered and equally long")
        if signed(observation.get("retention_change_bps"), "retention_change_bps") != cohorts[1]["retention_rate_bps"] - cohorts[0]["retention_rate_bps"]:
            raise ValueError("retention change does not reconcile")
    elif observation.get("retention_change_bps") is not None:
        raise ValueError("baseline or immature cohort cannot claim a retention trend")
    if not isinstance(observation.get("limitations"), list) or not observation["limitations"]:
        raise ValueError("limitations are required")
    return as_of


def validate_plan(observation, plan):
    observed = validate_observation(observation)
    if observation["comparison_state"] == "pending_maturity":
        raise ValueError("pending maturity blocks experiment planning")
    if not isinstance(plan, dict) or plan.get("artifact_type") != "retention-experiment-plan/v1":
        raise ValueError("expected retention-experiment-plan/v1")
    for key in ("product_id", "tenant_id", "policy_id", "timezone"):
        if plan.get(key) != observation[key]:
            raise ValueError(f"plan {key} differs from observation")
    if plan.get("source_observation_artifact_id") != observation["artifact_id"] or plan.get("source_comparison_state") != observation["comparison_state"]:
        raise ValueError("plan needs the exact observation and comparison state")
    if plan.get("cohort_ids") != [cohort["cohort_id"] for cohort in observation["cohorts"]]:
        raise ValueError("plan cohort IDs differ from observation")
    for key in ("artifact_id", "hypothesis", "proposed_change", "assignment_unit", "eligible_population", "primary_metric", "guardrail_metric", "stop_rule", "owner", "next_check"):
        required(plan.get(key), key)
    if plan["owner"] != observation["owner"]:
        raise ValueError("plan owner differs from observation")
    if plan["assignment_unit"] != "account" or plan["primary_metric"] != "day30-retained-per-eligible-v2" or plan["guardrail_metric"] == "none":
        raise ValueError("plan assignment, metric or guardrail is invalid")
    if integer(plan.get("baseline_rate_bps"), "baseline_rate_bps") != observation["cohorts"][-1]["retention_rate_bps"]:
        raise ValueError("plan baseline must use the latest observed mature cohort")
    if observation["comparison_state"] == "baseline_first":
        if plan.get("observed_change_bps") is not None:
            raise ValueError("baseline-first plan cannot claim a retention change")
    elif signed(plan.get("observed_change_bps"), "observed_change_bps") != observation["retention_change_bps"]:
        raise ValueError("plan change differs from observation")
    plan_as_of = timestamp(plan.get("as_of"), "plan as_of")
    refs = sources(plan.get("source_refs"), plan_as_of)
    if plan_as_of < observed or not any(ref["uri"] == "artifact:" + observation["artifact_id"] for ref in refs.values()) or not any(ref["uri"].startswith("owner-docs/") for ref in refs.values()):
        raise ValueError("plan needs exact observation and owner policy evidence")
    if plan.get("sample_plan_state") != "needs_power_review" or plan.get("minimum_sample_per_variant") is not None:
        raise ValueError("sample plan remains pending power review")
    if plan.get("decision_state") != "pending_owner_review" or plan.get("launch_state") != "not_launched":
        raise ValueError("plan cannot claim approval, launch or a winner")
    if any(plan.get(key) is not None for key in ("winner", "measured_outcome", "provider_launch_receipt")):
        raise ValueError("pending plan cannot contain a winner, measured outcome or launch receipt")


def main(argv):
    if not argv or argv[0] not in ("observation", "plan") or len(argv) != (2 if argv[0] == "observation" else 3):
        raise SystemExit("usage: validate_handoff.py observation <observation.json> | plan <observation.json> <plan.json>")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        (validate_observation if argv[0] == "observation" else validate_plan)(*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid retention handoff: {exc}") from exc
    print(f"valid retention {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
