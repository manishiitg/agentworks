#!/usr/bin/env python3
"""Contract checks for the Inbound Lead-to-Meeting Review handoffs."""

from __future__ import annotations

import importlib.util
import json
import unittest
from pathlib import Path


PACKAGE = Path(__file__).resolve().parents[1] / "agentic-engineering-platform/sales/inbound-lead-to-meeting-review"
SPEC = importlib.util.spec_from_file_location("sales_validator", PACKAGE / "scripts/validate_sales_artifact.py")
assert SPEC and SPEC.loader
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


class SalesHandoffContractTest(unittest.TestCase):
    def setUp(self) -> None:
        self.qualification = json.loads((PACKAGE / "examples/lead-qualification-brief.json").read_text())
        self.research = json.loads((PACKAGE / "examples/account-research-brief.json").read_text())
        self.followup = json.loads((PACKAGE / "examples/sales-followup-draft.json").read_text())
        self.approved = json.loads((PACKAGE / "examples/lead-qualification-approved.json").read_text())
        self.booking_draft = json.loads((PACKAGE / "examples/sales-followup-booking-draft.json").read_text())
        self.delivery = json.loads((PACKAGE / "examples/sales-delivery-receipt.json").read_text())
        self.meeting = json.loads((PACKAGE / "examples/sales-meeting-outcome.json").read_text())
        self.instant_meeting = json.loads((PACKAGE / "examples/sales-instant-meeting-outcome.json").read_text())

    def test_valid_required_and_optional_routes(self) -> None:
        validator.validate_qualification(self.qualification)
        validator.validate_research(self.research, self.qualification)
        validator.validate_followup(self.followup, self.qualification, self.research)
        self.followup["source_research_id"] = None
        validator.validate_followup(self.followup, self.qualification, None)

    def test_invalid_source_reference_blocks_handoff(self) -> None:
        invalid = json.loads((PACKAGE / "examples/invalid-lead-qualification-brief.json").read_text())
        with self.assertRaisesRegex(validator.InvalidArtifact, "unknown source"):
            validator.validate_qualification(invalid)

    def test_contact_and_duplicate_states_block_draft(self) -> None:
        self.followup["source_research_id"] = None
        self.qualification["duplicate_state"] = "possible"
        with self.assertRaisesRegex(validator.InvalidArtifact, "duplicate lead"):
            validator.validate_followup(self.followup, self.qualification, None)
        with self.assertRaisesRegex(validator.InvalidArtifact, "duplicate lead"):
            validator.require_ready_for_handoff(validator.validate_qualification(self.qualification))
        self.qualification["duplicate_state"] = "none"
        self.qualification["contact_policy_status"] = "blocked"
        with self.assertRaisesRegex(validator.InvalidArtifact, "contact policy"):
            validator.validate_followup(self.followup, self.qualification, None)

    def test_draft_cannot_be_presented_as_sent_or_use_other_lead(self) -> None:
        self.followup["source_research_id"] = None
        self.followup["send_state"] = "sent"
        with self.assertRaisesRegex(validator.InvalidArtifact, "unsent draft"):
            validator.validate_followup(self.followup, self.qualification, None)
        self.followup["send_state"] = "not_sent"
        self.followup["lead_id"] = "another-lead"
        with self.assertRaisesRegex(validator.InvalidArtifact, "different lead"):
            validator.validate_followup(self.followup, self.qualification, None)

    def test_optional_research_must_match_the_qualified_account(self) -> None:
        self.research["company_domain"] = "different-company.example"
        with self.assertRaisesRegex(validator.InvalidArtifact, "company_domain differs"):
            validator.validate_research(self.research, self.qualification)

    def test_delivery_and_booking_require_provider_evidence_and_exact_review(self) -> None:
        validator.validate_delivery(self.delivery, self.approved, self.booking_draft)
        validator.validate_meeting(self.meeting, self.approved, self.delivery, self.booking_draft)
        self.booking_draft["body"] += " An unreviewed sentence."
        with self.assertRaisesRegex(validator.InvalidArtifact, "differs from reviewed draft"):
            validator.validate_delivery(self.delivery, self.approved, self.booking_draft)

    def test_preflight_and_contact_policy_block_delivery(self) -> None:
        with self.assertRaisesRegex(validator.InvalidArtifact, "approved contact policy"):
            validator.validate_delivery(self.delivery, self.qualification, self.booking_draft)
        self.delivery["preflight_reply_state"] = "replied"
        with self.assertRaisesRegex(validator.InvalidArtifact, "lead reply"):
            validator.validate_delivery(self.delivery, self.approved, self.booking_draft)

    def test_booked_meeting_must_link_to_the_sent_action(self) -> None:
        self.meeting["source_action_id"] = "another-action"
        with self.assertRaisesRegex(validator.InvalidArtifact, "different delivery action"):
            validator.validate_meeting(self.meeting, self.approved, self.delivery, self.booking_draft)

    def test_instant_booking_requires_matching_lead_and_booking_session(self) -> None:
        validator.validate_meeting(self.instant_meeting, self.approved)
        self.instant_meeting["source_brief_id"] = "another-brief"
        with self.assertRaisesRegex(validator.InvalidArtifact, "different qualification brief"):
            validator.validate_meeting(self.instant_meeting, self.approved)


if __name__ == "__main__":
    unittest.main()
