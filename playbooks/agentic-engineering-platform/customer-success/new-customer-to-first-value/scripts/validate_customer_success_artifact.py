#!/usr/bin/env python3
"""Validate Customer Success handoffs; source truth still needs human review."""

from __future__ import annotations

import argparse
import json
import sys
from datetime import datetime
from pathlib import Path


class InvalidArtifact(ValueError):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise InvalidArtifact(message)


def obj(value: object, label: str) -> dict:
    require(isinstance(value, dict), f"{label} must be an object")
    return value


def text(value: object, label: str) -> str:
    require(isinstance(value, str) and bool(value.strip()), f"{label} must be non-empty text")
    return value.strip()


def items(value: object, label: str, *, allow_empty: bool = False) -> list:
    require(isinstance(value, list) and (allow_empty or bool(value)), f"{label} must be a {'list' if allow_empty else 'non-empty list'}")
    return value


def timestamp(value: object, label: str) -> datetime:
    try:
        parsed = datetime.fromisoformat(text(value, label).replace("Z", "+00:00"))
    except ValueError as exc:
        raise InvalidArtifact(f"{label} must be an ISO timestamp") from exc
    require(parsed.tzinfo is not None, f"{label} needs a timezone")
    return parsed


def base(raw: object, kind: str) -> dict:
    doc = obj(raw, kind)
    require(doc.get("schema_version") == 1, f"{kind}.schema_version must be 1")
    require(doc.get("artifact_type") == f"{kind}/v1", f"{kind}.artifact_type is wrong")
    timestamp(doc.get("created_at"), f"{kind}.created_at")
    return doc


def references(raw: object, label: str) -> set[str]:
    ids: set[str] = set()
    for index, value in enumerate(items(raw, label)):
        ref = obj(value, f"{label}[{index}]")
        ref_id = text(ref.get("id"), f"{label}[{index}].id")
        require(ref_id not in ids, f"duplicate reference {ref_id}")
        ids.add(ref_id)
        text(ref.get("uri"), f"{label}[{index}].uri")
        timestamp(ref.get("observed_at"), f"{label}[{index}].observed_at")
    return ids


def known_refs(raw: object, available: set[str], label: str, *, allow_empty: bool = False) -> None:
    for index, value in enumerate(items(raw, label, allow_empty=allow_empty)):
        require(text(value, f"{label}[{index}]") in available, f"{label} cites an unknown source")


def validate_onboarding(raw: object, accepted_handoff: object | None = None) -> dict:
    register = base(raw, "onboarding-milestone-register")
    for field in ("register_id", "account_id", "tenant_id", "owner", "handoff_ref"):
        text(register.get(field), field)
    refs = references(register.get("source_refs"), "source_refs")
    require(register["handoff_ref"] in refs, "handoff_ref cites an unknown source")
    rule = obj(register.get("first_value_rule"), "first_value_rule")
    for field in ("id", "description", "evidence_event"):
        text(rule.get(field), f"first_value_rule.{field}")
    timestamp(rule.get("target_at"), "first_value_rule.target_at")
    milestone_ids: set[str] = set()
    for index, value in enumerate(items(register.get("milestones"), "milestones")):
        label = f"milestones[{index}]"
        milestone = obj(value, label)
        milestone_id = text(milestone.get("id"), f"{label}.id")
        require(milestone_id not in milestone_ids, f"duplicate milestone {milestone_id}")
        milestone_ids.add(milestone_id)
        for field in ("title", "owner"):
            text(milestone.get(field), f"{label}.{field}")
        timestamp(milestone.get("due_at"), f"{label}.due_at")
        state = milestone.get("state")
        require(state in {"pending", "blocked", "complete", "unknown"}, f"{label}.state is invalid")
        known_refs(milestone.get("evidence_refs"), refs, f"{label}.evidence_refs", allow_empty=state != "complete")
        if state == "blocked":
            text(milestone.get("blocker"), f"{label}.blocker")
    items(register.get("limitations"), "limitations", allow_empty=True)
    if accepted_handoff is not None:
        acceptance = obj(accepted_handoff, "accepted_handoff")
        require(acceptance.get("artifact_type") == "onboarding-acceptance/v1" and acceptance.get("decision") == "accepted", "signed-deal handoff needs accepted CS decision")
        artifact_id = text(acceptance.get("artifact_id"), "accepted_handoff.artifact_id")
        receipt = obj(acceptance.get("owner_acceptance"), "accepted_handoff.owner_acceptance")
        require(receipt.get("owner_id") == acceptance.get("receiver_owner_id") and receipt.get("handoff_key") == acceptance.get("handoff_key") and receipt.get("contract_revision") == acceptance.get("contract_revision") and receipt.get("decision") == "accepted", "signed-deal handoff lacks exact receiving-owner receipt")
        timestamp(receipt.get("decided_at"), "accepted_handoff.owner_acceptance.decided_at")
        require(register["account_id"] == acceptance.get("customer_account_id") and register["tenant_id"] == acceptance.get("tenant_id"), "onboarding account differs from accepted handoff")
        require(register.get("purchased_scope") == acceptance.get("accepted_scope"), "onboarding purchased scope differs from accepted handoff")
        require(register.get("first_value_goal") == acceptance.get("first_value_goal"), "onboarding first-value goal differs from accepted handoff")
        require(rule["id"] == acceptance.get("first_value_rule") and rule["target_at"] == acceptance.get("target_at"), "onboarding first-value rule differs from accepted handoff")
        handoff_source = next((ref for ref in register["source_refs"] if ref["id"] == register["handoff_ref"]), None)
        require(handoff_source is not None and artifact_id in handoff_source["uri"], "onboarding source does not cite exact accepted handoff")
    return register


def validate_adoption(raw: object, onboarding_raw: object) -> dict:
    readout = base(raw, "first-value-readout")
    register = validate_onboarding(onboarding_raw)
    text(readout.get("readout_id"), "readout_id")
    require(readout.get("source_register_id") == register["register_id"], "adoption cites a different onboarding register")
    for field in ("account_id", "tenant_id"):
        require(readout.get(field) == register[field], f"adoption {field} differs from onboarding")
    rule = register["first_value_rule"]
    require(readout.get("first_value_rule_id") == rule["id"], "adoption first-value rule differs from onboarding")
    require(readout.get("evidence_event") == rule["evidence_event"], "adoption evidence event differs from onboarding")
    start = timestamp(readout.get("window_start"), "window_start")
    end = timestamp(readout.get("window_end"), "window_end")
    require(start <= end, "adoption window is reversed")
    coverage = readout.get("source_coverage")
    require(coverage in {"complete", "partial", "unavailable"}, "source_coverage is invalid")
    status = readout.get("status")
    require(status in {"reached", "not_observed", "unknown"}, "first-value status is invalid")
    refs = references(readout.get("source_refs"), "adoption.source_refs")
    require(text(readout.get("coverage_ref"), "coverage_ref") in refs, "adoption coverage_ref cites an unknown source")
    if coverage != "complete":
        text(readout.get("coverage_gap"), "coverage_gap")
    events = items(readout.get("observed_events"), "observed_events", allow_empty=True)
    event_ids: set[str] = set()
    for index, value in enumerate(events):
        label = f"observed_events[{index}]"
        event = obj(value, label)
        event_id = text(event.get("id"), f"{label}.id")
        require(event_id not in event_ids, f"duplicate event {event_id}")
        event_ids.add(event_id)
        require(event.get("name") == rule["evidence_event"], f"{label} is not the agreed first-value event")
        require(event.get("account_id") == register["account_id"] and event.get("tenant_id") == register["tenant_id"], f"{label} belongs to another account")
        occurred = timestamp(event.get("occurred_at"), f"{label}.occurred_at")
        require(start <= occurred <= end, f"{label} is outside the observation window")
        require(text(event.get("source_ref"), f"{label}.source_ref") in refs, f"{label} cites an unknown source")
    if status == "reached":
        require(bool(events) and coverage != "unavailable", "reached requires observed first-value evidence")
    elif status == "not_observed":
        require(not events and coverage == "complete", "not_observed requires complete source coverage and no event")
    else:
        require(not events and coverage != "complete", "unknown requires incomplete source coverage and no event")
    known_refs(readout.get("evidence_refs"), refs, "adoption.evidence_refs", allow_empty=status != "reached")
    valid_milestones = {m["id"] for m in register["milestones"]}
    for index, milestone_id in enumerate(items(readout.get("milestone_ids"), "milestone_ids")):
        require(text(milestone_id, f"milestone_ids[{index}]") in valid_milestones, "adoption cites an unknown milestone")
    items(readout.get("limitations"), "limitations", allow_empty=True)
    return readout


def validate_health(raw: object, onboarding_raw: object, adoption_raw: object) -> None:
    brief = base(raw, "customer-health-brief")
    readout = validate_adoption(adoption_raw, onboarding_raw)
    text(brief.get("brief_id"), "brief_id")
    require(brief.get("source_readout_id") == readout["readout_id"], "health cites a different first-value readout")
    for field in ("account_id", "tenant_id"):
        require(brief.get(field) == readout[field], f"health {field} differs from adoption")
    for field in ("owner", "next_action"):
        text(brief.get(field), field)
    require(brief.get("status") in {"watch", "review", "action_proposed"}, "health status is invalid")
    refs = references(brief.get("source_refs"), "health.source_refs")
    require(any(readout["readout_id"] in ref["uri"] for ref in brief["source_refs"]), "health lacks a source reference to its first-value readout")
    signal_ids: set[str] = set()
    for index, value in enumerate(items(brief.get("signals"), "signals")):
        label = f"signals[{index}]"
        signal = obj(value, label)
        signal_id = text(signal.get("id"), f"{label}.id")
        require(signal_id not in signal_ids, f"duplicate signal {signal_id}")
        signal_ids.add(signal_id)
        text(signal.get("summary"), f"{label}.summary")
        require(signal.get("kind") in {"observed", "hypothesis"}, f"{label}.kind is invalid")
        known_refs(signal.get("evidence_refs"), refs, f"{label}.evidence_refs")
    items(brief.get("limitations"), "limitations", allow_empty=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("onboarding", "adoption", "health"))
    parser.add_argument("artifact", type=Path)
    parser.add_argument("--onboarding", type=Path)
    parser.add_argument("--adoption", type=Path)
    parser.add_argument("--accepted-handoff", type=Path, help="Validated onboarding-acceptance/v1 for the signed-deal route")
    args = parser.parse_args()
    if args.kind in {"adoption", "health"} and args.onboarding is None:
        parser.error(f"{args.kind} validation requires --onboarding")
    if args.kind == "health" and args.adoption is None:
        parser.error("health validation requires --adoption")
    if args.kind == "onboarding" and (args.onboarding or args.adoption):
        parser.error("onboarding validation accepts no handoff files")
    if args.accepted_handoff and args.kind != "onboarding":
        parser.error("--accepted-handoff applies to onboarding validation only")
    try:
        artifact = json.loads(args.artifact.read_text())
        onboarding = json.loads(args.onboarding.read_text()) if args.onboarding else None
        adoption = json.loads(args.adoption.read_text()) if args.adoption else None
        accepted_handoff = json.loads(args.accepted_handoff.read_text()) if args.accepted_handoff else None
        if args.kind == "onboarding":
            validate_onboarding(artifact, accepted_handoff)
        elif args.kind == "adoption":
            validate_adoption(artifact, onboarding)
        else:
            validate_health(artifact, onboarding, adoption)
    except (OSError, json.JSONDecodeError, InvalidArtifact) as exc:
        print(f"INVALID {args.kind}: {exc}", file=sys.stderr)
        return 1
    print(f"VALID {args.kind}: {args.artifact}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
