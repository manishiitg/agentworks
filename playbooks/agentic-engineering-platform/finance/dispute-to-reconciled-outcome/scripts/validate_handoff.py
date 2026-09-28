#!/usr/bin/env python3
"""Validate one processor dispute case and its exact finance review."""
import argparse
import json
from datetime import datetime
from pathlib import Path

SCOPE = (
    "entity_id", "provider_account_id", "provider_mode", "dispute_id",
    "payment_id", "customer_id", "currency",
)
STAGES = {"needs_response", "under_review", "closed_won", "closed_lost"}
FINANCE_STATES = {"pending_response", "awaiting_provider", "provider_decided_unreconciled", "reconciled"}


def _present(value):
    return isinstance(value, str) and bool(value.strip())


def _has_timezone(value):
    if not _present(value):
        return False
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00")).tzinfo is not None
    except ValueError:
        return False


def validate(case: dict, finance: dict) -> list[str]:
    errors: list[str] = []
    if case.get("artifact_type") != "dispute-case/v1":
        errors.append("wrong dispute case artifact type")
    if finance.get("artifact_type") != "dispute-finance-review/v1":
        errors.append("wrong finance artifact type")
    for key in SCOPE:
        if not _present(case.get(key)) or finance.get(key) != case.get(key):
            errors.append(f"scope mismatch or missing {key}")
    amount = case.get("amount_minor")
    if type(amount) is not int or amount <= 0 or finance.get("amount_minor") != amount:
        errors.append("invalid or mismatched dispute amount_minor")
    if not _present(case.get("artifact_id")) or finance.get("case_artifact_id") != case.get("artifact_id"):
        errors.append("finance did not cite the exact case artifact")
    if not _present(case.get("provider_case_revision")) or finance.get("provider_case_revision") != case.get("provider_case_revision"):
        errors.append("finance did not cite the exact provider case revision")
    for key in ("case_source_id", "payment_source_id", "provider_status", "reason", "response_deadline_at", "provider_observed_at", "owner_id", "evidence_packet_hash"):
        if not _present(case.get(key)):
            errors.append(f"missing case evidence {key}")
    for key in ("response_deadline_at", "provider_observed_at"):
        if not _has_timezone(case.get(key)):
            errors.append(f"{key} needs a valid timezone-aware timestamp")
    if not isinstance(case.get("evidence_source_ids"), list) or not all(_present(value) for value in case["evidence_source_ids"]):
        errors.append("invalid evidence source IDs")
    if not isinstance(case.get("missing_evidence"), list) or not all(_present(value) for value in case["missing_evidence"]):
        errors.append("invalid missing-evidence list")
    if not case.get("evidence_source_ids") and not case.get("missing_evidence"):
        errors.append("case has neither source evidence nor a documented gap")
    if case.get("owner_decision") not in {"pending", "approved", "declined"}:
        errors.append("invalid owner decision")
    if case.get("owner_decision") != "pending" and not _present(case.get("owner_decision_at")):
        errors.append("owner decision lacks observed time")

    stage = case.get("case_stage")
    if stage not in STAGES:
        errors.append("invalid case stage")
    submission_state = case.get("submission_state")
    receipt = case.get("provider_submission_receipt")
    if submission_state not in {"not_submitted", "submitted"}:
        errors.append("invalid submission state")
    elif submission_state == "not_submitted" and receipt:
        errors.append("unsubmitted case claims provider receipt")
    elif submission_state == "submitted":
        if case.get("owner_decision") != "approved" or not _present(case.get("submission_key")) or not isinstance(receipt, dict):
            errors.append("submitted case lacks approval, stable key or provider receipt")
        elif (
            receipt.get("status") != "submitted"
            or not _present(receipt.get("submission_id"))
            or not _present(receipt.get("submitted_at"))
            or receipt.get("dispute_id") != case.get("dispute_id")
            or receipt.get("payment_id") != case.get("payment_id")
            or receipt.get("account_id") != case.get("provider_account_id")
            or receipt.get("mode") != case.get("provider_mode")
            or receipt.get("packet_hash") != case.get("evidence_packet_hash")
        ):
            errors.append("provider submission receipt does not match exact approved packet")

    outcome = case.get("provider_outcome_receipt")
    closed = stage in {"closed_won", "closed_lost"}
    if closed:
        expected = "won" if stage == "closed_won" else "lost"
        if not isinstance(outcome, dict) or (
            outcome.get("status") != expected
            or not _present(outcome.get("source_id"))
            or not _present(outcome.get("observed_at"))
            or outcome.get("dispute_id") != case.get("dispute_id")
            or outcome.get("payment_id") != case.get("payment_id")
            or outcome.get("account_id") != case.get("provider_account_id")
            or outcome.get("mode") != case.get("provider_mode")
            or outcome.get("amount_minor") != amount
            or outcome.get("currency") != case.get("currency")
        ):
            errors.append("closed case lacks exact provider outcome receipt")
    elif outcome:
        errors.append("open case claims provider outcome")

    for key in ("provider_observed_at", "finance_source_id", "finance_observed_at", "finance_owner_id"):
        if not _present(finance.get(key)):
            errors.append(f"missing finance evidence {key}")
    if finance.get("provider_observed_at") != case.get("provider_observed_at"):
        errors.append("finance provider observation does not match case revision")
    if not _has_timezone(finance.get("finance_observed_at")):
        errors.append("finance_observed_at needs a valid timezone-aware timestamp")
    state = finance.get("finance_state")
    if state not in FINANCE_STATES:
        errors.append("invalid finance state")
    if state == "pending_response" and (stage != "needs_response" or submission_state != "not_submitted"):
        errors.append("pending response conflicts with provider case")
    if state == "awaiting_provider" and (closed or (stage == "needs_response" and submission_state != "submitted")):
        errors.append("awaiting provider conflicts with case stage")
    if state in {"provider_decided_unreconciled", "reconciled"} and not closed:
        errors.append("finance claims provider decision before outcome")
    if closed and state in {"pending_response", "awaiting_provider"}:
        errors.append("closed provider case still reported as pending")

    ledger = finance.get("principal_ledger_match")
    fee_state = finance.get("fee_state")
    fee_match = finance.get("fee_ledger_match")
    if fee_state not in {"pending", "not_applicable", "reconciled"}:
        errors.append("invalid fee state")
    if fee_state == "not_applicable" and not _present(finance.get("fee_policy_source_id")):
        errors.append("no-fee decision lacks source evidence")
    if fee_state == "reconciled" and (not isinstance(fee_match, dict) or not _present(fee_match.get("entry_id")) or fee_match.get("dispute_id") != case.get("dispute_id") or fee_match.get("entity_id") != case.get("entity_id") or fee_match.get("currency") != case.get("currency") or type(fee_match.get("amount_minor")) is not int or fee_match.get("amount_minor") < 0 or not _present(fee_match.get("observed_at"))):
        errors.append("reconciled fee lacks exact ledger evidence")
    if fee_state != "reconciled" and fee_match:
        errors.append("unreconciled fee claims ledger match")
    if state == "reconciled":
        if fee_state == "pending" or not isinstance(ledger, dict) or (
            not _present(ledger.get("entry_id"))
            or not _present(ledger.get("observed_at"))
            or ledger.get("entity_id") != case.get("entity_id")
            or ledger.get("dispute_id") != case.get("dispute_id")
            or ledger.get("payment_id") != case.get("payment_id")
            or ledger.get("amount_minor") != amount
            or ledger.get("currency") != case.get("currency")
        ):
            errors.append("reconciled state lacks exact principal and fee ledger evidence")
    elif ledger:
        errors.append("nonreconciled state claims principal ledger match")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("case", type=Path)
    parser.add_argument("finance", type=Path)
    args = parser.parse_args()
    errors = validate(json.loads(args.case.read_text()), json.loads(args.finance.read_text()))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("Dispute and finance review valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
