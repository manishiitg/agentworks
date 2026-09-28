import copy
import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("vendor_handoff", Path(__file__).with_name("validate_handoff.py"))
handoff = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handoff)


def fixture(name):
    return json.loads((ROOT / "examples" / name).read_text())


class VendorHandoffTest(unittest.TestCase):
    def setUp(self):
        self.comparison = fixture("vendor-comparison.json")
        self.review = fixture("vendor-purchase-pending.json")

    def test_valid_comparison_and_pending_review(self):
        self.assertEqual(handoff.validate_pair(self.comparison, self.review), [])

    def test_valid_approved_and_blocked_states(self):
        self.assertEqual(handoff.validate_pair(self.comparison, fixture("vendor-purchase-approved.json")), [])
        self.assertEqual(handoff.validate_pair(self.comparison, fixture("vendor-purchase-blocked.json")), [])

    def test_invalid_purchase_and_payment_claim(self):
        self.assertTrue(handoff.validate_pair(self.comparison, fixture("invalid-vendor-purchase-review.json")))

    def test_wrong_selected_plan_or_quote_fails(self):
        for key in ("plan_id", "quote_id", "term_total", "requirements_revision"):
            with self.subTest(key=key):
                review = copy.deepcopy(self.review)
                review[key] = "wrong"
                self.assertTrue(handoff.validate_pair(self.comparison, review))

    def test_wrong_cost_and_expired_quote_fail(self):
        for key, value in (("term_total", "1.00"), ("valid_until", "2026-09-20")):
            with self.subTest(key=key):
                comparison = copy.deepcopy(self.comparison)
                comparison["candidates"][1][key] = value
                self.assertTrue(handoff.validate_comparison(comparison))

    def test_unknown_must_have_cannot_be_eligible(self):
        comparison = copy.deepcopy(self.comparison)
        comparison["candidates"][1]["criterion_results"][0]["state"] = "unknown"
        self.assertTrue(handoff.validate_comparison(comparison))

    def test_missing_criterion_citation_fails(self):
        comparison = copy.deepcopy(self.comparison)
        comparison["candidates"][1]["criterion_results"][0]["source_ref"] = "invented"
        self.assertTrue(handoff.validate_comparison(comparison))

    def test_overlapping_vendor_and_missing_budget_cannot_be_ready(self):
        for key, value in (("duplicate_state", "overlap"), ("available_budget", "100.00")):
            with self.subTest(key=key):
                review = copy.deepcopy(self.review)
                review[key] = value
                self.assertTrue(handoff.validate_pair(self.comparison, review))

    def test_missing_security_gate_cannot_be_approved(self):
        review = fixture("vendor-purchase-approved.json")
        review["security_state"] = "pending"
        self.assertTrue(handoff.validate_pair(self.comparison, review))

    def test_owner_receipt_must_match_exact_case(self):
        review = fixture("vendor-purchase-approved.json")
        review["owner_decision"]["comparison_artifact_id"] = "other"
        self.assertTrue(handoff.validate_pair(self.comparison, review))

    def test_quote_must_still_be_valid_at_review_and_approval(self):
        comparison = copy.deepcopy(self.comparison)
        comparison["candidates"][1]["valid_until"] = "2026-09-25"
        review = copy.deepcopy(self.review)
        review["observed_at"] = "2026-09-26T12:00:00Z"
        self.assertTrue(handoff.validate_pair(comparison, review))
        approved = fixture("vendor-purchase-approved.json")
        approved["owner_decision"]["decided_at"] = "2026-09-26T13:00:00Z"
        self.assertTrue(handoff.validate_pair(comparison, approved))

    def test_duplicate_case_is_rejected(self):
        review = copy.deepcopy(self.review)
        review["existing_case_keys"] = [review["case_key"]]
        self.assertTrue(handoff.validate_pair(self.comparison, review))

    def test_invalid_source_shape_returns_error(self):
        comparison = copy.deepcopy(self.comparison)
        comparison["source_refs"] = [{}]
        self.assertTrue(handoff.validate_comparison(comparison))


if __name__ == "__main__":
    unittest.main()
