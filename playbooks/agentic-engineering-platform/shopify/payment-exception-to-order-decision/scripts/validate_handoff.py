#!/usr/bin/env python3
"""Validate payment-to-order artifact joins and action gates; not gateway truth."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("store_id", "order_id", "case_id", "transaction_id", "currency")


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} must contain source IDs")
    for index, item in enumerate(value):
        nonempty(item, f"{label}[{index}]")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} must be unique")


def timestamp(value, label):
    nonempty(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def validate_payment(payment):
    if not isinstance(payment, dict) or payment.get("artifact_type") != "payment-exception/v1":
        raise ValueError("invalid payment artifact type")
    for field in (*IDENTITY, "transaction_kind", "transaction_status", "payment_policy_version", "owner_id", "proposed_action"):
        nonempty(payment.get(field), f"payment.{field}")
    if len(payment["currency"]) != 3 or not payment["currency"].isalpha():
        raise ValueError("payment.currency must be a three-letter code")
    if type(payment.get("amount_minor")) is not int or payment["amount_minor"] < 0:
        raise ValueError("payment.amount_minor must be nonnegative integer minor units")
    if payment.get("parent_transaction_ref") is not None:
        nonempty(payment["parent_transaction_ref"], "payment.parent_transaction_ref")
    timestamp(payment.get("observed_at"), "payment.observed_at")
    refs(payment.get("source_refs"), "payment.source_refs")


def validate(payment, decision):
    validate_payment(payment)
    if not isinstance(decision, dict) or decision.get("artifact_type") != "payment-order-decision/v1":
        raise ValueError("invalid order decision artifact type")
    for field in (*IDENTITY, "order_payment_state", "fulfillment_state", "owner_id", "next_evidence"):
        nonempty(decision.get(field), f"decision.{field}")
    for field in IDENTITY:
        if decision[field] != payment[field]:
            raise ValueError(f"handoff {field} mismatch")
    if timestamp(decision.get("observed_at"), "decision.observed_at") < timestamp(payment["observed_at"], "payment.observed_at"):
        raise ValueError("order decision predates payment observation")
    refs(decision.get("fulfillment_order_ids"), "decision.fulfillment_order_ids")
    refs(decision.get("source_refs"), "decision.source_refs")
    if decision.get("decision") not in ("hold", "release_review", "needs_information"):
        raise ValueError("decision.decision is invalid")
    if decision.get("approval_state") not in ("pending", "approved", "rejected"):
        raise ValueError("decision.approval_state is invalid")
    if decision.get("action_state") not in ("prepared", "executed", "verified"):
        raise ValueError("decision.action_state is invalid")
    if decision["decision"] == "release_review" and (payment["transaction_kind"] not in ("CAPTURE", "SALE") or payment["transaction_status"] != "SUCCESS"):
        raise ValueError("release review needs successful CAPTURE or SALE transaction")
    if decision["approval_state"] == "approved":
        nonempty(decision.get("approval_ref"), "decision.approval_ref")
    if decision["action_state"] in ("executed", "verified"):
        if decision["decision"] != "release_review" or decision["approval_state"] != "approved":
            raise ValueError("executed release needs approved release review")
        nonempty(decision.get("action_receipt_ref"), "decision.action_receipt_ref")
    if decision["action_state"] == "verified":
        nonempty(decision.get("verification_ref"), "decision.verification_ref")


if __name__ == "__main__":
    payment_only = len(sys.argv) == 3 and sys.argv[1] == "--payment-only"
    if len(sys.argv) != 3:
        raise SystemExit("usage: validate_handoff.py [--payment-only] payment.json [decision.json]")
    try:
        if payment_only:
            validate_payment(json.loads(Path(sys.argv[2]).read_text()))
        else:
            validate(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print("valid payment exception" if payment_only else "valid payment-to-order handoff")
