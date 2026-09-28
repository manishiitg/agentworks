#!/usr/bin/env python3
"""Validate a bounded pipeline exception and current-state seller action register."""
import argparse
import json
from datetime import datetime, timezone
from pathlib import Path

SCOPE = ('tenant_id', 'crm_account_id', 'pipeline_id', 'opportunity_id', 'window_start', 'window_end', 'current_snapshot_id')


def instant(value):
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
        return parsed.astimezone(timezone.utc) if parsed.tzinfo else None
    except ValueError:
        return None


def age_days(later, earlier):
    return (later.date() - earlier.date()).days if later and earlier else None


def validate(exception: dict, action: dict) -> list[str]:
    errors = []
    if exception.get('artifact_type') != 'pipeline-exception-brief/v1' or action.get('artifact_type') != 'deal-action-register/v1':
        errors.append('wrong artifact type')
    for key in SCOPE:
        if not exception.get(key) or action.get(key) != exception.get(key):
            errors.append(f'scope mismatch or missing {key}')
    if not exception.get('artifact_id') or action.get('source_exception_artifact_id') != exception.get('artifact_id'):
        errors.append('action register did not cite exact exception artifact')
    prior = instant(exception.get('prior_observed_at'))
    current = instant(exception.get('current_observed_at'))
    last = instant(exception.get('last_activity_at'))
    if not prior or not current or prior >= current or not last or last > current or exception.get('prior_snapshot_id') == exception.get('current_snapshot_id'):
        errors.append('ordered distinct snapshots or activity time missing')
    if exception.get('snapshot_comparable') is not True or not exception.get('stage_policy_id') or not exception.get('currency_policy_id') or not exception.get('coverage_note'):
        errors.append('snapshot comparability or coverage missing')
    refs = exception.get('source_refs')
    if not isinstance(refs, list) or len(refs) < 2 or any(not isinstance(ref, str) or not ref for ref in refs) or len(refs) != len(set(refs)):
        errors.append('prior/current source references missing or duplicated')
    if not exception.get('owner_id') or not exception.get('prior_stage') or not exception.get('current_stage') or not exception.get('currency') or not exception.get('finding'):
        errors.append('opportunity source state missing')
    amounts = (exception.get('prior_amount'), exception.get('current_amount'))
    if any(type(amount) not in (int, float) or amount < 0 for amount in amounts):
        errors.append('opportunity amount invalid')
    if exception.get('movement') == 'unchanged':
        if exception.get('prior_stage') != exception.get('current_stage') or amounts[0] != amounts[1]:
            errors.append('unchanged movement contradicts snapshots')
    elif exception.get('movement') == 'changed':
        if exception.get('prior_stage') == exception.get('current_stage') and amounts[0] == amounts[1]:
            errors.append('changed movement has no observed stage or amount change')
    else:
        errors.append('movement state invalid')
    threshold = exception.get('stale_threshold_days')
    age = age_days(current, last)
    if type(threshold) is not int or threshold < 1 or age is None or age < threshold or exception.get('stale_age_days') != age:
        errors.append('stale calculation invalid')
    next_activity = exception.get('next_activity_at')
    if next_activity is not None and (not instant(next_activity) or instant(next_activity) >= current):
        errors.append('current or upcoming next activity contradicts stale exception')

    action_observed = instant(action.get('current_observed_at'))
    action_last = instant(action.get('current_last_activity_at'))
    if not action.get('current_crm_revision') or action.get('current_source_opportunity_id') != exception.get('opportunity_id') or not action.get('current_activity_source_id') or not action_observed or (current and action_observed < current) or not action_last or action_last > action_observed or not action.get('current_coverage_note'):
        errors.append('current opportunity and activity read missing or older than snapshot')
    contact = action.get('current_contact_status')
    if contact not in ('clear', 'unknown', 'opted_out', 'recent_contact', 'meeting_booked'):
        errors.append('current contact state missing')
    if contact == 'clear' and not action.get('current_contact_source_id'):
        errors.append('clear contact state lacks source evidence')
    next_current = action.get('current_next_activity_at')
    if next_current is not None and not instant(next_current):
        errors.append('current next activity time invalid')
    keys = action.get('existing_action_keys')
    if not action.get('action_key') or not isinstance(keys, list) or any(not isinstance(key, str) or not key for key in keys) or action.get('action_key') in keys or len(keys) != len(set(keys)):
        errors.append('duplicate or missing action key')
    drift = action.get('current_stage') != exception.get('current_stage') or action.get('current_owner_id') != exception.get('owner_id')
    current_age = age_days(action_observed, action_last)
    no_longer_stale = current_age is None or type(threshold) is not int or current_age < threshold or next_current is not None
    resolution = action.get('resolution_state')
    proposal = action.get('proposed_action')
    if resolution not in ('pending_review', 'owner_reviewed', 'recheck') or proposal not in ('seller_review', 'contact_review', 'no_action'):
        errors.append('invalid seller decision or proposal state')
    if (drift or no_longer_stale) and (resolution != 'recheck' or proposal != 'no_action'):
        errors.append('changed or no-longer-stale opportunity requires recheck')
    if proposal == 'contact_review' and contact != 'clear':
        errors.append('contact review blocked by suppression or unknown contact state')
    if resolution == 'owner_reviewed' and not instant(action.get('owner_decided_at')):
        errors.append('owner-reviewed action lacks decision time')
    if resolution != 'owner_reviewed' and action.get('owner_decided_at'):
        errors.append('pending or recheck action claims owner decision')
    if not instant(action.get('next_check_at')):
        errors.append('next check time missing')

    external = action.get('external_action_state')
    if external == 'none':
        if action.get('external_action_approval') or action.get('external_action_receipt'):
            errors.append('no-action state claims approval or provider receipt')
    elif external in ('crm_updated', 'message_sent'):
        approval = action.get('external_action_approval')
        receipt = action.get('external_action_receipt')
        valid_approval = isinstance(approval, dict) and approval.get('owner_id') == action.get('current_owner_id') and approval.get('opportunity_id') == action.get('opportunity_id') and approval.get('action_key') == action.get('action_key') and approval.get('action_type') == external and instant(approval.get('approved_at'))
        valid_receipt = isinstance(receipt, dict) and receipt.get('opportunity_id') == action.get('opportunity_id') and receipt.get('action_key') == action.get('action_key') and receipt.get('action_type') == external and receipt.get('provider_id')
        if resolution != 'owner_reviewed' or not valid_approval or not valid_receipt or (external == 'message_sent' and (contact != 'clear' or proposal != 'contact_review')):
            errors.append('external action lacks exact seller approval, contact clearance or provider receipt')
    else:
        errors.append('invalid external action state')
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('exception', type=Path)
    parser.add_argument('action', type=Path)
    args = parser.parse_args()
    errors = validate(json.loads(args.exception.read_text()), json.loads(args.action.read_text()))
    if errors:
        for error in errors:
            print(f'ERROR: {error}')
        return 1
    print('Pipeline exception and seller action valid')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
