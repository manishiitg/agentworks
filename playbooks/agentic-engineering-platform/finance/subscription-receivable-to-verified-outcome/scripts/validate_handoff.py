#!/usr/bin/env python3
"""Validate one subscription receivable review and its exact finance outcome.

This checks internal claims and references. It cannot prove provider source truth.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from datetime import datetime
from pathlib import Path


class InvalidArtifact(ValueError):
    pass


def require(ok: bool, message: str) -> None:
    if not ok:
        raise InvalidArtifact(message)


def obj(value: object, name: str) -> dict:
    require(isinstance(value, dict), f"{name} must be an object")
    return value


def label(value: object, name: str) -> str:
    require(isinstance(value, str) and bool(value.strip()), f"{name} needs nonempty text")
    return value.strip()


def amount(value: object, name: str) -> int:
    require(type(value) is int and value >= 0, f"{name} needs nonnegative minor units")
    return value


def dated(value: object, name: str) -> datetime:
    try:
        parsed = datetime.fromisoformat(label(value, name).replace("Z", "+00:00"))
    except ValueError as exc:
        raise InvalidArtifact(f"{name} needs an ISO timestamp") from exc
    require(parsed.tzinfo is not None, f"{name} needs a timezone")
    return parsed


def refs(value: object, name: str, before: datetime) -> set[str]:
    require(isinstance(value, list) and bool(value), f"{name} needs source references")
    ids: set[str] = set()
    for i, raw in enumerate(value):
        entry = obj(raw, f"{name}[{i}]")
        source_id = label(entry.get("id"), f"{name}[{i}].id")
        require(source_id not in ids, f"duplicate source reference {source_id}")
        ids.add(source_id)
        label(entry.get("uri"), f"{name}[{i}].uri")
        require(dated(entry.get("observed_at"), f"{name}[{i}].observed_at") <= before, "source reference is later than artifact observation")
    return ids


def common(raw: object, kind: str) -> dict:
    doc = obj(raw, kind)
    require(doc.get("schema_version") == 1 and doc.get("artifact_type") == f"{kind}/v1", f"{kind} schema or type is wrong")
    for field in ("artifact_id", "case_id", "entity", "provider_account_id", "customer_id", "invoice_id", "currency", "owner_id"):
        label(doc.get(field), f"{kind}.{field}")
    require(doc.get("mode") in {"live", "test"}, f"{kind}.mode is invalid")
    require(bool(re.fullmatch(r"[A-Z]{3}", doc["currency"])), f"{kind}.currency is invalid")
    return doc


def validate_review(raw: object) -> None:
    review = common(raw, "receivable-review")
    if review.get("subscription_id") is not None:
        label(review["subscription_id"], "review.subscription_id")
    total = amount(review.get("invoice_total_minor"), "invoice_total_minor")
    credits = amount(review.get("credits_minor"), "credits_minor")
    prior = amount(review.get("previously_collected_minor"), "previously_collected_minor")
    remaining = amount(review.get("remaining_minor"), "remaining_minor")
    require(total > 0 and credits + prior < total and remaining == total - credits - prior, "remaining balance does not match invoice total, credits and prior collections")
    due = dated(review.get("due_at"), "review.due_at")
    cutoff = dated(review.get("cutoff_at"), "review.cutoff_at")
    created = dated(review.get("created_at"), "review.created_at")
    require(due <= cutoff <= created, "invoice due date, cutoff and creation are out of order")
    source_ids = refs(review.get("source_refs"), "review.source_refs", cutoff)
    require(review.get("invoice_source_ref") in source_ids, "reviewed invoice source is absent")
    require(review.get("invoice_status") in {"open", "past_due", "disputed"}, "review.invoice_status is invalid for an open receivable")
    attempt = review.get("latest_attempt")
    if attempt is not None:
        attempt = obj(attempt, "review.latest_attempt")
        label(attempt.get("id"), "attempt.id")
        require(attempt.get("status") in {"failed", "requires_action", "pending"}, "attempt cannot be treated as successful collection")
        require(dated(attempt.get("observed_at"), "attempt.observed_at") <= cutoff, "attempt is after review cutoff")
        require(attempt.get("source_ref") in source_ids, "attempt source is absent")
    retry = review.get("retry_at")
    if retry is not None:
        require(attempt is not None and dated(retry, "review.retry_at") > cutoff, "retry needs a failed or pending attempt and future provider time")
    require(review.get("contact_policy_ref") in source_ids, "contact policy source is absent")
    if review.get("prior_contact_ref") is not None:
        require(review["prior_contact_ref"] in source_ids, "prior contact source is absent")
    suppressed = review.get("suppression_reason")
    if suppressed is not None:
        label(suppressed, "review.suppression_reason")
    action = review.get("proposed_action")
    require(action in {"wait_for_retry", "draft_reminder", "no_contact", "escalate_owner"}, "review.proposed_action is invalid")
    if action == "wait_for_retry":
        require(retry is not None, "wait_for_retry needs a scheduled retry")
    if action == "draft_reminder":
        label(review.get("draft_message"), "review.draft_message")
    else:
        require(review.get("draft_message") is None, "non-reminder action cannot carry a customer message")
    if suppressed is not None or review["invoice_status"] == "disputed":
        require(action in {"no_contact", "escalate_owner"}, "suppressed or disputed invoice cannot propose contact")
    require(review.get("approval_state") in {"pending", "approved", "rejected"}, "review.approval_state is invalid")
    if review["approval_state"] == "approved":
        label(review.get("approval_ref"), "review.approval_ref")
    else:
        require(review.get("approval_ref") is None, "unapproved contact cannot claim approval")
    require(review.get("delivery_state") in {"unsent", "sent"}, "review.delivery_state is invalid")
    if review["delivery_state"] == "unsent":
        require(review.get("delivery_action_key") is None and review.get("delivery_receipt") is None, "unsent review cannot claim delivery")
    else:
        require(action == "draft_reminder" and review["approval_state"] == "approved" and suppressed is None, "sent reminder needs approved, unsuppressed draft")
        key = label(review.get("delivery_action_key"), "review.delivery_action_key")
        require(key.startswith(review["case_id"] + ":"), "delivery action key must be bound to case")
        receipt = obj(review.get("delivery_receipt"), "review.delivery_receipt")
        label(receipt.get("id"), "delivery_receipt.id")
        for field, expected in (("invoice_id", review["invoice_id"]), ("customer_id", review["customer_id"]), ("action_key", key), ("status", "sent")):
            require(receipt.get(field) == expected, f"delivery receipt {field} differs from approved case")
        require(receipt.get("source_ref") in source_ids, "delivery receipt source is absent")
        require(dated(receipt.get("observed_at"), "delivery_receipt.observed_at") <= cutoff, "delivery receipt must be observed by review cutoff")
    require(dated(review.get("next_check_at"), "review.next_check_at") > cutoff, "next check must follow cutoff")
    require(isinstance(review.get("limitations"), list), "review.limitations must be a list")


def validate_outcome(raw: object, review_raw: object) -> None:
    validate_review(review_raw)
    review = common(review_raw, "receivable-review")
    outcome = common(raw, "receivable-outcome")
    require(outcome.get("source_review_artifact_id") == review["artifact_id"], "outcome cites a different review artifact")
    for field in ("case_id", "entity", "provider_account_id", "mode", "customer_id", "invoice_id", "currency"):
        require(outcome[field] == review[field], f"outcome.{field} differs from reviewed invoice")
    observed = dated(outcome.get("observed_at"), "outcome.observed_at")
    created = dated(outcome.get("created_at"), "outcome.created_at")
    require(dated(review["cutoff_at"], "review.cutoff_at") < observed <= created and created >= dated(review["created_at"], "review.created_at"), "outcome needs a later provider observation")
    source_ids = refs(outcome.get("source_refs"), "outcome.source_refs", observed)
    require(outcome.get("current_invoice_source_ref") in source_ids, "current invoice source is absent")
    require(any(dated(item["observed_at"], "source.observed_at") > dated(review["cutoff_at"], "review.cutoff_at") for item in outcome["source_refs"]), "outcome needs a fresh source after review cutoff")
    collected = amount(outcome.get("new_collected_minor"), "outcome.new_collected_minor")
    remaining = amount(outcome.get("current_remaining_minor"), "outcome.current_remaining_minor")
    require(collected <= review["remaining_minor"] and remaining == review["remaining_minor"] - collected, "outcome collection and remaining balance do not reconcile")
    state = outcome.get("recovery_state")
    require(state in {"open", "partially_collected", "collected_unsettled", "verified_deposit"}, "outcome recovery_state is invalid")
    receipt = outcome.get("provider_payment_receipt")
    if state == "open":
        require(collected == 0 and receipt is None, "open case cannot claim a new collection")
    else:
        require(collected > 0, "collection state needs positive new collection")
        receipt = obj(receipt, "outcome.provider_payment_receipt")
        label(receipt.get("payment_id"), "payment_receipt.payment_id")
        for field, expected in (("invoice_id", review["invoice_id"]), ("customer_id", review["customer_id"]), ("account_id", review["provider_account_id"]), ("mode", review["mode"]), ("currency", review["currency"]), ("status", "succeeded"), ("amount_minor", collected)):
            require(receipt.get(field) == expected, f"payment receipt {field} differs from reviewed collection")
        require(receipt.get("source_ref") in source_ids, "payment receipt source is absent")
        require(dated(review["cutoff_at"], "review.cutoff_at") < dated(receipt.get("observed_at"), "payment_receipt.observed_at") <= observed, "payment receipt time is outside review and outcome observations")
    if state == "partially_collected":
        require(remaining > 0, "partial collection needs a positive remaining balance")
    if state in {"collected_unsettled", "verified_deposit"}:
        require(remaining == 0, "completed collection must clear the reviewed balance")
    payout = outcome.get("payout_allocation")
    bank = outcome.get("bank_ledger_match")
    if state == "verified_deposit":
        payout = obj(payout, "outcome.payout_allocation")
        bank = obj(bank, "outcome.bank_ledger_match")
        label(payout.get("payout_id"), "payout_allocation.payout_id")
        for field, expected in (("payment_id", receipt["payment_id"]), ("gross_minor", collected), ("currency", review["currency"])):
            require(payout.get(field) == expected, f"payout allocation {field} differs from new collection")
        fee = amount(payout.get("fee_minor"), "payout_allocation.fee_minor")
        net = amount(payout.get("net_minor"), "payout_allocation.net_minor")
        other = amount(payout.get("other_net_minor"), "payout_allocation.other_net_minor")
        payout_total = amount(payout.get("payout_total_minor"), "payout_allocation.payout_total_minor")
        require(fee <= collected and net == collected - fee and payout_total == net + other, "payout gross, fee, net and full payout do not reconcile")
        for field in ("bank_record_id", "ledger_entry_id"):
            label(bank.get(field), f"bank_ledger_match.{field}")
        for field, expected in (("payout_id", payout["payout_id"]), ("amount_minor", payout_total), ("currency", review["currency"])):
            require(bank.get(field) == expected, f"bank/ledger {field} differs from payout allocation")
        require(payout.get("source_ref") in source_ids, "payout allocation source is absent")
        for field in ("bank_source_ref", "ledger_source_ref"):
            require(bank.get(field) in source_ids, f"{field} is absent")
        require(bank["bank_source_ref"] != bank["ledger_source_ref"], "bank and ledger need distinct sources")
        for name, entry in (("payout_allocation", payout), ("bank_ledger_match", bank)):
            require(dated(entry.get("observed_at"), f"{name}.observed_at") <= observed, f"{name} observation follows outcome")
        require(dated(receipt["observed_at"], "payment_receipt.observed_at") <= dated(payout["observed_at"], "payout_allocation.observed_at") <= dated(bank["observed_at"], "bank_ledger_match.observed_at"), "payment, payout and deposit must be ordered")
    else:
        require(payout is None and bank is None, "unverified deposit evidence must not be presented as reconciled")
    label(outcome.get("owner_id"), "outcome.owner_id")
    require(dated(outcome.get("next_check_at"), "outcome.next_check_at") > observed, "next check must follow outcome observation")
    require(isinstance(outcome.get("limitations"), list), "outcome.limitations must be a list")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("review", "outcome"))
    parser.add_argument("artifact", type=Path)
    parser.add_argument("--review", type=Path)
    args = parser.parse_args()
    if args.kind == "outcome" and args.review is None:
        parser.error("outcome needs --review")
    if args.kind == "review" and args.review is not None:
        parser.error("review does not accept --review")
    try:
        artifact = json.loads(args.artifact.read_text())
        if args.kind == "review":
            validate_review(artifact)
        else:
            validate_outcome(artifact, json.loads(args.review.read_text()))
    except (OSError, json.JSONDecodeError, InvalidArtifact) as exc:
        print(f"INVALID {args.kind}: {exc}", file=sys.stderr)
        return 1
    print(f"VALID {args.kind}: {args.artifact}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
