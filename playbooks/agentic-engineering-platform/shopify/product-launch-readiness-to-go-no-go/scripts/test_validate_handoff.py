import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_catalog


EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


class LaunchHandoffTests(unittest.TestCase):
    def setUp(self):
        self.catalog = json.loads((EXAMPLES / "launch-catalog-readiness.json").read_text())
        self.decision = json.loads((EXAMPLES / "launch-storefront-decision.json").read_text())

    def test_valid_pending_go_review(self):
        validate(self.catalog, self.decision)

    def test_wrong_publication_rejected(self):
        self.decision["publication_id"] = "publication-other-market"
        with self.assertRaisesRegex(ValueError, "publication_id mismatch"):
            validate(self.catalog, self.decision)

    def test_ready_flag_with_blocker_rejected(self):
        self.catalog["blockers"] = ["Price not approved"]
        with self.assertRaisesRegex(ValueError, "conflicts with blockers"):
            validate_catalog(self.catalog)

    def test_go_review_with_blocked_journey_rejected(self):
        self.decision["journey_state"] = "blocked"
        with self.assertRaisesRegex(ValueError, "passing journey"):
            validate(self.catalog, self.decision)

    def test_published_without_approval_rejected(self):
        self.decision["publish_state"] = "published"
        with self.assertRaisesRegex(ValueError, "approved go review"):
            validate(self.catalog, self.decision)

    def test_verified_without_retest_rejected(self):
        self.decision.update(approval_state="approved", approval_ref="merchant:approval-1", publish_state="verified", publish_receipt_ref="shopify:publish-1")
        with self.assertRaisesRegex(ValueError, "retest_ref"):
            validate(self.catalog, self.decision)

    def test_valid_verified_launch(self):
        self.decision.update(approval_state="approved", approval_ref="merchant:approval-1", publish_state="verified", publish_receipt_ref="shopify:publish-1", retest_ref="storefront:us-product-after-1")
        validate(self.catalog, self.decision)

    def test_invalid_fixture_rejected(self):
        invalid = json.loads((EXAMPLES / "invalid-launch-storefront-decision.json").read_text())
        with self.assertRaises(ValueError):
            validate(self.catalog, invalid)


if __name__ == "__main__":
    unittest.main()
