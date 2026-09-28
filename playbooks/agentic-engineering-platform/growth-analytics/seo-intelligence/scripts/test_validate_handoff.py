#!/usr/bin/env python3
"""Contract checks for the SEO technical-to-buyer-question route."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_issue, validate_opportunity


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class SEOHandoffTests(unittest.TestCase):
    def setUp(self):
        self.issues = fixture("seo-issue-list.json")
        self.opportunities = fixture("seo-opportunity-list.json")

    def test_source_linked_manual_route(self):
        validate_issue(self.issues)
        validate_opportunity(self.issues, self.opportunities)

    def test_wrong_site_or_issue_cannot_flow_to_mapper(self):
        for field, replacement in (("site_url", "https://other.example/"), ("source_issue_artifact_id", "seo-issues-other")):
            changed = copy.deepcopy(self.opportunities)
            changed[field] = replacement
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "mismatch|different issue"):
                validate_opportunity(self.issues, changed)
        changed = copy.deepcopy(self.opportunities)
        changed["opportunities"][0]["blocking_issue_ids"] = ["SEO-OTHER"]
        with self.assertRaisesRegex(ValueError, "blocking issue"):
            validate_opportunity(self.issues, changed)

    def test_public_fetch_cannot_claim_indexation_or_publication(self):
        changed = copy.deepcopy(self.issues)
        changed["issues"][0]["index_state"] = "indexed"
        with self.assertRaisesRegex(ValueError, "cannot prove indexation"):
            validate_issue(changed)
        with self.assertRaisesRegex(ValueError, "separate approved route"):
            validate_opportunity(self.issues, fixture("invalid-seo-opportunity-list.json"))

    def test_unapproved_page_is_not_part_of_crawl(self):
        changed = copy.deepcopy(self.issues)
        changed["issues"][0]["page_url"] = "https://arbordesk.example/private"
        with self.assertRaisesRegex(ValueError, "outside approved crawl scope"):
            validate_issue(changed)

    def test_measured_demand_needs_comparable_source_counts(self):
        changed = copy.deepcopy(self.opportunities)
        item = changed["opportunities"][0]
        item["demand_state"] = "measured"
        item["search_metrics"] = {
            "property": "https://arbordesk.example/", "query": "clinic patient reminders", "page_url": item["page_url"],
            "market": changed["market"], "locale": changed["locale"], "device": changed["device"], "source_ref": "gsc-export",
            "current_start": "2026-09-01", "current_end": "2026-09-14", "baseline_start": "2026-08-18", "baseline_end": "2026-08-31",
            "current_impressions": 800, "current_clicks": 16, "current_ctr": "0.02",
            "baseline_impressions": 600, "baseline_clicks": 15, "baseline_ctr": "0.025", "coverage_state": "complete"
        }
        changed["source_refs"].append({"id": "gsc-export", "uri": "gsc:property-example#query=clinic-patient-reminders", "observed_at": "2026-09-25T10:05:00Z"})
        validate_opportunity(self.issues, changed)
        item["search_metrics"]["current_ctr"] = "0.03"
        with self.assertRaisesRegex(ValueError, "CTR does not match"):
            validate_opportunity(self.issues, changed)
        item["search_metrics"]["current_ctr"] = "0.02"
        item["search_metrics"]["baseline_end"] = "2026-09-01"
        with self.assertRaisesRegex(ValueError, "equally long"):
            validate_opportunity(self.issues, changed)


if __name__ == "__main__":
    unittest.main()
