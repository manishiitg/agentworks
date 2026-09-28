#!/usr/bin/env python3
"""Exercise Sales qualification, reviewed follow-up, delivery and booking joins."""

import json
import unittest
from pathlib import Path

from validate_sales_artifact import (
    InvalidArtifact, validate_delivery, validate_followup,
    validate_meeting, validate_qualification,
)

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class SalesContractTests(unittest.TestCase):
    def setUp(self):
        self.qualification = fixture("lead-qualification-approved.json")
        self.followup = fixture("sales-followup-booking-draft.json")
        self.delivery = fixture("sales-delivery-receipt.json")

    def test_good_qualification_to_observed_meeting(self):
        validate_qualification(self.qualification)
        validate_followup(self.followup, self.qualification, None)
        validate_delivery(self.delivery, self.qualification, self.followup)
        validate_meeting(
            fixture("sales-meeting-outcome.json"), self.qualification,
            self.delivery, self.followup,
        )

    def test_rejected_qualification_does_not_reach_followup(self):
        with self.assertRaises(InvalidArtifact):
            validate_qualification(fixture("invalid-lead-qualification-brief.json"))
        qualification = fixture("lead-qualification-brief.json")
        qualification["duplicate_state"] = "known"
        with self.assertRaisesRegex(InvalidArtifact, "duplicate lead"):
            validate_followup(self.followup, qualification, None)

    def test_delivery_must_match_exact_recipient(self):
        delivery = fixture("sales-delivery-receipt.json")
        delivery["recipient_ref"] = "crm-export:another-contact"
        with self.assertRaisesRegex(InvalidArtifact, "recipient differs"):
            validate_delivery(delivery, self.qualification, self.followup)


if __name__ == "__main__":
    unittest.main()
