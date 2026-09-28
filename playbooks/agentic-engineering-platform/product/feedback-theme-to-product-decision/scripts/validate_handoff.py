#!/usr/bin/env python3
"""Validate bounded feedback theme -> product owner decision artifacts."""
import argparse
import json
from pathlib import Path

SCOPE = ('tenant_id', 'product_id', 'theme_id', 'segment_id', 'period_start', 'period_end')
DECISIONS = {'pending_review', 'investigate', 'link_existing', 'defer', 'decline'}


def validate(theme: dict, decision: dict) -> list[str]:
    errors = []
    if theme.get('artifact_type') != 'feedback-theme-brief/v1' or decision.get('artifact_type') != 'product-feedback-decision/v1':
        errors.append('wrong artifact type')
    for key in SCOPE:
        if not theme.get(key) or decision.get(key) != theme.get(key):
            errors.append(f'scope mismatch or missing {key}')
    if not theme.get('artifact_id') or decision.get('theme_artifact_id') != theme.get('artifact_id'):
        errors.append('Product did not cite exact theme artifact')
    ids = theme.get('feedback_ids')
    if not isinstance(ids, list) or not ids or any(not isinstance(value, str) or not value for value in ids) or len(ids) != len(set(ids)):
        errors.append('feedback IDs missing or duplicated')
    numerator = theme.get('unique_reporters')
    denominator = theme.get('eligible_responses')
    if type(numerator) is not int or type(denominator) is not int or numerator <= 0 or denominator <= 0 or numerator > denominator or (isinstance(ids, list) and numerator != len(ids)):
        errors.append('theme count or denominator invalid')
    if not theme.get('source_channel') or not theme.get('coverage_note') or not theme.get('source_observed_at') or not theme.get('dedupe_rule'):
        errors.append('theme source coverage missing')
    if not theme.get('privacy_scope') or theme.get('review_state') != 'support_owner_reviewed':
        errors.append('theme privacy scope or Support review missing')
    refs = theme.get('representative_refs')
    if not isinstance(refs, list) or not refs or not isinstance(ids, list) or any(ref not in ids for ref in refs):
        errors.append('representative references are not in the bounded feedback set')
    if theme.get('population_claim') != 'bounded_segment':
        errors.append('theme makes unsupported population claim')
    if decision.get('theme_unique_reporters') != numerator or decision.get('theme_eligible_responses') != denominator or decision.get('theme_coverage_note') != theme.get('coverage_note'):
        errors.append('Product decision changed theme count, denominator or coverage')
    if not decision.get('product_source_id') or not decision.get('product_observed_at') or not decision.get('owner_id') or not isinstance(decision.get('evidence_gaps'), list):
        errors.append('Product source, owner or evidence-gap review missing')
    if decision.get('decision_state') not in DECISIONS:
        errors.append('invalid Product decision state')
    if decision.get('decision_state') != 'pending_review' and not decision.get('owner_decided_at'):
        errors.append('Product decision lacks owner timestamp')
    if decision.get('decision_state') == 'pending_review' and decision.get('owner_decided_at'):
        errors.append('pending Product decision claims owner approval')
    match = decision.get('issue_match')
    if not isinstance(match, dict) or match.get('status') not in ('none', 'related_not_exact', 'exact'):
        errors.append('issue match state missing')
    elif match['status'] == 'none' and match.get('issue_id'):
        errors.append('no-match state carries an issue ID')
    elif match['status'] != 'none' and (not match.get('issue_id') or not match.get('source_id')):
        errors.append('matched issue lacks current source evidence')
    if decision.get('decision_state') == 'link_existing' and (not isinstance(match, dict) or match.get('status') != 'exact'):
        errors.append('link-existing decision lacks an exact issue match')
    action = decision.get('issue_action_state')
    if action == 'none':
        if decision.get('issue_action_receipt') or decision.get('issue_action_approval'):
            errors.append('no-action state claims approval or a provider receipt')
    elif action in ('created', 'updated'):
        receipt = decision.get('issue_action_receipt')
        approval = decision.get('issue_action_approval')
        if not decision.get('owner_decided_at') or not isinstance(approval, dict) or approval.get('owner_id') != decision.get('owner_id') or approval.get('theme_id') != theme.get('theme_id') or approval.get('state') != action or not approval.get('issue_id') or not approval.get('approved_at') or not isinstance(receipt, dict) or receipt.get('state') != action or receipt.get('theme_id') != theme.get('theme_id') or receipt.get('issue_id') != approval.get('issue_id') or not receipt.get('provider_id'):
            errors.append('issue action lacks owner decision and exact provider receipt')
    else:
        errors.append('invalid issue action state')
    if decision.get('customer_promise') is not False:
        errors.append('customer promise cannot be inferred from Product decision')
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('theme', type=Path)
    parser.add_argument('decision', type=Path)
    args = parser.parse_args()
    errors = validate(json.loads(args.theme.read_text()), json.loads(args.decision.read_text()))
    if errors:
        for error in errors:
            print(f'ERROR: {error}')
        return 1
    print('Feedback theme and Product decision valid')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
