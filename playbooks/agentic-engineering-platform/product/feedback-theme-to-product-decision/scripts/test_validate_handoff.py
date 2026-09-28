import json
import unittest
from pathlib import Path
from validate_handoff import validate

ROOT = Path(__file__).resolve().parents[1] / 'examples'


class FeedbackContractTests(unittest.TestCase):
    def setUp(self):
        self.theme = json.loads((ROOT / 'feedback-theme-brief.json').read_text())
        self.decision = json.loads((ROOT / 'product-feedback-decision.json').read_text())

    def test_reviewable_pending_product_decision_passes(self):
        self.assertEqual(validate(self.theme, self.decision), [])

    def test_false_link_issue_write_and_promise_fail(self):
        invalid = json.loads((ROOT / 'invalid-product-feedback-decision.json').read_text())
        errors = validate(self.theme, invalid)
        self.assertTrue(any('owner timestamp' in error for error in errors))
        self.assertTrue(any('exact issue match' in error for error in errors))
        self.assertTrue(any('provider receipt' in error for error in errors))
        self.assertTrue(any('customer promise' in error for error in errors))

    def test_duplicate_ids_and_bad_denominator_fail(self):
        self.theme['feedback_ids'].append('survey:1')
        self.theme['unique_reporters'] = 41
        errors = validate(self.theme, self.decision)
        self.assertTrue(any('duplicated' in error for error in errors))
        self.assertTrue(any('denominator' in error for error in errors))

    def test_wrong_product_scope_fails(self):
        self.decision['product_id'] = 'other-product'
        self.assertTrue(any('product_id' in error for error in validate(self.theme, self.decision)))

    def test_product_cannot_change_theme_denominator_or_use_unreviewed_feedback(self):
        self.decision['theme_eligible_responses'] = 400
        self.theme['review_state'] = 'draft'
        errors = validate(self.theme, self.decision)
        self.assertTrue(any('denominator or coverage' in error for error in errors))
        self.assertTrue(any('Support review' in error for error in errors))

    def test_issue_receipt_requires_matching_exact_approval(self):
        self.decision['decision_state'] = 'investigate'
        self.decision['owner_decided_at'] = '2026-10-01T11:00:00Z'
        self.decision['issue_action_state'] = 'created'
        self.decision['issue_action_receipt'] = {'state': 'created', 'theme_id': self.theme['theme_id'], 'issue_id': 'ISSUE-25', 'provider_id': 'receipt-1'}
        errors = validate(self.theme, self.decision)
        self.assertTrue(any('provider receipt' in error for error in errors))


if __name__ == '__main__':
    unittest.main()
