import copy
import importlib.util
import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("funnel_handoff", Path(__file__).with_name("validate_handoff.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def example(name):
    return json.loads((ROOT / "examples" / name).read_text())


class FunnelHandoffTests(unittest.TestCase):
    def setUp(self):
        self.observation = example("funnel-observation.json")
        self.plan = example("funnel-experiment-plan.json")

    def test_reconciled_observation_and_pending_plan(self):
        MODULE.validate_observation(self.observation)
        MODULE.validate_plan(self.observation, self.plan)

    def test_new_company_can_start_with_a_baseline_without_a_trend(self):
        baseline = example("funnel-baseline-first.json")
        MODULE.validate_observation(baseline)
        plan = example("funnel-baseline-first-plan.json")
        MODULE.validate_plan(baseline, plan)
        plan["observed_change_bps"] = -200
        with self.assertRaisesRegex(ValueError, "cannot claim an observed change"):
            MODULE.validate_plan(baseline, plan)
        baseline["change_bps"] = -200
        with self.assertRaisesRegex(ValueError, "cannot claim a rate change"):
            MODULE.validate_observation(baseline)

    def test_stage_order_and_paid_rate_must_reconcile(self):
        wrong = copy.deepcopy(self.observation)
        wrong["current"]["counts"]["paid"] = 181
        with self.assertRaisesRegex(ValueError, "monotone"):
            MODULE.validate_observation(wrong)
        wrong = copy.deepcopy(self.observation)
        wrong["current_paid_rate_bps"] = 600
        with self.assertRaisesRegex(ValueError, "current_paid_rate_bps"):
            MODULE.validate_observation(wrong)
        wrong = copy.deepcopy(self.observation)
        wrong["change_bps"] = 200
        with self.assertRaisesRegex(ValueError, "rate change"):
            MODULE.validate_observation(wrong)

    def test_identity_coverage_and_billing_evidence(self):
        wrong = copy.deepcopy(self.observation)
        wrong["current"]["matched_identity_users"] = 100
        with self.assertRaisesRegex(ValueError, "identity coverage"):
            MODULE.validate_observation(wrong)
        wrong = copy.deepcopy(self.observation)
        wrong["current"]["paid_state_cutoff"] = "2026-09-15T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "paid state cutoff"):
            MODULE.validate_observation(wrong)
        wrong = copy.deepcopy(self.observation)
        wrong["current"]["paid_source_ref"] = "baseline-events"
        with self.assertRaisesRegex(ValueError, "distinct references"):
            MODULE.validate_observation(wrong)

    def test_wrong_cohort_or_false_launch_blocks_plan(self):
        wrong = copy.deepcopy(self.plan)
        wrong["cohort_id"] = "other"
        with self.assertRaisesRegex(ValueError, "cohort_id"):
            MODULE.validate_plan(self.observation, wrong)
        wrong = copy.deepcopy(self.plan)
        wrong["launch_state"] = "launched"
        with self.assertRaisesRegex(ValueError, "cannot claim launch"):
            MODULE.validate_plan(self.observation, wrong)
        wrong = copy.deepcopy(self.plan)
        wrong["source_refs"][0]["uri"] = "artifact:wrong"
        with self.assertRaisesRegex(ValueError, "exact prior observation"):
            MODULE.validate_plan(self.observation, wrong)

    def test_rejected_example_cannot_claim_a_winner(self):
        with self.assertRaisesRegex(ValueError, "guardrail"):
            MODULE.validate_plan(self.observation, example("invalid-funnel-experiment-plan.json"))


if __name__ == "__main__":
    unittest.main()
