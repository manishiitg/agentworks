#!/usr/bin/env python3
"""Check FinOps Crew artifacts; real source records and approvals still need review."""

import json
import sys
from datetime import date, datetime
from pathlib import Path


IDENTITY = ("provider", "account_id", "service_id", "resource_id", "environment", "currency")


def required(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def minor(value, label, *, positive=False):
    if isinstance(value, bool) or not isinstance(value, int) or value < (1 if positive else 0):
        raise ValueError(f"{label} must be a {'positive' if positive else 'nonnegative'} minor-unit integer")
    return value


def signed_minor(value, label):
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{label} must be a signed minor-unit integer")
    return value


def instant(value, label):
    required(value, label)
    try:
        result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if result.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return result


def day(value, label):
    required(value, label)
    try:
        return date.fromisoformat(value)
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO date") from exc


def dated_sources(value, observed):
    if not isinstance(value, list) or not value:
        raise ValueError("source_refs need dated source objects")
    found = {}
    for source in value:
        if not isinstance(source, dict):
            raise ValueError("source_ref must be an object")
        source_id = required(source.get("id"), "source id")
        required(source.get("uri"), "source uri")
        if source_id in found:
            raise ValueError("source IDs must be unique")
        if instant(source.get("observed_at"), "source observed_at") > observed:
            raise ValueError("source postdates artifact")
        found[source_id] = source
    return found


def citations(value, sources, label):
    if not isinstance(value, list) or not value or any(not isinstance(ref, str) for ref in value) or len(value) != len(set(value)):
        raise ValueError(f"{label} needs unique source IDs")
    if any(ref not in sources for ref in value):
        raise ValueError(f"{label} cites a missing source")


def header(value, kind):
    if not isinstance(value, dict) or value.get("artifact_type") != kind:
        raise ValueError(f"expected {kind}")
    for field in IDENTITY:
        required(value.get(field), field)
    if len(value["currency"]) != 3 or value["currency"].upper() != value["currency"]:
        raise ValueError("currency must be an uppercase ISO code")
    observed = instant(value.get("observed_at"), "observed_at")
    return observed, dated_sources(value.get("source_refs"), observed)


def match_identity(upstream, downstream, label):
    for field in IDENTITY:
        if upstream[field] != downstream[field]:
            raise ValueError(f"{label} {field} mismatch")


def artifact_citation(sources, artifact_id, label):
    if not any(source["uri"] == "artifact:" + artifact_id for source in sources.values()):
        raise ValueError(f"{label} lacks exact upstream artifact citation")


def validate_cost(review):
    observed, sources = header(review, "cloud-cost-review/v1")
    required(review.get("review_id"), "review_id")
    required(review.get("billing_basis"), "billing_basis")
    required(review.get("allocation_rule"), "allocation_rule")
    baseline = (day(review.get("baseline_start"), "baseline_start"), day(review.get("baseline_end"), "baseline_end"))
    current = (day(review.get("current_start"), "current_start"), day(review.get("current_end"), "current_end"))
    if baseline[0] > baseline[1] or current[0] > current[1] or baseline[1] >= current[0] or (baseline[1] - baseline[0]) != (current[1] - current[0]):
        raise ValueError("cost periods must be ordered and equally long")
    if current[1] > observed.date():
        raise ValueError("cost period cannot end after observation")
    cost = review.get("service_cost")
    candidate = review.get("candidate")
    if not isinstance(cost, dict) or not isinstance(candidate, dict):
        raise ValueError("cost and candidate objects are required")
    fields = ("baseline_usage_units", "current_usage_units", "baseline_unit_price_minor", "current_unit_price_minor", "baseline_cost_minor", "current_usage_cost_minor", "one_time_charge_minor", "current_billed_minor")
    for field in fields:
        minor(cost.get(field), field)
    for field in ("usage_effect_minor", "price_effect_minor", "total_change_minor"):
        signed_minor(cost.get(field), field)
    if cost["baseline_usage_units"] * cost["baseline_unit_price_minor"] != cost["baseline_cost_minor"] or cost["current_usage_units"] * cost["current_unit_price_minor"] != cost["current_usage_cost_minor"]:
        raise ValueError("usage cost does not match source units and price")
    if cost["current_usage_cost_minor"] + cost["one_time_charge_minor"] != cost["current_billed_minor"]:
        raise ValueError("current billed total does not reconcile")
    if (cost["current_usage_units"] - cost["baseline_usage_units"]) * cost["baseline_unit_price_minor"] != cost["usage_effect_minor"]:
        raise ValueError("usage effect does not reconcile")
    if cost["current_usage_units"] * (cost["current_unit_price_minor"] - cost["baseline_unit_price_minor"]) != cost["price_effect_minor"]:
        raise ValueError("price effect does not reconcile")
    if cost["usage_effect_minor"] + cost["price_effect_minor"] + cost["one_time_charge_minor"] != cost["total_change_minor"] or cost["current_billed_minor"] - cost["baseline_cost_minor"] != cost["total_change_minor"]:
        raise ValueError("cost change decomposition does not reconcile")
    citations(cost.get("evidence_refs"), sources, "cost evidence")
    required(candidate.get("candidate_id"), "candidate_id")
    required(candidate.get("owner"), "candidate owner")
    required(candidate.get("next_check"), "candidate next_check")
    minor(candidate.get("resource_baseline_cost_minor"), "resource_baseline_cost_minor", positive=True)
    minimum = minor(candidate.get("projected_min_savings_minor"), "projected_min_savings_minor")
    maximum = minor(candidate.get("projected_max_savings_minor"), "projected_max_savings_minor")
    if minimum > maximum or maximum > candidate["resource_baseline_cost_minor"]:
        raise ValueError("candidate saving range exceeds the resource baseline")
    if candidate.get("risk_state") != "needs_peak_and_dependency_review" or candidate.get("action_state") != "proposal":
        raise ValueError("cost candidate must remain a risk-checked proposal")
    citations(candidate.get("evidence_refs"), sources, "candidate evidence")
    if not isinstance(review.get("limitations"), list) or not review["limitations"]:
        raise ValueError("cost limitations must be listed")
    return observed


def validate_change(review, change):
    cost_observed = validate_cost(review)
    observed, sources = header(change, "cloud-change-review/v1")
    match_identity(review, change, "change")
    if observed < cost_observed:
        raise ValueError("change review predates cost review")
    required(change.get("change_id"), "change_id")
    if change.get("source_review_id") != review["review_id"] or change.get("candidate_id") != review["candidate"]["candidate_id"]:
        raise ValueError("change cites a different cost review or candidate")
    artifact_citation(sources, review["review_id"], "change")
    required(change.get("owner"), "change owner")
    required(change.get("next_action"), "change next_action")
    state = change.get("change_state")
    if state in ("proposal", "rejected"):
        expected_approval = "pending_owner_review" if state == "proposal" else "rejected"
        if change.get("approval_state") != expected_approval or any(change.get(field) is not None for field in ("approval_ref", "risk_review_ref", "deployment_receipt_ref", "health_evidence_ref")) or change.get("health_state") != "not_tested":
            raise ValueError("unapproved change cannot claim deployment or health")
    elif state == "deployed":
        if change.get("approval_state") != "approved" or change.get("health_state") != "pass":
            raise ValueError("deployed change needs approval and health pass")
        for field in ("approval_ref", "risk_review_ref", "plan_ref", "deployment_receipt_ref", "health_evidence_ref"):
            ref = required(change.get(field), field)
            if ref not in sources:
                raise ValueError(f"{field} lacks source receipt")
        if len({change["approval_ref"], change["risk_review_ref"], change["plan_ref"], change["deployment_receipt_ref"], change["health_evidence_ref"]}) != 5:
            raise ValueError("risk, approval, plan, deployment and health need distinct evidence")
    else:
        raise ValueError("change_state must be proposal, rejected or deployed")
    return observed


def validate_savings(review, change, readout):
    change_observed = validate_change(review, change)
    observed, sources = header(readout, "cloud-savings-readout/v1")
    match_identity(review, readout, "savings")
    if observed < change_observed:
        raise ValueError("savings readout predates change review")
    required(readout.get("readout_id"), "readout_id")
    required(readout.get("owner"), "finance owner")
    required(readout.get("next_action"), "savings next_action")
    if readout.get("source_review_id") != review["review_id"] or readout.get("source_change_id") != change["change_id"] or readout.get("candidate_id") != review["candidate"]["candidate_id"]:
        raise ValueError("savings cites a different review, change or candidate")
    artifact_citation(sources, review["review_id"], "savings")
    artifact_citation(sources, change["change_id"], "savings")
    for field in ("billing_basis", "allocation_rule"):
        if readout.get(field) != review[field]:
            raise ValueError(f"savings {field} mismatch")
    state = readout.get("outcome_state")
    if state == "pending_change":
        if change["change_state"] == "deployed" or readout.get("verified_savings_minor") is not None or readout.get("verification") is not None:
            raise ValueError("pending change cannot claim billed savings")
        return
    if state == "pending_verification":
        if change["change_state"] != "deployed" or readout.get("verified_savings_minor") is not None or readout.get("verification") is not None:
            raise ValueError("pending verification cannot claim billed savings")
        return
    if state != "verified" or change["change_state"] != "deployed":
        raise ValueError("verified savings require an approved deployed change")
    verification = readout.get("verification")
    if not isinstance(verification, dict):
        raise ValueError("verified savings need comparable billing evidence")
    baseline = (day(verification.get("baseline_start"), "verification.baseline_start"), day(verification.get("baseline_end"), "verification.baseline_end"))
    post = (day(verification.get("post_start"), "verification.post_start"), day(verification.get("post_end"), "verification.post_end"))
    if baseline != (day(review["current_start"], "current_start"), day(review["current_end"], "current_end")) or post[0] <= change_observed.date() or post[0] > post[1] or (baseline[1] - baseline[0]) != (post[1] - post[0]) or post[1] > observed.date():
        raise ValueError("savings need complete comparable periods after deployment")
    baseline_cost = minor(verification.get("baseline_resource_cost_minor"), "baseline_resource_cost_minor", positive=True)
    post_cost = minor(verification.get("post_resource_cost_minor"), "post_resource_cost_minor")
    if baseline_cost != review["candidate"]["resource_baseline_cost_minor"] or post_cost > baseline_cost:
        raise ValueError("resource billing basis changed or no positive saving observed")
    if minor(readout.get("verified_savings_minor"), "verified_savings_minor") != baseline_cost - post_cost:
        raise ValueError("verified saving does not match resource bills")
    baseline_work = minor(verification.get("baseline_work_units"), "baseline_work_units", positive=True)
    post_work = minor(verification.get("post_work_units"), "post_work_units", positive=True)
    if baseline_work != post_work:
        raise ValueError("workload is not comparable")
    required(verification.get("work_unit"), "work_unit")
    if verification.get("credit_effect_minor") != 0 or verification.get("price_or_commitment_change_minor") != 0:
        raise ValueError("credits or price changes cannot be counted as rightsizing savings")
    citations(verification.get("billing_source_refs"), sources, "billing evidence")
    if verification.get("workload_source_ref") not in sources or verification.get("health_state") != "pass":
        raise ValueError("verified savings need workload and health evidence")


def main(argv):
    modes = {"cost": (1, validate_cost), "change": (2, validate_change), "savings": (3, validate_savings)}
    if not argv or argv[0] not in modes or len(argv) != modes[argv[0]][0] + 1:
        raise SystemExit("usage: validate_handoff.py cost|change|savings artifact.json ...")
    try:
        artifacts = [json.loads(Path(file).read_text()) for file in argv[1:]]
        modes[argv[0]][1](*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid FinOps handoff: {exc}") from exc
    print(f"valid FinOps {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
