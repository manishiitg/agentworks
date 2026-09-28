#!/usr/bin/env python3
"""Exercise onboarding, observed adoption and optional health handoffs."""

import copy
import json
import unittest
from pathlib import Path

from validate_customer_success_artifact import (
    InvalidArtifact, validate_adoption, validate_health, validate_onboarding,
)

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class CustomerSuccessContractTests(unittest.TestCase):
    def test_accepted_signed_deal_must_join_exact_onboarding_register(self):
        register = fixture("onboarding-milestone-register.json")
        register["purchased_scope"] = ["Business plan"]
        register["first_value_goal"] = "Customer exports a production project report"
        register["source_refs"][0]["uri"] = "accepted/acceptance-artifact-1"
        acceptance = {
            "artifact_type": "onboarding-acceptance/v1", "artifact_id": "acceptance-artifact-1", "decision": "accepted",
            "tenant_id": register["tenant_id"], "customer_account_id": register["account_id"],
            "accepted_scope": ["Business plan"], "first_value_rule": register["first_value_rule"]["id"],
            "first_value_goal": register["first_value_goal"],
            "target_at": register["first_value_rule"]["target_at"], "receiver_owner_id": "cs-1",
            "handoff_key": "handoff-1", "contract_revision": "r1",
            "owner_acceptance": {"owner_id": "cs-1", "handoff_key": "handoff-1", "contract_revision": "r1", "decision": "accepted", "decided_at": "2026-09-25T09:30:00Z"},
        }
        validate_onboarding(register, acceptance)
        for field, value in (("decision", "needs_resolution"), ("customer_account_id", "other"), ("accepted_scope", ["Enterprise plan"]), ("first_value_goal", "Different promise"), ("first_value_rule", "different-rule"), ("artifact_id", "different-artifact")):
            rejected = copy.deepcopy(acceptance)
            rejected[field] = value
            with self.subTest(field=field), self.assertRaises(InvalidArtifact):
                validate_onboarding(register, rejected)

    def test_good_onboarding_to_health(self):
        onboarding = fixture("onboarding-milestone-register.json")
        adoption = fixture("first-value-readout.json")
        validate_onboarding(onboarding)
        validate_adoption(adoption, onboarding)
        validate_health(fixture("customer-health-brief.json"), onboarding, adoption)

    def test_rejected_first_value_claim(self):
        with self.assertRaises(InvalidArtifact):
            validate_adoption(
                fixture("invalid-first-value-readout.json"),
                fixture("onboarding-milestone-register.json"),
            )

    def test_wrong_account_cannot_reach_first_value(self):
        adoption = fixture("first-value-readout.json")
        adoption["account_id"] = "other-account"
        with self.assertRaisesRegex(InvalidArtifact, "account_id differs"):
            validate_adoption(adoption, fixture("onboarding-milestone-register.json"))


if __name__ == "__main__":
    unittest.main()
