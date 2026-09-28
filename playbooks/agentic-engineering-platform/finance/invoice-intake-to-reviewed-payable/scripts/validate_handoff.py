#!/usr/bin/env python3
"""Validate the structural invoice-to-payables handoff; source truth still needs live reads."""

import json
import re
import sys
from datetime import date, datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path


IDENTITY = ("tenant_id", "entity_id", "document_id", "document_hash", "document_version")
INVOICE = ("vendor_id", "invoice_number", "currency", "total_amount")
SHA256 = re.compile(r"^[0-9a-f]{64}$")


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def moment(value, label):
    nonempty(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def calendar_date(value, label):
    nonempty(value, label)
    try:
        return date.fromisoformat(value)
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO date") from exc


def money(value, label):
    nonempty(value, label)
    if not re.fullmatch(r"\d+(?:\.\d{1,2})?", value):
        raise ValueError(f"{label} must be a nonnegative two-decimal amount")
    try:
        return Decimal(value)
    except InvalidOperation as exc:
        raise ValueError(f"{label} must be decimal") from exc


def sources(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} needs source IDs")
    for index, item in enumerate(value):
        nonempty(item, f"{label}[{index}]")
    if len(value) != len(set(value)):
        raise ValueError(f"{label} source IDs must be unique")


def source_ref(value, available, label):
    nonempty(value, label)
    if value not in available:
        raise ValueError(f"{label} lacks source citation")


def artifact(value, kind, fields):
    if not isinstance(value, dict) or value.get("artifact_type") != kind:
        raise ValueError(f"expected {kind}")
    for field in fields:
        nonempty(value.get(field), f"{kind}.{field}")
    sources(value.get("source_refs"), f"{kind}.source_refs")
    return moment(value.get("observed_at"), f"{kind}.observed_at")


def invoice_key(value):
    return ":".join((value["entity_id"], value["vendor_id"], value["invoice_number"].strip().casefold()))


def validate_intake(intake):
    artifact(intake, "document-intake-record/v1", (*IDENTITY, *INVOICE, "intake_id", "duplicate_key", "vendor_match_ref"))
    if not SHA256.fullmatch(intake["document_hash"]):
        raise ValueError("document_hash must be SHA-256 hex")
    if intake.get("extraction_state") != "verified":
        raise ValueError("only verified invoice extraction can cross the handoff")
    if intake["duplicate_key"] != invoice_key(intake):
        raise ValueError("duplicate_key must bind entity, vendor and invoice number")
    source_ref(intake["vendor_match_ref"], intake["source_refs"], "vendor_match_ref")
    if not intake["vendor_match_ref"].startswith("ap:vendor"):
        raise ValueError("vendor_match_ref must cite an AP vendor record")
    if not isinstance(intake.get("fields"), dict):
        raise ValueError("fields must be an object")
    values = {}
    for name in ("vendor_name", "invoice_number", "issue_date", "due_date", "currency", "net_amount", "tax_amount", "total_amount"):
        field = intake["fields"].get(name)
        if not isinstance(field, dict):
            raise ValueError(f"field {name} is required")
        nonempty(field.get("value"), f"fields.{name}.value")
        source_ref(field.get("source_ref"), intake["source_refs"], f"fields.{name}.source_ref")
        prefix = f"doc:{intake['document_id']}@{intake['document_version']}#p"
        if not field["source_ref"].startswith(prefix):
            raise ValueError(f"fields.{name} needs the exact document version and page/span")
        values[name] = field["value"]
    for name in ("invoice_number", "currency", "total_amount"):
        if intake.get(name) != values[name]:
            raise ValueError(f"{name} conflicts with extracted field")
    if not re.fullmatch(r"[A-Z]{3}", intake["currency"]):
        raise ValueError("currency must be an uppercase ISO code")
    issued = calendar_date(values["issue_date"], "issue_date")
    due = calendar_date(values["due_date"], "due_date")
    if due < issued:
        raise ValueError("due_date precedes issue_date")
    net = money(values["net_amount"], "net_amount")
    tax = money(values["tax_amount"], "tax_amount")
    total = money(values["total_amount"], "total_amount")
    if total <= 0 or net + tax != total:
        raise ValueError("invoice total does not equal net plus tax")
    if not isinstance(intake.get("validation_gaps"), list) or intake["validation_gaps"]:
        raise ValueError("verified extraction cannot have validation gaps")


def validate_review(intake, review):
    validate_intake(intake)
    observed = artifact(review, "payable-review/v1", (*IDENTITY, *INVOICE, "source_intake_id", "review_id", "duplicate_key", "ap_source_ref", "policy_ref", "owner_id", "next_action"))
    for key in (*IDENTITY, *INVOICE, "duplicate_key"):
        if review[key] != intake[key]:
            raise ValueError(f"handoff {key} mismatch")
    if review["source_intake_id"] != intake["intake_id"]:
        raise ValueError("review cites a different intake")
    source_ref(review["source_intake_id"], review["source_refs"], "source_intake_id")
    source_ref(review["ap_source_ref"], review["source_refs"], "ap_source_ref")
    source_ref(review["policy_ref"], review["source_refs"], "policy_ref")
    if observed < moment(intake["observed_at"], "intake.observed_at"):
        raise ValueError("review predates extraction")
    ap_observed = moment(review.get("ap_observed_at"), "ap_observed_at")
    if ap_observed > observed:
        raise ValueError("AP source read cannot follow review")
    if ap_observed < moment(intake["observed_at"], "intake.observed_at"):
        raise ValueError("AP source must be re-read after extraction")
    duplicate = review.get("duplicate_state")
    disposition = review.get("disposition")
    approval = review.get("approval_state")
    action = review.get("action_state")
    if duplicate not in ("none", "possible", "existing", "paid"):
        raise ValueError("invalid duplicate_state")
    if approval not in ("pending", "approved", "rejected"):
        raise ValueError("invalid approval_state")
    if action not in ("none", "bill_written", "payment_scheduled", "paid"):
        raise ValueError("invalid action_state")
    if duplicate == "possible" and (disposition != "needs_review" or action != "none"):
        raise ValueError("possible duplicate needs review and no write")
    if duplicate in ("existing", "paid"):
        nonempty(review.get("existing_bill_id"), "existing_bill_id")
        source_ref("ap:" + review["existing_bill_id"], review["source_refs"], "existing_bill_id")
        if disposition not in ("existing_bill", "already_paid"):
            raise ValueError("existing bill cannot be proposed as new")
        if action == "bill_written":
            raise ValueError("existing bill cannot be written again")
    elif review.get("existing_bill_id"):
        raise ValueError("new invoice cannot claim an existing bill")
    if duplicate == "paid" and (disposition != "already_paid" or action != "paid"):
        raise ValueError("paid duplicate must retain paid state")
    if duplicate == "none" and disposition not in ("ready_for_owner_review", "approved_for_bill_write"):
        raise ValueError("new invoice needs owner review or approval")
    if disposition == "approved_for_bill_write" and approval != "approved":
        raise ValueError("bill write needs owner approval")
    if approval == "rejected" and action != "none":
        raise ValueError("rejected bill cannot be acted on")
    if action != "none":
        if action == "bill_written" and approval != "approved":
            raise ValueError("bill write needs owner approval")
        if action in ("payment_scheduled", "paid") and duplicate == "none":
            raise ValueError("payment state needs a provider bill")
        receipt = review.get("bill_receipt_ref") if action == "bill_written" else review.get("payment_receipt_ref")
        source_ref(receipt, review["source_refs"], "provider receipt")
    if action == "paid" and not review.get("payment_receipt_ref"):
        raise ValueError("paid requires payment receipt")
    if action == "none" and (review.get("bill_receipt_ref") or review.get("payment_receipt_ref")):
        raise ValueError("no action cannot carry a provider receipt")


def main(argv):
    if not argv or argv[0] not in ("intake", "review") or len(argv) != (2 if argv[0] == "intake" else 3):
        raise SystemExit("usage: validate_handoff.py intake record.json | review record.json review.json")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        (validate_intake if argv[0] == "intake" else validate_review)(*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid invoice handoff: {exc}") from exc
    print(f"valid invoice {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
