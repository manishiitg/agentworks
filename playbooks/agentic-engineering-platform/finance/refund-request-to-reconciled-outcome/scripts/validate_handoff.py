#!/usr/bin/env python3
"""Validate a refund decision -> finance reconciliation artifact pair."""
import argparse
import json
from pathlib import Path

SCOPE = ('entity_id', 'provider_account_id', 'provider_mode', 'customer_id', 'request_id', 'payment_id', 'currency')


def validate(decision: dict, finance: dict) -> list[str]:
    errors = []
    for key in SCOPE:
        if not decision.get(key) or finance.get(key) != decision.get(key):
            errors.append(f'scope mismatch or missing {key}')
    if decision.get('artifact_type') != 'refund-decision/v1':
        errors.append('wrong decision artifact type')
    if finance.get('artifact_type') != 'refund-reconciliation/v1':
        errors.append('wrong finance artifact type')
    for key in ('original_minor', 'succeeded_refunds_minor', 'pending_refunds_minor', 'remaining_before_minor', 'requested_minor'):
        if type(decision.get(key)) is not int or decision[key] < 0:
            errors.append(f'invalid nonnegative integer {key}')
    amounts_valid = not any(error.startswith('invalid nonnegative') for error in errors)
    if amounts_valid:
        expected = decision['original_minor'] - decision['succeeded_refunds_minor'] - decision['pending_refunds_minor']
        if decision['remaining_before_minor'] != expected or expected < 0:
            errors.append('remaining amount does not reconcile')
        if decision['requested_minor'] == 0 or decision['requested_minor'] > expected:
            errors.append('requested amount is zero or exceeds remaining')
    if not decision.get('request_source_id') or not decision.get('payment_source_id') or not isinstance(decision.get('refund_history_source_ids'), list) or not decision.get('refund_history_observed_at') or not decision.get('observed_at'):
        errors.append('request/payment/refund evidence missing')
    if not decision.get('reason') or not decision.get('policy_version') or not decision.get('owner_id') or not decision.get('owner_decision_at') or decision.get('owner_decision') not in ('pending', 'approved', 'denied'):
        errors.append('policy or owner decision missing')
    action = decision.get('action_status')
    if action not in ('unprocessed', 'provider_succeeded'):
        errors.append('invalid action status')
    receipt = decision.get('provider_receipt')
    if action == 'unprocessed' and (decision.get('provider_refund_id') or receipt):
        errors.append('unprocessed case claims provider result')
    if action == 'provider_succeeded':
        if decision.get('owner_decision') != 'approved' or not decision.get('action_key') or not decision.get('provider_refund_id') or not isinstance(receipt, dict) or receipt.get('status') != 'succeeded' or receipt.get('refund_id') != decision.get('provider_refund_id') or receipt.get('payment_id') != decision.get('payment_id') or receipt.get('account_id') != decision.get('provider_account_id') or receipt.get('mode') != decision.get('provider_mode') or receipt.get('amount_minor') != decision.get('requested_minor') or receipt.get('currency') != decision.get('currency'):
            errors.append('processed refund lacks exact approval and succeeded provider receipt')
    if finance.get('decision_artifact_id') != decision.get('artifact_id') or not decision.get('artifact_id'):
        errors.append('finance did not cite exact decision artifact')
    if not finance.get('provider_observed_at') or not finance.get('finance_source_id') or not finance.get('finance_observed_at'):
        errors.append('finance source observations missing')
    state = finance.get('state')
    if state not in ('pending_action', 'denied', 'blocked', 'processed_unreconciled', 'reconciled'):
        errors.append('invalid finance state')
    if state == 'denied' and decision.get('owner_decision') != 'denied':
        errors.append('denied state lacks owner denial')
    if state == 'pending_action' and (decision.get('owner_decision') == 'denied' or action != 'unprocessed'):
        errors.append('pending action conflicts with decision or provider result')
    if state in ('processed_unreconciled', 'reconciled'):
        if action != 'provider_succeeded' or finance.get('provider_refund_id') != decision.get('provider_refund_id'):
            errors.append('finance processed state lacks matching provider refund')
    ledger = finance.get('ledger_match')
    if state == 'reconciled' and (not isinstance(ledger, dict) or not ledger.get('entry_id') or not ledger.get('observed_at') or ledger.get('refund_id') != decision.get('provider_refund_id') or ledger.get('amount_minor') != decision.get('requested_minor') or ledger.get('currency') != decision.get('currency')):
        errors.append('reconciled state lacks exact ledger evidence')
    if state != 'reconciled' and ledger:
        errors.append('unreconciled state claims a ledger match')
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('decision', type=Path)
    parser.add_argument('finance', type=Path)
    args = parser.parse_args()
    errors = validate(json.loads(args.decision.read_text()), json.loads(args.finance.read_text()))
    if errors:
        for error in errors:
            print(f'ERROR: {error}')
        return 1
    print('Refund decision and finance reconciliation valid')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
