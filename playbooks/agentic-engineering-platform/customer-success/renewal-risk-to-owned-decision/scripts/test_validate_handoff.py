#!/usr/bin/env python3
"""Falsify account, notice, billing and renewal-action claims."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_health, validate_pair

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class RenewalHandoffTests(unittest.TestCase):
    def setUp(self):
        self.health = fixture("renewal-health-brief.json")
        self.decision = fixture("renewal-decision-register.json")

    def assert_rejected(self, health, decision, phrase):
        self.assertTrue(any(phrase in error for error in validate_pair(health, decision)))

    def test_valid_pending_unknown_and_missing_terms(self):
        self.assertEqual(validate_health(self.health), [])
        self.assertEqual(validate_pair(self.health, self.decision), [])
        unknown = fixture("renewal-health-unknown.json")
        decision = copy.deepcopy(self.decision)
        decision["source_health_artifact_id"] = unknown["artifact_id"]
        self.assertEqual(validate_pair(unknown, decision), [])
        self.assertEqual(validate_pair(self.health, fixture("renewal-terms-pending.json")), [])

    def test_wrong_account_or_artifact_stops_handoff(self):
        for field, value in (("tenant_id", "other-tenant"), ("account_id", "other-account"), ("source_health_artifact_id", "other-artifact")):
            decision = copy.deepcopy(self.decision)
            decision[field] = value
            with self.subTest(field=field):
                self.assertNotEqual(validate_pair(self.health, decision), [])

    def test_notice_arithmetic_and_timezone_are_exact(self):
        for field, value in (("notice_deadline_at", "2026-10-03T00:00:00Z"), ("days_to_notice", 7), ("notice_period_days", 30)):
            decision = copy.deepcopy(self.decision)
            decision[field] = value
            with self.subTest(field=field):
                self.assert_rejected(self.health, decision, "arithmetic invalid")
        decision = copy.deepcopy(self.decision)
        decision["renewal_at"] = "2026-12-01T05:00:00Z"
        decision["notice_timezone"] = "America/New_York"
        decision["notice_deadline_at"] = "2026-10-02T04:00:00Z"
        self.assertEqual(validate_pair(self.health, decision), [])
        decision["notice_rule_type"] = "business_days"
        self.assert_rejected(self.health, decision, "reviewable renewal lacks executed terms")

    def test_owner_review_requires_exact_receipt(self):
        decision = copy.deepcopy(self.decision)
        decision["decision_state"] = "owner_reviewed"
        self.assert_rejected(self.health, decision, "owner-reviewed renewal")
        decision["owner_decision"] = {
            "owner_id": "renewals-1", "contract_id": "C-42", "contract_revision": "r3",
            "case_key": decision["case_key"], "choice": "investigate", "decided_at": "2026-09-26T12:30:00Z",
        }
        self.assertEqual(validate_pair(self.health, decision), [])
        decision["owner_decision"]["contract_revision"] = "r4"
        self.assert_rejected(self.health, decision, "owner-reviewed renewal")

    def test_false_renewal_or_customer_action_fails(self):
        self.assert_rejected(self.health, fixture("invalid-renewal-decision.json"), "unverified outcome")
        health = copy.deepcopy(self.health)
        health["intent_claim"] = "will_churn"
        self.assertTrue(any("churn intent" in error for error in validate_health(health)))
        health = copy.deepcopy(self.health)
        health["signals"][0]["occurred_at"] = "2025-09-25T11:15:00Z"
        self.assertTrue(any("outside bounded window" in error for error in validate_health(health)))

    def test_duplicate_case_and_unsupported_no_notice(self):
        decision = copy.deepcopy(self.decision)
        decision["existing_case_keys"] = [decision["case_key"]]
        self.assert_rejected(self.health, decision, "duplicate")
        decision["case_operation"] = "update"
        decision["prior_case_artifact_id"] = "renewal-register-acme-42-0"
        self.assertEqual(validate_pair(self.health, decision), [])
        decision["prior_case_artifact_id"] = decision["artifact_id"]
        self.assert_rejected(self.health, decision, "prior case evidence")
        decision = copy.deepcopy(self.decision)
        decision["notice_state"] = "observed"
        self.assert_rejected(self.health, decision, "provider evidence")


if __name__ == "__main__":
    unittest.main()
