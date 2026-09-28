#!/usr/bin/env python3
"""Exercise money, identity, approval, and receipt gates in the handoff."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_order


EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


class ShopifyHandoffTests(unittest.TestCase):
    def setUp(self):
        self.order = json.loads((EXAMPLES / "store-order-exception.json").read_text())
        self.review = json.loads((EXAMPLES / "return-resolution-review.json").read_text())

    def test_valid_pending_review(self):
        validate_order(self.order)
        validate(self.order, self.review)

    def test_rejects_wrong_order(self):
        self.review["order_id"] = "another-order"
        with self.assertRaisesRegex(ValueError, "order_id mismatch"):
            validate(self.order, self.review)

    def test_rejects_refund_above_remaining_capture(self):
        self.review["already_refunded_minor"] = 2000
        self.review["proposed_refund_minor"] = 7000
        with self.assertRaisesRegex(ValueError, "exceeds verified remaining"):
            validate(self.order, self.review)

    def test_rejects_approval_without_owner_reference(self):
        self.review["approval_state"] = "approved"
        with self.assertRaisesRegex(ValueError, "approval_ref"):
            validate(self.order, self.review)

    def test_rejects_issued_refund_without_receipt(self):
        self.review.update(approval_state="approved", approval_ref="approval:82", resolution_state="executed", proposed_refund_minor=3000)
        with self.assertRaisesRegex(ValueError, "refund_receipt_ref"):
            validate(self.order, self.review)

    def test_rejects_sent_message_without_receipt(self):
        self.review["message_state"] = "sent"
        with self.assertRaisesRegex(ValueError, "message_receipt_ref"):
            validate(self.order, self.review)

    def test_rejects_executed_return_without_provider_receipt(self):
        self.order["affected_line_fulfillment_state"] = "fulfilled"
        self.review.update(decision="return_review", resolution_route="shopify_return_review", approval_state="approved", approval_ref="approval:82", resolution_state="executed")
        with self.assertRaisesRegex(ValueError, "return_receipt_ref"):
            validate(self.order, self.review)

    def test_rejects_shopify_return_route_for_unfulfilled_line(self):
        self.review.update(decision="return_review", resolution_route="shopify_return_review")
        with self.assertRaisesRegex(ValueError, "requires a fulfilled affected line"):
            validate(self.order, self.review)

    def test_rejects_shopify_return_record_for_unfulfilled_line(self):
        self.order["shopify_return_ref"] = "shopify:return-99"
        with self.assertRaisesRegex(ValueError, "unfulfilled line"):
            validate_order(self.order)

    def test_requires_explicit_shopify_return_state(self):
        del self.order["shopify_return_ref"]
        with self.assertRaisesRegex(ValueError, "must be present"):
            validate_order(self.order)

    def test_valid_fulfilled_return_review(self):
        self.order["affected_line_fulfillment_state"] = "fulfilled"
        self.review.update(decision="return_review", resolution_route="shopify_return_review")
        validate(self.order, self.review)

    def test_rejects_verified_refund_without_later_source_check(self):
        self.review.update(approval_state="approved", approval_ref="approval:82", resolution_state="verified", proposed_refund_minor=3000, refund_receipt_ref="refund:300")
        with self.assertRaisesRegex(ValueError, "verification_ref"):
            validate(self.order, self.review)

    def test_rejects_boolean_money_amount(self):
        self.review["captured_amount_minor"] = True
        with self.assertRaisesRegex(ValueError, "minor units"):
            validate(self.order, self.review)

    def test_fixture_with_multiple_invalid_claims_is_rejected(self):
        invalid = copy.deepcopy(json.loads((EXAMPLES / "invalid-return-resolution-review.json").read_text()))
        with self.assertRaises(ValueError):
            validate(self.order, invalid)


if __name__ == "__main__":
    unittest.main()
