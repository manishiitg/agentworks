#!/usr/bin/env python3
"""Validate security finding/remediation joins and evidence gates, not provider truth."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("tenant_id", "finding_id", "asset_id", "environment", "observed_build", "scope_ref", "policy_version")


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
        raise ValueError(f"{label} needs at least one source ID")
    for index, ref in enumerate(value):
        nonempty(ref, f"{label}[{index}]")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} IDs must be unique")


def required_ref(value, sources, label):
    nonempty(value, label)
    if value not in sources:
        raise ValueError(f"{label} lacks source citation")


def validate_finding(finding):
    if not isinstance(finding, dict) or finding.get("artifact_type") != "security-finding/v1":
        raise ValueError("expected security-finding/v1")
    for field in (*IDENTITY, "owner_id", "verification_criterion"):
        nonempty(finding.get(field), f"finding.{field}")
    timestamp(finding.get("observed_at"), "finding.observed_at")
    refs(finding.get("source_refs"), "finding.source_refs")
    required_ref(finding["scope_ref"], finding["source_refs"], "finding.scope_ref")
    if finding.get("applicability") not in ("unconfirmed", "confirmed", "not_applicable"):
        raise ValueError("finding applicability is invalid")
    if finding.get("severity") not in ("pending", "low", "medium", "high", "critical"):
        raise ValueError("finding severity is invalid")
    if finding["applicability"] == "confirmed":
        required_ref(finding.get("applicability_ref"), finding["source_refs"], "finding.applicability_ref")
        if finding["severity"] == "pending":
            raise ValueError("confirmed finding needs customer severity decision")
    elif finding["severity"] != "pending":
        raise ValueError("unconfirmed or inapplicable finding cannot claim severity")


def validate_remediation(finding, ledger):
    validate_finding(finding)
    if not isinstance(ledger, dict) or ledger.get("artifact_type") != "security-remediation-ledger/v1":
        raise ValueError("expected security-remediation-ledger/v1")
    for field in (*IDENTITY, "issue_id", "owner_id", "next_action"):
        nonempty(ledger.get(field), f"ledger.{field}")
    for field in IDENTITY:
        if ledger[field] != finding[field]:
            raise ValueError(f"handoff {field} mismatch")
    observed = timestamp(ledger.get("observed_at"), "ledger.observed_at")
    if observed < timestamp(finding["observed_at"], "finding.observed_at"):
        raise ValueError("ledger predates finding")
    refs(ledger.get("source_refs"), "ledger.source_refs")
    required_ref(ledger["issue_id"], ledger["source_refs"], "ledger.issue_id")
    if ledger.get("change_state") not in ("not_started", "proposed", "merged", "approved"):
        raise ValueError("change_state is invalid")
    if ledger.get("deployment_state") not in ("not_deployed", "deployed"):
        raise ValueError("deployment_state is invalid")
    if ledger.get("retest_state") not in ("not_run", "blocked", "fail", "pass"):
        raise ValueError("retest_state is invalid")
    if ledger.get("disposition") not in ("open", "closed", "accepted_risk"):
        raise ValueError("disposition is invalid")
    if ledger["change_state"] != "not_started":
        nonempty(ledger.get("change_sha"), "ledger.change_sha")
    if ledger["change_state"] == "approved":
        required_ref(ledger.get("change_approval_ref"), ledger["source_refs"], "ledger.change_approval_ref")
    if ledger["deployment_state"] == "deployed":
        if ledger["change_state"] != "approved":
            raise ValueError("deployment needs reviewed change")
        for field in ("deployment_id", "deployed_build", "deployment_source_sha"):
            nonempty(ledger.get(field), f"ledger.{field}")
        if ledger["deployment_source_sha"] != ledger["change_sha"]:
            raise ValueError("deployed source SHA does not match reviewed change")
        required_ref(ledger["deployment_id"], ledger["source_refs"], "ledger.deployment_id")
    elif ledger.get("deployed_build") or ledger.get("deployment_id"):
        raise ValueError("not_deployed cannot claim deployment")
    if ledger["retest_state"] != "not_run":
        if ledger["deployment_state"] != "deployed":
            raise ValueError("retest needs affected deployed build")
        for field in ("retest_run_id", "retest_build", "retest_criterion", "retest_reviewer_id"):
            nonempty(ledger.get(field), f"ledger.{field}")
        if ledger["retest_build"] != ledger["deployed_build"]:
            raise ValueError("retest build does not match affected deployment")
        if ledger["retest_criterion"] != finding["verification_criterion"]:
            raise ValueError("retest criterion does not match finding")
        required_ref(ledger["retest_run_id"], ledger["source_refs"], "ledger.retest_run_id")
    if ledger["disposition"] == "closed":
        if finding["applicability"] != "confirmed" or ledger["deployment_state"] != "deployed" or ledger["retest_state"] != "pass":
            raise ValueError("closure needs confirmed finding, deployed fix and passing retest")
        if ledger["retest_reviewer_id"] == ledger["owner_id"]:
            raise ValueError("closure needs independent retest reviewer")
        for field in ("closure_approval_ref", "closure_ref"):
            required_ref(ledger.get(field), ledger["source_refs"], f"ledger.{field}")
    if ledger["disposition"] == "accepted_risk":
        if finding["applicability"] != "confirmed":
            raise ValueError("risk acceptance needs confirmed finding")
        for field in ("risk_approval_ref", "risk_scope", "risk_expiry_at"):
            nonempty(ledger.get(field), f"ledger.{field}")
        required_ref(ledger["risk_approval_ref"], ledger["source_refs"], "ledger.risk_approval_ref")
        if timestamp(ledger["risk_expiry_at"], "ledger.risk_expiry_at") <= observed:
            raise ValueError("risk acceptance expiry must be in the future")


def main(argv):
    if len(argv) not in (2, 3) or argv[0] not in ("finding", "remediation"):
        raise SystemExit("usage: validate_handoff.py finding finding.json | remediation finding.json ledger.json")
    if (argv[0] == "finding" and len(argv) != 2) or (argv[0] == "remediation" and len(argv) != 3):
        raise SystemExit("wrong artifact count")
    try:
        finding = json.loads(Path(argv[1]).read_text())
        if argv[0] == "finding":
            validate_finding(finding)
        else:
            validate_remediation(finding, json.loads(Path(argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print(f"valid security {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
