import copy
import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("feature_handoff", Path(__file__).with_name("validate_handoff.py"))
handoff = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handoff)


def fixture(name):
    return json.loads((ROOT / "examples" / name).read_text())


class FeatureHandoffTest(unittest.TestCase):
    def setUp(self):
        self.observation = fixture("feature-adoption-observation.json")
        self.decision = fixture("feature-adoption-decision.json")

    def test_first_baseline_and_pending_review(self):
        self.assertEqual(handoff.validate_pair(self.observation, self.decision), [])

    def test_comparable_and_not_evaluable_examples(self):
        self.assertEqual(handoff.validate_observation(fixture("feature-adoption-comparable.json")), [])
        self.assertEqual(handoff.validate_observation(fixture("feature-adoption-not-evaluable.json")), [])

    def test_rejects_false_product_action_and_owner_review(self):
        self.assertTrue(handoff.validate_pair(self.observation, fixture("invalid-feature-adoption-decision.json")))

    def test_rejects_wrong_release_and_flag_at_handoff(self):
        for field in ("release_id", "flag_revision", "measurement_rule_id", "window_end"):
            with self.subTest(field=field):
                decision = copy.deepcopy(self.decision)
                decision[field] += "-wrong"
                self.assertTrue(handoff.validate_pair(self.observation, decision))

    def test_rejects_denominator_and_rate_errors(self):
        for field, value in (("eligible_accounts", 30), ("exposure_pct", 90), ("use_pct", 90)):
            with self.subTest(field=field):
                observation = copy.deepcopy(self.observation)
                observation[field] = value
                self.assertTrue(handoff.validate_observation(observation))

    def test_partial_coverage_cannot_claim_target_result(self):
        observation = fixture("feature-adoption-not-evaluable.json")
        observation["target_state"] = "below"
        self.assertTrue(handoff.validate_observation(observation))

    def test_comparison_requires_same_rules_and_correct_trend(self):
        for field, value in (("flag_revision", "flag-r1"), ("window_end", "2026-09-20T00:00:00Z"), ("use_pct", 80)):
            with self.subTest(field=field):
                observation = fixture("feature-adoption-comparable.json")
                observation["prior"][field] = value
                self.assertTrue(handoff.validate_observation(observation))

    def test_bad_chronology_returns_errors_without_crash(self):
        observation = fixture("feature-adoption-comparable.json")
        observation["released_at"] = "invalid"
        self.assertTrue(handoff.validate_observation(observation))

    def test_owner_review_requires_exact_receipt(self):
        decision = copy.deepcopy(self.decision)
        decision["decision_state"] = "owner_reviewed"
        self.assertTrue(handoff.validate_pair(self.observation, decision))
        decision["owner_decision"] = {
            "owner_id":"owner-product-1", "case_key":decision["case_key"],
            "observation_artifact_id":self.observation["artifact_id"],
            "choice":"investigate", "decided_at":"2026-09-21T14:00:00Z"
        }
        self.assertEqual(handoff.validate_pair(self.observation, decision), [])

    def test_duplicate_new_case_is_rejected(self):
        decision = copy.deepcopy(self.decision)
        decision["existing_case_keys"] = [decision["case_key"]]
        self.assertTrue(handoff.validate_pair(self.observation, decision))

    def test_unevaluable_cannot_recommend_keep(self):
        observation = fixture("feature-adoption-not-evaluable.json")
        decision = copy.deepcopy(self.decision)
        decision["source_observation_artifact_id"] = observation["artifact_id"]
        decision["evaluation_state"] = "not_evaluable"
        decision["target_state"] = "unknown"
        decision["exposed_accounts"] = 40
        decision["used_accounts"] = 12
        decision["recommendation"] = "keep"
        self.assertTrue(handoff.validate_pair(observation, decision))


if __name__ == "__main__":
    unittest.main()
