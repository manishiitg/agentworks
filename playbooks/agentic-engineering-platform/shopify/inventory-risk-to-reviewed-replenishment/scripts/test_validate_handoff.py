import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_signal


EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


class ReplenishmentHandoffTests(unittest.TestCase):
    def setUp(self):
        self.signal = json.loads((EXAMPLES / "inventory-signal.json").read_text())
        self.review = json.loads((EXAMPLES / "replenishment-review.json").read_text())

    def test_review_recomputes(self):
        validate(self.signal, self.review)

    def test_invalid_fixture(self):
        with self.assertRaises(ValueError):
            validate(self.signal, json.loads((EXAMPLES / "invalid-replenishment-review.json").read_text()))

    def test_wrong_location_blocks_handoff(self):
        self.review["location_id"] = "other-location"
        with self.assertRaisesRegex(ValueError, "location_id mismatch"):
            validate(self.signal, self.review)

    def test_available_must_match_source_without_double_subtraction(self):
        self.review["available_qty"] -= self.signal["committed_qty"]
        with self.assertRaisesRegex(ValueError, "available_qty must match"):
            validate(self.signal, self.review)

    def test_formula_and_pack_rounding(self):
        self.review["target_qty"] = 21
        with self.assertRaisesRegex(ValueError, "target_qty formula"):
            validate(self.signal, self.review)
        self.review["target_qty"] = 22
        self.review["proposed_qty"] = 13
        with self.assertRaisesRegex(ValueError, "proposed_qty"):
            validate(self.signal, self.review)

    def test_cost_must_recompute(self):
        self.review["total_cost_minor"] = 16000
        with self.assertRaisesRegex(ValueError, "total_cost_minor"):
            validate(self.signal, self.review)

    def test_supplier_and_budget_currency_must_match(self):
        self.review["supplier_quote_currency"] = "EUR"
        with self.assertRaisesRegex(ValueError, "supplier_quote_currency"):
            validate(self.signal, self.review)
        self.review["supplier_quote_currency"] = "USD"
        self.review["budget_currency"] = "EUR"
        with self.assertRaisesRegex(ValueError, "budget_currency"):
            validate(self.signal, self.review)

    def test_unknown_open_po_blocks_reorder(self):
        self.review["open_po_state"] = "unknown"
        with self.assertRaisesRegex(ValueError, "unresolved open PO"):
            validate(self.signal, self.review)

    def test_missing_supplier_terms_proof_blocks(self):
        self.review["supplier_terms_ref"] = None
        with self.assertRaisesRegex(ValueError, "supplier_terms_ref"):
            validate(self.signal, self.review)

    def test_zero_shortage_defer(self):
        self.review["demand_units"] = 0
        self.review["target_qty"] = 2
        self.review["shortage_qty"] = 0
        self.review["proposed_qty"] = 0
        self.review["total_cost_minor"] = 0
        self.review["decision"] = "defer"
        validate(self.signal, self.review)

    def test_missing_data_is_explicit(self):
        self.review["decision"] = "needs_information"
        self.review["open_po_state"] = "unknown"
        for field in ("target_qty", "shortage_qty", "proposed_qty", "total_cost_minor"):
            self.review[field] = None
        validate(self.signal, self.review)

    def test_po_and_receipt_need_separate_evidence(self):
        self.review["po_state"] = "draft"
        with self.assertRaisesRegex(ValueError, "approved reorder and budget"):
            validate(self.signal, self.review)
        self.review.update(approval_state="approved", approval_ref="merchant:approval-4", budget_state="approved")
        with self.assertRaisesRegex(ValueError, "pre_action_recheck_ref"):
            validate(self.signal, self.review)
        self.review.update(pre_action_recheck_ref="shopify:recheck-4", po_ref="shopify:po-44")
        validate(self.signal, self.review)
        self.review["po_state"] = "ordered"
        with self.assertRaisesRegex(ValueError, "supplier_confirmation_ref"):
            validate(self.signal, self.review)
        self.review["supplier_confirmation_ref"] = "supplier:confirmation-44"
        self.review["stock_state"] = "received"
        with self.assertRaisesRegex(ValueError, "transfer_receipt_ref"):
            validate(self.signal, self.review)
        self.review.update(transfer_receipt_ref="shopify:transfer-receipt-44", inventory_verification_ref="shopify:inventory-level-after-44", received_observed_at="2026-09-30T10:00:00Z")
        validate(self.signal, self.review)

    def test_signal_rejects_boolean_stock(self):
        self.signal["available_qty"] = True
        with self.assertRaisesRegex(ValueError, "available_qty"):
            validate_signal(self.signal)


if __name__ == "__main__":
    unittest.main()
