#!/usr/bin/env python3
"""Check exact signed-deal identity and receiving-owner acceptance."""

import argparse
import json
from datetime import datetime, timezone
from pathlib import Path

IDENTITY = ("handoff_key", "tenant_id", "crm_account_id", "opportunity_id", "customer_account_id", "contract_id", "contract_revision", "crm_revision")


def instant(value):
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.astimezone(timezone.utc) if parsed.tzinfo else None
    except ValueError:
        return None


def strings(value):
    return isinstance(value, list) and bool(value) and all(isinstance(item, str) and item.strip() for item in value) and len(value) == len(set(value))


def validate_handoff(handoff):
    errors = []
    if handoff.get("artifact_type") != "sales-cs-handoff/v1" or not handoff.get("artifact_id"):
        errors.append("wrong or missing handoff artifact")
    if any(not handoff.get(field) for field in IDENTITY):
        errors.append("handoff identity incomplete")
    keys = handoff.get("existing_handoff_keys")
    if not isinstance(keys, list) or any(not isinstance(key, str) or not key for key in keys) or len(keys) != len(set(keys)) or handoff.get("handoff_key") in keys:
        errors.append("duplicate or missing handoff key ledger")
    if not handoff.get("sales_owner_id") or not handoff.get("cs_owner_id"):
        errors.append("Sales or CS owner missing")
    refs = handoff.get("source_refs")
    if not isinstance(refs, dict) or any(not isinstance(refs.get(key), str) or not refs.get(key) for key in ("crm", "agreement", "entitlement")):
        errors.append("CRM, agreement or entitlement source missing")
    promises_complete = strings(handoff.get("purchased_scope")) and bool(handoff.get("first_value_goal")) and bool(handoff.get("first_value_rule")) and bool(instant(handoff.get("target_at"))) and isinstance(refs, dict) and bool(refs.get("first_value"))
    observed = instant(handoff.get("observed_at"))
    signed = instant(handoff.get("signed_at"))
    if not observed or (signed and signed > observed):
        errors.append("handoff observation or signing chronology invalid")
    ready = handoff.get("crm_stage") == "closed_won" and handoff.get("contract_status") == "executed" and signed and handoff.get("provisioning_authorized") is True and promises_complete
    if handoff.get("handoff_state") == "ready":
        if not ready or handoff.get("blockers") != []:
            errors.append("ready handoff lacks executed scope or provisioning authority")
    elif handoff.get("handoff_state") == "blocked":
        if ready or not strings(handoff.get("blockers")):
            errors.append("blocked handoff needs an actual unresolved gate")
    else:
        errors.append("handoff state invalid")
    return errors


def validate_pair(handoff, acceptance):
    errors = validate_handoff(handoff)
    if acceptance.get("artifact_type") != "onboarding-acceptance/v1" or not acceptance.get("artifact_id") or acceptance.get("source_handoff_artifact_id") != handoff.get("artifact_id"):
        errors.append("acceptance does not cite exact handoff artifact")
    for field in IDENTITY:
        if acceptance.get(field) != handoff.get(field):
            errors.append(f"acceptance identity mismatch: {field}")
    if acceptance.get("receiver_owner_id") != handoff.get("cs_owner_id"):
        errors.append("receiving owner mismatch")
    observed = instant(acceptance.get("observed_at"))
    prepared = instant(handoff.get("observed_at"))
    if not observed or not prepared or observed < prepared:
        errors.append("acceptance must follow Sales observation")
    refs = acceptance.get("current_source_refs")
    if not isinstance(refs, dict) or any(not isinstance(refs.get(key), str) or not refs.get(key) for key in ("crm", "agreement", "entitlement")):
        errors.append("current CRM, agreement or entitlement re-read missing")
    drift = acceptance.get("current_contract_revision") != handoff.get("contract_revision") or acceptance.get("current_crm_revision") != handoff.get("crm_revision")
    scope_matches = acceptance.get("accepted_scope") == handoff.get("purchased_scope") and acceptance.get("first_value_goal") == handoff.get("first_value_goal") and acceptance.get("first_value_rule") == handoff.get("first_value_rule") and acceptance.get("target_at") == handoff.get("target_at")
    eligible = handoff.get("handoff_state") == "ready" and not drift and scope_matches and acceptance.get("current_entitlement_state") == "active"
    decision = acceptance.get("decision")
    receipt = acceptance.get("owner_acceptance")
    if decision == "accepted":
        valid_receipt = isinstance(receipt, dict) and receipt.get("owner_id") == handoff.get("cs_owner_id") and receipt.get("handoff_key") == handoff.get("handoff_key") and receipt.get("contract_revision") == handoff.get("contract_revision") and receipt.get("decision") == "accepted" and instant(receipt.get("decided_at"))
        if not eligible or acceptance.get("blockers") != [] or not valid_receipt or not observed or instant(receipt.get("decided_at")) < observed:
            errors.append("false acceptance: current scope, entitlement or owner receipt missing")
    elif decision == "needs_resolution":
        if eligible or not strings(acceptance.get("blockers")) or receipt is not None:
            errors.append("needs-resolution decision must reflect a real blocker and no acceptance")
    else:
        errors.append("acceptance decision invalid")
    if acceptance.get("customer_action_state") != "none" or acceptance.get("first_value_observed") is not False:
        errors.append("handoff cannot claim customer action or first value")
    return errors


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("handoff", type=Path)
    parser.add_argument("acceptance", type=Path, nargs="?")
    parser.add_argument("--handoff-only", action="store_true")
    args = parser.parse_args()
    if args.handoff_only == bool(args.acceptance):
        parser.error("provide an acceptance or use --handoff-only")
    handoff = json.loads(args.handoff.read_text())
    errors = validate_handoff(handoff) if args.handoff_only else validate_pair(handoff, json.loads(args.acceptance.read_text()))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("Signed deal handoff valid" if args.handoff_only else "Signed deal and receiving acceptance valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
