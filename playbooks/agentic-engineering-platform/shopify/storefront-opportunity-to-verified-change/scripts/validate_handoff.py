#!/usr/bin/env python3
"""Validate a bounded Shopify growth-to-catalog handoff, not merchant source truth."""

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


def metric_fields(metric, label):
    if not isinstance(metric, dict):
        raise ValueError(f"{label} needs a metric")
    for field in ("name", "source_ref", "start", "end", "segment"):
        nonempty(metric.get(field), f"{label}.{field}")
    numerator, denominator = metric.get("numerator"), metric.get("denominator")
    if type(numerator) is not int or type(denominator) is not int or numerator < 0 or denominator <= 0 or numerator > denominator:
        raise ValueError("metric needs a valid numerator and denominator")
    if metric["start"] > metric["end"]:
        raise ValueError("metric date range is reversed")


def validate_opportunity(opportunity):
    if not isinstance(opportunity, dict) or opportunity.get("artifact_type") != "shopify-growth-opportunity/v1":
        raise ValueError("invalid growth opportunity artifact type")
    for field in ("store_id", "market", "product_id", "variant_id", "opportunity_id", "observed_at", "page_url", "buyer_task", "observation", "hypothesis", "owner_id"):
        nonempty(opportunity.get(field), f"opportunity.{field}")
    refs(opportunity.get("source_refs"), "opportunity.source_refs")
    kind = opportunity.get("evidence_kind")
    metric = opportunity.get("metric")
    if kind not in ("qualitative", "measured"):
        raise ValueError("opportunity.evidence_kind is invalid")
    if kind == "qualitative" and metric is not None:
        raise ValueError("qualitative opportunity cannot claim a measured metric")
    if kind == "measured":
        metric_fields(metric, "opportunity.metric")


def validate(opportunity, change):
    validate_opportunity(opportunity)
    if not isinstance(change, dict) or change.get("artifact_type") != "catalog-change-review/v1":
        raise ValueError("invalid catalog change artifact type")
    for field in ("store_id", "market", "product_id", "variant_id", "opportunity_id", "owner_id", "observed_at", "current_value", "proposed_value", "change_reason", "next_evidence"):
        nonempty(change.get(field), f"change.{field}")
    for field in ("store_id", "market", "product_id", "variant_id", "opportunity_id"):
        if opportunity[field] != change[field]:
            raise ValueError(f"handoff {field} mismatch")
    refs(change.get("source_refs"), "change.source_refs")
    if change.get("approval_state") not in ("pending", "approved", "rejected"):
        raise ValueError("change.approval_state is invalid")
    if change.get("publish_state") not in ("not_published", "published", "verified"):
        raise ValueError("change.publish_state is invalid")
    if change.get("measurement_state") not in ("not_started", "inconclusive", "comparable_result"):
        raise ValueError("change.measurement_state is invalid")
    if change["approval_state"] == "approved":
        nonempty(change.get("approval_ref"), "change.approval_ref")
    if change["publish_state"] in ("published", "verified"):
        if change["approval_state"] != "approved":
            raise ValueError("published change needs owner approval")
        nonempty(change.get("approval_ref"), "change.approval_ref")
        nonempty(change.get("publish_receipt_ref"), "change.publish_receipt_ref")
    if change["publish_state"] == "verified":
        nonempty(change.get("retest_ref"), "change.retest_ref")
    if change["measurement_state"] == "comparable_result":
        if opportunity["evidence_kind"] != "measured" or change["publish_state"] != "verified":
            raise ValueError("comparable result requires measured baseline and verified publish")
        nonempty(change.get("followup_report_ref"), "change.followup_report_ref")
        followup = change.get("followup_metric")
        metric_fields(followup, "change.followup_metric")
        baseline = opportunity["metric"]
        if followup["name"] != baseline["name"] or followup["segment"] != baseline["segment"]:
            raise ValueError("follow-up metric must use the same name and segment")
        if followup["start"] <= baseline["end"]:
            raise ValueError("follow-up window must be after baseline")
        if followup["source_ref"] != change["followup_report_ref"]:
            raise ValueError("follow-up metric source must match report reference")


if __name__ == "__main__":
    opportunity_only = len(sys.argv) == 3 and sys.argv[1] == "--opportunity-only"
    if len(sys.argv) != 3:
        raise SystemExit("usage: validate_handoff.py [--opportunity-only] opportunity.json [change.json]")
    try:
        if opportunity_only:
            validate_opportunity(json.loads(Path(sys.argv[2]).read_text()))
        else:
            validate(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print("valid growth opportunity" if opportunity_only else "valid growth-to-catalog handoff")
