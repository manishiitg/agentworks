#!/usr/bin/env python3
"""Validate checkout recovery artifact structure and decision gates, not source truth."""

import json
import re
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("store_id", "market", "checkout_id", "campaign_id", "channel")
PROTECTED_KEYS = {"email", "phone", "address", "recovery_url", "abandoned_checkout_url"}
ELIGIBILITY_PROOFS = (
    "checkout_state_ref", "order_lookup_ref", "consent_ref",
    "policy_ref", "suppression_ref", "message_history_ref",
)


def required_text(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def optional_ref(record, key):
    if record.get(key) is not None:
        required_text(record[key], key)


def timestamp(value, label):
    required_text(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def source_refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} needs source references")
    for index, ref in enumerate(value):
        required_text(ref, f"{label}[{index}]")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} must be unique")


def no_contact_data(record, label):
    if any(key.lower() in PROTECTED_KEYS for key in record):
        raise ValueError(f"{label} must not include customer contact details or recovery URL")
    refs = record.get("source_refs") if isinstance(record.get("source_refs"), list) else []
    for text in (record.get("hypothesis"), record.get("draft_text"), *refs):
        if isinstance(text, str) and re.search(r"https?://|[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}", text, re.I):
            raise ValueError(f"{label} must not include a contact address or recovery URL")


def choice(record, key, allowed):
    if record.get(key) not in allowed:
        raise ValueError(f"{key} must be one of {', '.join(sorted(allowed))}")


def validate_signal(signal):
    if not isinstance(signal, dict) or signal.get("artifact_type") != "checkout-recovery-signal/v1":
        raise ValueError("invalid signal artifact type")
    no_contact_data(signal, "signal")
    for key in (*IDENTITY, "hypothesis", "owner_id"):
        required_text(signal.get(key), f"signal.{key}")
    choice(signal, "channel", {"email", "sms"})
    choice(signal, "signal_kind", {"abandoned_checkout"})
    timestamp(signal.get("observed_at"), "signal.observed_at")
    source_refs(signal.get("source_refs"), "signal.source_refs")


def validate(signal, review):
    validate_signal(signal)
    if not isinstance(review, dict) or review.get("artifact_type") != "checkout-recovery-review/v1":
        raise ValueError("invalid review artifact type")
    no_contact_data(review, "review")
    for key in (*IDENTITY, "policy_version", "owner_id", "next_evidence"):
        required_text(review.get(key), f"review.{key}")
    for key in IDENTITY:
        if review[key] != signal[key]:
            raise ValueError(f"handoff {key} mismatch")
    observed = timestamp(review.get("observed_at"), "review.observed_at")
    if observed < timestamp(signal["observed_at"], "signal.observed_at"):
        raise ValueError("review predates signal")
    completed = review.get("checkout_completed_at")
    if completed is not None:
        timestamp(completed, "review.checkout_completed_at")
    source_refs(review.get("source_refs"), "review.source_refs")
    choice(review, "later_order_state", {"none_verified", "linked_order_observed", "unknown"})
    choice(review, "consent_state", {"subscribed", "not_subscribed", "unknown"})
    choice(review, "contact_policy_state", {"allowed", "blocked", "unknown"})
    choice(review, "prior_send_state", {"none_verified", "sent_or_scheduled", "unknown"})
    choice(review, "suppression_state", {"clear", "blocked", "unknown"})
    choice(review, "decision", {"suppress", "needs_information", "draft_review"})
    choice(review, "approval_state", {"pending", "approved", "rejected"})
    choice(review, "send_state", {"not_sent", "sent"})
    choice(review, "recovery_state", {"not_observed", "linked_order_observed"})
    for key in (*ELIGIBILITY_PROOFS, "approval_ref", "pre_send_recheck_ref", "provider_send_ref", "linked_order_ref", "checkout_order_link_ref"):
        optional_ref(review, key)
    for key in ("send_observed_at", "recovery_observed_at"):
        if review.get(key) is not None:
            timestamp(review[key], f"review.{key}")

    eligible = (
        completed is None
        and review["later_order_state"] == "none_verified"
        and review["consent_state"] == "subscribed"
        and review["contact_policy_state"] == "allowed"
        and review["prior_send_state"] == "none_verified"
        and review["suppression_state"] == "clear"
    )
    blocked = (
        completed is not None
        or review["later_order_state"] == "linked_order_observed"
        or review["consent_state"] == "not_subscribed"
        or review["contact_policy_state"] == "blocked"
        or review["prior_send_state"] == "sent_or_scheduled"
        or review["suppression_state"] == "blocked"
    )
    if review["decision"] == "draft_review":
        if not eligible:
            raise ValueError("draft review needs current consent, order, policy and suppression clearance")
        for key in ELIGIBILITY_PROOFS:
            required_text(review.get(key), f"review.{key}")
            if review[key] not in review["source_refs"]:
                raise ValueError(f"review.{key} must appear in source_refs")
        if len({review[key] for key in ELIGIBILITY_PROOFS}) != len(ELIGIBILITY_PROOFS):
            raise ValueError("eligibility proof references must be distinct")
        required_text(review.get("draft_text"), "review.draft_text")
        if review["approval_state"] == "rejected":
            raise ValueError("rejected draft cannot remain in draft review")
    elif review.get("draft_text") is not None:
        raise ValueError("suppressed or unknown case must not contain a draft")
    if review["decision"] == "suppress" and not blocked:
        raise ValueError("suppression needs an observed blocker")
    if review["decision"] == "needs_information" and (blocked or eligible):
        raise ValueError("needs_information requires unresolved evidence without a known blocker")
    if review["approval_state"] == "approved":
        required_text(review.get("approval_ref"), "review.approval_ref")
    if review["send_state"] == "sent":
        if review["decision"] != "draft_review" or review["approval_state"] != "approved":
            raise ValueError("sent state needs an approved eligible draft")
        required_text(review.get("pre_send_recheck_ref"), "review.pre_send_recheck_ref")
        required_text(review.get("provider_send_ref"), "review.provider_send_ref")
        if timestamp(review.get("send_observed_at"), "review.send_observed_at") < observed:
            raise ValueError("send observation predates eligibility review")
    elif review.get("provider_send_ref") is not None:
        raise ValueError("not_sent cannot have a provider send receipt")
    elif review.get("send_observed_at") is not None:
        raise ValueError("not_sent cannot have a send observation")
    if review["later_order_state"] == "linked_order_observed":
        required_text(review.get("linked_order_ref"), "review.linked_order_ref")
        required_text(review.get("checkout_order_link_ref"), "review.checkout_order_link_ref")
    if review["recovery_state"] == "linked_order_observed":
        required_text(review.get("linked_order_ref"), "review.linked_order_ref")
        required_text(review.get("checkout_order_link_ref"), "review.checkout_order_link_ref")
        if timestamp(review.get("recovery_observed_at"), "review.recovery_observed_at") < observed:
            raise ValueError("linked order observation predates eligibility review")
    elif review.get("recovery_observed_at") is not None:
        raise ValueError("not_observed cannot have a linked order observation")


if __name__ == "__main__":
    signal_only = len(sys.argv) == 3 and sys.argv[1] == "--signal-only"
    if len(sys.argv) != 3:
        raise SystemExit("usage: validate_handoff.py [--signal-only] signal.json [review.json]")
    try:
        if signal_only:
            validate_signal(json.loads(Path(sys.argv[2]).read_text()))
        else:
            validate(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print("valid checkout signal" if signal_only else "valid checkout recovery review")
