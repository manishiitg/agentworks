#!/usr/bin/env python3
"""Check a Shopify order-to-resolution artifact handoff; merchant review still applies."""

import json
import sys
from pathlib import Path


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} must contain source IDs")
    for index, item in enumerate(value):
        nonempty(item, f"{label}[{index}]")


def validate_order(order):
    if not isinstance(order, dict) or order.get("artifact_type") != "store-order-exception/v1":
        raise ValueError("invalid order exception artifact type")
    for field in ("store_id", "order_id", "customer_ref", "case_id", "currency", "observed_at", "policy_version", "owner_id", "order_state", "payment_state", "fulfillment_state", "affected_line_fulfillment_state", "customer_request_ref", "return_state", "refund_state", "proposed_action"):
        nonempty(order.get(field), f"order.{field}")
    if order["affected_line_fulfillment_state"] not in ("unfulfilled", "partially_fulfilled", "fulfilled", "unknown"):
        raise ValueError("order.affected_line_fulfillment_state is invalid")
    if "shopify_return_ref" not in order:
        raise ValueError("order.shopify_return_ref must be present, even when null")
    if order.get("shopify_return_ref") is not None:
        nonempty(order["shopify_return_ref"], "order.shopify_return_ref")
        if order["affected_line_fulfillment_state"] == "unfulfilled":
            raise ValueError("unfulfilled line cannot have a Shopify Return")
    if len(order["currency"]) != 3 or not order["currency"].isalpha():
        raise ValueError("order.currency must be a three-letter code")
    refs(order.get("line_item_ids"), "order.line_item_ids")
    if len(set(order["line_item_ids"])) != len(order["line_item_ids"]):
        raise ValueError("order.line_item_ids must be unique")
    refs(order.get("source_refs"), "order.source_refs")


def validate(order, review):
    validate_order(order)
    if not isinstance(review, dict) or review.get("artifact_type") != "return-resolution-review/v1":
        raise ValueError("invalid return resolution artifact type")
    for field in ("store_id", "order_id", "customer_ref", "case_id", "currency", "owner_id", "policy_version", "decision", "resolution_route", "approval_state", "resolution_state", "message_state", "unsent_customer_draft", "next_evidence"):
        nonempty(review.get(field), f"review.{field}")
    for field in ("store_id", "order_id", "customer_ref", "case_id", "currency", "policy_version"):
        if order[field] != review[field]:
            raise ValueError(f"handoff {field} mismatch")
    refs(review.get("source_refs"), "review.source_refs")
    if review["decision"] not in ("refund_review", "return_review", "decline_review", "needs_information"):
        raise ValueError("review.decision is invalid")
    if review["resolution_route"] not in ("refund_or_order_edit_review", "shopify_return_review", "decline_or_information"):
        raise ValueError("review.resolution_route is invalid")
    if review["resolution_route"] == "shopify_return_review" and order["affected_line_fulfillment_state"] != "fulfilled":
        raise ValueError("Shopify Return route requires a fulfilled affected line")
    if review["decision"] == "return_review" and review["resolution_route"] != "shopify_return_review":
        raise ValueError("return_review requires Shopify Return route")
    if review["decision"] == "refund_review" and review["resolution_route"] != "refund_or_order_edit_review":
        raise ValueError("refund_review requires refund or order-edit route")
    if review["approval_state"] not in ("pending", "approved", "rejected"):
        raise ValueError("review.approval_state is invalid")
    if review["resolution_state"] not in ("prepared", "executed", "verified"):
        raise ValueError("review.resolution_state is invalid")
    if review["message_state"] not in ("unsent", "sent"):
        raise ValueError("review.message_state is invalid")
    if review["approval_state"] == "approved":
        nonempty(review.get("approval_ref"), "review.approval_ref")
    if review["resolution_state"] in ("executed", "verified") and review["approval_state"] != "approved":
        raise ValueError("executed resolution needs owner approval")
    captured = review.get("captured_amount_minor")
    refunded = review.get("already_refunded_minor")
    proposed = review.get("proposed_refund_minor")
    if type(captured) is not int or captured < 0 or type(refunded) is not int or refunded < 0 or refunded > captured:
        raise ValueError("captured and prior refund amounts must be valid nonnegative minor units")
    if proposed is not None:
        if type(proposed) is not int or proposed < 0 or proposed > captured - refunded:
            raise ValueError("proposed refund exceeds verified remaining captured amount")
        if proposed > 0 and review["decision"] != "refund_review":
            raise ValueError("positive proposed refund needs refund_review decision")
    if review["decision"] == "refund_review" and review["resolution_state"] in ("executed", "verified"):
        if not proposed:
            raise ValueError("executed refund needs a verified proposed amount")
        nonempty(review.get("refund_receipt_ref"), "review.refund_receipt_ref")
    if review["decision"] == "return_review" and review["resolution_state"] in ("executed", "verified"):
        nonempty(review.get("return_receipt_ref"), "review.return_receipt_ref")
    if review["message_state"] == "sent":
        nonempty(review.get("message_receipt_ref"), "review.message_receipt_ref")
    if review["decision"] in ("decline_review", "needs_information") and review["resolution_state"] in ("executed", "verified") and review["message_state"] != "sent":
        raise ValueError("customer decision route needs a sent message receipt")
    if review["resolution_state"] == "verified":
        nonempty(review.get("verification_ref"), "review.verification_ref")


if __name__ == "__main__":
    order_only = len(sys.argv) == 3 and sys.argv[1] == "--order-only"
    if len(sys.argv) != 3:
        raise SystemExit("usage: validate_handoff.py [--order-only] order.json [review.json]")
    try:
        if order_only:
            validate_order(json.loads(Path(sys.argv[2]).read_text()))
        else:
            validate(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print("valid order exception artifact" if order_only else "valid order-to-resolution handoff")
