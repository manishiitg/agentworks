#!/usr/bin/env python3
"""Validate GTM launch and source joins; Sales qualification uses its own validator."""

import json
import sys
from datetime import datetime
from pathlib import Path


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be nonempty text")
    return value


def timestamp(value, label):
    nonempty(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} must contain source IDs")
    for index, item in enumerate(value):
        nonempty(item, f"{label}[{index}]")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} must be unique")
    return set(value)


def validate_brief(brief):
    if not isinstance(brief, dict) or brief.get("artifact_type") != "gtm-launch-brief/v1":
        raise ValueError("invalid GTM launch brief type")
    for field in ("launch_id", "offer_version", "market", "audience", "buyer_problem", "currency", "qualified_lead_definition", "owner_id"):
        nonempty(brief.get(field), f"brief.{field}")
    if len(brief["currency"]) != 3 or not brief["currency"].isalpha():
        raise ValueError("brief.currency must be a three-letter code")
    if type(brief.get("budget_minor")) is not int or brief["budget_minor"] < 0:
        raise ValueError("brief.budget_minor must be a nonnegative integer")
    timestamp(brief.get("observed_at"), "brief.observed_at")
    sources = refs(brief.get("source_refs"), "brief.source_refs")
    claims = refs(brief.get("claim_refs"), "brief.claim_refs")
    if not claims <= sources:
        raise ValueError("brief claim refs must point to sources")
    refs(brief.get("channels"), "brief.channels")
    if brief.get("baseline_state") not in ("measured", "baseline_first"):
        raise ValueError("brief.baseline_state is invalid")
    if brief.get("approval_state") != "approved":
        raise ValueError("launch brief needs owner approval before distribution")
    if nonempty(brief.get("approval_ref"), "brief.approval_ref") not in sources:
        raise ValueError("brief approval must cite a source")


def validate_register(brief, register):
    validate_brief(brief)
    if not isinstance(register, dict) or register.get("artifact_type") != "launch-signal-register/v1":
        raise ValueError("invalid launch signal register type")
    for field in ("launch_id", "offer_version", "market", "campaign_id", "asset_id", "owner_id"):
        nonempty(register.get(field), f"register.{field}")
    for field in ("launch_id", "offer_version", "market"):
        if register[field] != brief[field]:
            raise ValueError(f"handoff {field} mismatch")
    if timestamp(register.get("observed_at"), "register.observed_at") < timestamp(brief["observed_at"], "brief.observed_at"):
        raise ValueError("register predates approved brief")
    sources = refs(register.get("source_refs"), "register.source_refs")
    if register.get("asset_state") not in ("prepared", "approved", "published", "verified"):
        raise ValueError("register.asset_state is invalid")
    if register["asset_state"] in ("published", "verified"):
        if nonempty(register.get("publication_receipt_ref"), "register.publication_receipt_ref") not in sources:
            raise ValueError("published asset needs a cited provider receipt")
    events = register.get("events")
    if not isinstance(events, list):
        raise ValueError("register.events must be a list")
    seen = set()
    for index, event in enumerate(events):
        label = f"register.events[{index}]"
        if not isinstance(event, dict):
            raise ValueError(f"{label} must be an object")
        for field in ("event_id", "lead_id", "lead_source_ref", "source_ref"):
            nonempty(event.get(field), f"{label}.{field}")
        if event["event_id"] in seen:
            raise ValueError("duplicate event ID in register")
        seen.add(event["event_id"])
        if event["source_ref"] not in sources or event["lead_source_ref"] not in sources:
            raise ValueError("event source refs must be in register")
        if event.get("dedup_state") not in ("none", "possible", "duplicate"):
            raise ValueError("event dedup_state is invalid")
        if event.get("contact_policy_state") not in ("review_required", "approved", "blocked", "unknown"):
            raise ValueError("event contact_policy_state is invalid")
        if event.get("attribution_state") not in ("source_linked", "unknown"):
            raise ValueError("event attribution_state is invalid")
        if event["attribution_state"] == "source_linked":
            if event.get("campaign_id") != register["campaign_id"] or register["asset_state"] not in ("published", "verified"):
                raise ValueError("source-linked event needs matching campaign and published asset")
        elif event.get("campaign_id") not in (None, register["campaign_id"]):
            raise ValueError("unknown attribution cannot cite another campaign")


def validate_lead(brief, register, qualification):
    validate_register(brief, register)
    if not isinstance(qualification, dict) or qualification.get("artifact_type") != "lead-qualification-brief/v1":
        raise ValueError("invalid Sales qualification artifact type")
    lead_id = nonempty(qualification.get("lead_id"), "qualification.lead_id")
    matches = [event for event in register["events"] if event["lead_id"] == lead_id and event["dedup_state"] == "none"]
    if len(matches) != 1:
        raise ValueError("qualification needs one nonduplicate source event")
    event = matches[0]
    if event["contact_policy_state"] in ("blocked", "unknown"):
        raise ValueError("lead contact policy is blocked or unknown")
    if qualification.get("duplicate_state") != "none" or qualification.get("contact_policy_status") == "blocked":
        raise ValueError("Sales qualification is duplicate or contact blocked")
    if qualification.get("fit_status") != "qualified":
        raise ValueError("pipeline handoff needs a qualified lead")
    if qualification.get("contact_ref") != event["lead_source_ref"]:
        raise ValueError("qualification contact differs from source lead")
    source_refs = qualification.get("source_refs")
    if not isinstance(source_refs, list) or not any(isinstance(ref, dict) and ref.get("uri") == event["lead_source_ref"] for ref in source_refs):
        raise ValueError("qualification lacks matching lead source reference")
    if timestamp(qualification.get("created_at"), "qualification.created_at") < timestamp(register["observed_at"], "register.observed_at"):
        raise ValueError("qualification predates launch signal observation")


if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    expected = {"brief": 3, "signals": 4, "lead": 5}
    if mode not in expected or len(sys.argv) != expected[mode]:
        raise SystemExit("usage: validate_handoff.py brief BRIEF | signals BRIEF REGISTER | lead BRIEF REGISTER QUALIFICATION")
    try:
        documents = [json.loads(Path(name).read_text()) for name in sys.argv[2:]]
        if mode == "brief":
            validate_brief(*documents)
        elif mode == "signals":
            validate_register(*documents)
        else:
            validate_lead(*documents)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid {mode} handoff: {exc}") from exc
    print(f"valid {mode} handoff")
