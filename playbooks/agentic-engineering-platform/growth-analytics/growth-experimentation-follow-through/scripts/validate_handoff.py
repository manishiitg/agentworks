#!/usr/bin/env python3
"""Block false growth experiment launches and unsupported outcome claims."""

import json
import sys
from datetime import datetime, timedelta
from pathlib import Path


def need(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def number(value, label, *, positive=False):
    if isinstance(value, bool) or not isinstance(value, int) or value < (1 if positive else 0):
        raise ValueError(f"{label} must be a {'positive' if positive else 'nonnegative'} integer")
    return value


def instant(value, label):
    need(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def refs(items, as_of):
    if not isinstance(items, list) or not items:
        raise ValueError("dated source_refs required")
    result = {}
    for item in items:
        if not isinstance(item, dict):
            raise ValueError("source_ref must be an object")
        key = need(item.get("id"), "source id")
        need(item.get("uri"), "source uri")
        if key in result or instant(item.get("observed_at"), "source observed_at") > as_of:
            raise ValueError("source IDs must be unique and no later than artifact")
        result[key] = item["uri"]
    return result


def rate(numerator, denominator):
    return (numerator * 10000 + denominator // 2) // denominator


def validate_plan(plan):
    if not isinstance(plan, dict) or plan.get("artifact_type") != "frozen-experiment-plan/v1":
        raise ValueError("expected frozen-experiment-plan/v1")
    for key in ("artifact_id", "revision", "experiment_id", "product_id", "tenant_id", "owner", "source_plan_artifact_id", "stop_rule", "normalization_note"):
        need(plan.get(key), key)
    as_of = instant(plan.get("as_of"), "plan as_of")
    sources = refs(plan.get("source_refs"), as_of)
    if "artifact:" + plan["source_plan_artifact_id"] not in sources.values() or not any(uri.startswith("owner-docs/") for uri in sources.values()):
        raise ValueError("frozen plan needs upstream proposal and owner policy sources")
    return as_of


def validate_execution(plan, record):
    plan_as_of = validate_plan(plan)
    if not isinstance(record, dict) or record.get("artifact_type") != "experiment-execution-record/v1":
        raise ValueError("expected experiment-execution-record/v1")
    for key in ("artifact_id", "experiment_id", "product_id", "tenant_id", "plan_artifact_id", "plan_revision", "owner", "rollback_owner", "timezone", "stop_rule", "next_check"):
        need(record.get(key), key)
    as_of = instant(record.get("as_of"), "as_of")
    if as_of < plan_as_of:
        raise ValueError("execution precedes frozen plan")
    sources = refs(record.get("source_refs"), as_of)
    if "artifact:" + record["plan_artifact_id"] not in sources.values():
        raise ValueError("exact frozen plan source is required")
    for plan_key, record_key in (("artifact_id", "plan_artifact_id"), ("revision", "plan_revision"), ("experiment_id", "experiment_id"), ("product_id", "product_id"), ("tenant_id", "tenant_id"), ("owner", "owner"), ("stop_rule", "stop_rule")):
        if plan[plan_key] != record[record_key]:
            raise ValueError("execution differs from frozen plan: " + record_key)
    policy = record.get("frozen_policy")
    if not isinstance(policy, dict):
        raise ValueError("frozen_policy is required")
    if policy != plan.get("frozen_policy"):
        raise ValueError("execution policy differs from frozen plan")
    for key in ("assignment_unit", "control_variant_id", "treatment_variant_id", "primary_metric", "guardrail_metric"):
        need(policy.get(key), key)
    if policy["assignment_unit"] != "account" or policy["control_variant_id"] == policy["treatment_variant_id"]:
        raise ValueError("assignment and variants are invalid")
    if number(policy.get("control_allocation_bps"), "control_allocation_bps") + number(policy.get("treatment_allocation_bps"), "treatment_allocation_bps") != 10000:
        raise ValueError("allocation must total 100%")
    number(policy.get("minimum_sample_per_variant"), "minimum_sample_per_variant", positive=True)
    threshold = number(policy.get("guardrail_max_bps"), "guardrail_max_bps")
    if threshold > 10000:
        raise ValueError("guardrail threshold exceeds 100%")
    exposure_minimum = number(policy.get("minimum_exposure_coverage_bps"), "minimum_exposure_coverage_bps", positive=True)
    tolerance = number(policy.get("allocation_tolerance_bps"), "allocation_tolerance_bps")
    if exposure_minimum > 10000 or tolerance > 10000:
        raise ValueError("exposure or allocation policy exceeds 100%")
    window_start = instant(policy.get("readout_start"), "readout_start")
    exposure_end = instant(policy.get("exposure_end"), "exposure_end")
    window_end = instant(policy.get("readout_end"), "readout_end")
    if exposure_end < window_start or window_end < exposure_end + timedelta(days=30):
        raise ValueError("day-30 readout must wait 30 days after final exposure")
    state = record.get("state")
    decision = record.get("approval")
    provider = record.get("provider")
    if state == "pending_approval":
        if decision is not None or provider is not None:
            raise ValueError("pending approval cannot claim decision or launch")
        return
    if state != "launched" or not isinstance(decision, dict) or not isinstance(provider, dict):
        raise ValueError("execution must be pending_approval or provider-confirmed launched")
    if decision.get("state") != "approved" or decision.get("plan_revision") != record["plan_revision"]:
        raise ValueError("approval must bind the exact plan revision")
    decision_id = need(decision.get("id"), "approval id")
    approved_at = instant(decision.get("approved_at"), "approved_at")
    if "approval:" + decision_id not in sources.values() or approved_at > as_of:
        raise ValueError("dated owner approval source is required")
    for key in ("object_id", "revision", "launch_receipt_id", "exposure_source_id"):
        need(provider.get(key), "provider " + key)
    launched_at = instant(provider.get("launched_at"), "launched_at")
    if launched_at < approved_at or launched_at > as_of or window_start < launched_at:
        raise ValueError("launch and readout timestamps are inconsistent")
    if "provider:" + provider["launch_receipt_id"] not in sources.values() or "provider:" + provider["exposure_source_id"] not in sources.values():
        raise ValueError("provider launch and exposure receipts required")


def validate_readout(plan, execution, readout):
    validate_execution(plan, execution)
    if execution["state"] != "launched":
        raise ValueError("pending execution blocks outcome readout")
    if not isinstance(readout, dict) or readout.get("artifact_type") != "experiment-outcome-readout/v1":
        raise ValueError("expected experiment-outcome-readout/v1")
    for key in ("artifact_id", "owner", "next_check"):
        need(readout.get(key), key)
    for key in ("experiment_id", "product_id", "tenant_id"):
        if readout.get(key) != execution[key]:
            raise ValueError("readout " + key + " differs from execution")
    if readout.get("source_execution_artifact_id") != execution["artifact_id"]:
        raise ValueError("readout needs exact execution artifact")
    policy = execution["frozen_policy"]
    if readout.get("primary_metric") != policy["primary_metric"] or readout.get("guardrail_metric") != policy["guardrail_metric"]:
        raise ValueError("readout changed frozen metric")
    as_of = instant(readout.get("as_of"), "as_of")
    if as_of < instant(execution["as_of"], "execution as_of"):
        raise ValueError("readout precedes execution")
    sources = refs(readout.get("source_refs"), as_of)
    if "artifact:" + execution["artifact_id"] not in sources.values():
        raise ValueError("readout needs exact execution source")
    state = readout.get("state")
    if readout.get("winner") is not None or readout.get("shipping_state") != "not_shipped":
        raise ValueError("readout cannot claim a winner or shipping action")
    if state == "pending_window":
        if as_of >= instant(policy["readout_end"], "readout_end") or readout.get("arms") is not None or readout.get("observed_change_bps") is not None or readout.get("verdict") != "pending":
            raise ValueError("pending window cannot claim an outcome")
        return
    if as_of < instant(policy["readout_end"], "readout_end"):
        raise ValueError("outcome window is immature")
    if state not in ("inconclusive", "measured") or readout.get("verdict") not in ("inconclusive", "owner_review_required"):
        raise ValueError("outcome state or verdict is invalid")
    for prefix in ("exposure:", "analytics:", "guardrail:"):
        relevant = [item for item in readout["source_refs"] if item["uri"].startswith(prefix)]
        if not relevant or not any(instant(item["observed_at"], "outcome source time") >= instant(policy["readout_end"], "readout_end") for item in relevant):
            raise ValueError("final exposure, primary and guardrail evidence must follow the full window")
    arms = readout.get("arms")
    if not isinstance(arms, list) or len(arms) != 2 or [arm.get("variant_id") for arm in arms] != [policy["control_variant_id"], policy["treatment_variant_id"]]:
        raise ValueError("exact control and treatment arms required")
    for arm in arms:
        eligible = number(arm.get("eligible_accounts"), "eligible_accounts", positive=True)
        exposed = number(arm.get("exposed_accounts"), "exposed_accounts")
        retained = number(arm.get("primary_success_accounts"), "primary_success_accounts")
        guardrail = number(arm.get("guardrail_event_accounts"), "guardrail_event_accounts")
        if exposed > eligible or retained > exposed or guardrail > exposed:
            raise ValueError("arm counts are inconsistent")
        if number(arm.get("primary_rate_bps"), "primary_rate_bps") != rate(retained, eligible) or number(arm.get("guardrail_rate_bps"), "guardrail_rate_bps") != rate(guardrail, eligible):
            raise ValueError("arm rate arithmetic does not reconcile")
    change = arms[1]["primary_rate_bps"] - arms[0]["primary_rate_bps"]
    if readout.get("observed_change_bps") != change:
        raise ValueError("observed change does not reconcile")
    enough = all(arm["eligible_accounts"] >= policy["minimum_sample_per_variant"] for arm in arms)
    coverage_ok = all(rate(arm["exposed_accounts"], arm["eligible_accounts"]) >= policy["minimum_exposure_coverage_bps"] for arm in arms)
    control_share = rate(arms[0]["eligible_accounts"], arms[0]["eligible_accounts"] + arms[1]["eligible_accounts"])
    allocation_ok = abs(control_share - policy["control_allocation_bps"]) <= policy["allocation_tolerance_bps"]
    guardrail_ok = all(arm["guardrail_rate_bps"] <= policy["guardrail_max_bps"] for arm in arms)
    if not enough or not coverage_ok or not allocation_ok or not guardrail_ok:
        if state != "inconclusive" or readout["verdict"] != "inconclusive":
            raise ValueError("sample, exposure, allocation or guardrail gate requires inconclusive")
    elif state != "measured" or readout["verdict"] != "owner_review_required":
        raise ValueError("complete readout requires owner review")
    if not isinstance(readout.get("limitations"), list) or not readout["limitations"]:
        raise ValueError("limitations are required")


def main(argv):
    if not argv or argv[0] not in ("execution", "readout") or len(argv) != (3 if argv[0] == "execution" else 4):
        raise SystemExit("usage: validate_handoff.py execution <plan.json> <record.json> | readout <plan.json> <record.json> <readout.json>")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        (validate_execution if argv[0] == "execution" else validate_readout)(*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid experiment handoff: {exc}") from exc
    print("valid experiment " + argv[0] + " contract")


if __name__ == "__main__":
    main(sys.argv[1:])
