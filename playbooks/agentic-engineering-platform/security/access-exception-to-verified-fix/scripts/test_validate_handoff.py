#!/usr/bin/env python3
"""Executable positive and negative cases for the access-remediation handoff."""

import copy
import json
import sys
from pathlib import Path

from validate_handoff import validate_matrix, validate_remediation


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def read(name):
    return json.loads((EXAMPLES / name).read_text())


def rejects(callback, label):
    try:
        callback()
    except ValueError:
        return
    raise AssertionError(f"accepted invalid {label}")


def main():
    matrix = read("access-review-matrix.json")
    pending = read("access-remediation-pending.json")
    verified = read("access-remediation-verified.json")
    validate_matrix(matrix)
    validate_remediation(matrix, pending)
    validate_remediation(matrix, verified)
    rejects(lambda: validate_remediation(matrix, read("invalid-access-remediation.json")), "merge-only closure")

    invalid = copy.deepcopy(matrix)
    invalid.pop("direct_attempt_ref")
    rejects(lambda: validate_matrix(invalid), "UI-only claim")
    invalid = copy.deepcopy(matrix)
    invalid["direct_http_status"] = 403
    rejects(lambda: validate_matrix(invalid), "no actual exception")
    invalid = copy.deepcopy(matrix)
    invalid["policy_cells"][0]["resource_tenant_id"] = "tenant-a"
    rejects(lambda: validate_matrix(invalid), "policy cell mismatch")
    invalid = copy.deepcopy(matrix)
    invalid["observed_build"] = "sha:other"
    rejects(lambda: validate_remediation(invalid, pending), "build mismatch")

    invalid = copy.deepcopy(verified)
    invalid["deployed_build"] = "build:old"
    rejects(lambda: validate_remediation(matrix, invalid), "wrong deployed build")
    invalid = copy.deepcopy(verified)
    invalid["retest_cell_id"] = "other-cell"
    rejects(lambda: validate_remediation(matrix, invalid), "wrong-cell retest")
    invalid = copy.deepcopy(verified)
    invalid["retest_actor_id"] = "other-actor"
    rejects(lambda: validate_remediation(matrix, invalid), "wrong-actor retest")
    invalid = copy.deepcopy(verified)
    invalid["retest_http_status"] = 200
    rejects(lambda: validate_remediation(matrix, invalid), "false passing retest")
    invalid = copy.deepcopy(verified)
    invalid["retest_reviewer_id"] = invalid["owner_id"]
    rejects(lambda: validate_remediation(matrix, invalid), "self-reviewed closure")
    invalid = copy.deepcopy(pending)
    invalid["retest_run_id"] = "retest:invented"
    rejects(lambda: validate_remediation(matrix, invalid), "premature retest claim")
    invalid = copy.deepcopy(verified)
    invalid["source_refs"].remove(invalid["closure_ref"])
    rejects(lambda: validate_remediation(matrix, invalid), "closure without source receipt")
    print("validated access remediation contract examples")


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, ValueError) as exc:
        print(exc, file=sys.stderr)
        raise SystemExit(1) from exc
