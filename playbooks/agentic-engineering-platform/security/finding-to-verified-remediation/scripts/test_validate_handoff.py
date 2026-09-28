#!/usr/bin/env python3
"""Contract tests for scope, deployment, retest and closure."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_finding, validate_remediation


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def sample(name):
    return json.loads((EXAMPLES / name).read_text())


class SecurityHandoffTests(unittest.TestCase):
    def setUp(self):
        self.finding = sample("security-finding.json")
        self.open = sample("security-remediation-ledger.json")
        self.verified = sample("verified-security-remediation-ledger.json")

    def test_fictional_open_and_verified_contracts(self):
        validate_finding(self.finding)
        validate_remediation(self.finding, self.open)
        validate_remediation(self.finding, self.verified)

    def test_merged_only_closure_fails(self):
        with self.assertRaisesRegex(ValueError, "closure needs"):
            validate_remediation(self.finding, sample("invalid-security-remediation-ledger.json"))

    def test_wrong_asset_and_scope_fail(self):
        ledger = copy.deepcopy(self.open)
        ledger["asset_id"] = "other-asset"
        with self.assertRaisesRegex(ValueError, "asset_id mismatch"):
            validate_remediation(self.finding, ledger)
        finding = copy.deepcopy(self.finding)
        finding["source_refs"].remove(finding["scope_ref"])
        with self.assertRaisesRegex(ValueError, "scope_ref lacks source"):
            validate_finding(finding)

    def test_scanner_only_report_cannot_claim_confirmed_severity(self):
        finding = copy.deepcopy(self.finding)
        finding["applicability"] = "unconfirmed"
        with self.assertRaisesRegex(ValueError, "cannot claim severity"):
            validate_finding(finding)

    def test_deployment_and_retest_must_match_approved_fix(self):
        ledger = copy.deepcopy(self.verified)
        ledger["deployment_source_sha"] = "sha:other"
        with self.assertRaisesRegex(ValueError, "source SHA"):
            validate_remediation(self.finding, ledger)
        ledger = copy.deepcopy(self.verified)
        ledger["retest_build"] = "artifact:other"
        with self.assertRaisesRegex(ValueError, "retest build"):
            validate_remediation(self.finding, ledger)
        ledger = copy.deepcopy(self.verified)
        ledger["retest_reviewer_id"] = ledger["owner_id"]
        with self.assertRaisesRegex(ValueError, "independent"):
            validate_remediation(self.finding, ledger)

    def test_risk_acceptance_needs_expiring_owner_decision(self):
        ledger = copy.deepcopy(self.open)
        ledger["disposition"] = "accepted_risk"
        ledger["risk_scope"] = "checkout-api staging sec-81"
        ledger["risk_approval_ref"] = "risk-approval-81"
        ledger["risk_expiry_at"] = "2026-10-25T00:00:00Z"
        ledger["source_refs"].append("risk-approval-81")
        validate_remediation(self.finding, ledger)
        ledger["risk_expiry_at"] = "2026-09-24T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "expiry"):
            validate_remediation(self.finding, ledger)


if __name__ == "__main__":
    unittest.main()
