#!/usr/bin/env python3
"""Validate exact-candidate QA handoffs; external result truth still needs source reads."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("release_id", "sha", "build_id", "environment")
CONTRACT = (*IDENTITY, "policy_version", "journey_revision")


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def timestamp(value, label):
    nonempty(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} needs at least one ID")
    for index, ref in enumerate(value):
        nonempty(ref, f"{label}[{index}]")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} IDs must be unique")


def fields(value, kind, required):
    if not isinstance(value, dict) or value.get("artifact_type") != kind:
        raise ValueError(f"expected {kind}")
    for field in required:
        nonempty(value.get(field), f"{kind}.{field}")
    refs(value.get("source_refs"), f"{kind}.source_refs")


def joined(first, second, keys=CONTRACT):
    for key in keys:
        if first.get(key) != second.get(key):
            raise ValueError(f"handoff {key} mismatch")


def validate_journey(journey):
    fields(journey, "journey-result/v1", (*CONTRACT, "test_id", "attempt_id"))
    timestamp(journey.get("observed_at"), "journey.observed_at")
    if journey.get("status") not in ("pass", "fail", "blocked"):
        raise ValueError("journey status is invalid")
    refs(journey.get("assertion_ids"), "journey.assertion_ids")
    if journey["status"] in ("pass", "fail"):
        refs(journey.get("evidence_refs"), "journey.evidence_refs")
    elif journey.get("evidence_refs") is not None:
        refs(journey["evidence_refs"], "journey.evidence_refs")


def validate_flake(journey, flake):
    validate_journey(journey)
    fields(flake, "flake-investigation/v1", (*CONTRACT, "test_id", "owner_id", "proposed_action"))
    joined(journey, flake, (*CONTRACT, "test_id"))
    if timestamp(flake.get("observed_at"), "flake.observed_at") < timestamp(journey["observed_at"], "journey.observed_at"):
        raise ValueError("flake investigation predates journey attempt")
    attempts = flake.get("attempts")
    if not isinstance(attempts, list) or len(attempts) < 2:
        raise ValueError("flake needs at least two distinct attempts")
    ids = []
    outcomes = set()
    for index, attempt in enumerate(attempts):
        if not isinstance(attempt, dict):
            raise ValueError("flake attempt must be an object")
        for key in ("attempt_id", "source_ref"):
            nonempty(attempt.get(key), f"flake.attempts[{index}].{key}")
        if attempt.get("status") not in ("pass", "fail", "blocked"):
            raise ValueError("flake attempt status is invalid")
        ids.append(attempt["attempt_id"])
        outcomes.add(attempt["status"])
    if len(set(ids)) != len(ids) or journey["attempt_id"] not in ids:
        raise ValueError("flake attempts must be unique and include journey attempt")
    if not {"pass", "fail"}.issubset(outcomes):
        raise ValueError("flake investigation needs observed pass and fail attempts")
    if flake.get("classification") not in ("unresolved", "test_defect", "product_race", "environment"):
        raise ValueError("flake classification is invalid")
    if flake.get("confidence") not in ("low", "medium", "high"):
        raise ValueError("flake confidence is invalid")


def validate_gate(journey, gate, flake=None):
    validate_journey(journey)
    fields(gate, "release-quality-brief/v1", (*CONTRACT, "owner_id"))
    joined(journey, gate)
    if timestamp(gate.get("observed_at"), "gate.observed_at") < timestamp(journey["observed_at"], "journey.observed_at"):
        raise ValueError("gate predates journey evidence")
    gate_time = timestamp(gate["observed_at"], "gate.observed_at")
    required = gate.get("required_suites")
    refs(required, "gate.required_suites")
    results = gate.get("suite_results")
    if not isinstance(results, list):
        raise ValueError("gate suite_results must be a list")
    by_id = {}
    for index, result in enumerate(results):
        if not isinstance(result, dict):
            raise ValueError("suite result must be an object")
        suite_id = result.get("suite_id")
        nonempty(suite_id, f"gate.suite_results[{index}].suite_id")
        if suite_id in by_id:
            raise ValueError("duplicate suite result")
        if result.get("status") not in ("pass", "fail", "blocked", "skipped"):
            raise ValueError("suite status is invalid")
        for key in ("sha", "build_id", "environment"):
            if result.get(key) != gate[key]:
                raise ValueError(f"suite {suite_id} {key} mismatch")
        if timestamp(result.get("observed_at"), f"suite {suite_id}.observed_at") > gate_time:
            raise ValueError(f"suite {suite_id} result postdates gate")
        refs(result.get("attempt_ids"), "suite.attempt_ids")
        refs(result.get("source_refs"), "suite.source_refs")
        if not set(result["source_refs"]).issubset(set(gate["source_refs"])):
            raise ValueError(f"suite {suite_id} lacks gate source citation")
        by_id[suite_id] = result
    if set(by_id) - set(required):
        raise ValueError("gate contains undeclared suite result")
    tested = by_id.get(journey["test_id"])
    expected_statuses = (journey["status"], "blocked") if flake is not None else (journey["status"],)
    if not tested or tested["status"] not in expected_statuses or journey["attempt_id"] not in tested["attempt_ids"]:
        raise ValueError("gate does not contain exact journey attempt and status")
    if not set(journey["source_refs"]).issubset(set(tested["source_refs"])):
        raise ValueError("gate journey result lacks attempt source citation")
    if flake is not None:
        validate_flake(journey, flake)
        if tested["status"] != "blocked" or any(attempt["attempt_id"] not in tested["attempt_ids"] for attempt in flake["attempts"]):
            raise ValueError("gate must show all flaky attempts as blocked")
    if gate.get("verdict") not in ("pass", "fail", "needs_review"):
        raise ValueError("gate verdict is invalid")
    missing = set(required) - set(by_id)
    failures = any(result["status"] == "fail" for result in results)
    nonpassing = missing or any(result["status"] != "pass" for result in results)
    if gate["verdict"] == "pass" and (nonpassing or flake is not None):
        raise ValueError("pass needs all required suites and no open flake investigation")
    if gate["verdict"] == "fail" and not failures:
        raise ValueError("fail needs a failing required suite")
    if gate["verdict"] == "needs_review" and not nonpassing and flake is None:
        nonempty(gate.get("review_reason"), "gate.review_reason")
    if gate.get("approval_state") not in ("pending", "approved", "rejected"):
        raise ValueError("gate approval_state is invalid")
    if gate["approval_state"] == "approved":
        nonempty(gate.get("approval_ref"), "gate.approval_ref")
    if gate.get("publication_state") != "prepared":
        raise ValueError("gate artifact must remain prepared")


def validate_publication(journey, gate, receipt):
    validate_gate(journey, gate)
    if gate["approval_state"] != "approved":
        raise ValueError("publication requires owner-approved gate")
    fields(receipt, "release-status-receipt/v1", (*IDENTITY, "verdict", "approval_ref", "provider_status_id"))
    joined(gate, receipt, IDENTITY)
    if receipt["verdict"] != gate["verdict"] or receipt["approval_ref"] != gate["approval_ref"]:
        raise ValueError("publication verdict or approval mismatch")
    if timestamp(receipt.get("published_at"), "publication.published_at") < timestamp(gate["observed_at"], "gate.observed_at"):
        raise ValueError("publication predates gate")


def main(argv):
    modes = {
        "journey": (1, validate_journey),
        "flake": (2, validate_flake),
        "gate": (2, validate_gate),
        "gate-with-flake": (3, lambda journey, flake, gate: validate_gate(journey, gate, flake)),
        "publication": (3, validate_publication),
    }
    if not argv or argv[0] not in modes or len(argv) != modes[argv[0]][0] + 1:
        raise SystemExit("usage: validate_handoff.py journey|flake|gate|gate-with-flake|publication artifact.json ...")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        modes[argv[0]][1](*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print(f"valid QA {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
