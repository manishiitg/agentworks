import copy
import importlib.util
import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("retention_handoff", Path(__file__).with_name("validate_handoff.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def example(name):
    return json.loads((ROOT / "examples" / name).read_text())


class RetentionHandoffTests(unittest.TestCase):
    def setUp(self):
        self.observation = example("cohort-retention-observation.json")
        self.plan = example("retention-experiment-plan.json")

    def test_comparable_mature_cohorts_and_pending_plan(self):
        MODULE.validate_observation(self.observation)
        MODULE.validate_plan(self.observation, self.plan)

    def test_one_mature_cohort_is_baseline_not_trend(self):
        baseline = example("cohort-retention-baseline-first.json")
        MODULE.validate_observation(baseline)
        plan = example("retention-baseline-first-plan.json")
        MODULE.validate_plan(baseline, plan)
        plan["observed_change_bps"] = -1000
        with self.assertRaisesRegex(ValueError, "cannot claim a retention change"):
            MODULE.validate_plan(baseline, plan)

    def test_immature_cohort_cannot_be_churn_or_feed_planner(self):
        pending = example("cohort-retention-pending-maturity.json")
        MODULE.validate_observation(pending)
        with self.assertRaisesRegex(ValueError, "pending maturity blocks"):
            MODULE.validate_plan(pending, self.plan)
        pending["cohorts"][0]["retained_accounts"] = 0
        with self.assertRaisesRegex(ValueError, "cannot claim retention or churn"):
            MODULE.validate_observation(pending)

    def test_maturity_date_counts_and_coverage_must_reconcile(self):
        wrong = copy.deepcopy(self.observation)
        wrong["cohorts"][1]["maturity_date"] = "2026-08-29"
        with self.assertRaisesRegex(ValueError, "maturity date"):
            MODULE.validate_observation(wrong)
        wrong = copy.deepcopy(self.observation)
        wrong["cohorts"][1]["retention_rate_bps"] = 6000
        with self.assertRaisesRegex(ValueError, "retention count or rate"):
            MODULE.validate_observation(wrong)
        wrong = copy.deepcopy(self.observation)
        wrong["cohorts"][1]["matched_identity_accounts"] = 100
        with self.assertRaisesRegex(ValueError, "identity coverage"):
            MODULE.validate_observation(wrong)

    def test_false_launch_and_wrong_observation_block_plan(self):
        with self.assertRaisesRegex(ValueError, "guardrail"):
            MODULE.validate_plan(self.observation, example("invalid-retention-experiment-plan.json"))
        wrong = copy.deepcopy(self.plan)
        wrong["launch_state"] = "launched"
        with self.assertRaisesRegex(ValueError, "cannot claim approval"):
            MODULE.validate_plan(self.observation, wrong)
        wrong = copy.deepcopy(self.plan)
        wrong["source_refs"][0]["uri"] = "artifact:wrong"
        with self.assertRaisesRegex(ValueError, "exact observation"):
            MODULE.validate_plan(self.observation, wrong)
        wrong = copy.deepcopy(self.plan)
        wrong["source_refs"][1]["uri"] = "misc/unapproved-note"
        with self.assertRaisesRegex(ValueError, "owner policy evidence"):
            MODULE.validate_plan(self.observation, wrong)
        wrong = copy.deepcopy(self.plan)
        wrong["winner"] = "guided setup"
        with self.assertRaisesRegex(ValueError, "pending plan cannot contain"):
            MODULE.validate_plan(self.observation, wrong)


if __name__ == "__main__":
    unittest.main()
