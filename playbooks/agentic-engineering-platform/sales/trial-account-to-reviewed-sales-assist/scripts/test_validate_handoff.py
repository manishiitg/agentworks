import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class TrialSalesContractTests(unittest.TestCase):
    def test_observed_trial_can_prepare_an_unsent_reviewed_draft(self):
        self.assertEqual(validate(fixture("trial-usage-observed.json"), fixture("trial-sales-draft.json")), [])

    def test_unknown_coverage_stays_in_review(self):
        self.assertEqual(validate(fixture("trial-usage-unknown.json"), fixture("trial-sales-needs-review.json")), [])
        sales = fixture("trial-sales-needs-review.json")
        sales["decision"] = "draft"
        self.assertTrue(any("draft is blocked" in error for error in validate(fixture("trial-usage-unknown.json"), sales)))

    def test_partial_coverage_cannot_claim_inactivity(self):
        errors = validate(fixture("invalid-trial-usage-inactive.json"), fixture("trial-sales-needs-review.json"))
        self.assertTrue(any("cannot prove inactivity" in error for error in errors), errors)

    def test_blocked_contact_and_false_delivery_cannot_become_a_draft(self):
        errors = validate(fixture("trial-usage-observed.json"), fixture("invalid-trial-sales-draft.json"))
        self.assertTrue(any("draft is blocked" in error for error in errors), errors)
        self.assertTrue(any("falsely claims delivery" in error for error in errors), errors)

    def test_wrong_account_or_stale_revision_is_rejected(self):
        sales = fixture("trial-sales-draft.json")
        sales["product_account_id"] = "account-other"
        sales["trial_revision"] = "rev2"
        sales["account_link"]["crm_account_id"] = "crm-other"
        errors = validate(fixture("trial-usage-observed.json"), sales)
        self.assertTrue(any("product_account_id" in error for error in errors), errors)
        self.assertTrue(any("trial revision" in error for error in errors), errors)
        self.assertTrue(any("CRM account mapping" in error for error in errors), errors)

    def test_converted_trial_or_prior_reply_blocks_new_draft(self):
        usage = copy.deepcopy(fixture("trial-usage-observed.json"))
        sales = fixture("trial-sales-draft.json")
        usage["trial_status"] = "converted"
        sales["reply_state"] = "responded"
        self.assertTrue(any("draft is blocked" in error for error in validate(usage, sales)))

    def test_future_window_and_stale_sales_observation_are_rejected(self):
        usage = fixture("trial-usage-observed.json")
        sales = fixture("trial-sales-draft.json")
        usage["window_end_at"] = "2026-09-27T10:00:00Z"
        sales["observed_at"] = "2026-09-25T10:20:00Z"
        errors = validate(usage, sales)
        self.assertTrue(any("beyond observed trial status" in error for error in errors), errors)
        self.assertTrue(any("predates" in error for error in errors), errors)


if __name__ == "__main__":
    unittest.main()
