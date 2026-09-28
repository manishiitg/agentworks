#!/usr/bin/env python3
"""Validate bounded health evidence and an exact-contract renewal decision."""

import argparse
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError


def instant(value):
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed if parsed.tzinfo else None
    except ValueError:
        return None


def unique_strings(value, *, empty=False):
    return isinstance(value, list) and (empty or bool(value)) and all(isinstance(item, str) and item.strip() for item in value) and len(set(value)) == len(value)


def zone_for(value):
    try:
        return ZoneInfo(value) if isinstance(value, str) else None
    except ZoneInfoNotFoundError:
        return None


def validate_health(health):
    errors = []
    if health.get("artifact_type") != "renewal-health-brief/v1" or not health.get("artifact_id"):
        errors.append("wrong or missing health artifact")
    if not health.get("tenant_id") or not health.get("account_id") or not health.get("owner_id"):
        errors.append("health account or owner missing")
    start, end, observed = (instant(health.get(key)) for key in ("window_start", "window_end", "observed_at"))
    if not start or not end or not observed or start >= end or end > observed:
        errors.append("health window or observation chronology invalid")
    if health.get("coverage") not in ("complete", "partial", "unavailable") or not health.get("coverage_note"):
        errors.append("health source coverage missing")
    if health.get("first_value_state") not in ("reached", "not_observed", "unknown"):
        errors.append("first-value state invalid")
    if health.get("coverage") != "complete" and health.get("first_value_state") == "not_observed":
        errors.append("incomplete coverage cannot prove first value not observed")
    refs = health.get("source_refs")
    ref_ids = set()
    if not isinstance(refs, list) or not refs:
        errors.append("health source references missing")
    else:
        for ref in refs:
            if not isinstance(ref, dict) or not ref.get("id") or not ref.get("uri") or not instant(ref.get("observed_at")):
                errors.append("health source reference invalid")
                continue
            if ref["id"] in ref_ids or (observed and instant(ref["observed_at"]) > observed):
                errors.append("duplicate or future health source reference")
            ref_ids.add(ref["id"])
    signals = health.get("signals")
    signal_ids = set()
    if not isinstance(signals, list) or not signals:
        errors.append("health signals missing")
    else:
        for signal in signals:
            if not isinstance(signal, dict) or not signal.get("id") or signal.get("kind") not in ("observed", "hypothesis") or not signal.get("summary") or not unique_strings(signal.get("evidence_refs")):
                errors.append("health signal invalid")
                continue
            occurred = instant(signal.get("occurred_at"))
            if not occurred or not start or not end or not start <= occurred <= end:
                errors.append("health signal outside bounded window")
            if signal["id"] in signal_ids or any(ref not in ref_ids for ref in signal["evidence_refs"]):
                errors.append("duplicate signal or unknown evidence reference")
            signal_ids.add(signal["id"])
    if health.get("churn_probability") is not None or health.get("intent_claim") != "none" or health.get("customer_action_state") != "none":
        errors.append("health brief claims churn intent or customer action")
    return errors


def validate_pair(health, decision):
    errors = validate_health(health)
    if decision.get("artifact_type") != "renewal-decision-register/v1" or not decision.get("artifact_id") or decision.get("source_health_artifact_id") != health.get("artifact_id"):
        errors.append("renewal does not cite exact health artifact")
    for key in ("tenant_id", "account_id"):
        if not health.get(key) or decision.get(key) != health.get(key):
            errors.append(f"renewal account mismatch: {key}")
    if not all(decision.get(key) for key in ("legal_entity_id", "contract_id", "contract_revision", "subscription_id", "owner_id", "case_key")):
        errors.append("contract identity or renewal owner missing")
    keys = decision.get("existing_case_keys")
    if not unique_strings(keys, empty=True):
        errors.append("duplicate or missing renewal case key ledger")
    elif decision.get("case_operation") == "new":
        if decision.get("case_key") in keys or decision.get("prior_case_artifact_id") is not None:
            errors.append("duplicate new renewal case or unexpected prior artifact")
    elif decision.get("case_operation") == "update":
        if decision.get("case_key") not in keys or not decision.get("prior_case_artifact_id") or decision.get("prior_case_artifact_id") == decision.get("artifact_id"):
            errors.append("renewal repeat lacks exact prior case evidence")
    else:
        errors.append("renewal case operation invalid")
    health_time, read_time = instant(health.get("observed_at")), instant(decision.get("observed_at"))
    contract_time, billing_time = instant(decision.get("contract_observed_at")), instant(decision.get("billing_observed_at"))
    if not read_time or not health_time or read_time < health_time or not contract_time or contract_time > read_time or not billing_time or billing_time > read_time:
        errors.append("current contract or billing re-read chronology invalid")
    refs = decision.get("source_refs")
    if not isinstance(refs, dict) or any(not isinstance(refs.get(key), str) or not refs.get(key) for key in ("contract", "billing", "notice")):
        errors.append("contract, billing or notice source missing")
    if decision.get("billing_state") not in ("current", "open", "past_due", "unknown"):
        errors.append("billing state invalid")
    if decision.get("billing_state") != "unknown" and (type(decision.get("open_balance")) not in (int, float) or decision.get("open_balance") < 0 or not decision.get("currency")):
        errors.append("billing amount or currency missing")
    if decision.get("billing_state") == "unknown" and decision.get("open_balance") is not None:
        errors.append("unknown billing state claims balance")
    if decision.get("renewal_outcome") != "unknown" or decision.get("external_action_state") != "none" or decision.get("external_action_receipt") is not None:
        errors.append("renewal decision claims unverified outcome or external action")
    if decision.get("notice_state") == "none":
        if decision.get("notice_receipt") is not None:
            errors.append("notice-none state has a receipt")
    elif decision.get("notice_state") == "observed":
        receipt = decision.get("notice_receipt")
        if not isinstance(receipt, dict) or receipt.get("contract_id") != decision.get("contract_id") or receipt.get("contract_revision") != decision.get("contract_revision") or not receipt.get("provider_id") or not instant(receipt.get("sent_at")) or (read_time and instant(receipt["sent_at"]) > read_time):
            errors.append("observed notice lacks exact provider evidence")
    else:
        errors.append("notice state invalid")

    state = decision.get("decision_state")
    if state == "needs_terms":
        if decision.get("contract_status") == "executed" and instant(decision.get("renewal_at")) and type(decision.get("notice_period_days")) is int and isinstance(decision.get("auto_renewal"), bool) and decision.get("notice_rule_type") == "calendar_days" and zone_for(decision.get("notice_timezone")):
            errors.append("needs-terms state has complete executed terms")
        if not unique_strings(decision.get("blockers")) or decision.get("owner_decision") is not None or decision.get("notice_deadline_at") is not None or decision.get("days_to_notice") is not None:
            errors.append("needs-terms state claims a completed decision or notice calculation")
    elif state in ("pending_review", "owner_reviewed"):
        if decision.get("contract_status") != "executed" or not isinstance(decision.get("auto_renewal"), bool) or decision.get("notice_rule_type") != "calendar_days" or decision.get("blockers") != []:
            errors.append("reviewable renewal lacks executed terms or auto-renewal rule")
        renewal = instant(decision.get("renewal_at"))
        deadline = instant(decision.get("notice_deadline_at"))
        days = decision.get("notice_period_days")
        zone = zone_for(decision.get("notice_timezone"))
        if not renewal or not deadline or type(days) is not int or days < 0 or not zone or not read_time:
            errors.append("renewal and notice terms incomplete")
        else:
            expected = renewal.astimezone(zone) - timedelta(days=days)
            if deadline.astimezone(timezone.utc) != expected.astimezone(timezone.utc):
                errors.append("notice deadline arithmetic invalid")
            if decision.get("days_to_notice") != (expected.date() - read_time.astimezone(zone).date()).days:
                errors.append("days-to-notice arithmetic invalid")
        if state == "pending_review" and decision.get("owner_decision") is not None:
            errors.append("pending renewal claims owner decision")
        if state == "owner_reviewed":
            receipt = decision.get("owner_decision")
            if not isinstance(receipt, dict) or receipt.get("owner_id") != decision.get("owner_id") or receipt.get("contract_id") != decision.get("contract_id") or receipt.get("contract_revision") != decision.get("contract_revision") or receipt.get("case_key") != decision.get("case_key") or receipt.get("choice") not in ("investigate", "discussion_review", "terms_review", "no_action") or not instant(receipt.get("decided_at")) or (read_time and instant(receipt["decided_at"]) < read_time):
                errors.append("owner-reviewed renewal lacks exact decision receipt")
    else:
        errors.append("renewal decision state invalid")
    if decision.get("proposed_action") not in ("investigate", "discussion_review", "terms_review", "no_action"):
        errors.append("proposed renewal action invalid")
    if state == "needs_terms" and decision.get("proposed_action") != "terms_review":
        errors.append("missing terms require terms review")
    return errors


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("health", type=Path)
    parser.add_argument("decision", type=Path, nargs="?")
    parser.add_argument("--health-only", action="store_true")
    args = parser.parse_args()
    if args.health_only == bool(args.decision):
        parser.error("provide a decision or use --health-only")
    health = json.loads(args.health.read_text())
    errors = validate_health(health) if args.health_only else validate_pair(health, json.loads(args.decision.read_text()))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("Renewal health valid" if args.health_only else "Renewal health and decision valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
