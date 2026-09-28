#!/usr/bin/env python3
"""Validate a governed team metric and separately owned improvement review."""

import argparse
import json
import math
from datetime import datetime, timezone
from pathlib import Path

SCOPE = ("tenant_id", "team_id", "service_id", "environment", "metric_id", "policy_revision", "population_rule_id", "identity_rule_id", "window_start", "window_end")
STABLE = SCOPE[:-2]


def instant(value):
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.astimezone(timezone.utc) if parsed.tzinfo else None
    except ValueError:
        return None


def numeric(value):
    return type(value) in (int, float) and math.isfinite(value)


def rate(num, den):
    return 100 * num / den if den else 0.0


def close(actual, expected):
    return numeric(actual) and math.isclose(actual, expected, abs_tol=0.01)


def counts(doc, prefix=""):
    num, den = doc.get("numerator"), doc.get("denominator")
    if type(num) is not int or type(den) is not int or den < 1 or not 0 <= num <= den:
        return [f"{prefix}numerator or denominator invalid"]
    if not close(doc.get("rate_pct"), rate(num, den)):
        return [f"{prefix}rate arithmetic invalid"]
    return []


def validate_observation(obs):
    errors = []
    if not isinstance(obs, dict) or obs.get("artifact_type") != "engineering-metric-observation/v1" or not obs.get("artifact_id"):
        return ["wrong or missing engineering observation"]
    if any(not isinstance(obs.get(key), str) or not obs.get(key) for key in SCOPE):
        errors.append("engineering metric scope incomplete")
    start, end, observed = (instant(obs.get(key)) for key in ("window_start", "window_end", "observed_at"))
    if not start or not end or not observed or start >= end or end > observed:
        errors.append("engineering observation chronology invalid")
    refs = obs.get("source_refs")
    if not isinstance(refs, dict) or any(not isinstance(refs.get(key), str) or not refs.get(key) for key in ("policy", "current")):
        errors.append("metric policy or current source missing")
    if obs.get("metric_family") not in ("delivery", "quality", "reliability") or not obs.get("metric_definition"):
        errors.append("metric definition or family missing")
    if obs.get("coverage") not in ("complete", "partial", "unavailable") or not obs.get("coverage_note"):
        errors.append("metric source coverage missing")
    errors.extend(counts(obs))
    minimum = obs.get("minimum_denominator")
    target = obs.get("target_max_pct")
    if type(minimum) is not int or minimum < 1 or not numeric(target) or not 0 <= target <= 100:
        errors.append("minimum population or predeclared target invalid")
    den = obs.get("denominator")
    num = obs.get("numerator")
    evaluable = obs.get("coverage") == "complete" and type(den) is int and type(minimum) is int and den >= minimum
    state = obs.get("evaluation_state")
    if not evaluable:
        if state != "not_evaluable" or obs.get("target_state") != "unknown" or obs.get("prior") is not None or obs.get("change_pct_points") is not None:
            errors.append("partial or small population must stay not evaluable")
    elif state == "baseline_first":
        if obs.get("prior") is not None or obs.get("change_pct_points") is not None:
            errors.append("one baseline cannot claim change")
    elif state == "comparable":
        prior = obs.get("prior")
        if not isinstance(prior, dict):
            errors.append("comparable metric needs a prior window")
        else:
            if not isinstance(refs, dict) or not refs.get("prior"):
                errors.append("prior source reference missing")
            for key in STABLE:
                if prior.get(key) != obs.get(key):
                    errors.append(f"prior metric scope mismatch: {key}")
            prior_start, prior_end = instant(prior.get("window_start")), instant(prior.get("window_end"))
            if not prior_start or not prior_end or not start or not end or prior_start >= prior_end or prior_end > start or (prior_end - prior_start) != (end - start):
                errors.append("prior engineering window not comparable")
            if prior.get("coverage") != "complete":
                errors.append("prior source coverage incomplete")
            errors.extend(counts(prior, "prior "))
            if numeric(prior.get("rate_pct")) and numeric(obs.get("rate_pct")) and not close(obs.get("change_pct_points"), obs["rate_pct"] - prior["rate_pct"]):
                errors.append("engineering metric change arithmetic invalid")
    else:
        errors.append("evaluable metric requires baseline or comparable state")
    if evaluable and type(num) is int and type(den) is int and den > 0 and numeric(target):
        expected = "at_or_below" if rate(num, den) <= target else "exceeded"
        if obs.get("target_state") != expected:
            errors.append("metric target state invalid")
    if obs.get("causal_claim") != "none" or obs.get("individual_ranking") is not False or obs.get("source_write_state") != "none":
        errors.append("metric observation claims cause, ranking or source write")
    return errors


def validate_pair(obs, review):
    errors = validate_observation(obs)
    if not isinstance(review, dict) or review.get("artifact_type") != "engineering-improvement-review/v1" or not review.get("artifact_id") or review.get("source_observation_artifact_id") != obs.get("artifact_id"):
        return errors + ["improvement review does not cite exact metric observation"]
    for key in SCOPE:
        if review.get(key) != obs.get(key):
            errors.append(f"improvement review scope mismatch: {key}")
    for key in ("metric_family", "numerator", "denominator", "rate_pct", "evaluation_state", "target_state"):
        if review.get(key) != obs.get(key):
            errors.append(f"improvement review changed metric: {key}")
    source_time, obs_time = instant(review.get("issue_observed_at")), instant(obs.get("observed_at"))
    if not review.get("issue_source_ref") or not source_time or not obs_time or source_time < obs_time or not review.get("owner_id") or not isinstance(review.get("evidence_gaps"), list):
        errors.append("fresh issue source or engineering owner missing")
    match = review.get("issue_match_state")
    if match not in ("none", "related", "exact") or (match == "none" and review.get("issue_id") is not None) or (match != "none" and not review.get("issue_id")):
        errors.append("current engineering issue match invalid")
    case_key = review.get("case_key")
    expected_key = f"{obs.get('tenant_id')}/{obs.get('team_id')}/{obs.get('service_id')}/{obs.get('metric_id')}/{obs.get('policy_revision')}"
    keys = review.get("existing_case_keys")
    if case_key != expected_key or not isinstance(keys, list) or any(not isinstance(key, str) or not key for key in keys) or len(keys) != len(set(keys)):
        errors.append("engineering case ledger invalid")
    elif review.get("case_operation") == "new":
        if case_key in keys or review.get("prior_case_artifact_id") is not None:
            errors.append("duplicate new engineering case")
    elif review.get("case_operation") == "update":
        if case_key not in keys or not review.get("prior_case_artifact_id") or review.get("prior_case_artifact_id") == review.get("artifact_id"):
            errors.append("engineering case update lacks prior artifact")
    else:
        errors.append("engineering case operation invalid")
    recommendation = review.get("recommendation")
    if recommendation not in ("investigate", "instrument", "prioritize", "monitor") or not review.get("owner_question") or not review.get("action_key"):
        errors.append("bounded improvement question or action missing")
    if obs.get("evaluation_state") == "not_evaluable" and recommendation not in ("investigate", "instrument"):
        errors.append("unevaluable metric cannot support priority or monitor disposition")
    decision = review.get("decision_state")
    receipt = review.get("owner_decision")
    if decision == "pending_review":
        if receipt is not None:
            errors.append("pending review claims owner decision")
    elif decision in ("accepted", "deferred", "rejected"):
        if not isinstance(receipt, dict) or receipt.get("owner_id") != review.get("owner_id") or receipt.get("case_key") != case_key or receipt.get("observation_artifact_id") != obs.get("artifact_id") or receipt.get("choice") != decision or not instant(receipt.get("decided_at")) or (source_time and instant(receipt.get("decided_at")) < source_time):
            errors.append("owner decision lacks exact receipt")
    else:
        errors.append("engineering decision state invalid")
    if review.get("issue_action_state") != "none" or review.get("notification_state") != "none" or review.get("outcome_state") != "unmeasured" or review.get("causal_claim") != "none" or review.get("individual_ranking") is not False:
        errors.append("improvement review claims issue action, notification, effect or ranking")
    return errors


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("observation", type=Path)
    parser.add_argument("review", type=Path, nargs="?")
    parser.add_argument("--observation-only", action="store_true")
    args = parser.parse_args()
    if args.observation_only == bool(args.review):
        parser.error("provide review or use --observation-only")
    observation = json.loads(args.observation.read_text())
    errors = validate_observation(observation) if args.observation_only else validate_pair(observation, json.loads(args.review.read_text()))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("Engineering metric observation valid" if args.observation_only else "Engineering metric and owner review valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
