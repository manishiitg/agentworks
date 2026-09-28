#!/usr/bin/env python3
"""Validate a location-aware inventory handoff; source truth still needs merchant review."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("store_id", "market", "product_id", "variant_id", "inventory_item_id", "location_id", "case_id")


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
        raise ValueError(f"{label} must include a timezone")
    return parsed


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} must contain source IDs")
    for index, item in enumerate(value):
        nonempty(item, f"{label}[{index}]")


def validate_exception(item):
    if not isinstance(item, dict) or item.get("artifact_type") != "inventory-availability-exception/v1":
        raise ValueError("invalid inventory exception artifact type")
    for field in (*IDENTITY, "storefront_state", "inventory_authority_ref", "owner_id", "proposed_action"):
        nonempty(item.get(field), f"exception.{field}")
    timestamp(item.get("observed_at"), "exception.observed_at")
    if type(item.get("available_qty")) is not int:
        raise ValueError("exception.available_qty must be an integer; negative is allowed")
    if type(item.get("committed_qty")) is not int or item["committed_qty"] < 0:
        raise ValueError("exception.committed_qty must be a nonnegative integer")
    refs(item.get("source_refs"), "exception.source_refs")


def validate(exception, review):
    validate_exception(exception)
    if not isinstance(review, dict) or review.get("artifact_type") != "inventory-action-review/v1":
        raise ValueError("invalid inventory action artifact type")
    for field in (*IDENTITY, "proposed_action", "owner_id", "next_evidence"):
        nonempty(review.get(field), f"review.{field}")
    for field in IDENTITY:
        if review[field] != exception[field]:
            raise ValueError(f"handoff {field} mismatch")
    if timestamp(review.get("observed_at"), "review.observed_at") < timestamp(exception["observed_at"], "exception.observed_at"):
        raise ValueError("review predates inventory observation")
    refs(review.get("source_refs"), "review.source_refs")
    if review.get("decision") not in ("reconcile_sync", "replenishment_review", "storefront_update_review", "needs_information"):
        raise ValueError("review.decision is invalid")
    if review.get("approval_state") not in ("pending", "approved", "rejected"):
        raise ValueError("review.approval_state is invalid")
    if review.get("action_state") not in ("prepared", "executed", "verified"):
        raise ValueError("review.action_state is invalid")
    if review["approval_state"] == "approved":
        nonempty(review.get("approval_ref"), "review.approval_ref")
    if review["action_state"] in ("executed", "verified"):
        if review["approval_state"] != "approved" or review["decision"] == "needs_information":
            raise ValueError("executed action needs an approved decision")
        nonempty(review.get("action_receipt_ref"), "review.action_receipt_ref")
    if review["action_state"] == "verified":
        nonempty(review.get("verification_ref"), "review.verification_ref")


if __name__ == "__main__":
    exception_only = len(sys.argv) == 3 and sys.argv[1] == "--exception-only"
    if len(sys.argv) != 3:
        raise SystemExit("usage: validate_handoff.py [--exception-only] exception.json [review.json]")
    try:
        if exception_only:
            validate_exception(json.loads(Path(sys.argv[2]).read_text()))
        else:
            validate(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print("valid inventory exception" if exception_only else "valid inventory action handoff")
