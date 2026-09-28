#!/usr/bin/env python3
"""Exercise site-bound strategist-to-search artifact joins."""

import json
import unittest
from pathlib import Path

from validate_growth_artifact import InvalidArtifact, validate_brief, validate_search, validate_content, validate_page, validate_shipped, validate_distribution, validate_traffic

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class WebsiteGrowthContractTests(unittest.TestCase):
    def test_good_priority_to_search_handoff(self):
        brief = fixture("growth-priority-brief.json")
        validate_brief(brief)
        validate_search(fixture("search-opportunity-list.json"), brief)

    def test_other_site_search_is_rejected(self):
        with self.assertRaises(InvalidArtifact):
            validate_search(
                fixture("invalid-search-opportunity-list.json"),
                fixture("growth-priority-brief.json"),
            )

    def test_page_must_have_been_inspected(self):
        brief = fixture("growth-priority-brief.json")
        brief["findings"][0]["page_url"] = "https://arbordesk.example/uninspected"
        with self.assertRaisesRegex(InvalidArtifact, "was not inspected"):
            validate_brief(brief)

    def test_approved_content_to_reviewable_page_handoff(self):
        brief = fixture("growth-priority-brief.json")
        search = fixture("search-opportunity-list.json")
        content = fixture("content-brief.json")
        validate_content(content, search, brief)
        validate_page(fixture("reviewable-page-draft.json"), content, search, brief)

    def test_unapproved_hypothesis_cannot_become_a_content_brief(self):
        with self.assertRaises(InvalidArtifact):
            validate_content(fixture("invalid-content-brief.json"), fixture("search-opportunity-list.json"), fixture("growth-priority-brief.json"))

    def test_owner_approval_is_required_for_sourced_content(self):
        content = fixture("content-brief.json")
        content["approved_opportunity"]["decision"] = "pending"
        with self.assertRaisesRegex(InvalidArtifact, "owner approval"):
            validate_content(content, fixture("search-opportunity-list.json"), fixture("growth-priority-brief.json"))

    def test_unverified_claim_and_false_publication_block_page(self):
        with self.assertRaises(InvalidArtifact):
            validate_page(fixture("invalid-page-draft.json"), fixture("content-brief.json"), fixture("search-opportunity-list.json"), fixture("growth-priority-brief.json"))

    def test_unverified_claim_cannot_appear_in_an_unpublished_draft(self):
        page = fixture("reviewable-page-draft.json")
        page["sections"][0]["claim_ids"] = ["C-002"]
        with self.assertRaisesRegex(InvalidArtifact, "unverified claim"):
            validate_page(page, fixture("content-brief.json"), fixture("search-opportunity-list.json"), fixture("growth-priority-brief.json"))

    def test_wrong_search_artifact_stops_content_handoff(self):
        search = fixture("search-opportunity-list.json")
        search["artifact_id"] = "different-search-run"
        with self.assertRaisesRegex(InvalidArtifact, "different search artifact"):
            validate_content(fixture("content-brief.json"), search, fixture("growth-priority-brief.json"))

    def route(self):
        return (fixture("growth-priority-brief.json"), fixture("search-opportunity-list.json"),
                fixture("content-brief.json"), fixture("approved-page-draft.json"),
                fixture("shipped-change.json"), fixture("distribution-plan.json"))

    def test_verified_publish_distribution_and_measurement_route(self):
        brief, search, content, page, shipped, distribution = self.route()
        validate_shipped(shipped, page, content, search, brief)
        validate_distribution(distribution, shipped, page, content, search, brief)
        validate_traffic(fixture("traffic-readout.json"), shipped, page, content, search, brief, distribution)

    def test_unapproved_page_cannot_be_shipped(self):
        brief, search, content, _, shipped, _ = self.route()
        with self.assertRaisesRegex(InvalidArtifact, "approved page draft"):
            validate_shipped(shipped, fixture("reviewable-page-draft.json"), content, search, brief)

    def test_false_publish_receipt_is_rejected(self):
        brief, search, content, page, _, _ = self.route()
        with self.assertRaises(InvalidArtifact):
            validate_shipped(fixture("invalid-shipped-change.json"), page, content, search, brief)
        shipped = fixture("shipped-change.json")
        shipped["source_draft_ref"] = "cms-draft:other@v1"
        with self.assertRaisesRegex(InvalidArtifact, "different draft revision"):
            validate_shipped(shipped, page, content, search, brief)

    def test_distribution_requires_exact_verified_ship(self):
        brief, search, content, page, shipped, _ = self.route()
        with self.assertRaises(InvalidArtifact):
            validate_distribution(fixture("invalid-distribution-plan.json"), shipped, page, content, search, brief)
        shipped["change_state"] = "pending"
        shipped["publish_receipt_ref"] = None
        shipped["published_at"] = None
        shipped["live_check_ref"] = None
        shipped["live_checked_at"] = None
        shipped["live_revision_ref"] = None
        shipped["live_checks"] = None
        with self.assertRaisesRegex(InvalidArtifact, "verified shipped change"):
            validate_distribution(fixture("distribution-plan.json"), shipped, page, content, search, brief)

    def test_invalid_traffic_counts_or_windows_are_rejected(self):
        brief, search, content, page, shipped, distribution = self.route()
        with self.assertRaises(InvalidArtifact):
            validate_traffic(fixture("invalid-traffic-readout.json"), shipped, page, content, search, brief, distribution)
        report = fixture("traffic-readout.json")
        report["current"]["end_at"] = "2026-10-12T11:00:00Z"
        with self.assertRaises(InvalidArtifact):
            validate_traffic(report, shipped, page, content, search, brief, distribution)

    def test_baseline_first_is_explicitly_inconclusive(self):
        brief, search, content, page, shipped, _ = self.route()
        report = fixture("traffic-readout.json")
        report.update(measurement_state="baseline_first", baseline=None, current=None,
                      observed_traffic_direction=None, observed_action_rate_direction=None,
                      source_distribution_artifact_id=None)
        validate_traffic(report, shipped, page, content, search, brief)
        report["observed_traffic_direction"] = "increase"
        with self.assertRaisesRegex(InvalidArtifact, "cannot claim a trend"):
            validate_traffic(report, shipped, page, content, search, brief)


if __name__ == "__main__":
    unittest.main()
