import copy
import importlib.util
import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("ai_visibility_handoff", Path(__file__).with_name("validate_handoff.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def example(name):
    return json.loads((ROOT / "examples" / name).read_text())


class AIVisibilityHandoffTests(unittest.TestCase):
    def setUp(self):
        self.snapshot = example("ai-visibility-snapshot.json")
        self.opportunity = example("ai-citation-opportunity.json")

    def test_sample_and_exact_opportunity(self):
        MODULE.validate_snapshot(self.snapshot)
        MODULE.validate_opportunity(self.snapshot, self.opportunity)

    def test_false_rank_and_published_claim_rejected(self):
        with self.assertRaisesRegex(ValueError, "citation counts"):
            MODULE.validate_opportunity(self.snapshot, example("invalid-ai-citation-opportunity.json"))
        wrong = copy.deepcopy(self.opportunity)
        wrong["publication_state"] = "published"
        with self.assertRaisesRegex(ValueError, "owner review"):
            MODULE.validate_opportunity(self.snapshot, wrong)
        wrong = copy.deepcopy(self.snapshot)
        wrong["claim_state"] = "rank_1"
        with self.assertRaisesRegex(ValueError, "stable rank"):
            MODULE.validate_snapshot(wrong)

    def test_failed_attempt_is_not_absent_citation(self):
        wrong = copy.deepcopy(self.snapshot)
        wrong["attempts"][1]["status"] = "failed"
        wrong["attempts"][1]["failure_reason"] = "answer unavailable"
        with self.assertRaisesRegex(ValueError, "failed run"):
            MODULE.validate_snapshot(wrong)
        wrong["attempts"][1]["brand_mentioned"] = None
        wrong["attempts"][1]["brand_cited_urls"] = None
        wrong["attempts"][1]["competitor_cited_urls"] = None
        with self.assertRaisesRegex(ValueError, "valid_attempts"):
            MODULE.validate_snapshot(wrong)

    def test_wrong_brand_url_or_unreconciled_count(self):
        wrong = copy.deepcopy(self.snapshot)
        wrong["attempts"][0]["brand_cited_urls"] = ["https://clinicflow.example/physio"]
        with self.assertRaisesRegex(ValueError, "brand citation host"):
            MODULE.validate_snapshot(wrong)
        wrong = copy.deepcopy(self.snapshot)
        wrong["brand_citation_count"] = 2
        with self.assertRaisesRegex(ValueError, "brand_citation_count"):
            MODULE.validate_snapshot(wrong)

    def test_exact_question_page_and_source_required(self):
        wrong = copy.deepcopy(self.opportunity)
        wrong["question_id"] = "Q-other"
        with self.assertRaisesRegex(ValueError, "question_id"):
            MODULE.validate_opportunity(self.snapshot, wrong)
        wrong = copy.deepcopy(self.opportunity)
        wrong["page_observation_ref"] = "missing"
        with self.assertRaisesRegex(ValueError, "page_observation_ref"):
            MODULE.validate_opportunity(self.snapshot, wrong)
        wrong = copy.deepcopy(self.opportunity)
        wrong["source_refs"][1]["uri"] = "https://arbordesk.example/pricing#capture"
        with self.assertRaisesRegex(ValueError, "exact target page"):
            MODULE.validate_opportunity(self.snapshot, wrong)
        wrong = copy.deepcopy(self.opportunity)
        wrong["source_refs"][0]["uri"] = "artifact:other"
        with self.assertRaisesRegex(ValueError, "exact prior snapshot"):
            MODULE.validate_opportunity(self.snapshot, wrong)


if __name__ == "__main__":
    unittest.main()
