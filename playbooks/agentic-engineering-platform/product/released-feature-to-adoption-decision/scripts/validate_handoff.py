#!/usr/bin/env python3
"""Validate released-feature account counts and a separate Product decision."""

import argparse
import json
import math
from datetime import datetime, timezone
from pathlib import Path

SCOPE = ("tenant_id", "product_id", "feature_id", "release_id", "build_id", "flag_id", "flag_revision", "segment_id", "measurement_rule_id", "identity_rule_id", "window_start", "window_end")


def instant(value):
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.astimezone(timezone.utc) if parsed.tzinfo else None
    except ValueError:
        return None


def number(value):
    return type(value) in (int, float) and math.isfinite(value)


def rate(numerator, denominator):
    return 100 * numerator / denominator if denominator else 0.0


def close(actual, expected):
    return number(actual) and math.isclose(actual, expected, abs_tol=0.01)


def counts(doc, prefix=""):
    errors = []
    eligible, exposed, used = (doc.get(f"{prefix}{field}") for field in ("eligible_accounts", "exposed_accounts", "used_accounts"))
    if any(type(value) is not int for value in (eligible, exposed, used)) or not (eligible > 0 and 0 <= used <= exposed <= eligible):
        errors.append(f"{prefix}eligible/exposed/used account counts invalid")
        return errors
    if not close(doc.get(f"{prefix}exposure_pct"), rate(exposed, eligible)) or not close(doc.get(f"{prefix}use_pct"), rate(used, exposed)):
        errors.append(f"{prefix}exposure or use rate arithmetic invalid")
    return errors


def validate_observation(obs):
    errors = []
    if obs.get("artifact_type") != "feature-adoption-observation/v1" or not obs.get("artifact_id"):
        errors.append("wrong or missing feature observation")
    if any(not obs.get(key) for key in SCOPE):
        errors.append("feature observation scope incomplete")
    start, end, observed, released = (instant(obs.get(key)) for key in ("window_start", "window_end", "observed_at", "released_at"))
    if not start or not end or not observed or not released or released > start or start >= end or end > observed:
        errors.append("release or observation chronology invalid")
    refs = obs.get("source_refs")
    if not isinstance(refs, dict) or any(not isinstance(refs.get(key), str) or not refs.get(key) for key in ("release", "flag", "eligibility", "exposure", "use")):
        errors.append("release, flag or analytics source missing")
    if obs.get("coverage") not in ("complete", "partial", "unavailable") or not obs.get("coverage_note"):
        errors.append("analytics coverage missing")
    errors.extend(counts(obs))
    minimum = obs.get("minimum_exposed")
    target = obs.get("target_use_pct")
    if type(minimum) is not int or minimum < 1 or not number(target) or not 0 <= target <= 100:
        errors.append("predeclared sample or target invalid")
    exposed = obs.get("exposed_accounts")
    used = obs.get("used_accounts")
    evaluable = obs.get("coverage") == "complete" and type(exposed) is int and type(minimum) is int and exposed >= minimum
    state = obs.get("evaluation_state")
    if not evaluable:
        if state != "not_evaluable" or obs.get("target_state") != "unknown" or obs.get("prior") is not None or obs.get("trend_pct_points") is not None:
            errors.append("incomplete or small sample must stay not evaluable")
    elif state == "baseline_first":
        if obs.get("prior") is not None or obs.get("trend_pct_points") is not None:
            errors.append("one baseline cannot claim a trend")
    elif state == "comparable":
        prior = obs.get("prior")
        if not isinstance(prior, dict):
            errors.append("comparable observation lacks prior window")
        else:
            for key in ("tenant_id", "product_id", "feature_id", "release_id", "build_id", "flag_id", "flag_revision", "segment_id", "measurement_rule_id", "identity_rule_id"):
                if prior.get(key) != obs.get(key):
                    errors.append(f"prior feature scope mismatch: {key}")
            prior_start, prior_end = instant(prior.get("window_start")), instant(prior.get("window_end"))
            if not prior_start or not prior_end or not start or not end or not released or prior_start >= prior_end or prior_end > start or (prior_end - prior_start) != (end - start) or prior_start < released:
                errors.append("prior feature window not comparable")
            if prior.get("coverage") != "complete":
                errors.append("prior source coverage incomplete")
            errors.extend(counts(prior))
            if number(prior.get("use_pct")) and number(obs.get("use_pct")) and not close(obs.get("trend_pct_points"), obs["use_pct"] - prior["use_pct"]):
                errors.append("use trend arithmetic invalid")
    else:
        errors.append("evaluable observation needs baseline or comparable state")
    if evaluable and type(used) is int and type(exposed) is int and number(target):
        expected_target = "met" if rate(used, exposed) >= target else "below"
        if obs.get("target_state") != expected_target:
            errors.append("target state does not match predeclared use rate")
    if obs.get("causal_claim") != "none" or obs.get("experiment_state") != "none" or obs.get("product_change_state") != "none":
        errors.append("observation claims cause, experiment or product change")
    return errors


def validate_pair(obs, decision):
    errors = validate_observation(obs)
    if decision.get("artifact_type") != "feature-adoption-decision/v1" or not decision.get("artifact_id") or decision.get("source_observation_artifact_id") != obs.get("artifact_id"):
        errors.append("Product decision does not cite exact observation")
    for key in SCOPE:
        if decision.get(key) != obs.get(key):
            errors.append(f"Product decision scope mismatch: {key}")
    if decision.get("evaluation_state") != obs.get("evaluation_state") or decision.get("target_state") != obs.get("target_state") or decision.get("eligible_accounts") != obs.get("eligible_accounts") or decision.get("exposed_accounts") != obs.get("exposed_accounts") or decision.get("used_accounts") != obs.get("used_accounts"):
        errors.append("Product decision changed observation counts or state")
    issue_time, obs_time = instant(decision.get("issue_observed_at")), instant(obs.get("observed_at"))
    if not decision.get("issue_source_ref") or not issue_time or not obs_time or issue_time < obs_time or not decision.get("owner_id") or not isinstance(decision.get("evidence_gaps"), list):
        errors.append("current issue and owner review missing")
    match = decision.get("issue_match_state")
    if match not in ("none", "related", "exact") or (match == "none" and decision.get("issue_id") is not None) or (match != "none" and not decision.get("issue_id")):
        errors.append("current issue match invalid")
    keys = decision.get("existing_case_keys")
    if not isinstance(keys, list) or any(not isinstance(key, str) or not key for key in keys) or len(keys) != len(set(keys)) or not decision.get("case_key"):
        errors.append("product case key ledger invalid")
    elif decision.get("case_operation") == "new":
        if decision["case_key"] in keys or decision.get("prior_case_artifact_id") is not None:
            errors.append("duplicate new product case")
    elif decision.get("case_operation") == "update":
        if decision["case_key"] not in keys or not decision.get("prior_case_artifact_id") or decision.get("prior_case_artifact_id") == decision.get("artifact_id"):
            errors.append("product repeat lacks prior artifact")
    else:
        errors.append("product case operation invalid")
    recommendation = decision.get("recommendation")
    if recommendation not in ("investigate", "instrument", "iterate", "keep", "stop"):
        errors.append("Product recommendation invalid")
    if obs.get("evaluation_state") == "not_evaluable" and recommendation not in ("investigate", "instrument"):
        errors.append("incomplete observation cannot support product disposition")
    state = decision.get("decision_state")
    receipt = decision.get("owner_decision")
    if state == "pending_review":
        if receipt is not None:
            errors.append("pending Product review claims owner decision")
    elif state == "owner_reviewed":
        if not isinstance(receipt, dict) or receipt.get("owner_id") != decision.get("owner_id") or receipt.get("case_key") != decision.get("case_key") or receipt.get("observation_artifact_id") != obs.get("artifact_id") or receipt.get("choice") not in ("investigate", "instrument", "iterate", "keep", "stop") or not instant(receipt.get("decided_at")) or (issue_time and instant(receipt["decided_at"]) < issue_time):
            errors.append("owner-reviewed Product decision lacks exact receipt")
    else:
        errors.append("Product decision state invalid")
    if decision.get("causal_claim") != "none" or decision.get("issue_action_state") != "none" or decision.get("product_change_state") != "none" or decision.get("customer_promise") is not False:
        errors.append("Product decision claims cause, action or customer promise")
    return errors


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("observation", type=Path)
    parser.add_argument("decision", type=Path, nargs="?")
    parser.add_argument("--observation-only", action="store_true")
    args = parser.parse_args()
    if args.observation_only == bool(args.decision):
        parser.error("provide a decision or use --observation-only")
    obs = json.loads(args.observation.read_text())
    errors = validate_observation(obs) if args.observation_only else validate_pair(obs, json.loads(args.decision.read_text()))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("Feature adoption observation valid" if args.observation_only else "Feature adoption and Product decision valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
