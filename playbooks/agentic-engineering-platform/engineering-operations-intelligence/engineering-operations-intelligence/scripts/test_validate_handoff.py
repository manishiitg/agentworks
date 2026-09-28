import copy
import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("engineering_metric_handoff", Path(__file__).with_name("validate_handoff.py"))
handoff = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handoff)


def fixture(name):
    return json.loads((ROOT / "examples" / name).read_text())


class EngineeringMetricHandoffTest(unittest.TestCase):
    def setUp(self):
        self.observation = fixture("engineering-metric-observation.json")
        self.review = fixture("engineering-improvement-review.json")

    def test_comparable_metric_and_pending_review(self):
        self.assertEqual(handoff.validate_pair(self.observation, self.review), [])

    def test_baseline_and_incomplete_examples(self):
        self.assertEqual(handoff.validate_observation(fixture("engineering-metric-baseline.json")), [])
        self.assertEqual(handoff.validate_observation(fixture("engineering-metric-not-evaluable.json")), [])

    def test_rejects_false_action_outcome_and_ranking(self):
        self.assertTrue(handoff.validate_pair(self.observation, fixture("invalid-engineering-improvement-review.json")))

    def test_exact_team_policy_and_window_handoff(self):
        for key in ("team_id", "service_id", "policy_revision", "window_end", "rate_pct"):
            with self.subTest(key=key):
                review = copy.deepcopy(self.review)
                review[key] = "wrong"
                self.assertTrue(handoff.validate_pair(self.observation, review))

    def test_rate_and_population_arithmetic(self):
        for key, value in (("numerator", 90), ("denominator", 0), ("rate_pct", 20.0)):
            with self.subTest(key=key):
                observation = copy.deepcopy(self.observation)
                observation[key] = value
                self.assertTrue(handoff.validate_observation(observation))

    def test_changed_rule_or_unequal_windows_rejects_comparison(self):
        for key, value in (("policy_revision", "r0"), ("window_end", "2026-09-13T00:00:00Z"), ("rate_pct", 50.0)):
            with self.subTest(key=key):
                observation = copy.deepcopy(self.observation)
                observation["prior"][key] = value
                self.assertTrue(handoff.validate_observation(observation))

    def test_partial_coverage_cannot_claim_target_miss_or_trend(self):
        observation = fixture("engineering-metric-not-evaluable.json")
        observation["target_state"] = "exceeded"
        observation["change_pct_points"] = 7.0
        self.assertTrue(handoff.validate_observation(observation))

    def test_owner_acceptance_requires_exact_receipt(self):
        review = copy.deepcopy(self.review)
        review["decision_state"] = "accepted"
        self.assertTrue(handoff.validate_pair(self.observation, review))
        review["owner_decision"] = {
            "owner_id":"platform-lead", "case_key":review["case_key"],
            "observation_artifact_id":self.observation["artifact_id"],
            "choice":"accepted", "decided_at":"2026-09-21T14:00:00Z"
        }
        self.assertEqual(handoff.validate_pair(self.observation, review), [])

    def test_duplicate_new_case_rejected(self):
        review = copy.deepcopy(self.review)
        review["existing_case_keys"] = [review["case_key"]]
        self.assertTrue(handoff.validate_pair(self.observation, review))

    def test_unevaluable_metric_cannot_be_prioritized(self):
        observation = fixture("engineering-metric-not-evaluable.json")
        review = copy.deepcopy(self.review)
        review.update(source_observation_artifact_id=observation["artifact_id"],numerator=5,
                      denominator=40,rate_pct=12.5,evaluation_state="not_evaluable",
                      target_state="unknown",recommendation="prioritize")
        self.assertTrue(handoff.validate_pair(observation, review))


if __name__ == "__main__":
    unittest.main()
