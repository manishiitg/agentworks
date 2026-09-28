import importlib.util
import json
import unittest
from pathlib import Path

from validate_handoff import validate_brief, validate_lead, validate_register


PACKAGE = Path(__file__).resolve().parent.parent
EXAMPLES = PACKAGE / "examples"
SALES_VALIDATOR = PACKAGE.parents[1] / "sales/inbound-lead-to-meeting-review/scripts/validate_sales_artifact.py"
SPEC = importlib.util.spec_from_file_location("gtm_sales_validator", SALES_VALIDATOR)
assert SPEC and SPEC.loader
sales = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(sales)


class GTMHandoffTests(unittest.TestCase):
    def setUp(self):
        self.brief = json.loads((EXAMPLES / "gtm-launch-brief.json").read_text())
        self.register = json.loads((EXAMPLES / "launch-signal-register.json").read_text())
        self.qualification = json.loads((EXAMPLES / "lead-qualification-brief.json").read_text())

    def test_complete_launch_to_sales_handoff(self):
        validate_lead(self.brief, self.register, self.qualification)
        sales.require_ready_for_handoff(sales.validate_qualification(self.qualification))

    def test_unapproved_brief_stops_launch(self):
        self.brief["approval_state"] = "pending"
        with self.assertRaisesRegex(ValueError, "owner approval"):
            validate_register(self.brief, self.register)

    def test_claim_without_source_rejected(self):
        self.brief["claim_refs"].append("unsourced-claim")
        with self.assertRaisesRegex(ValueError, "claim refs"):
            validate_brief(self.brief)

    def test_wrong_offer_rejected(self):
        self.register["offer_version"] = "different-offer"
        with self.assertRaisesRegex(ValueError, "offer_version mismatch"):
            validate_register(self.brief, self.register)

    def test_publication_needs_receipt(self):
        self.register["publication_receipt_ref"] = None
        with self.assertRaisesRegex(ValueError, "publication_receipt_ref"):
            validate_register(self.brief, self.register)

    def test_campaign_mismatch_blocks_attribution(self):
        self.register["events"][0]["campaign_id"] = "another-campaign"
        with self.assertRaisesRegex(ValueError, "matching campaign"):
            validate_register(self.brief, self.register)

    def test_duplicate_event_cannot_be_qualified(self):
        self.register["events"][0]["dedup_state"] = "duplicate"
        with self.assertRaisesRegex(ValueError, "one nonduplicate"):
            validate_lead(self.brief, self.register, self.qualification)

    def test_blocked_contact_stops_pipeline_handoff(self):
        self.register["events"][0]["contact_policy_state"] = "blocked"
        with self.assertRaisesRegex(ValueError, "contact policy"):
            validate_lead(self.brief, self.register, self.qualification)

    def test_wrong_lead_source_rejected(self):
        self.qualification["contact_ref"] = "crm:other"
        with self.assertRaisesRegex(ValueError, "contact differs"):
            validate_lead(self.brief, self.register, self.qualification)

    def test_invalid_fixture_rejected(self):
        invalid = json.loads((EXAMPLES / "invalid-launch-signal-register.json").read_text())
        with self.assertRaises(ValueError):
            validate_register(self.brief, invalid)


if __name__ == "__main__":
    unittest.main()
