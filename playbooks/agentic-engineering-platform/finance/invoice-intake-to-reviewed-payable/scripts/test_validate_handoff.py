#!/usr/bin/env python3
"""Contract tests for exact invoice identity, AP state and action receipts."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_intake, validate_review


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class InvoiceHandoffTests(unittest.TestCase):
    def setUp(self):
        self.intake = fixture("document-intake-record.json")
        self.review = fixture("payable-review.json")

    def test_fictional_invoice_and_payable_review(self):
        validate_intake(self.intake)
        validate_review(self.intake, self.review)

    def test_missing_page_evidence_and_wrong_arithmetic_stop_handoff(self):
        intake = copy.deepcopy(self.intake)
        intake["fields"]["vendor_name"]["source_ref"] = "doc:other-document@v1#p1:l3"
        intake["source_refs"].append("doc:other-document@v1#p1:l3")
        with self.assertRaisesRegex(ValueError, "page/span"):
            validate_intake(intake)
        intake = copy.deepcopy(self.intake)
        intake["fields"]["tax_amount"]["value"] = "9.00"
        with self.assertRaisesRegex(ValueError, "net plus tax"):
            validate_intake(intake)

    def test_hash_vendor_and_invoice_key_must_join(self):
        for field, value in (
            ("document_hash", "b" * 64),
            ("vendor_id", "other-vendor"),
            ("invoice_number", "INV-89"),
            ("duplicate_key", "example-us-inc:vendor-42:inv-89"),
        ):
            review = copy.deepcopy(self.review)
            review[field] = value
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "mismatch"):
                validate_review(self.intake, review)

    def test_possible_or_existing_duplicate_cannot_be_new_bill(self):
        review = copy.deepcopy(self.review)
        review["duplicate_state"] = "possible"
        with self.assertRaisesRegex(ValueError, "possible duplicate"):
            validate_review(self.intake, review)
        review = copy.deepcopy(self.review)
        review["duplicate_state"] = "existing"
        review["existing_bill_id"] = "bill-88"
        review["source_refs"].append("ap:bill-88")
        with self.assertRaisesRegex(ValueError, "existing bill"):
            validate_review(self.intake, review)
        review["disposition"] = "existing_bill"
        review["approval_state"] = "approved"
        review["action_state"] = "bill_written"
        review["bill_receipt_ref"] = "ap:receipt-bill-duplicate"
        review["source_refs"].append("ap:receipt-bill-duplicate")
        with self.assertRaisesRegex(ValueError, "written again"):
            validate_review(self.intake, review)

    def test_paid_claim_requires_provider_receipt(self):
        with self.assertRaisesRegex(ValueError, "provider receipt"):
            validate_review(self.intake, fixture("invalid-payable-review.json"))

    def test_bill_write_requires_approval_and_receipt(self):
        review = copy.deepcopy(self.review)
        review["action_state"] = "bill_written"
        review["disposition"] = "approved_for_bill_write"
        with self.assertRaisesRegex(ValueError, "owner approval"):
            validate_review(self.intake, review)
        review["approval_state"] = "approved"
        with self.assertRaisesRegex(ValueError, "provider receipt"):
            validate_review(self.intake, review)
        review["bill_receipt_ref"] = "ap:receipt-bill-88"
        review["source_refs"].append("ap:receipt-bill-88")
        validate_review(self.intake, review)

    def test_ap_source_must_be_current(self):
        review = copy.deepcopy(self.review)
        review["ap_observed_at"] = "2026-09-26T09:00:00Z"
        with self.assertRaisesRegex(ValueError, "re-read after extraction"):
            validate_review(self.intake, review)


if __name__ == "__main__":
    unittest.main()
