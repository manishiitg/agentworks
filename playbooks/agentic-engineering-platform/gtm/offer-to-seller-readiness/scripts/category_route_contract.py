"""Source-bound contracts for the Product, GTM and Support category routes.

These checks validate artifact shape and handoff claims. Builder still has to
read the customer's current source records and obtain the named owner review.
"""

from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path


ROUTES = {
    "opportunity-to-reviewed-requirements": ("product_id", "opportunity_id"),
    "approved-requirement-to-release-readiness": ("product_id", "requirement_id"),
    "offer-to-seller-readiness": ("offer_id", "plan_id", "price_revision"),
    "launch-signal-to-pipeline-review": ("offer_id", "launch_id", "campaign_id"),
    "case-pattern-to-knowledge-review": ("product_id", "question_key"),
}


def require_text(value: object, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def timestamp(value: object, label: str) -> datetime:
    raw = require_text(value, label)
    try:
        parsed = datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def unique_refs(value: object, label: str, minimum: int = 1) -> list[str]:
    if not isinstance(value, list) or len(value) < minimum:
        raise ValueError(f"{label} needs at least {minimum} source IDs")
    refs = [require_text(ref, label) for ref in value]
    if len(refs) != len(set(refs)):
        raise ValueError(f"{label} contains duplicate IDs")
    return refs


def validate(package: Path, artifacts: list[dict]) -> None:
    manifest = json.loads((package / "playbook.json").read_text())
    route_id = manifest["id"]
    identity = ROUTES[route_id]
    slots = manifest["agent_slots"]
    if len(artifacts) != len(slots):
        raise ValueError("one artifact is required for every Crew slot")
    previous = None
    for index, (slot, artifact) in enumerate(zip(slots, artifacts)):
        label = slot["id"]
        if not isinstance(artifact, dict) or artifact.get("artifact_type") != slot["output"]:
            raise ValueError(f"{label} has the wrong artifact type")
        if artifact.get("producer_id") != slot["agent_playbook_id"]:
            raise ValueError(f"{label} has the wrong Crew producer")
        for field in ("artifact_id", "tenant_id", "subject_id", "source_revision", *identity):
            require_text(artifact.get(field), f"{label}.{field}")
        unique_refs(artifact.get("source_refs"), f"{label}.source_refs")
        observed = timestamp(artifact.get("observed_at"), f"{label}.observed_at")
        if artifact.get("state") in ("accepted", "approved"):
            require_text(artifact.get("approval_ref"), f"{label}.approval_ref")
        if artifact.get("action_state") == "executed":
            require_text(artifact.get("provider_receipt"), f"{label}.provider_receipt")
        if previous is not None:
            for field in ("tenant_id", "subject_id", *identity):
                if artifact[field] != previous[field]:
                    raise ValueError(f"{label} {field} does not match upstream")
            if artifact.get("input_artifact_id") != previous["artifact_id"]:
                raise ValueError(f"{label} does not cite the exact upstream artifact")
            if artifact.get("input_revision") != previous["source_revision"]:
                raise ValueError(f"{label} does not cite the upstream revision")
            if observed < timestamp(previous["observed_at"], "upstream.observed_at"):
                raise ValueError(f"{label} predates its upstream source")
        previous = artifact

    first, second = artifacts[:2]
    if route_id == "opportunity-to-reviewed-requirements":
        third = artifacts[2]
        if first.get("state") != "reviewed" or not first.get("counterexample_ref"):
            raise ValueError("discovery needs reviewed evidence and a counterexample")
        if second.get("state") != "accepted" or not second.get("strategy_goal_ref"):
            raise ValueError("priority needs exact owner acceptance and strategy goal")
        if third.get("state") != "pending_review" or not third.get("acceptance_criteria") or third.get("delivery_write_state") != "none":
            raise ValueError("requirements must remain a reviewable draft with criteria and no delivery write")
    elif route_id == "approved-requirement-to-release-readiness":
        if first.get("state") != "approved" or not first.get("acceptance_criteria"):
            raise ValueError("release needs approved, testable requirements")
        if second.get("decision") == "go":
            for field in ("qa_gate_ref", "help_revision", "measurement_rule", "rollout_approval_ref"):
                require_text(second.get(field), f"release.{field}")
            if second.get("qa_gate_state") != "pass" or second.get("release_state") != "verified":
                raise ValueError("go requires passing QA and verified release state")
        elif second.get("decision") == "no_go":
            if not second.get("blockers"):
                raise ValueError("no-go needs named blockers")
        else:
            raise ValueError("release decision must be go or no_go")
    elif route_id == "offer-to-seller-readiness":
        if first.get("state") != "accepted" or not first.get("entitlement_revision") or not first.get("unit_economics_ref"):
            raise ValueError("seller enablement needs an accepted priced offer and cost evidence")
        claims = unique_refs(second.get("claim_refs"), "enablement.claim_refs")
        if not set(claims).issubset(second["source_refs"]) or second.get("distribution_state") != "unpublished":
            raise ValueError("enablement claims need current sources and unpublished review state")
    elif route_id == "launch-signal-to-pipeline-review":
        if first.get("state") != "observed" or not first.get("provider_receipt"):
            raise ValueError("launch signal needs observed provider evidence")
        event_ids = unique_refs(first.get("event_ids"), "launch.event_ids")
        counts = second.get("counts")
        if not isinstance(counts, dict) or any(type(counts.get(k)) is not int or counts[k] < 0 for k in ("submitted", "unique", "matched", "accepted", "opportunities")):
            raise ValueError("pipeline counts must be nonnegative integers")
        if not (counts["opportunities"] <= counts["accepted"] <= counts["matched"] <= counts["unique"] <= counts["submitted"]):
            raise ValueError("pipeline counts violate ordered stage bounds")
        if counts["unique"] != len(event_ids) or (counts["matched"] < counts["unique"] and second.get("attribution_state") != "partial"):
            raise ValueError("pipeline event counts or attribution coverage are inconsistent")
        if counts["opportunities"] and not second.get("opportunity_refs"):
            raise ValueError("pipeline opportunity claim needs source IDs")
    elif route_id == "case-pattern-to-knowledge-review":
        case_ids = unique_refs(first.get("case_ids"), "triage.case_ids", 2)
        if first.get("state") != "reviewed" or not first.get("priority_policy_ref"):
            raise ValueError("case pattern needs reviewed triage and priority policy")
        if second.get("case_ids") != case_ids or not second.get("article_revision"):
            raise ValueError("knowledge draft needs the same cases and current article revision")
        if second.get("publication_state") != "unpublished" or second.get("deflection_state") != "unknown":
            raise ValueError("draft cannot claim publication or measured deflection")
