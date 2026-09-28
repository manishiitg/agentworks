#!/usr/bin/env python3
"""Validate an exact-meeting brief -> reviewed sales proposal artifact pair."""
import argparse
import json
from pathlib import Path

SCOPE = ('tenant_id', 'account_id', 'opportunity_id', 'meeting_id')


def validate(brief: dict, proposal: dict) -> list[str]:
    errors = []
    if brief.get('artifact_type') != 'sales-call-brief/v1' or proposal.get('artifact_type') != 'sales-proposal-draft/v1':
        errors.append('wrong artifact type')
    for key in SCOPE:
        if not brief.get(key) or proposal.get(key) != brief.get(key):
            errors.append(f'scope mismatch or missing {key}')
    if not brief.get('artifact_id') or proposal.get('brief_artifact_id') != brief.get('artifact_id'):
        errors.append('proposal does not cite exact brief')
    if not brief.get('calendar_source_id') or not brief.get('crm_source_id') or not brief.get('observed_at') or not brief.get('seller_id'):
        errors.append('brief lacks meeting or CRM evidence')
    facts = brief.get('verified_facts')
    if not isinstance(facts, list) or any(not isinstance(fact, dict) or not fact.get('text') or not fact.get('source_id') for fact in facts):
        errors.append('brief facts lack sources')
    discovery = proposal.get('discovery')
    if not isinstance(discovery, dict) or not discovery.get('note_id') or not discovery.get('approved_by') or not discovery.get('approved_at') or discovery.get('status') != 'approved' or discovery.get('meeting_id') != brief.get('meeting_id'):
        errors.append('proposal lacks approved post-call discovery for exact meeting')
    if not proposal.get('offer_source_id') or not proposal.get('price_version') or not proposal.get('currency') or not proposal.get('commercial_owner_id'):
        errors.append('offer, price, currency or commercial owner missing')
    if proposal.get('approval_state') not in ('pending', 'approved') or proposal.get('delivery_state') != 'unsent' or proposal.get('delivery_receipt') is not None:
        errors.append('proposal falsely claims approval or delivery state')
    claims = proposal.get('scope_claims')
    if not isinstance(claims, list) or not claims or any(not isinstance(claim, dict) or not claim.get('text') or not claim.get('discovery_ref') or not claim.get('product_ref') for claim in claims):
        errors.append('proposal scope claim lacks discovery or product source')
    elif isinstance(discovery, dict):
        for claim in claims:
            if not claim['discovery_ref'].startswith(discovery.get('note_id', '') + ':') or not claim['product_ref'].startswith(proposal.get('offer_source_id', '') + ':'):
                errors.append('proposal claim references a different discovery or offer version')
    lines = proposal.get('line_items')
    if not isinstance(lines, list) or not lines:
        errors.append('proposal lacks line items')
    else:
        total = 0
        for line in lines:
            if not isinstance(line, dict) or not line.get('price_source_id') or type(line.get('quantity')) is not int or line['quantity'] <= 0 or type(line.get('unit_price_minor')) is not int or line['unit_price_minor'] < 0 or type(line.get('line_total_minor')) is not int or line['line_total_minor'] != line['quantity'] * line['unit_price_minor']:
                errors.append('line item lacks exact approved price or arithmetic')
            else:
                if not line['price_source_id'].startswith(proposal.get('price_version', '') + ':'):
                    errors.append('line item references a different price version')
                total += line['line_total_minor']
        if type(proposal.get('subtotal_minor')) is not int or proposal['subtotal_minor'] != total:
            errors.append('proposal subtotal does not equal line items')
    discount = proposal.get('discount_minor')
    if type(discount) is not int or discount < 0:
        errors.append('invalid discount')
    elif discount and (not proposal.get('discount_rule_id') or not proposal.get('discount_approved_by')):
        errors.append('discount lacks rule and approver')
    if type(proposal.get('subtotal_minor')) is int and type(discount) is int and type(proposal.get('total_minor')) is int:
        if proposal['total_minor'] != proposal['subtotal_minor'] - discount or proposal['total_minor'] < 0:
            errors.append('proposal total arithmetic mismatch')
    else:
        errors.append('proposal total missing')
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('brief', type=Path)
    parser.add_argument('proposal', type=Path)
    args = parser.parse_args()
    errors = validate(json.loads(args.brief.read_text()), json.loads(args.proposal.read_text()))
    if errors:
        for error in errors:
            print(f'ERROR: {error}')
        return 1
    print('Call brief and reviewed proposal valid')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
