#!/usr/bin/env python3
"""Validate exact vendor comparison and a separate current purchase decision."""

import argparse
import json
import re
from datetime import date, datetime, timezone
from decimal import Decimal, InvalidOperation
from pathlib import Path

IDENTITY = ("tenant_id", "entity_id", "request_id", "requirements_revision", "currency", "seat_count", "term_months", "budget_amount")
MONEY = re.compile(r"^(?:0|[1-9]\d*)(?:\.\d{1,2})?$")


def moment(value):
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.astimezone(timezone.utc) if parsed.tzinfo else None
    except ValueError:
        return None


def money(value):
    if not isinstance(value, str) or not MONEY.fullmatch(value):
        return None
    try:
        return Decimal(value)
    except InvalidOperation:
        return None


def source(value, refs):
    return isinstance(value, str) and value and isinstance(refs, list) and value in refs


def candidate_state(candidate, criteria, budget):
    results = {item.get("criterion_id"): item.get("state") for item in candidate["criterion_results"]}
    if money(candidate["term_total"]) > budget or any(results.get(item["id"]) == "not_met" for item in criteria if item["must_have"]):
        return "ineligible"
    if any(results.get(item["id"]) == "unknown" for item in criteria if item["must_have"]):
        return "pending_due_diligence"
    return "eligible"


def validate_comparison(comp):
    errors = []
    if not isinstance(comp, dict) or comp.get("artifact_type") != "vendor-comparison/v1" or not comp.get("artifact_id"):
        return ["wrong or missing vendor comparison"]
    if any(not comp.get(key) for key in IDENTITY) or not comp.get("case_key"):
        errors.append("purchase request identity incomplete")
    if comp.get("entity_id") and comp.get("request_id") and comp.get("case_key") != f"{comp['entity_id']}/{comp['request_id']}":
        errors.append("purchase case key must bind entity and request")
    if not re.fullmatch(r"[A-Z]{3}", str(comp.get("currency", ""))):
        errors.append("currency must be uppercase three-letter code")
    if type(comp.get("seat_count")) is not int or comp["seat_count"] < 1 or type(comp.get("term_months")) is not int or comp["term_months"] < 1:
        errors.append("seat count or term invalid")
    budget = money(comp.get("budget_amount"))
    if budget is None or budget <= 0:
        errors.append("budget amount invalid")
    observed = moment(comp.get("observed_at"))
    if not observed:
        errors.append("comparison observation time invalid")
    refs = comp.get("source_refs")
    if not isinstance(refs, list) or not refs or any(not isinstance(ref, str) or not ref for ref in refs) or len(refs) != len(set(refs)):
        errors.append("comparison source references invalid")
    if not source(comp.get("requirements_source_ref"), refs):
        errors.append("requirements revision lacks source")
    criteria = comp.get("criteria")
    if not isinstance(criteria, list) or not criteria or any(not isinstance(item, dict) or not item.get("id") or type(item.get("weight")) is not int or item["weight"] < 1 or type(item.get("must_have")) is not bool for item in criteria):
        errors.append("approved criteria incomplete")
        criteria = []
    elif len({item["id"] for item in criteria}) != len(criteria):
        errors.append("duplicate criterion ID")
    candidates = comp.get("candidates")
    if not isinstance(candidates, list) or len(candidates) < 2:
        errors.append("comparison needs at least two exact candidates")
        candidates = []
    if len({item.get("candidate_id") for item in candidates if isinstance(item, dict)}) != len(candidates):
        errors.append("duplicate candidate ID")
    for index, candidate in enumerate(candidates):
        label = f"candidate {index}"
        if not isinstance(candidate, dict) or any(not candidate.get(key) for key in ("candidate_id", "vendor_id", "product_id", "plan_id", "plan_revision", "quote_id", "valid_until", "plan_source_ref", "quote_source_ref")):
            errors.append(f"{label} exact plan or quote missing")
            continue
        if candidate.get("currency") != comp.get("currency") or not source(candidate["plan_source_ref"], refs) or not source(candidate["quote_source_ref"], refs):
            errors.append(f"{label} currency or commercial source mismatch")
        try:
            valid_until = date.fromisoformat(candidate["valid_until"])
            if observed and valid_until < observed.date():
                errors.append(f"{label} quote expired")
        except (ValueError, TypeError):
            errors.append(f"{label} quote validity invalid")
        minimum = candidate.get("minimum_seats")
        seat_price, usage, onboarding, total = (money(candidate.get(key)) for key in ("seat_unit_monthly", "usage_monthly", "onboarding_fee", "term_total"))
        if type(minimum) is not int or minimum < 1 or any(value is None for value in (seat_price, usage, onboarding, total)) or type(comp.get("seat_count")) is not int or type(comp.get("term_months")) is not int:
            errors.append(f"{label} total cost inputs invalid")
        elif total != (seat_price * max(comp["seat_count"], minimum) + usage) * comp["term_months"] + onboarding:
            errors.append(f"{label} total cost arithmetic invalid")
        results = candidate.get("criterion_results")
        if not isinstance(results, list) or len(results) != len(criteria) or any(not isinstance(item, dict) or item.get("state") not in ("met", "not_met", "unknown") or not source(item.get("source_ref"), refs) for item in results):
            errors.append(f"{label} criterion evidence missing")
            continue
        if {item.get("criterion_id") for item in results} != {item["id"] for item in criteria} or len({item.get("criterion_id") for item in results}) != len(results):
            errors.append(f"{label} criterion set mismatched")
        if total is not None and budget is not None and criteria and all(isinstance(item, dict) and item.get("id") for item in criteria):
            if candidate.get("eligibility_state") != candidate_state(candidate, criteria, budget):
                errors.append(f"{label} eligibility state invalid")
    selected = next((item for item in candidates if isinstance(item, dict) and item.get("candidate_id") == comp.get("selected_candidate_id")), None)
    if not selected or selected.get("eligibility_state") == "ineligible" or not comp.get("selection_reason"):
        errors.append("selected plan absent, ineligible or unexplained")
    if comp.get("contact_state") != "none" or comp.get("purchase_action_state") != "none" or comp.get("compliance_claim") != "none":
        errors.append("comparison claims contact, purchase or compliance")
    return errors


def validate_pair(comp, review):
    errors = validate_comparison(comp)
    if not isinstance(review, dict) or review.get("artifact_type") != "vendor-purchase-review/v1" or not review.get("artifact_id") or review.get("source_comparison_artifact_id") != comp.get("artifact_id"):
        return errors + ["purchase review does not cite exact comparison"]
    for key in IDENTITY:
        if review.get(key) != comp.get(key):
            errors.append(f"purchase review identity mismatch: {key}")
    selected = next((item for item in comp.get("candidates", []) if isinstance(item, dict) and item.get("candidate_id") == comp.get("selected_candidate_id")), None)
    if selected:
        for key in ("candidate_id", "vendor_id", "product_id", "plan_id", "plan_revision", "quote_id", "term_total"):
            review_key = "selected_candidate_id" if key == "candidate_id" else key
            if review.get(review_key) != selected.get(key):
                errors.append(f"purchase review changed selected {key}")
        try:
            quote_valid_until = date.fromisoformat(selected["valid_until"])
            review_time = moment(review.get("observed_at"))
            if review_time and quote_valid_until < review_time.date():
                errors.append("selected quote expired before purchase review")
        except (ValueError, TypeError, KeyError):
            errors.append("selected quote validity missing")
    refs = review.get("source_refs")
    if not isinstance(refs, list) or not refs or any(not isinstance(ref, str) or not ref for ref in refs) or len(refs) != len(set(refs)):
        errors.append("review sources invalid")
    for key in ("vendor_register_ref", "commitments_ref", "budget_source_ref", "security_source_ref", "privacy_source_ref", "approval_policy_ref"):
        if not source(review.get(key), refs):
            errors.append(f"review source missing: {key}")
    compared, read, observed = (moment(value) for value in (comp.get("observed_at"), review.get("sources_observed_at"), review.get("observed_at")))
    if not compared or not read or not observed or not compared <= read <= observed:
        errors.append("procurement sources must be re-read after comparison")
    if not review.get("owner_id") or not isinstance(review.get("evidence_gaps"), list):
        errors.append("purchase owner or evidence gaps missing")
    keys = review.get("existing_case_keys")
    case_key = review.get("case_key")
    if case_key != comp.get("case_key") or not isinstance(keys, list) or any(not isinstance(key, str) or not key for key in keys) or len(keys) != len(set(keys)):
        errors.append("purchase case ledger invalid")
    elif review.get("case_operation") == "new":
        if case_key in keys or review.get("prior_case_artifact_id") is not None:
            errors.append("duplicate new purchase case")
    elif review.get("case_operation") == "update":
        if case_key not in keys or not review.get("prior_case_artifact_id") or review.get("prior_case_artifact_id") == review.get("artifact_id"):
            errors.append("purchase update lacks prior artifact")
    else:
        errors.append("purchase case operation invalid")
    duplicate = review.get("duplicate_state")
    if duplicate not in ("none", "possible", "overlap") or (duplicate == "none" and review.get("overlap_ref") is not None) or (duplicate != "none" and not source(review.get("overlap_ref"), refs)):
        errors.append("vendor or commitment duplicate state invalid")
    available = money(review.get("available_budget"))
    total = money(review.get("term_total"))
    budget_state = "unknown" if available is None else "sufficient" if total is not None and available >= total else "insufficient"
    if review.get("budget_state") != budget_state:
        errors.append("current available budget state invalid")
    for key in ("security_state", "privacy_state"):
        if review.get(key) not in ("cleared", "pending", "blocked", "not_required"):
            errors.append(f"{key} invalid")
    gates_clear = bool(selected and selected.get("eligibility_state") == "eligible" and duplicate == "none" and budget_state == "sufficient" and review.get("security_state") in ("cleared", "not_required") and review.get("privacy_state") in ("cleared", "not_required"))
    approval = review.get("approval_state")
    receipt = review.get("owner_decision")
    if approval == "pending":
        if receipt is not None:
            errors.append("pending review has owner receipt")
        expected = "ready_for_owner_review" if gates_clear else "pending_due_diligence"
    elif approval in ("approved", "rejected"):
        if not isinstance(receipt, dict) or receipt.get("owner_id") != review.get("owner_id") or receipt.get("case_key") != case_key or receipt.get("comparison_artifact_id") != comp.get("artifact_id") or receipt.get("choice") != approval or not moment(receipt.get("decided_at")) or (observed and moment(receipt.get("decided_at")) < observed):
            errors.append("owner decision lacks exact receipt")
        if approval == "approved" and not gates_clear:
            errors.append("unresolved gate cannot be approved")
        if approval == "approved" and selected and isinstance(receipt, dict) and moment(receipt.get("decided_at")):
            try:
                if date.fromisoformat(selected["valid_until"]) < moment(receipt["decided_at"]).date():
                    errors.append("quote expired before owner approval")
            except (ValueError, TypeError, KeyError):
                errors.append("approved quote validity missing")
        expected = "approved_for_purchase" if approval == "approved" else "rejected"
    else:
        errors.append("approval state invalid")
        expected = None
    if review.get("disposition") != expected:
        errors.append("purchase disposition does not match gates and owner")
    if review.get("purchase_action_state") != "none" or review.get("provider_receipt_ref") is not None or review.get("vendor_contact_state") != "none" or review.get("contract_state") != "none" or review.get("payment_state") != "none":
        errors.append("purchase review claims contact, signature, provider action or payment")
    return errors


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("comparison", type=Path)
    parser.add_argument("review", type=Path, nargs="?")
    parser.add_argument("--comparison-only", action="store_true")
    args = parser.parse_args()
    if args.comparison_only == bool(args.review):
        parser.error("provide a review or use --comparison-only")
    comparison = json.loads(args.comparison.read_text())
    errors = validate_comparison(comparison) if args.comparison_only else validate_pair(comparison, json.loads(args.review.read_text()))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("Vendor comparison valid" if args.comparison_only else "Vendor comparison and purchase review valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
