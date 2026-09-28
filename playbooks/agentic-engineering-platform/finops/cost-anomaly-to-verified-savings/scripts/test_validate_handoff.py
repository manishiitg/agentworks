#!/usr/bin/env python3
"""Contract checks for cost, change and finance savings states."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_change, validate_cost, validate_savings


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class FinOpsHandoffTests(unittest.TestCase):
    def setUp(self):
        self.cost = fixture("cloud-cost-review.json")
        self.proposal = fixture("cloud-change-review.json")
        self.pending = fixture("cloud-savings-pending.json")
        self.deployed = fixture("cloud-change-deployed.json")
        self.verified = fixture("cloud-savings-verified.json")

    def test_proposal_and_pending_finance_result(self):
        validate_cost(self.cost)
        validate_change(self.cost, self.proposal)
        validate_savings(self.cost, self.proposal, self.pending)

    def test_approved_change_and_comparable_billed_savings(self):
        validate_change(self.cost, self.deployed)
        validate_savings(self.cost, self.deployed, self.verified)

    def test_source_cost_math_and_candidate_range(self):
        changed = copy.deepcopy(self.cost)
        changed["service_cost"]["usage_effect_minor"] = 270000
        with self.assertRaisesRegex(ValueError, "usage effect"):
            validate_cost(changed)
        changed = copy.deepcopy(self.cost)
        changed["candidate"]["projected_max_savings_minor"] = 70000
        with self.assertRaisesRegex(ValueError, "exceeds the resource baseline"):
            validate_cost(changed)
        changed = copy.deepcopy(self.cost)
        changed["service_cost"]["current_unit_price_minor"] = 130
        with self.assertRaisesRegex(ValueError, "usage cost"):
            validate_cost(changed)

    def test_candidate_or_resource_mismatch_blocks_change(self):
        changed = copy.deepcopy(self.proposal)
        changed["resource_id"] = "other-worker"
        with self.assertRaisesRegex(ValueError, "resource_id mismatch"):
            validate_change(self.cost, changed)
        changed = copy.deepcopy(self.proposal)
        changed["candidate_id"] = "other-candidate"
        with self.assertRaisesRegex(ValueError, "different cost review or candidate"):
            validate_change(self.cost, changed)

    def test_pending_proposal_cannot_claim_deployment_or_savings(self):
        changed = copy.deepcopy(self.proposal)
        changed["deployment_receipt_ref"] = "deployment"
        with self.assertRaisesRegex(ValueError, "unapproved change"):
            validate_change(self.cost, changed)
        with self.assertRaisesRegex(ValueError, "approved deployed change"):
            validate_savings(self.cost, self.proposal, fixture("invalid-cloud-savings-verified.json"))

    def test_verified_savings_need_math_workload_and_health(self):
        changed = copy.deepcopy(self.verified)
        changed["verified_savings_minor"] = 60000
        with self.assertRaisesRegex(ValueError, "does not match resource bills"):
            validate_savings(self.cost, self.deployed, changed)
        changed = copy.deepcopy(self.verified)
        changed["verification"]["post_work_units"] = 400
        with self.assertRaisesRegex(ValueError, "workload is not comparable"):
            validate_savings(self.cost, self.deployed, changed)
        changed = copy.deepcopy(self.verified)
        changed["verification"]["credit_effect_minor"] = 35000
        with self.assertRaisesRegex(ValueError, "credits or price changes"):
            validate_savings(self.cost, self.deployed, changed)
        changed = copy.deepcopy(self.verified)
        changed["verification"]["post_end"] = "2026-10-30"
        with self.assertRaisesRegex(ValueError, "complete comparable periods"):
            validate_savings(self.cost, self.deployed, changed)


if __name__ == "__main__":
    unittest.main()
