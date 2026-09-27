"""Validate exact, source-bound handoffs in the additional category Playbooks.

A structurally valid artifact is not proof that a customer's live source or
named owner has been checked. That work remains in the pending setup checks.
"""
from __future__ import annotations

import json
from datetime import datetime
from pathlib import Path

IDENTITY = {
    'release-to-observed-adoption': ('product_id', 'feature_id', 'release_id'),
    'accepted-offer-to-launch-readiness': ('offer_id', 'plan_id', 'price_revision'),
    'verified-knowledge-to-case-reply': ('product_id', 'article_id', 'case_id'),
    'performance-regression-to-owned-change': ('service_id', 'budget_revision', 'build_id'),
}


def text(value: object, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f'{label} must be a nonempty string')
    return value


def refs(value: object, label: str) -> list[str]:
    if not isinstance(value, list) or not value:
        raise ValueError(f'{label} needs source references')
    values = [text(item, label) for item in value]
    if len(values) != len(set(values)):
        raise ValueError(f'{label} has duplicate references')
    return values


def time(value: object, label: str) -> datetime:
    try:
        result = datetime.fromisoformat(text(value, label).replace('Z', '+00:00'))
    except ValueError as exc:
        raise ValueError(f'{label} must be an ISO timestamp') from exc
    if result.tzinfo is None:
        raise ValueError(f'{label} needs a timezone')
    return result


def count(value: object, label: str) -> int:
    if type(value) is not int or value < 0:
        raise ValueError(f'{label} must be a nonnegative integer')
    return value


def validate(package: Path, artifacts: list[dict]) -> None:
    manifest = json.loads((package / 'playbook.json').read_text())
    route_id = manifest['id']
    identity = IDENTITY[route_id]
    slots = manifest['agent_slots']
    if len(artifacts) != len(slots):
        raise ValueError('one artifact is required for every Crew slot')
    previous = None
    for slot, artifact in zip(slots, artifacts):
        label = slot['id']
        if not isinstance(artifact, dict) or artifact.get('artifact_type') != slot['output']:
            raise ValueError(f'{label} has the wrong artifact type')
        if artifact.get('producer_id') != slot['agent_playbook_id']:
            raise ValueError(f'{label} has the wrong Crew producer')
        for field in ('artifact_id', 'tenant_id', 'subject_id', 'source_revision', *identity):
            text(artifact.get(field), f'{label}.{field}')
        refs(artifact.get('source_refs'), f'{label}.source_refs')
        observed = time(artifact.get('observed_at'), f'{label}.observed_at')
        if artifact.get('state') in ('accepted', 'approved'):
            text(artifact.get('approval_ref'), f'{label}.approval_ref')
        if artifact.get('action_state') == 'executed':
            text(artifact.get('provider_receipt'), f'{label}.provider_receipt')
        if previous is not None:
            for field in ('tenant_id', 'subject_id', *identity):
                if artifact[field] != previous[field]:
                    raise ValueError(f'{label}.{field} differs from upstream')
            if artifact.get('input_artifact_id') != previous['artifact_id']:
                raise ValueError(f'{label} does not cite the exact upstream artifact')
            if artifact.get('input_revision') != previous['source_revision']:
                raise ValueError(f'{label} does not cite the upstream revision')
            if observed < time(previous['observed_at'], 'upstream.observed_at'):
                raise ValueError(f'{label} predates upstream evidence')
        previous = artifact

    first, second = artifacts[:2]
    if route_id == 'release-to-observed-adoption':
        if (first.get('state') != 'verified' or first.get('decision') != 'go'
                or first.get('qa_gate_state') != 'pass' or first.get('release_state') != 'verified'):
            raise ValueError('adoption needs a passing, verified release')
        for field in ('qa_gate_ref', 'deployment_ref', 'flag_revision', 'rollout_approval_ref', 'measurement_rule'):
            text(first.get(field), f'release.{field}')
        eligible = count(second.get('eligible_count'), 'adoption.eligible_count')
        exposed = count(second.get('exposed_count'), 'adoption.exposed_count')
        using = count(second.get('using_count'), 'adoption.using_count')
        if using > exposed or exposed > eligible or second.get('state') != 'observed':
            raise ValueError('adoption counts must be observed and nested')
        if second.get('event_rule_revision') != first['measurement_rule']:
            raise ValueError('adoption event rule differs from release plan')
        text(second.get('observation_window'), 'adoption.observation_window')
        if second.get('identity_coverage') not in ('complete', 'partial', 'unknown') or second.get('causal_claim') != 'none' or second.get('claim_scope') != 'observed_only':
            raise ValueError('adoption coverage or causal claim is unsupported')
    elif route_id == 'accepted-offer-to-launch-readiness':
        third = artifacts[2]
        if first.get('state') != 'accepted':
            raise ValueError('positioning needs a pricing-owner accepted offer')
        for field in ('entitlement_revision', 'unit_economics_ref'):
            text(first.get(field), f'pricing.{field}')
        if second.get('state') != 'reviewed':
            raise ValueError('strategy needs reviewed buyer evidence')
        buyer_evidence = refs(second.get('buyer_evidence_refs'), 'strategy.buyer_evidence_refs')
        if not set(buyer_evidence).issubset(second['source_refs']):
            raise ValueError('strategy buyer evidence lacks source references')
        strategy_claims = refs(second.get('claim_refs'), 'strategy.claim_refs')
        launch_claims = refs(third.get('claim_refs'), 'launch.claim_refs')
        if not set(strategy_claims).issubset(second['source_refs']):
            raise ValueError('strategy has a claim without its source')
        if not set(launch_claims).issubset(third['source_refs']) or not set(launch_claims).issubset(strategy_claims):
            raise ValueError('launch claims differ from sourced positioning')
        refs(third.get('asset_revisions'), 'launch.asset_revisions')
        text(third.get('channel_policy_ref'), 'launch.channel_policy_ref')
        if third.get('state') != 'pending_review' or third.get('launch_state') != 'unpublished':
            raise ValueError('launch plan must remain pending and unpublished')
    elif route_id == 'verified-knowledge-to-case-reply':
        if first.get('state') != 'verified' or first.get('publication_state') != 'observed':
            raise ValueError('reply needs observed live knowledge')
        for field in ('article_revision', 'publication_receipt', 'live_url', 'locale'):
            text(first.get(field), f'knowledge.{field}')
        if first['publication_receipt'] not in first['source_refs']:
            raise ValueError('live article lacks its publication receipt source')
        if (second.get('state') != 'pending_review' or second.get('article_revision') != first['article_revision']
                or second.get('case_state') != 'open' or second.get('suppression_state') != 'clear'
                or second.get('send_state') != 'unsent'):
            raise ValueError('reply must use the live revision on an open permitted case and stay unsent')
        for field in ('recipient_id', 'contact_policy_ref'):
            text(second.get(field), f'reply.{field}')
        if first['article_revision'] not in refs(second.get('reply_source_refs'), 'reply.reply_source_refs') or first['article_revision'] not in second['source_refs']:
            raise ValueError('reply lacks the exact published article revision')
    elif route_id == 'performance-regression-to-owned-change':
        if (first.get('state') != 'reviewed' or first.get('comparison_state') != 'comparable'
                or first.get('regression_state') != 'observed'):
            raise ValueError('delivery needs a comparable observed regression')
        baseline = text(first.get('baseline_ref'), 'investigation.baseline_ref')
        current = text(first.get('current_ref'), 'investigation.current_ref')
        if baseline == current or not {baseline, current}.issubset(first['source_refs']):
            raise ValueError('performance comparison needs distinct, source-linked baseline and current evidence')
        text(first.get('hypothesis'), 'investigation.hypothesis')
        for field in ('issue_ref', 'change_owner', 'proposed_action'):
            text(second.get(field), f'delivery.{field}')
        if (second.get('state') != 'pending_review' or second.get('deployment_state') != 'not_deployed'
                or second.get('recovery_state') != 'unverified'):
            raise ValueError('a proposed change is not a deployment or recovery')
