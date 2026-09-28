import copy
import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("post_incident_handoff", Path(__file__).with_name("validate_handoff.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def example(name):
    return json.loads((ROOT / "examples" / name).read_text())


class PostIncidentHandoffTests(unittest.TestCase):
    def setUp(self):
        self.review = example("post-incident-review.json")
        self.register = example("incident-improvement-register.json")

    def test_approved_review_and_verified_action(self):
        MODULE.validate_review(self.review)
        MODULE.validate_register(self.review, self.register)
        renamed = copy.deepcopy(self.review)
        renamed["detection_alert_ref"] = "pager-source"
        next(item for item in renamed["source_refs"] if item["id"] == "alert")["id"] = "pager-source"
        next(item for item in renamed["timeline"] if item["source_ref"] == "alert")["source_ref"] = "pager-source"
        renamed["contributing_factors"][0]["evidence_refs"][1] = "pager-source"
        MODULE.validate_review(renamed)

    def test_draft_is_pending_with_no_issue(self):
        draft = example("post-incident-draft.json")
        MODULE.validate_review(draft)
        MODULE.validate_register(draft, example("incident-improvement-pending.json"))
        wrong = example("incident-improvement-pending.json")
        wrong["actions"][0]["state"] = "verified"
        with self.assertRaisesRegex(ValueError, "approved review and owner decision"):
            MODULE.validate_register(draft, wrong)

    def test_unstable_or_unsourced_review_is_rejected(self):
        wrong = copy.deepcopy(self.review)
        wrong["recovery"]["state"] = "investigating"
        with self.assertRaisesRegex(ValueError, "verified stability"):
            MODULE.validate_review(wrong)
        with self.assertRaisesRegex(ValueError, "confirmed factor needs two"):
            MODULE.validate_review(example("invalid-post-incident-review.json"))
        wrong = copy.deepcopy(self.review)
        wrong["impact"]["rate_bps"] = 900
        with self.assertRaisesRegex(ValueError, "impact arithmetic"):
            MODULE.validate_review(wrong)
        wrong = copy.deepcopy(self.review)
        wrong["detection_delay_seconds"] = 120
        with self.assertRaisesRegex(ValueError, "detection delay"):
            MODULE.validate_review(wrong)
        wrong = copy.deepcopy(self.review)
        wrong["timeline"][1]["source_ref"] = "missing"
        with self.assertRaisesRegex(ValueError, "missing or wrong source"):
            MODULE.validate_review(wrong)

    def test_closed_issue_alone_is_not_verification(self):
        with self.assertRaisesRegex(ValueError, "independent control evidence"):
            MODULE.validate_register(self.review, example("invalid-improvement-register.json"))
        wrong = copy.deepcopy(self.register)
        wrong["actions"][0]["verification"]["result"] = "fail"
        with self.assertRaisesRegex(ValueError, "verification must pass"):
            MODULE.validate_register(self.review, wrong)

    def test_exact_review_and_acceptance_boundaries(self):
        wrong = copy.deepcopy(self.register)
        wrong["source_review_artifact_id"] = "different-review"
        with self.assertRaisesRegex(ValueError, "exact review artifact"):
            MODULE.validate_register(self.review, wrong)
        wrong = copy.deepcopy(self.register)
        wrong["actions"][0]["decision"]["source_ref"] = "issue-current"
        with self.assertRaisesRegex(ValueError, "missing or wrong source"):
            MODULE.validate_register(self.review, wrong)
        wrong = copy.deepcopy(self.register)
        wrong["actions"][0]["issue"]["id"] = "ENG-999"
        with self.assertRaisesRegex(ValueError, "issue receipt must follow acceptance"):
            MODULE.validate_register(self.review, wrong)
        wrong = copy.deepcopy(self.register)
        wrong["actions"][1]["issue"] = {"id": "ENG-902"}
        with self.assertRaisesRegex(ValueError, "pending action cannot claim"):
            MODULE.validate_register(self.review, wrong)


if __name__ == "__main__":
    unittest.main()
