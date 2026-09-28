#!/usr/bin/env python3
"""Validate an exact order exception before a Support Crew drafts an update."""

import argparse
import json
import re
from datetime import datetime
from pathlib import Path

IDENTITY = ('business_id', 'order_id', 'customer_id', 'case_id')
ORDER_SOURCES = ('order_source_id', 'payment_source_id', 'fulfillment_source_id', 'carrier_source_id')
SOURCE_STATES = {
    ('label_created', 'not_accepted'): 'pickup_unverified',
    ('accepted', 'accepted'): 'carrier_accepted',
    ('in_transit', 'in_transit'): 'in_transit',
    ('delivered', 'delivered'): 'delivered',
    ('unknown', 'unknown'): 'unknown',
}


def timestamp(value: object) -> datetime | None:
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    except ValueError:
        return None
    return parsed if parsed.tzinfo else None


def nonempty(value: object) -> bool:
    return isinstance(value, str) and bool(value.strip())


def references(value: object) -> bool:
    return isinstance(value, list) and len(value) > 0 and all(nonempty(item) for item in value)


def validate_order(order: dict) -> list[str]:
    errors: list[str] = []
    if order.get('artifact_type') != 'order-exception/v1':
        errors.append('wrong order artifact type')
    for key in ('artifact_id', *IDENTITY, 'payment_id', 'fulfillment_id', 'shipment_id', 'policy_version', 'owner_id', *ORDER_SOURCES):
        if not nonempty(order.get(key)):
            errors.append(f'missing {key}')
    for source_key, expected_key in (
        ('payment_record_order_id', 'order_id'),
        ('fulfillment_record_order_id', 'order_id'),
        ('carrier_record_shipment_id', 'shipment_id'),
    ):
        if not nonempty(order.get(source_key)) or order.get(source_key) != order.get(expected_key):
            errors.append(f'source identity mismatch {source_key}')
    if not timestamp(order.get('observed_at')) or not timestamp(order.get('promised_at')):
        errors.append('order observation or promised time is invalid')
    if not references(order.get('source_refs')):
        errors.append('order source references missing')
    elif any(order.get(key) not in order['source_refs'] for key in ORDER_SOURCES):
        errors.append('order source references omit a joined source')
    if order.get('order_state') != 'paid':
        errors.append('this route requires a paid order')
    if order.get('payment_state') not in ('captured', 'sale', 'authorized', 'pending', 'failed', 'refunded'):
        errors.append('invalid payment state')
    if order.get('payment_state') not in ('captured', 'sale'):
        errors.append('paid order lacks captured or sale evidence')
    if order.get('refund_state') not in ('none', 'unknown', 'provider_succeeded'):
        errors.append('invalid refund state')
    if order.get('refund_state') == 'provider_succeeded' and not nonempty(order.get('refund_receipt_id')):
        errors.append('refund claim lacks provider receipt')
    if order.get('refund_state') != 'provider_succeeded' and order.get('refund_receipt_id'):
        errors.append('refund receipt conflicts with observed state')
    state_pair = (order.get('fulfillment_state'), order.get('carrier_state'))
    expected = SOURCE_STATES.get(state_pair) if all(isinstance(item, str) for item in state_pair) else None
    if expected is None or order.get('exception_state') != expected:
        errors.append('exception state does not follow fulfillment and carrier evidence')
    if order.get('resolution_state') != 'open':
        errors.append('order exception must remain open until a separate observed resolution')
    if order.get('customer_contact_state') != 'not_sent':
        errors.append('order investigation cannot claim a customer send')
    return errors


def validate_update(order: dict, update: dict) -> list[str]:
    errors = validate_order(order)
    if update.get('artifact_type') != 'order-customer-update-review/v1':
        errors.append('wrong update artifact type')
    for key in IDENTITY:
        if not nonempty(update.get(key)) or update.get(key) != order.get(key):
            errors.append(f'update identity mismatch {key}')
    if not nonempty(update.get('order_artifact_id')) or update.get('order_artifact_id') != order.get('artifact_id'):
        errors.append('update did not cite exact order artifact')
    for key in ('artifact_id', 'case_revision', 'case_source_id', 'owner_id', 'policy_version'):
        if not nonempty(update.get(key)):
            errors.append(f'update missing {key}')
    for source_key, expected_key in (('case_record_order_id', 'order_id'), ('case_record_customer_id', 'customer_id')):
        if not nonempty(update.get(source_key)) or update.get(source_key) != order.get(expected_key):
            errors.append(f'case source identity mismatch {source_key}')
    if not timestamp(update.get('case_observed_at')):
        errors.append('case observation time is invalid')
    elif timestamp(order.get('observed_at')) and timestamp(update['case_observed_at']) < timestamp(order['observed_at']):
        errors.append('case read predates order investigation')
    update_refs = update.get('source_refs')
    if not references(update_refs) or order.get('artifact_id') not in update_refs or update.get('case_source_id') not in update_refs:
        errors.append('update source references omit order artifact or current case')
    if update.get('claim_state') not in (order.get('exception_state'), 'unknown'):
        errors.append('customer claim exceeds observed order state')
    if update.get('contact_decision') not in ('draft_review', 'no_message'):
        errors.append('invalid contact decision')
    if update.get('message_state') != 'unsent' or update.get('approval_state') != 'pending':
        errors.append('review route cannot claim approval or delivery')
    if update.get('resolution_state') != 'open':
        errors.append('draft cannot claim case resolution')
    if update.get('contact_decision') == 'draft_review':
        for key in ('recipient_ref', 'channel', 'draft_text'):
            if not nonempty(update.get(key)):
                errors.append(f'draft missing {key}')
    elif update.get('draft_text') or not nonempty(update.get('no_message_reason')):
        errors.append('no-message decision needs a reason and no draft')
    draft = update.get('draft_text')
    if isinstance(draft, str):
        if order.get('exception_state') != 'delivered' and re.search(r'\b(delivered|arrived)\b', draft, re.I):
            errors.append('draft asserts delivery without carrier evidence')
        if re.search(r'\b(lost|refund(?:ed)?|reship(?:ped)?)\b', draft, re.I):
            errors.append('draft asserts loss, refund or reship outside this review route')
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('stage', choices=('order', 'update'))
    parser.add_argument('order', type=Path)
    parser.add_argument('update', type=Path, nargs='?')
    args = parser.parse_args()
    if args.stage == 'update' and args.update is None:
        parser.error('update stage needs an update artifact')
    order = json.loads(args.order.read_text())
    errors = validate_order(order) if args.stage == 'order' else validate_update(order, json.loads(args.update.read_text()))
    for error in errors:
        print(f'ERROR: {error}')
    if errors:
        return 1
    print('Order exception handoff valid')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
