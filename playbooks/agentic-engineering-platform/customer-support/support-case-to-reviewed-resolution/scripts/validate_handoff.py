#!/usr/bin/env python3
"""Validate support artifact contracts. Provider truth still requires source reads."""

import hashlib
import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("tenant_id", "case_id", "account_id")


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


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
        raise ValueError(f"{label} needs at least one source ID")
    for index, ref in enumerate(value):
        nonempty(ref, f"{label}[{index}]")
    if len(value) != len(set(value)):
        raise ValueError(f"{label} must be unique")


def artifact(value, kind, fields):
    if not isinstance(value, dict) or value.get("artifact_type") != kind:
        raise ValueError(f"expected {kind}")
    for field in (*IDENTITY, *fields):
        nonempty(value.get(field), f"{kind}.{field}")
    refs(value.get("source_refs"), f"{kind}.source_refs")
    return timestamp(value.get("observed_at"), f"{kind}.observed_at")


def joined(left, right, fields=IDENTITY):
    for field in fields:
        if left.get(field) != right.get(field):
            raise ValueError(f"handoff {field} mismatch")


def message_fingerprint(reply):
    payload = {
        field: reply[field]
        for field in (
            "tenant_id", "case_id", "account_id", "thread_revision",
            "recipient_id", "channel", "message",
        )
    }
    canonical = json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return "sha256:" + hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def validate_triage(triage):
    artifact(triage, "support-case-triage/v1", (
        "thread_revision", "channel", "priority", "priority_policy_version",
        "duplicate_state", "response_deadline_at", "owner_id", "symptom", "recommended_action",
    ))
    if timestamp(triage["response_deadline_at"], "triage.response_deadline_at") <= timestamp(triage["observed_at"], "triage.observed_at"):
        raise ValueError("response deadline must follow triage observation")
    if triage.get("incident_state") not in ("unverified", "confirmed", "not_applicable"):
        raise ValueError("incident_state is invalid")
    if triage["incident_state"] == "confirmed" and not triage.get("incident_ref"):
        raise ValueError("confirmed incident needs incident_ref")


def validate_reply(triage, reply):
    validate_triage(triage)
    reply_time = artifact(reply, "support-reply-draft/v1", (
        "thread_revision", "draft_id", "recipient_id", "channel", "message",
    ))
    joined(triage, reply, (*IDENTITY, "thread_revision"))
    if reply_time < timestamp(triage["observed_at"], "triage.observed_at"):
        raise ValueError("reply predates triage")
    refs(reply.get("claim_refs"), "reply.claim_refs")
    if not set(reply["claim_refs"]).issubset(set(reply["source_refs"])):
        raise ValueError("reply claim lacks source evidence")
    if reply.get("delivery_state") != "unsent":
        raise ValueError("reply artifact must remain unsent")
    if reply.get("approval_state") not in ("pending", "approved", "rejected"):
        raise ValueError("reply approval_state is invalid")
    if reply["approval_state"] == "approved":
        nonempty(reply.get("approval_ref"), "reply.approval_ref")
        if reply.get("approved_message_fingerprint") != message_fingerprint(reply):
            raise ValueError("approved reply fingerprint does not bind exact message and recipient")


def validate_escalation(triage, escalation):
    validate_triage(triage)
    observed = artifact(escalation, "support-escalation-brief/v1", (
        "thread_revision", "receiving_owner_id", "impact", "deadline_at",
    ))
    joined(triage, escalation, (*IDENTITY, "thread_revision"))
    if observed < timestamp(triage["observed_at"], "triage.observed_at"):
        raise ValueError("escalation predates triage")
    if timestamp(escalation["deadline_at"], "escalation.deadline_at") <= observed:
        raise ValueError("escalation deadline must follow observation")
    if escalation.get("acceptance_state") not in ("pending", "accepted", "rejected"):
        raise ValueError("escalation acceptance_state is invalid")
    if escalation["acceptance_state"] == "accepted":
        nonempty(escalation.get("acceptance_ref"), "escalation.acceptance_ref")


def validate_delivery(triage, reply, delivery):
    validate_reply(triage, reply)
    if not isinstance(delivery, dict) or delivery.get("artifact_type") != "support-delivery-receipt/v1":
        raise ValueError("expected support-delivery-receipt/v1")
    joined(reply, delivery, (*IDENTITY, "thread_revision", "draft_id", "recipient_id", "channel"))
    for field in ("approval_ref", "message_fingerprint", "provider_message_id"):
        nonempty(delivery.get(field), f"delivery.{field}")
    if reply["approval_state"] != "approved":
        raise ValueError("delivery needs exact approved draft")
    if delivery["approval_ref"] != reply.get("approval_ref"):
        raise ValueError("delivery approval reference mismatch")
    if delivery["message_fingerprint"] != message_fingerprint(reply):
        raise ValueError("delivery message fingerprint mismatch")
    refs(delivery.get("source_refs"), "delivery.source_refs")
    if timestamp(delivery.get("delivered_at"), "delivery.delivered_at") < timestamp(reply["observed_at"], "reply.observed_at"):
        raise ValueError("delivery predates draft")


def validate_outcome(triage, reply, delivery, outcome):
    validate_delivery(triage, reply, delivery)
    observed = artifact(outcome, "support-case-outcome/v1", ("status", "status_revision", "resolution_basis"))
    joined(triage, outcome)
    if observed < timestamp(delivery["delivered_at"], "delivery.delivered_at"):
        raise ValueError("case outcome predates delivery")
    if outcome["status"] == "resolved":
        if outcome["resolution_basis"] != "provider_delivery":
            raise ValueError("resolved case requires supported resolution basis")
        if outcome.get("provider_message_id") != delivery["provider_message_id"]:
            raise ValueError("resolution delivery reference mismatch")
    elif outcome["status"] not in ("open", "pending", "reopened"):
        raise ValueError("case outcome status is invalid")


def main(argv):
    modes = {
        "triage": (1, validate_triage),
        "reply": (2, validate_reply),
        "escalation": (2, validate_escalation),
        "delivery": (3, validate_delivery),
        "outcome": (4, validate_outcome),
    }
    if len(argv) < 2 or argv[0] not in modes or len(argv) != modes[argv[0]][0] + 1:
        raise SystemExit("usage: validate_handoff.py triage|reply|escalation|delivery|outcome artifact.json ...")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        modes[argv[0]][1](*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print(f"valid support {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
