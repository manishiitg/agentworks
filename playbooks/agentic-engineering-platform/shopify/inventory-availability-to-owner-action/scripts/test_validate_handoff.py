import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_exception


EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


class InventoryHandoffTests(unittest.TestCase):
    def setUp(self):
        self.exception = json.loads((EXAMPLES / "inventory-availability-exception.json").read_text())
        self.review = json.loads((EXAMPLES / "inventory-action-review.json").read_text())

    def test_valid_pending_with_negative_available(self):
        validate(self.exception, self.review)

    def test_wrong_location_rejected(self):
        self.review["location_id"] = "location-9"
        with self.assertRaisesRegex(ValueError, "location_id mismatch"):
            validate(self.exception, self.review)

    def test_boolean_quantity_rejected(self):
        self.exception["available_qty"] = True
        with self.assertRaisesRegex(ValueError, "available_qty"):
            validate_exception(self.exception)

    def test_approval_without_reference_rejected(self):
        self.review["approval_state"] = "approved"
        with self.assertRaisesRegex(ValueError, "approval_ref"):
            validate(self.exception, self.review)

    def test_executed_without_receipt_rejected(self):
        self.review.update(decision="reconcile_sync", approval_state="approved", approval_ref="owner:approval-1", action_state="executed")
        with self.assertRaisesRegex(ValueError, "action_receipt_ref"):
            validate(self.exception, self.review)

    def test_verified_with_receipt_and_retest(self):
        self.review.update(decision="reconcile_sync", approval_state="approved", approval_ref="owner:approval-1", action_state="verified", action_receipt_ref="warehouse:sync-1", verification_ref="shopify:inventory-level-after-1")
        validate(self.exception, self.review)

    def test_invalid_fixture_rejected(self):
        invalid = json.loads((EXAMPLES / "invalid-inventory-action-review.json").read_text())
        with self.assertRaises(ValueError):
            validate(self.exception, invalid)


if __name__ == "__main__":
    unittest.main()
