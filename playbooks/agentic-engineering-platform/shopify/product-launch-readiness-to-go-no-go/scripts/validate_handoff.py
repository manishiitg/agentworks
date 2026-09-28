#!/usr/bin/env python3
"""Validate a bounded Shopify product-launch preflight, not live store truth."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("store_id", "market", "launch_id", "product_id", "variant_id", "publication_id", "currency")


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


def refs(value, label, allow_empty=False):
    if not isinstance(value, list) or (not value and not allow_empty):
        raise ValueError(f"{label} must be a list of source IDs")
    for index, item in enumerate(value):
        nonempty(item, f"{label}[{index}]")


def validate_catalog(catalog):
    if not isinstance(catalog, dict) or catalog.get("artifact_type") != "launch-catalog-readiness/v1":
        raise ValueError("invalid launch catalog artifact type")
    for field in (*IDENTITY, "inventory_policy", "publication_state", "product_state", "owner_id"):
        nonempty(catalog.get(field), f"catalog.{field}")
    if len(catalog["currency"]) != 3 or not catalog["currency"].isalpha():
        raise ValueError("catalog.currency must be a three-letter code")
    if type(catalog.get("price_minor")) is not int or catalog["price_minor"] < 0:
        raise ValueError("catalog.price_minor must be nonnegative integer minor units")
    if type(catalog.get("available_qty")) is not int:
        raise ValueError("catalog.available_qty must be an integer")
    if type(catalog.get("media_ready")) is not bool or type(catalog.get("ready_for_review")) is not bool:
        raise ValueError("catalog readiness flags must be booleans")
    timestamp(catalog.get("observed_at"), "catalog.observed_at")
    refs(catalog.get("blockers"), "catalog.blockers", allow_empty=True)
    refs(catalog.get("source_refs"), "catalog.source_refs")
    if catalog["ready_for_review"] and (catalog["blockers"] or not catalog["media_ready"]):
        raise ValueError("catalog ready_for_review conflicts with blockers or media")


def validate(catalog, decision):
    validate_catalog(catalog)
    if not isinstance(decision, dict) or decision.get("artifact_type") != "launch-storefront-decision/v1":
        raise ValueError("invalid launch decision artifact type")
    for field in (*IDENTITY, "page_url", "buyer_task", "owner_id", "next_evidence"):
        nonempty(decision.get(field), f"decision.{field}")
    for field in IDENTITY:
        if catalog[field] != decision[field]:
            raise ValueError(f"handoff {field} mismatch")
    if timestamp(decision.get("observed_at"), "decision.observed_at") < timestamp(catalog["observed_at"], "catalog.observed_at"):
        raise ValueError("journey check predates catalog observation")
    refs(decision.get("source_refs"), "decision.source_refs")
    if decision.get("journey_state") not in ("pass", "blocked", "unknown"):
        raise ValueError("decision.journey_state is invalid")
    if decision.get("decision") not in ("hold", "go_review", "needs_information"):
        raise ValueError("decision.decision is invalid")
    if decision.get("approval_state") not in ("pending", "approved", "rejected"):
        raise ValueError("decision.approval_state is invalid")
    if decision.get("publish_state") not in ("not_published", "published", "verified"):
        raise ValueError("decision.publish_state is invalid")
    if decision["decision"] == "go_review" and (not catalog["ready_for_review"] or catalog["blockers"] or decision["journey_state"] != "pass"):
        raise ValueError("go review needs clear catalog and passing journey")
    if decision["approval_state"] == "approved":
        nonempty(decision.get("approval_ref"), "decision.approval_ref")
    if decision["publish_state"] in ("published", "verified"):
        if decision["decision"] != "go_review" or decision["approval_state"] != "approved":
            raise ValueError("published launch needs approved go review")
        nonempty(decision.get("publish_receipt_ref"), "decision.publish_receipt_ref")
    if decision["publish_state"] == "verified":
        nonempty(decision.get("retest_ref"), "decision.retest_ref")


if __name__ == "__main__":
    catalog_only = len(sys.argv) == 3 and sys.argv[1] == "--catalog-only"
    if len(sys.argv) != 3:
        raise SystemExit("usage: validate_handoff.py [--catalog-only] catalog.json [decision.json]")
    try:
        if catalog_only:
            validate_catalog(json.loads(Path(sys.argv[2]).read_text()))
        else:
            validate(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print("valid launch catalog artifact" if catalog_only else "valid launch handoff")
