"""Contract checks for New Customer to First Value handoffs."""

from __future__ import annotations

import importlib.util
import json
import unittest
from pathlib import Path


PACKAGE = Path(__file__).resolve().parents[1] / "agentic-engineering-platform/customer-success/new-customer-to-first-value"
SPEC = importlib.util.spec_from_file_location("customer_success_validator", PACKAGE / "scripts/validate_customer_success_artifact.py")
assert SPEC and SPEC.loader
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


class CustomerSuccessHandoffTest(unittest.TestCase):
    def setUp(self) -> None:
        self.onboarding = json.loads((PACKAGE / "examples/onboarding-milestone-register.json").read_text())
        self.adoption = json.loads((PACKAGE / "examples/first-value-readout.json").read_text())
        self.health = json.loads((PACKAGE / "examples/customer-health-brief.json").read_text())

    def test_valid_required_and_optional_handoffs(self) -> None:
        validator.validate_onboarding(self.onboarding)
        validator.validate_adoption(self.adoption, self.onboarding)
        validator.validate_health(self.health, self.onboarding, self.adoption)

    def test_signed_deal_acceptance_feeds_exact_onboarding_register(self) -> None:
        sales_package = PACKAGE.parent / "signed-deal-to-onboarding-handoff"
        accepted = json.loads((sales_package / "examples/onboarding-acceptance.json").read_text())
        register = json.loads((sales_package / "examples/onboarding-register-from-accepted.json").read_text())
        validator.validate_onboarding(register, accepted)
        accepted["decision"] = "needs_resolution"
        with self.assertRaisesRegex(validator.InvalidArtifact, "accepted CS decision"):
            validator.validate_onboarding(register, accepted)

    def test_completion_needs_evidence_and_known_source(self) -> None:
        self.onboarding["milestones"][0]["evidence_refs"] = []
        with self.assertRaisesRegex(validator.InvalidArtifact, "non-empty list"):
            validator.validate_onboarding(self.onboarding)
        self.onboarding["milestones"][0]["evidence_refs"] = ["not-a-source"]
        with self.assertRaisesRegex(validator.InvalidArtifact, "unknown source"):
            validator.validate_onboarding(self.onboarding)

    def test_reached_requires_observed_agreed_event(self) -> None:
        invalid = json.loads((PACKAGE / "examples/invalid-first-value-readout.json").read_text())
        with self.assertRaisesRegex(validator.InvalidArtifact, "reached requires observed"):
            validator.validate_adoption(invalid, self.onboarding)
        self.adoption["observed_events"][0]["name"] = "user_signed_in"
        with self.assertRaisesRegex(validator.InvalidArtifact, "not the agreed first-value event"):
            validator.validate_adoption(self.adoption, self.onboarding)

    def test_account_tenant_and_rule_must_match_handoff(self) -> None:
        self.adoption["tenant_id"] = "another-tenant"
        with self.assertRaisesRegex(validator.InvalidArtifact, "tenant_id differs"):
            validator.validate_adoption(self.adoption, self.onboarding)
        self.adoption["tenant_id"] = self.onboarding["tenant_id"]
        self.adoption["first_value_rule_id"] = "another-rule"
        with self.assertRaisesRegex(validator.InvalidArtifact, "rule differs"):
            validator.validate_adoption(self.adoption, self.onboarding)

    def test_not_observed_needs_complete_coverage(self) -> None:
        self.adoption["status"] = "not_observed"
        self.adoption["observed_events"] = []
        self.adoption["source_coverage"] = "partial"
        self.adoption["coverage_gap"] = "Some product events were unavailable."
        with self.assertRaisesRegex(validator.InvalidArtifact, "complete source coverage"):
            validator.validate_adoption(self.adoption, self.onboarding)

    def test_coverage_must_have_a_source_and_gap_when_incomplete(self) -> None:
        self.adoption["coverage_ref"] = "missing"
        with self.assertRaisesRegex(validator.InvalidArtifact, "unknown source"):
            validator.validate_adoption(self.adoption, self.onboarding)
        self.adoption["coverage_ref"] = "coverage"
        self.adoption["source_coverage"] = "partial"
        with self.assertRaisesRegex(validator.InvalidArtifact, "coverage_gap"):
            validator.validate_adoption(self.adoption, self.onboarding)

    def test_health_must_cite_the_correct_readout(self) -> None:
        self.health["source_readout_id"] = "another-readout"
        with self.assertRaisesRegex(validator.InvalidArtifact, "different first-value readout"):
            validator.validate_health(self.health, self.onboarding, self.adoption)


if __name__ == "__main__":
    unittest.main()
