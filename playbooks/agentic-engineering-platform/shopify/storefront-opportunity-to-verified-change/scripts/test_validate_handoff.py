#!/usr/bin/env python3
"""Exercise identity, measurement, approval, and publish proof gates."""

import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_opportunity


EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


class GrowthCatalogHandoffTests(unittest.TestCase):
    def setUp(self):
        self.opportunity = json.loads((EXAMPLES / "shopify-growth-opportunity.json").read_text())
        self.change = json.loads((EXAMPLES / "catalog-change-review.json").read_text())

    def test_valid_pending_review(self):
        validate_opportunity(self.opportunity)
        validate(self.opportunity, self.change)

    def test_rejects_wrong_variant(self):
        self.change["variant_id"] = "variant-81-l"
        with self.assertRaisesRegex(ValueError, "variant_id mismatch"):
            validate(self.opportunity, self.change)

    def test_rejects_qualitative_metric_claim(self):
        self.opportunity["metric"] = {"name": "conversion", "numerator": 1, "denominator": 5}
        with self.assertRaisesRegex(ValueError, "qualitative opportunity"):
            validate_opportunity(self.opportunity)

    def test_rejects_bad_measured_denominator(self):
        self.opportunity["evidence_kind"] = "measured"
        self.opportunity["metric"] = {"name": "add-to-cart/session", "source_ref": "analytics:44", "start": "2026-09-01", "end": "2026-09-14", "segment": "US mobile", "numerator": 96, "denominator": 0}
        with self.assertRaisesRegex(ValueError, "numerator and denominator"):
            validate_opportunity(self.opportunity)

    def test_rejects_published_without_approval(self):
        self.change["publish_state"] = "published"
        with self.assertRaisesRegex(ValueError, "owner approval"):
            validate(self.opportunity, self.change)

    def test_rejects_published_without_receipt(self):
        self.change.update(approval_state="approved", approval_ref="merchant:approval-7", publish_state="published")
        with self.assertRaisesRegex(ValueError, "publish_receipt_ref"):
            validate(self.opportunity, self.change)

    def test_rejects_verified_without_retest(self):
        self.change.update(approval_state="approved", approval_ref="merchant:approval-7", publish_state="verified", publish_receipt_ref="shopify:edit-7")
        with self.assertRaisesRegex(ValueError, "retest_ref"):
            validate(self.opportunity, self.change)

    def test_rejects_comparable_result_without_measured_baseline(self):
        self.change["measurement_state"] = "comparable_result"
        with self.assertRaisesRegex(ValueError, "measured baseline"):
            validate(self.opportunity, self.change)

    def test_valid_measured_and_verified_result(self):
        self.opportunity["evidence_kind"] = "measured"
        self.opportunity["metric"] = {"name": "add-to-cart/session", "source_ref": "analytics:baseline-44", "start": "2026-09-01", "end": "2026-09-14", "segment": "US mobile", "numerator": 96, "denominator": 1200}
        self.change.update(approval_state="approved", approval_ref="merchant:approval-7", publish_state="verified", publish_receipt_ref="shopify:edit-7", retest_ref="storefront:variant-81-m-after", measurement_state="comparable_result", followup_report_ref="analytics:followup-45", followup_metric={"name": "add-to-cart/session", "source_ref": "analytics:followup-45", "start": "2026-09-15", "end": "2026-09-28", "segment": "US mobile", "numerator": 105, "denominator": 1250})
        validate(self.opportunity, self.change)

    def test_rejects_changed_followup_denominator_rule(self):
        self.opportunity["evidence_kind"] = "measured"
        self.opportunity["metric"] = {"name": "add-to-cart/session", "source_ref": "analytics:baseline-44", "start": "2026-09-01", "end": "2026-09-14", "segment": "US mobile", "numerator": 96, "denominator": 1200}
        self.change.update(approval_state="approved", approval_ref="merchant:approval-7", publish_state="verified", publish_receipt_ref="shopify:edit-7", retest_ref="storefront:variant-81-m-after", measurement_state="comparable_result", followup_report_ref="analytics:followup-45", followup_metric={"name": "checkout/order", "source_ref": "analytics:followup-45", "start": "2026-09-15", "end": "2026-09-28", "segment": "US mobile", "numerator": 50, "denominator": 200})
        with self.assertRaisesRegex(ValueError, "same name and segment"):
            validate(self.opportunity, self.change)

    def test_invalid_fixture_rejected(self):
        invalid = json.loads((EXAMPLES / "invalid-catalog-change-review.json").read_text())
        with self.assertRaises(ValueError):
            validate(self.opportunity, invalid)


if __name__ == "__main__":
    unittest.main()
