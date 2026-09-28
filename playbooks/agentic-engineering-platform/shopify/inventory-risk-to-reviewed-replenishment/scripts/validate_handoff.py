#!/usr/bin/env python3
"""Validate supplier reorder math and state evidence; merchant must verify source truth."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("store_id", "market", "product_id", "variant_id", "inventory_item_id", "location_id", "case_id")
PROOFS = ("inventory_ref", "demand_ref", "incoming_ref", "open_po_check_ref", "supplier_terms_ref", "budget_ref")
CALCULATED = ("target_qty", "shortage_qty", "proposed_qty", "total_cost_minor")


def text(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def timestamp(value, label):
    text(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def integer(value, label, minimum=0):
    if type(value) is not int or value < minimum:
        raise ValueError(f"{label} must be an integer >= {minimum}")


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} needs source references")
    for index, ref in enumerate(value):
        text(ref, f"{label}[{index}]")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} must be unique")


def choice(record, key, values):
    if record.get(key) not in values:
        raise ValueError(f"{key} must be one of {', '.join(sorted(values))}")


def validate_signal(signal):
    if not isinstance(signal, dict) or signal.get("artifact_type") != "inventory-availability-exception/v1":
        raise ValueError("invalid inventory signal type")
    for key in (*IDENTITY, "storefront_state", "inventory_authority_ref", "owner_id", "proposed_action"):
        text(signal.get(key), f"signal.{key}")
    timestamp(signal.get("observed_at"), "signal.observed_at")
    integer(signal.get("available_qty"), "signal.available_qty", minimum=-10**12)
    integer(signal.get("committed_qty"), "signal.committed_qty")
    refs(signal.get("source_refs"), "signal.source_refs")


def validate(signal, review):
    validate_signal(signal)
    if not isinstance(review, dict) or review.get("artifact_type") != "replenishment-review/v1":
        raise ValueError("invalid replenishment review type")
    for key in (*IDENTITY, "supplier_id", "supplier_sku", "currency", "terms_version", "demand_window_id", "owner_id", "next_evidence"):
        text(review.get(key), f"review.{key}")
    for key in IDENTITY:
        if review[key] != signal[key]:
            raise ValueError(f"handoff {key} mismatch")
    if review["currency"] != review["currency"].upper() or len(review["currency"]) != 3 or not review["currency"].isalpha():
        raise ValueError("currency needs three uppercase letters")
    observed = timestamp(review.get("observed_at"), "review.observed_at")
    if observed < timestamp(signal["observed_at"], "signal.observed_at"):
        raise ValueError("review predates inventory signal")
    if review.get("available_qty") != signal["available_qty"] or type(review.get("available_qty")) is not int:
        raise ValueError("review.available_qty must match inventory signal")
    refs(review.get("source_refs"), "review.source_refs")
    choice(review, "decision", {"needs_information", "defer", "reorder_review"})
    choice(review, "open_po_state", {"none_verified", "accounted_in_incoming", "unknown", "unaccounted"})
    choice(review, "budget_state", {"approved", "needs_review", "unknown"})
    choice(review, "approval_state", {"pending", "approved", "rejected"})
    choice(review, "po_state", {"not_created", "draft", "ordered"})
    choice(review, "stock_state", {"not_received", "received"})

    if review["decision"] == "needs_information":
        for key in CALCULATED:
            if review.get(key) is not None:
                raise ValueError(f"review.{key} must be null when inputs are missing")
        if review["po_state"] != "not_created" or review["stock_state"] != "not_received":
            raise ValueError("missing-information review cannot claim PO or receipt")
    else:
        for key in ("supplier_quote_currency", "budget_currency"):
            text(review.get(key), f"review.{key}")
            if review[key] != review["currency"]:
                raise ValueError(f"review.{key} must match review.currency")
        for key in ("demand_units", "confirmed_incoming_qty", "backorder_qty", "lead_time_days", "review_period_days", "safety_stock_qty", "min_order_qty", "unit_cost_minor", *CALCULATED):
            integer(review.get(key), f"review.{key}")
        integer(review.get("open_po_incoming_qty"), "review.open_po_incoming_qty")
        if review["open_po_state"] not in ("none_verified", "accounted_in_incoming"):
            raise ValueError("unresolved open PO requires needs_information")
        if review["open_po_state"] == "none_verified" and review["open_po_incoming_qty"] != 0:
            raise ValueError("no open PO must have zero open_po_incoming_qty")
        if review["open_po_incoming_qty"] > review["confirmed_incoming_qty"]:
            raise ValueError("open PO incoming cannot exceed confirmed incoming")
        integer(review.get("demand_window_days"), "review.demand_window_days", 1)
        integer(review.get("case_pack_qty"), "review.case_pack_qty", 1)
        for key in PROOFS:
            text(review.get(key), f"review.{key}")
            if review[key] not in review["source_refs"]:
                raise ValueError(f"review.{key} must appear in source_refs")
        if len({review[key] for key in PROOFS}) != len(PROOFS):
            raise ValueError("replenishment proof references must be distinct")
        horizon = review["lead_time_days"] + review["review_period_days"]
        target = (review["demand_units"] * horizon + review["demand_window_days"] - 1) // review["demand_window_days"] + review["safety_stock_qty"]
        shortage = max(0, target - review["available_qty"] - review["confirmed_incoming_qty"] + review["backorder_qty"])
        if review["target_qty"] != target:
            raise ValueError("target_qty formula mismatch")
        if review["shortage_qty"] != shortage:
            raise ValueError("shortage_qty formula mismatch")
        if shortage == 0:
            if review["decision"] != "defer" or review["proposed_qty"] != 0:
                raise ValueError("zero shortage must defer with zero proposed quantity")
        else:
            rounded = (max(shortage, review["min_order_qty"]) + review["case_pack_qty"] - 1) // review["case_pack_qty"] * review["case_pack_qty"]
            if review["decision"] != "reorder_review" or review["proposed_qty"] != rounded:
                raise ValueError("proposed_qty must meet shortage, MOQ and case pack")
        if review["total_cost_minor"] != review["proposed_qty"] * review["unit_cost_minor"]:
            raise ValueError("total_cost_minor formula mismatch")

    if review["approval_state"] == "approved":
        text(review.get("approval_ref"), "review.approval_ref")
    if review["po_state"] != "not_created":
        if review["decision"] != "reorder_review" or review["approval_state"] != "approved" or review["budget_state"] != "approved":
            raise ValueError("PO action needs approved reorder and budget")
        text(review.get("pre_action_recheck_ref"), "review.pre_action_recheck_ref")
        text(review.get("po_ref"), "review.po_ref")
    if review["po_state"] == "ordered":
        text(review.get("supplier_confirmation_ref"), "review.supplier_confirmation_ref")
    if review["stock_state"] == "received":
        if review["po_state"] != "ordered":
            raise ValueError("received stock needs an ordered PO")
        text(review.get("transfer_receipt_ref"), "review.transfer_receipt_ref")
        text(review.get("inventory_verification_ref"), "review.inventory_verification_ref")
        if timestamp(review.get("received_observed_at"), "review.received_observed_at") < observed:
            raise ValueError("receipt observation predates review")


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
    print("valid inventory signal" if signal_only else "valid replenishment review")
