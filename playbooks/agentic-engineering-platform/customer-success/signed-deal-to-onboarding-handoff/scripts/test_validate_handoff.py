#!/usr/bin/env python3
"""Falsify signed-deal and receiving-acceptance claims."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_handoff, validate_pair

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class SignedDealHandoffTests(unittest.TestCase):
    def setUp(self):
        self.handoff = fixture("sales-cs-handoff.json")
        self.acceptance = fixture("onboarding-acceptance.json")

    def assert_rejected(self, handoff, acceptance, phrase):
        self.assertTrue(any(phrase in error for error in validate_pair(handoff, acceptance)))

    def test_fictional_accepted_and_pending_cases(self):
        self.assertEqual(validate_handoff(self.handoff), [])
        self.assertEqual(validate_pair(self.handoff, self.acceptance), [])
        self.assertEqual(validate_pair(self.handoff, fixture("needs-resolution.json")), [])

    def test_closed_won_without_execution_is_blocked(self):
        handoff = copy.deepcopy(self.handoff)
        handoff["contract_status"] = "draft"
        handoff["signed_at"] = None
        self.assertTrue(any("ready handoff" in error for error in validate_handoff(handoff)))
        handoff["handoff_state"] = "blocked"
        handoff["blockers"] = ["Agreement not executed"]
        self.assertEqual(validate_handoff(handoff), [])
        self.assert_rejected(handoff, self.acceptance, "false acceptance")

    def test_missing_first_value_terms_can_be_recorded_as_blocked(self):
        handoff = copy.deepcopy(self.handoff)
        handoff["first_value_goal"] = None
        handoff["first_value_rule"] = None
        handoff["target_at"] = None
        handoff["source_refs"].pop("first_value")
        handoff["handoff_state"] = "blocked"
        handoff["blockers"] = ["Executed agreement has no approved first-value term"]
        self.assertEqual(validate_handoff(handoff), [])
        review = copy.deepcopy(self.acceptance)
        review["decision"] = "needs_resolution"
        review["blockers"] = ["First-value term needs owner agreement"]
        review["owner_acceptance"] = None
        review["first_value_goal"] = None
        review["first_value_rule"] = None
        review["target_at"] = None
        self.assertEqual(validate_pair(handoff, review), [])

    def test_drift_and_unready_entitlement_cannot_be_accepted(self):
        self.assert_rejected(self.handoff, fixture("invalid-onboarding-acceptance.json"), "false acceptance")
        for field, value in (("current_contract_revision", "r4"), ("current_crm_revision", "crm-89"), ("current_entitlement_state", "pending"), ("accepted_scope", ["Business plan"])):
            acceptance = copy.deepcopy(self.acceptance)
            acceptance[field] = value
            with self.subTest(field=field):
                self.assert_rejected(self.handoff, acceptance, "false acceptance")

    def test_wrong_account_or_upstream_artifact_stops_handoff(self):
        for field, value in (("customer_account_id", "other-customer"), ("contract_id", "other-contract"), ("source_handoff_artifact_id", "other-artifact")):
            acceptance = copy.deepcopy(self.acceptance)
            acceptance[field] = value
            with self.subTest(field=field):
                self.assertNotEqual(validate_pair(self.handoff, acceptance), [])

    def test_duplicate_key_and_false_owner_receipt_stop_handoff(self):
        handoff = copy.deepcopy(self.handoff)
        handoff["existing_handoff_keys"] = [handoff["handoff_key"]]
        self.assertTrue(any("duplicate" in error for error in validate_handoff(handoff)))
        acceptance = copy.deepcopy(self.acceptance)
        acceptance["owner_acceptance"]["owner_id"] = "another-owner"
        self.assert_rejected(self.handoff, acceptance, "false acceptance")

    def test_acceptance_is_not_customer_action_or_first_value(self):
        acceptance = copy.deepcopy(self.acceptance)
        acceptance["first_value_observed"] = True
        self.assert_rejected(self.handoff, acceptance, "cannot claim customer action")
        acceptance = copy.deepcopy(self.acceptance)
        acceptance["customer_action_state"] = "provisioned"
        self.assert_rejected(self.handoff, acceptance, "cannot claim customer action")


if __name__ == "__main__":
    unittest.main()
