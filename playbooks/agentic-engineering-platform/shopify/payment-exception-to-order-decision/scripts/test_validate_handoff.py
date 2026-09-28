import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_payment


EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


class PaymentHandoffTests(unittest.TestCase):
    def setUp(self):
        self.payment = json.loads((EXAMPLES / "payment-exception.json").read_text())
        self.decision = json.loads((EXAMPLES / "payment-order-decision.json").read_text())

    def test_authorization_case_is_held(self):
        validate(self.payment, self.decision)

    def test_wrong_transaction_rejected(self):
        self.decision["transaction_id"] = "other-transaction"
        with self.assertRaisesRegex(ValueError, "transaction_id mismatch"):
            validate(self.payment, self.decision)

    def test_boolean_amount_rejected(self):
        self.payment["amount_minor"] = True
        with self.assertRaisesRegex(ValueError, "amount_minor"):
            validate_payment(self.payment)

    def test_authorization_cannot_release(self):
        self.decision["decision"] = "release_review"
        with self.assertRaisesRegex(ValueError, "successful CAPTURE or SALE"):
            validate(self.payment, self.decision)

    def test_capture_release_needs_approval_and_receipt(self):
        self.payment.update(transaction_kind="CAPTURE", parent_transaction_ref="transaction-601")
        self.decision.update(decision="release_review", action_state="executed")
        with self.assertRaisesRegex(ValueError, "approved release review"):
            validate(self.payment, self.decision)
        self.decision.update(approval_state="approved", approval_ref="merchant:approval-1")
        with self.assertRaisesRegex(ValueError, "action_receipt_ref"):
            validate(self.payment, self.decision)

    def test_verified_capture_release(self):
        self.payment.update(transaction_kind="CAPTURE", parent_transaction_ref="transaction-601")
        self.decision.update(decision="release_review", approval_state="approved", approval_ref="merchant:approval-1", action_state="verified", action_receipt_ref="fulfillment:release-1", verification_ref="shopify:fulfillment-order-after-1")
        validate(self.payment, self.decision)

    def test_invalid_fixture_rejected(self):
        invalid = json.loads((EXAMPLES / "invalid-payment-order-decision.json").read_text())
        with self.assertRaises(ValueError):
            validate(self.payment, invalid)


if __name__ == "__main__":
    unittest.main()
