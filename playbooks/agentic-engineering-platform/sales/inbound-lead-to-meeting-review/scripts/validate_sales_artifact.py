#!/usr/bin/env python3
"""Validate Sales artifacts from qualification through observed booking.

The checks block malformed or out-of-scope handoffs. They do not prove
source truth or authorization to contact a person.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from datetime import datetime
from pathlib import Path
from urllib.parse import urlparse


class InvalidArtifact(ValueError):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise InvalidArtifact(message)


def obj(value: object, label: str) -> dict:
    require(isinstance(value, dict), f"{label} must be an object")
    return value


def text(value: object, label: str) -> str:
    require(isinstance(value, str) and bool(value.strip()), f"{label} must be non-empty text")
    return value.strip()


def items(value: object, label: str, *, allow_empty: bool = False) -> list:
    require(isinstance(value, list) and (allow_empty or bool(value)), f"{label} must be a {'list' if allow_empty else 'non-empty list'}")
    return value


def timestamp(value: object, label: str) -> None:
    try:
        parsed = datetime.fromisoformat(text(value, label).replace("Z", "+00:00"))
    except ValueError as exc:
        raise InvalidArtifact(f"{label} must be an ISO timestamp") from exc
    require(parsed.tzinfo is not None, f"{label} needs a timezone")


def base(raw: object, kind: str) -> dict:
    doc = obj(raw, kind)
    require(doc.get("schema_version") == 1, f"{kind}.schema_version must be 1")
    require(doc.get("artifact_type") == f"{kind}/v1", f"{kind}.artifact_type is wrong")
    timestamp(doc.get("created_at"), f"{kind}.created_at")
    return doc


def references(raw: object, label: str) -> set[str]:
    ids: set[str] = set()
    for index, value in enumerate(items(raw, label)):
        ref = obj(value, f"{label}[{index}]")
        ref_id = text(ref.get("id"), f"{label}[{index}].id")
        require(ref_id not in ids, f"duplicate reference {ref_id}")
        ids.add(ref_id)
        text(ref.get("uri"), f"{label}[{index}].uri")
        timestamp(ref.get("observed_at"), f"{label}[{index}].observed_at")
    return ids


def known_refs(raw: object, available: set[str], label: str) -> None:
    for index, value in enumerate(items(raw, label)):
        require(text(value, f"{label}[{index}]") in available, f"{label} cites an unknown source")


def validate_qualification(raw: object) -> dict:
    brief = base(raw, "lead-qualification-brief")
    for field in ("brief_id", "lead_id", "entity", "period", "contact_ref", "inbound_request", "owner", "next_action"):
        text(brief.get(field), field)
    require("@" not in brief["contact_ref"], "contact_ref must identify an authorized source record, not contain an email address")
    domain = brief.get("company_domain")
    require(domain is None or (isinstance(domain, str) and bool(re.fullmatch(r"[a-zA-Z0-9-]+(?:\.[a-zA-Z0-9-]+)+", domain))), "company_domain must be a domain or null")
    require(brief.get("fit_status") in {"qualified", "needs_review", "not_fit"}, "fit_status is invalid")
    require(brief.get("duplicate_state") in {"none", "possible", "known"}, "duplicate_state is invalid")
    require(brief.get("contact_policy_status") in {"review_required", "approved", "blocked"}, "contact_policy_status is invalid")
    available = references(brief.get("source_refs"), "source_refs")
    known_refs(brief.get("evidence_refs"), available, "evidence_refs")
    criteria_ids: set[str] = set()
    for index, value in enumerate(items(brief.get("criteria"), "criteria")):
        label = f"criteria[{index}]"
        criterion = obj(value, label)
        criterion_id = text(criterion.get("id"), f"{label}.id")
        require(criterion_id not in criteria_ids, f"duplicate criterion {criterion_id}")
        criteria_ids.add(criterion_id)
        require(criterion.get("result") in {"match", "no_match", "unknown"}, f"{label}.result is invalid")
        text(criterion.get("reason"), f"{label}.reason")
        known_refs(criterion.get("evidence_refs"), available, f"{label}.evidence_refs")
    items(brief.get("missing_info"), "missing_info", allow_empty=True)
    items(brief.get("limitations"), "limitations", allow_empty=True)
    return brief


def require_ready_for_handoff(brief: dict) -> None:
    require(brief["fit_status"] == "qualified", "handoff requires a qualified lead")
    require(brief["duplicate_state"] == "none", "handoff blocked by duplicate lead state")
    require(brief["contact_policy_status"] != "blocked", "handoff blocked by contact policy")


def validate_research(raw: object, qualification_raw: object) -> dict:
    research = base(raw, "account-research-brief")
    qualification = validate_qualification(qualification_raw)
    require_ready_for_handoff(qualification)
    text(research.get("research_id"), "research_id")
    require(research.get("source_brief_id") == qualification["brief_id"], "research cites a different qualification brief")
    require(qualification["company_domain"] is not None, "research requires a resolved company domain")
    require(research.get("company_domain") == qualification["company_domain"], "research company_domain differs from lead")
    available = references(research.get("source_refs"), "source_refs") | references(qualification.get("source_refs"), "qualification.source_refs")
    finding_ids: set[str] = set()
    for index, value in enumerate(items(research.get("findings"), "findings")):
        label = f"findings[{index}]"
        finding = obj(value, label)
        finding_id = text(finding.get("id"), f"{label}.id")
        require(finding_id not in finding_ids, f"duplicate finding {finding_id}")
        finding_ids.add(finding_id)
        text(finding.get("finding"), f"{label}.finding")
        require(finding.get("evidence_kind") in {"observed", "owner_provided", "hypothesis"}, f"{label}.evidence_kind is invalid")
        known_refs(finding.get("evidence_refs"), available, f"{label}.evidence_refs")
    items(research.get("questions"), "questions", allow_empty=True)
    items(research.get("limitations"), "limitations", allow_empty=True)
    return research


def validate_followup(raw: object, qualification_raw: object, research_raw: object | None) -> None:
    draft = base(raw, "sales-followup-draft")
    qualification = validate_qualification(qualification_raw)
    require_ready_for_handoff(qualification)
    require(draft.get("source_brief_id") == qualification["brief_id"], "follow-up cites a different qualification brief")
    require(draft.get("lead_id") == qualification["lead_id"], "follow-up cites a different lead")
    require(draft.get("recipient_ref") == qualification["contact_ref"], "follow-up recipient differs from lead contact")
    require(draft.get("status") == "draft" and draft.get("send_state") == "not_sent", "follow-up must remain an unsent draft")
    require(draft.get("approval_required") is True, "follow-up must require owner approval")
    research_id = draft.get("source_research_id")
    if research_raw is None:
        require(research_id is None, "follow-up cites research that was not validated")
        research_refs: set[str] = set()
    else:
        research = validate_research(research_raw, qualification_raw)
        require(research_id == research["research_id"], "follow-up cites a different research brief")
        research_refs = references(research.get("source_refs"), "research.source_refs")
    for field in ("draft_id", "owner", "channel", "subject", "body", "next_action"):
        text(draft.get(field), field)
    timestamp(draft.get("next_check_at"), "next_check_at")
    available = references(draft.get("source_refs"), "source_refs") | references(qualification.get("source_refs"), "qualification.source_refs") | research_refs
    known_refs(draft.get("claim_refs"), available, "claim_refs")
    items(draft.get("limitations"), "limitations", allow_empty=True)


def draft_fingerprint(draft: dict) -> str:
    """Bind a review to the exact recipient and message, not a mutable draft ID."""
    approved_fields = {key: draft[key] for key in ("draft_id", "recipient_ref", "channel", "subject", "body")}
    serialized = json.dumps(approved_fields, sort_keys=True, ensure_ascii=False, separators=(",", ":"))
    return hashlib.sha256(serialized.encode()).hexdigest()


def validate_delivery(raw: object, qualification_raw: object, followup_raw: object, research_raw: object | None = None) -> dict:
    receipt = base(raw, "sales-delivery-receipt")
    qualification = validate_qualification(qualification_raw)
    validate_followup(followup_raw, qualification_raw, research_raw)
    require_ready_for_handoff(qualification)
    require(qualification["contact_policy_status"] == "approved", "delivery requires approved contact policy")
    for field in ("action_id", "owner", "approval_request_id", "approved_by", "provider", "provider_message_id", "booking_url"):
        text(receipt.get(field), field)
    booking_url = urlparse(receipt["booking_url"])
    require(booking_url.scheme == "https" and bool(booking_url.netloc), "booking_url must be HTTPS")
    require(receipt["booking_url"] in followup_raw["body"], "booking URL differs from reviewed message")
    require(receipt.get("source_brief_id") == qualification["brief_id"], "delivery cites a different qualification brief")
    require(receipt.get("source_draft_id") == followup_raw["draft_id"], "delivery cites a different draft")
    require(receipt.get("lead_id") == qualification["lead_id"], "delivery cites a different lead")
    require(receipt.get("recipient_ref") == followup_raw["recipient_ref"], "delivery recipient differs from reviewed draft")
    require(receipt.get("channel") == followup_raw["channel"], "delivery channel differs from reviewed draft")
    require(receipt.get("draft_fingerprint") == draft_fingerprint(followup_raw), "delivery message differs from reviewed draft")
    require(receipt.get("state") == "sent", "delivery needs a provider-confirmed sent state")
    for field in ("approved_at", "preflight_checked_at", "sent_at"):
        timestamp(receipt.get(field), field)
    require(receipt.get("preflight_duplicate_state") == "none", "delivery blocked by duplicate state")
    require(receipt.get("preflight_contact_policy_status") == "approved", "delivery blocked by contact policy")
    require(receipt.get("preflight_reply_state") == "none", "delivery blocked by a lead reply")
    require(receipt.get("preflight_meeting_state") == "none", "delivery blocked by an existing meeting")
    references(receipt.get("source_refs"), "delivery.source_refs")
    receipt_uris = [ref["uri"] for ref in receipt["source_refs"]]
    require(any(receipt["approval_request_id"] in uri for uri in receipt_uris), "delivery lacks approval reference")
    require(any(receipt["provider_message_id"] in uri for uri in receipt_uris), "delivery lacks provider message reference")
    require(datetime.fromisoformat(receipt["approved_at"].replace("Z", "+00:00")) <= datetime.fromisoformat(receipt["sent_at"].replace("Z", "+00:00")), "approval must precede send")
    require(datetime.fromisoformat(receipt["preflight_checked_at"].replace("Z", "+00:00")) <= datetime.fromisoformat(receipt["sent_at"].replace("Z", "+00:00")), "preflight must precede send")
    return receipt


def validate_meeting(raw: object, qualification_raw: object, delivery_raw: object | None = None, followup_raw: object | None = None, research_raw: object | None = None) -> None:
    meeting = base(raw, "sales-meeting-outcome")
    qualification = validate_qualification(qualification_raw)
    require_ready_for_handoff(qualification)
    require(qualification["contact_policy_status"] == "approved", "meeting requires an approved booking policy")
    require(meeting.get("lead_id") == qualification["lead_id"], "meeting cites a different lead")
    mode = meeting.get("booking_mode")
    require(mode in {"booking_link", "instant"}, "meeting booking_mode is invalid")
    if mode == "booking_link":
        require(delivery_raw is not None and followup_raw is not None, "booking-link meeting requires delivery and draft")
        receipt = validate_delivery(delivery_raw, qualification_raw, followup_raw, research_raw)
        require(meeting.get("source_action_id") == receipt["action_id"], "meeting cites a different delivery action")
    else:
        require(delivery_raw is None and followup_raw is None, "instant booking must not cite an email delivery")
        require(meeting.get("source_brief_id") == qualification["brief_id"], "instant meeting cites a different qualification brief")
        text(meeting.get("booking_session_id"), "booking_session_id")
    require(meeting.get("state") == "booked", "meeting must have a confirmed booked state")
    for field in ("meeting_id", "owner", "provider", "provider_event_id"):
        text(meeting.get(field), field)
    timestamp(meeting.get("starts_at"), "starts_at")
    timestamp(meeting.get("observed_at"), "observed_at")
    references(meeting.get("source_refs"), "meeting.source_refs")
    require(any(meeting["provider_event_id"] in ref["uri"] for ref in meeting["source_refs"]), "meeting lacks provider event reference")
    if mode == "booking_link":
        require(datetime.fromisoformat(meeting["observed_at"].replace("Z", "+00:00")) >= datetime.fromisoformat(receipt["sent_at"].replace("Z", "+00:00")), "meeting observation predates delivery")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("qualification", "research", "followup", "delivery", "meeting"))
    parser.add_argument("artifact", type=Path)
    parser.add_argument("--qualification", type=Path)
    parser.add_argument("--research", type=Path)
    parser.add_argument("--followup", type=Path)
    parser.add_argument("--delivery", type=Path)
    parser.add_argument("--require-ready", action="store_true", help="block a qualification handoff unless fit, duplicate, and contact states are ready")
    args = parser.parse_args()
    if args.kind in {"research", "followup", "delivery", "meeting"} and args.qualification is None:
        parser.error(f"{args.kind} validation requires --qualification")
    if args.kind == "delivery" and args.followup is None:
        parser.error("delivery validation requires --followup")
    if args.kind == "qualification" and (args.qualification or args.research or args.followup or args.delivery):
        parser.error("qualification validation accepts no handoff files")
    if args.kind == "research" and args.research:
        parser.error("--research is only for followup validation")
    if args.kind != "qualification" and args.require_ready:
        parser.error("--require-ready is only for qualification validation")
    try:
        artifact = json.loads(args.artifact.read_text())
        qualification = json.loads(args.qualification.read_text()) if args.qualification else None
        research = json.loads(args.research.read_text()) if args.research else None
        followup = json.loads(args.followup.read_text()) if args.followup else None
        delivery = json.loads(args.delivery.read_text()) if args.delivery else None
        if args.kind == "qualification":
            brief = validate_qualification(artifact)
            if args.require_ready:
                require_ready_for_handoff(brief)
        elif args.kind == "research":
            validate_research(artifact, qualification)
        elif args.kind == "followup":
            validate_followup(artifact, qualification, research)
        elif args.kind == "delivery":
            validate_delivery(artifact, qualification, followup, research)
        else:
            validate_meeting(artifact, qualification, delivery, followup, research)
    except (OSError, json.JSONDecodeError, InvalidArtifact) as exc:
        print(f"INVALID {args.kind}: {exc}", file=sys.stderr)
        return 1
    print(f"VALID {args.kind}: {args.artifact}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
