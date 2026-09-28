#!/usr/bin/env python3
"""Validate an exact trial observation before a permission-gated Sales assist."""
import argparse
import json
from datetime import datetime
from pathlib import Path

SCOPE = ("entity_id", "product_id", "tenant_id", "product_account_id", "trial_id", "subscription_id")


def present(value):
    return isinstance(value, str) and bool(value.strip())


def instant(value):
    if not present(value):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    return parsed if parsed.tzinfo is not None else None


def validate(usage: dict, sales: dict) -> list[str]:
    errors: list[str] = []
    if usage.get("artifact_type") != "trial-usage-observation/v1":
        errors.append("wrong trial usage artifact type")
    if sales.get("artifact_type") != "trial-sales-assist/v1":
        errors.append("wrong Sales assist artifact type")
    for key in SCOPE:
        if not present(usage.get(key)) or sales.get(key) != usage.get(key):
            errors.append(f"scope mismatch or missing {key}")
    if not present(usage.get("artifact_id")) or sales.get("usage_artifact_id") != usage.get("artifact_id"):
        errors.append("Sales did not cite the exact usage artifact")
    if not present(usage.get("trial_revision")) or sales.get("trial_revision") != usage.get("trial_revision"):
        errors.append("Sales did not cite the current trial revision")
    for key in ("trial_source_id", "event_rule_version", "coverage_source_id"):
        if not present(usage.get(key)):
            errors.append(f"missing trial source {key}")
    for key in ("trial_observed_at", "trial_start_at", "trial_end_at", "window_start_at", "window_end_at", "observed_at"):
        if instant(usage.get(key)) is None:
            errors.append(f"{key} needs a timezone-aware timestamp")
    start, end = instant(usage.get("window_start_at")), instant(usage.get("window_end_at"))
    trial_start, trial_end = instant(usage.get("trial_start_at")), instant(usage.get("trial_end_at"))
    trial_observed = instant(usage.get("trial_observed_at"))
    if start and end and start >= end:
        errors.append("invalid usage window")
    if trial_start and trial_end and trial_start >= trial_end:
        errors.append("invalid trial period")
    if (trial_start and start and start < trial_start) or (trial_end and end and end > trial_end):
        errors.append("usage window falls outside the trial")
    if trial_observed and end and end > trial_observed:
        errors.append("usage window extends beyond observed trial status")
    if usage.get("trial_status") not in {"trialing", "expired", "converted"}:
        errors.append("invalid trial status")
    if usage.get("coverage") not in {"complete", "partial", "unknown"}:
        errors.append("invalid event coverage")
    events = usage.get("qualifying_events")
    if not isinstance(events, list):
        errors.append("qualifying_events must be a list")
        events = []
    seen: set[str] = set()
    for event in events:
        if not isinstance(event, dict) or not present(event.get("event_id")) or not present(event.get("source_id")):
            errors.append("qualifying event lacks stable source identity")
            continue
        if event["event_id"] in seen:
            errors.append("duplicate qualifying event")
        seen.add(event["event_id"])
        if event.get("tenant_id") != usage.get("tenant_id") or event.get("product_account_id") != usage.get("product_account_id") or event.get("rule_version") != usage.get("event_rule_version"):
            errors.append("qualifying event has wrong account, tenant or rule")
        occurred = instant(event.get("occurred_at"))
        if occurred is None or (start and occurred < start) or (end and occurred >= end):
            errors.append("qualifying event falls outside the valid window")
    state = usage.get("usage_state")
    if state not in {"observed", "not_observed", "unknown"}:
        errors.append("invalid usage state")
    if events and state != "observed":
        errors.append("qualifying events contradict usage state")
    if not events and state == "observed":
        errors.append("observed use lacks a qualifying event")
    if not events and state == "not_observed" and usage.get("coverage") != "complete":
        errors.append("partial coverage cannot prove inactivity")
    if not events and state == "unknown" and usage.get("coverage") == "complete":
        errors.append("complete empty window must report not_observed")

    link = sales.get("account_link")
    if not isinstance(link, dict) or (
        link.get("product_account_id") != usage.get("product_account_id")
        or not present(link.get("crm_account_id"))
        or sales.get("crm_account_id") != link.get("crm_account_id")
        or not present(link.get("source_id"))
        or instant(link.get("observed_at")) is None
    ):
        errors.append("CRM account mapping does not join the product account")
    for key in ("owner_id", "contact_policy_version", "suppression_source_id", "prior_contact_source_id", "fit_source_id"):
        if not present(sales.get(key)):
            errors.append(f"missing Sales source {key}")
    if instant(sales.get("observed_at")) is None:
        errors.append("Sales observation needs a timezone-aware timestamp")
    sales_observed, usage_observed = instant(sales.get("observed_at")), instant(usage.get("observed_at"))
    if sales_observed and usage_observed and sales_observed < usage_observed:
        errors.append("Sales observation predates the trial usage handoff")
    if sales.get("seller_fit") not in {"approved", "blocked", "pending"}:
        errors.append("invalid seller fit decision")
    if sales.get("contact_permission") not in {"allowed", "blocked", "unknown"}:
        errors.append("invalid contact permission")
    if sales.get("contact_permission") == "allowed" and not present(sales.get("permission_source_id")):
        errors.append("allowed contact lacks permission source")
    if sales.get("suppression") not in {"clear", "blocked", "unknown"}:
        errors.append("invalid suppression state")
    if sales.get("reply_state") not in {"none", "responded", "unknown"}:
        errors.append("invalid reply state")
    if sales.get("meeting_state") not in {"none", "booked", "unknown"}:
        errors.append("invalid meeting state")
    if sales.get("send_state") != "not_sent" or sales.get("provider_delivery_receipt"):
        errors.append("Sales assist falsely claims delivery")
    contact = sales.get("contact")
    if contact is not None and (not isinstance(contact, dict) or not present(contact.get("contact_id")) or contact.get("crm_account_id") != sales.get("crm_account_id") or not present(contact.get("source_id"))):
        errors.append("contact is not linked to the exact CRM account")
    decision = sales.get("decision")
    if decision not in {"draft", "no_contact", "needs_review"}:
        errors.append("invalid Sales decision")
    draft = sales.get("draft")
    if decision == "draft":
        if usage.get("trial_status") != "trialing" or usage.get("usage_state") == "unknown" or sales.get("seller_fit") != "approved" or sales.get("contact_permission") != "allowed" or sales.get("suppression") != "clear" or sales.get("reply_state") != "none" or sales.get("meeting_state") != "none":
            errors.append("draft is blocked by current trial, fit or contact state")
        if not isinstance(contact, dict) or not isinstance(draft, dict) or not present(sales.get("offer_source_id")) or not present(sales.get("booking_url")) or sales.get("approval_required") is not True:
            errors.append("draft lacks exact contact, approved offer or review")
        elif (
            draft.get("recipient_id") != contact.get("contact_id")
            or draft.get("channel") != contact.get("channel")
            or not present(draft.get("subject"))
            or not present(draft.get("body"))
        ):
            errors.append("draft recipient, channel or content is invalid")
    elif draft is not None:
        errors.append("no-contact or unresolved decision must not carry a draft")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("usage", type=Path)
    parser.add_argument("sales", type=Path)
    args = parser.parse_args()
    failures = validate(json.loads(args.usage.read_text()), json.loads(args.sales.read_text()))
    for failure in failures:
        print(f"ERROR: {failure}")
    if failures:
        return 1
    print("Trial usage and Sales assist valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
