#!/usr/bin/env python3
"""Check access-cell and remediation artifact joins; provider and policy truth remain external."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("tenant_id", "scope_ref", "policy_ref", "policy_version", "environment", "observed_build", "matrix_id", "selected_cell_id", "actor_id", "actor_role", "actor_tenant_id", "resource_type", "resource_id", "resource_tenant_id", "action", "expected_decision")


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
        raise ValueError(f"{label} needs source IDs")
    for index, ref in enumerate(value):
        nonempty(ref, f"{label}[{index}]")
    if len(value) != len(set(value)):
        raise ValueError(f"{label} IDs must be unique")


def cited(value, sources, label):
    nonempty(value, label)
    if value not in sources:
        raise ValueError(f"{label} lacks source citation")


def access_decision(status):
    if isinstance(status, bool) or not isinstance(status, int):
        raise ValueError("direct HTTP status must be an integer")
    if 200 <= status < 300:
        return "allow"
    if status in (401, 403):
        return "deny"
    raise ValueError("direct HTTP status cannot prove allow or deny")


def validate_matrix(matrix):
    if not isinstance(matrix, dict) or matrix.get("artifact_type") != "access-review-matrix/v1":
        raise ValueError("expected access-review-matrix/v1")
    for field in IDENTITY:
        nonempty(matrix.get(field), f"matrix.{field}")
    if matrix["expected_decision"] not in ("allow", "deny"):
        raise ValueError("expected decision must be allow or deny")
    timestamp(matrix.get("observed_at"), "matrix.observed_at")
    refs(matrix.get("source_refs"), "matrix.source_refs")
    for field in ("scope_ref", "policy_ref", "direct_attempt_ref"):
        cited(matrix.get(field), matrix["source_refs"], f"matrix.{field}")
    nonempty(matrix.get("owner_id"), "matrix.owner_id")
    if not isinstance(matrix.get("policy_cells"), list) or not matrix["policy_cells"]:
        raise ValueError("matrix needs predeclared policy cells")
    selected = [cell for cell in matrix["policy_cells"] if isinstance(cell, dict) and cell.get("cell_id") == matrix["selected_cell_id"]]
    if len(selected) != 1:
        raise ValueError("selected policy cell must exist exactly once")
    for field in ("actor_role", "actor_tenant_id", "resource_type", "resource_id", "resource_tenant_id", "action", "expected_decision"):
        if selected[0].get(field) != matrix[field]:
            raise ValueError(f"selected cell {field} mismatch")
    observed = access_decision(matrix.get("direct_http_status"))
    if matrix.get("observed_decision") != observed:
        raise ValueError("observed decision does not match direct HTTP status")
    if observed == matrix["expected_decision"]:
        raise ValueError("selected cell is not an observed exception")
    if matrix["expected_decision"] == "deny" and matrix["actor_tenant_id"] != matrix["resource_tenant_id"]:
        if not matrix.get("isolated_fixture"):
            raise ValueError("cross-tenant case needs isolated fixture declaration")


def validate_remediation(matrix, ledger):
    validate_matrix(matrix)
    if not isinstance(ledger, dict) or ledger.get("artifact_type") != "access-remediation-ledger/v1":
        raise ValueError("expected access-remediation-ledger/v1")
    for field in IDENTITY:
        if ledger.get(field) != matrix[field]:
            raise ValueError(f"handoff {field} mismatch")
    for field in ("issue_id", "owner_id", "next_action"):
        nonempty(ledger.get(field), f"ledger.{field}")
    observed = timestamp(ledger.get("observed_at"), "ledger.observed_at")
    if observed < timestamp(matrix["observed_at"], "matrix.observed_at"):
        raise ValueError("ledger predates access observation")
    refs(ledger.get("source_refs"), "ledger.source_refs")
    cited(ledger["issue_id"], ledger["source_refs"], "ledger.issue_id")
    if ledger.get("change_state") not in ("not_started", "proposed", "approved", "merged"):
        raise ValueError("invalid change state")
    if ledger.get("deployment_state") not in ("not_deployed", "deployed"):
        raise ValueError("invalid deployment state")
    if ledger.get("retest_state") not in ("not_run", "blocked", "fail", "pass"):
        raise ValueError("invalid retest state")
    if ledger.get("disposition") not in ("open", "closed"):
        raise ValueError("invalid disposition")
    if ledger["change_state"] in ("approved", "merged"):
        nonempty(ledger.get("change_sha"), "ledger.change_sha")
        cited(ledger.get("change_approval_ref"), ledger["source_refs"], "ledger.change_approval_ref")
    if ledger["deployment_state"] == "deployed":
        if ledger["change_state"] != "merged":
            raise ValueError("deployment needs merged approved change")
        for field in ("deployment_id", "deployed_build", "deployment_source_sha", "deployed_at"):
            nonempty(ledger.get(field), f"ledger.{field}")
        if ledger["deployment_source_sha"] != ledger["change_sha"]:
            raise ValueError("deployed source SHA does not match approved change")
        if timestamp(ledger["deployed_at"], "ledger.deployed_at") <= timestamp(matrix["observed_at"], "matrix.observed_at"):
            raise ValueError("deployment must follow access observation")
        cited(ledger["deployment_id"], ledger["source_refs"], "ledger.deployment_id")
    elif any(ledger.get(field) for field in ("deployment_id", "deployed_build", "deployment_source_sha", "deployed_at")):
        raise ValueError("not_deployed cannot claim a deployment")
    if ledger["retest_state"] != "not_run":
        if ledger["deployment_state"] != "deployed":
            raise ValueError("retest needs affected deployed build")
        for field in ("retest_run_id", "retest_build", "retest_cell_id", "retest_actor_id", "retest_reviewer_id", "retest_at"):
            nonempty(ledger.get(field), f"ledger.{field}")
        if ledger["retest_build"] != ledger["deployed_build"] or ledger["retest_cell_id"] != matrix["selected_cell_id"] or ledger["retest_actor_id"] != matrix["actor_id"]:
            raise ValueError("retest build, cell or actor mismatch")
        if timestamp(ledger["retest_at"], "ledger.retest_at") <= timestamp(ledger["deployed_at"], "ledger.deployed_at"):
            raise ValueError("retest must follow deployment")
        cited(ledger["retest_run_id"], ledger["source_refs"], "ledger.retest_run_id")
        if ledger["retest_state"] in ("pass", "fail"):
            actual = access_decision(ledger.get("retest_http_status"))
            if (actual == matrix["expected_decision"]) != (ledger["retest_state"] == "pass"):
                raise ValueError("retest verdict contradicts direct HTTP status and policy")
    elif any(ledger.get(field) for field in ("retest_run_id", "retest_build", "retest_cell_id", "retest_actor_id", "retest_at", "retest_http_status")):
        raise ValueError("not_run cannot claim retest evidence")
    if ledger["disposition"] == "closed":
        if ledger["retest_state"] != "pass":
            raise ValueError("closure needs passing same-cell retest")
        if ledger["retest_reviewer_id"] == ledger["owner_id"]:
            raise ValueError("closure needs independent retest reviewer")
        for field in ("closure_approval_ref", "closure_ref"):
            cited(ledger.get(field), ledger["source_refs"], f"ledger.{field}")
    elif ledger.get("closure_ref"):
        raise ValueError("open case cannot claim closure")


def main(argv):
    if (len(argv) != 2 or argv[0] != "matrix") and (len(argv) != 3 or argv[0] != "remediation"):
        raise SystemExit("usage: validate_handoff.py matrix matrix.json | remediation matrix.json ledger.json")
    try:
        matrix = json.loads(Path(argv[1]).read_text())
        if argv[0] == "matrix":
            validate_matrix(matrix)
        else:
            validate_remediation(matrix, json.loads(Path(argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid access handoff: {exc}") from exc
    print(f"valid access {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
