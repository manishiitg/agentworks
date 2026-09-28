import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_signal


EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


class CheckoutRecoveryTests(unittest.TestCase):
    def setUp(self):
        self.signal = json.loads((EXAMPLES / "checkout-recovery-signal.json").read_text())
        self.review = json.loads((EXAMPLES / "checkout-recovery-review.json").read_text())

    def test_reviewable_unsent_draft(self):
        validate(self.signal, self.review)

    def test_invalid_fixture(self):
        with self.assertRaises(ValueError):
            validate(self.signal, json.loads((EXAMPLES / "invalid-checkout-recovery-review.json").read_text()))

    def test_mismatched_checkout(self):
        self.review["checkout_id"] = "another-checkout"
        with self.assertRaisesRegex(ValueError, "checkout_id mismatch"):
            validate(self.signal, self.review)

    def test_recovered_checkout_suppresses_draft(self):
        self.review["checkout_completed_at"] = "2026-09-24T14:33:00Z"
        with self.assertRaisesRegex(ValueError, "draft review needs"):
            validate(self.signal, self.review)
        self.review.update(decision="suppress", draft_text=None)
        validate(self.signal, self.review)

    def test_existing_automation_suppresses_draft(self):
        self.review["prior_send_state"] = "sent_or_scheduled"
        with self.assertRaisesRegex(ValueError, "draft review needs"):
            validate(self.signal, self.review)
        self.review.update(decision="suppress", draft_text=None)
        validate(self.signal, self.review)

    def test_unknown_consent_needs_information(self):
        self.review["consent_state"] = "unknown"
        with self.assertRaisesRegex(ValueError, "draft review needs"):
            validate(self.signal, self.review)
        self.review.update(decision="needs_information", draft_text=None)
        validate(self.signal, self.review)

    def test_draft_needs_separate_consent_and_history_proof(self):
        self.review["consent_ref"] = None
        with self.assertRaisesRegex(ValueError, "consent_ref"):
            validate(self.signal, self.review)
        self.review["consent_ref"] = "shopify:consent-34@2026-09-24T14:35Z"
        self.review["message_history_ref"] = None
        with self.assertRaisesRegex(ValueError, "message_history_ref"):
            validate(self.signal, self.review)

    def test_send_needs_approval_and_provider_receipt(self):
        self.review["send_state"] = "sent"
        with self.assertRaisesRegex(ValueError, "approved eligible draft"):
            validate(self.signal, self.review)
        self.review.update(approval_state="approved", approval_ref="merchant:approval-19")
        with self.assertRaisesRegex(ValueError, "pre_send_recheck_ref"):
            validate(self.signal, self.review)
        self.review["pre_send_recheck_ref"] = "shopify:recheck-19"
        with self.assertRaisesRegex(ValueError, "provider_send_ref"):
            validate(self.signal, self.review)
        self.review["provider_send_ref"] = "messaging:send-19"
        self.review["send_observed_at"] = "2026-09-24T14:40:00Z"
        validate(self.signal, self.review)

    def test_recovered_order_needs_trusted_link(self):
        self.review.update(later_order_state="linked_order_observed", decision="suppress", draft_text=None, recovery_state="linked_order_observed")
        with self.assertRaisesRegex(ValueError, "linked_order_ref"):
            validate(self.signal, self.review)
        self.review["linked_order_ref"] = "shopify:order-99"
        with self.assertRaisesRegex(ValueError, "checkout_order_link_ref"):
            validate(self.signal, self.review)
        self.review["checkout_order_link_ref"] = "shopify:checkout-77-to-order-99"
        self.review["recovery_observed_at"] = "2026-09-24T14:40:00Z"
        validate(self.signal, self.review)

    def test_contact_details_rejected(self):
        signal = copy.deepcopy(self.signal)
        signal["email"] = "private@example.test"
        with self.assertRaisesRegex(ValueError, "contact details"):
            validate_signal(signal)
        self.review["draft_text"] = "Return to https://example.test/private-checkout-token"
        with self.assertRaisesRegex(ValueError, "recovery URL"):
            validate(self.signal, self.review)


if __name__ == "__main__":
    unittest.main()
