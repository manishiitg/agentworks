#!/usr/bin/env python3
"""Validate sourced post-incident review and independently verified actions."""

import json
import sys
from datetime import date, datetime
from pathlib import Path


def required(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def integer(value, label, *, positive=False):
    if isinstance(value, bool) or not isinstance(value, int) or value < (1 if positive else 0):
        raise ValueError(f"{label} must be a {'positive' if positive else 'nonnegative'} integer")
    return value


def when(value, label):
    required(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} needs an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def due(value):
    required(value, "due_date")
    try:
        return date.fromisoformat(value)
    except ValueError as exc:
        raise ValueError("due_date must be an ISO date") from exc


def sources(items, as_of):
    if not isinstance(items, list) or not items:
        raise ValueError("dated source_refs required")
    result = {}
    for item in items:
        if not isinstance(item, dict):
            raise ValueError("source_ref must be an object")
        key = required(item.get("id"), "source id")
        required(item.get("uri"), "source uri")
        observed = when(item.get("observed_at"), "source observed_at")
        if key in result or observed > as_of:
            raise ValueError("source IDs must be unique and dated by artifact")
        result[key] = (item["uri"], observed)
    return result


def source(refs, key, prefix=None):
    required(key, "source_ref")
    if key not in refs or (prefix and not refs[key][0].startswith(prefix)):
        raise ValueError("missing or wrong source reference: " + key)
    return refs[key]


def rate(numerator, denominator):
    return (numerator * 10000 + denominator // 2) // denominator


def validate_review(review):
    if not isinstance(review, dict) or review.get("artifact_type") != "post-incident-review/v1":
        raise ValueError("expected post-incident-review/v1")
    for key in ("artifact_id", "review_id", "review_revision", "incident_id", "service_id", "environment", "owner", "timezone", "next_check"):
        required(review.get(key), key)
    start = when(review.get("incident_window_start"), "incident_window_start")
    end = when(review.get("incident_window_end"), "incident_window_end")
    as_of = when(review.get("as_of"), "as_of")
    if not start < end < as_of:
        raise ValueError("incident window and review cutoff are inconsistent")
    refs = sources(review.get("source_refs"), as_of)
    if not any(uri.startswith("incident:") for uri, _ in refs.values()):
        raise ValueError("canonical incident source required")
    recovery = review.get("recovery")
    if not isinstance(recovery, dict) or recovery.get("state") != "stable_verified":
        raise ValueError("review requires verified stability")
    recovery_uri, recovery_at = source(refs, recovery.get("source_ref"), "incident:")
    if when(recovery.get("observed_at"), "recovery observed_at") != recovery_at or recovery_at < end:
        raise ValueError("recovery evidence must follow the incident window")
    impact = review.get("impact")
    if not isinstance(impact, dict):
        raise ValueError("impact is required")
    required(impact.get("metric"), "impact metric")
    required(impact.get("scope"), "impact scope")
    source(refs, impact.get("source_ref"), "monitoring:")
    failed = integer(impact.get("failed_requests"), "failed_requests")
    total = integer(impact.get("total_requests"), "total_requests", positive=True)
    if failed > total or integer(impact.get("rate_bps"), "impact rate_bps") != rate(failed, total):
        raise ValueError("impact arithmetic does not reconcile")
    first_failure = when(impact.get("first_failure_at"), "first_failure_at")
    if first_failure < start or first_failure > end:
        raise ValueError("first failure falls outside incident window")
    timeline = review.get("timeline")
    if not isinstance(timeline, list) or len(timeline) < 2:
        raise ValueError("sourced timeline needs at least two events")
    event_ids = set()
    for event in timeline:
        if not isinstance(event, dict):
            raise ValueError("timeline event must be an object")
        event_id = required(event.get("event_id"), "event_id")
        if event_id in event_ids:
            raise ValueError("timeline event IDs must be distinct")
        event_ids.add(event_id)
        occurred = when(event.get("occurred_at"), "event time")
        if occurred < start or occurred > end:
            raise ValueError("timeline event falls outside incident window")
        source(refs, event.get("source_ref"))
        required(event.get("fact"), "timeline fact")
    alert_ref = required(review.get("detection_alert_ref"), "detection_alert_ref")
    source(refs, alert_ref, "monitoring:")
    alert_events = [event for event in timeline if event["source_ref"] == alert_ref]
    if len(alert_events) != 1:
        raise ValueError("one sourced alert event is required for detection delay")
    alert_at = when(alert_events[0]["occurred_at"], "alert event time")
    delay = (alert_at - first_failure).total_seconds()
    if delay < 0 or delay != integer(review.get("detection_delay_seconds"), "detection_delay_seconds"):
        raise ValueError("detection delay does not reconcile")
    factors = review.get("contributing_factors")
    if not isinstance(factors, list) or not factors:
        raise ValueError("contributing factors or explicit unknowns required")
    factor_ids = set()
    for factor in factors:
        if not isinstance(factor, dict):
            raise ValueError("factor must be an object")
        factor_id = required(factor.get("id"), "factor id")
        if factor_id in factor_ids:
            raise ValueError("factor IDs must be distinct")
        factor_ids.add(factor_id)
        required(factor.get("statement"), "factor statement")
        required(factor.get("limitation"), "factor limitation")
        evidence = factor.get("evidence_refs")
        if not isinstance(evidence, list) or len(evidence) < (2 if factor.get("classification") == "confirmed" else 1) or len(set(evidence)) != len(evidence):
            raise ValueError("confirmed factor needs two distinct sources; hypothesis needs one")
        if factor.get("classification") not in ("confirmed", "hypothesis"):
            raise ValueError("factor classification is invalid")
        for ref in evidence:
            source(refs, ref)
    if not isinstance(review.get("unknowns"), list) or not isinstance(review.get("what_worked"), list):
        raise ValueError("unknowns and what_worked lists required")
    actions = review.get("actions")
    if not isinstance(actions, list) or not actions:
        raise ValueError("proposed actions required")
    action_ids = set()
    for action in actions:
        if not isinstance(action, dict):
            raise ValueError("action must be an object")
        action_id = required(action.get("action_id"), "action_id")
        if action_id in action_ids:
            raise ValueError("action IDs must be distinct")
        action_ids.add(action_id)
        for key in ("gap", "owner", "verification_criterion"):
            required(action.get(key), "action " + key)
        due(action.get("due_date"))
    if review.get("publication_state") != "not_published":
        raise ValueError("review cannot claim publication without a separate receipt route")
    state = review.get("review_state")
    decision = review.get("approval")
    if state == "pending_owner_review":
        if decision is not None:
            raise ValueError("draft review cannot claim approval")
    elif state == "approved" and isinstance(decision, dict):
        approval_id = required(decision.get("id"), "review approval id")
        approved_at = when(decision.get("approved_at"), "review approved_at")
        approval_uri, approval_source_at = source(refs, decision.get("source_ref"), "approval:")
        if decision.get("state") != "approved" or approval_uri != "approval:" + approval_id or approved_at != approval_source_at or approved_at > as_of or approved_at < end:
            raise ValueError("review approval source is inconsistent")
    else:
        raise ValueError("review state must be pending or owner-approved")
    return as_of


def validate_register(review, register):
    reviewed_at = validate_review(review)
    if not isinstance(register, dict) or register.get("artifact_type") != "incident-improvement-register/v1":
        raise ValueError("expected incident-improvement-register/v1")
    for key in ("artifact_id", "owner", "next_check"):
        required(register.get(key), key)
    if register.get("source_review_artifact_id") != review["artifact_id"]:
        raise ValueError("register needs exact review artifact")
    for key in ("review_id", "review_revision", "incident_id", "service_id", "environment"):
        if register.get(key) != review[key]:
            raise ValueError("register " + key + " differs from review")
    as_of = when(register.get("as_of"), "register as_of")
    if as_of < reviewed_at:
        raise ValueError("register predates reviewed source")
    refs = sources(register.get("source_refs"), as_of)
    review_uri, review_source_at = source(refs, register.get("source_review_ref"), "artifact:")
    if review_uri != "artifact:" + review["artifact_id"] or review_source_at != reviewed_at:
        raise ValueError("register needs exact review source")
    entries = register.get("actions")
    if not isinstance(entries, list) or [item.get("action_id") for item in entries] != [item["action_id"] for item in review["actions"]]:
        raise ValueError("register action IDs differ from review")
    for proposal, entry in zip(review["actions"], entries):
        for key in ("owner", "due_date", "verification_criterion"):
            if entry.get(key) != proposal[key]:
                raise ValueError("action " + key + " differs from reviewed proposal")
        state = entry.get("state")
        decision, issue, verification = (entry.get(key) for key in ("decision", "issue", "verification"))
        if state == "pending_review":
            if any(value is not None for value in (decision, issue, verification)):
                raise ValueError("pending action cannot claim decision, issue or verification")
            continue
        if review["review_state"] != "approved" or state not in ("accepted_pending_issue", "accepted_open", "verified") or not isinstance(decision, dict):
            raise ValueError("accepted action needs approved review and owner decision")
        decision_id = required(decision.get("id"), "action decision id")
        decision_at = when(decision.get("decided_at"), "action decision time")
        decision_uri, decision_source_at = source(refs, decision.get("source_ref"), "approval:")
        if decision.get("state") != "accepted" or decision_at < reviewed_at or decision_at > as_of or decision_uri != "approval:" + decision_id or decision_source_at != decision_at:
            raise ValueError("action decision source is inconsistent")
        if state == "accepted_pending_issue":
            if issue is not None or verification is not None:
                raise ValueError("accepted pending issue cannot claim issue or verification")
            continue
        if not isinstance(issue, dict):
            raise ValueError("accepted action needs exact issue receipt and current state")
        issue_id = required(issue.get("id"), "issue id")
        receipt_id = required(issue.get("create_receipt_id"), "issue create receipt id")
        receipt_uri, receipt_at = source(refs, issue.get("create_receipt_ref"), "issue-receipt:")
        current_uri, issue_current_at = source(refs, issue.get("current_source_ref"), "issue:")
        if receipt_uri != "issue-receipt:" + receipt_id or not current_uri.startswith("issue:" + issue_id + "@") or receipt_at < decision_at or issue_current_at < receipt_at or not required(issue.get("state"), "issue state"):
            raise ValueError("issue receipt must follow acceptance")
        if state == "accepted_open":
            if verification is not None:
                raise ValueError("open action cannot claim verification")
            continue
        if not isinstance(verification, dict):
            raise ValueError("verified action needs independent control evidence")
        verification_uri, verification_at = source(refs, verification.get("source_ref"), "verification:")
        if when(verification.get("observed_at"), "verification time") != verification_at or verification_at < receipt_at or verification.get("result") != "pass":
            raise ValueError("verification must pass after accepted work")
        required(verification.get("finding"), "verification finding")
    if register.get("publication_state") != "not_published":
        raise ValueError("register cannot claim publication without separate receipt")


def main(argv):
    if not argv or argv[0] not in ("review", "register") or len(argv) != (2 if argv[0] == "review" else 3):
        raise SystemExit("usage: validate_handoff.py review <review.json> | register <review.json> <register.json>")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        (validate_review if argv[0] == "review" else validate_register)(*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid post-incident handoff: {exc}") from exc
    print("valid post-incident " + argv[0] + " contract")


if __name__ == "__main__":
    main(sys.argv[1:])
