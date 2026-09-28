#!/usr/bin/env python3
"""Contract tests for comparable campaign evidence and unlaunched experiment plans."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_competitor, validate_performance, validate_plan


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class MarketingHandoffTests(unittest.TestCase):
    def setUp(self):
        self.brief = fixture("campaign-performance-brief.json")
        self.context = fixture("competitor-context.json")
        self.plan = fixture("growth-experiment-plan.json")

    def test_fictional_campaign_context_and_plan(self):
        validate_performance(self.brief)
        validate_competitor(self.brief, self.context)
        validate_plan(self.brief, self.plan, self.context)

    def test_qualified_rate_must_match_source_counts(self):
        brief = copy.deepcopy(self.brief)
        brief["current_rate"] = "0.04"
        with self.assertRaisesRegex(ValueError, "rate does not match"):
            validate_performance(brief)

    def test_baseline_period_and_source_coverage_must_be_comparable(self):
        brief = copy.deepcopy(self.brief)
        brief["baseline_end"] = "2026-09-05"
        with self.assertRaisesRegex(ValueError, "equal duration"):
            validate_performance(brief)
        brief = copy.deepcopy(self.brief)
        brief["coverage_gaps"] = []
        with self.assertRaisesRegex(ValueError, "partial coverage"):
            validate_performance(brief)
        brief = copy.deepcopy(self.brief)
        brief["period_end"] = "2026-09-24"
        brief["period_start"] = "2026-09-18"
        brief["baseline_start"] = "2026-09-11"
        brief["baseline_end"] = "2026-09-17"
        with self.assertRaisesRegex(ValueError, "window has not elapsed"):
            validate_performance(brief)

    def test_campaign_and_metric_identity_must_join(self):
        for field, value in (
            ("account_id", "ads-other"),
            ("campaign_id", "cmp-other"),
            ("offer_id", "other-offer"),
            ("qualified_event_id", "crm-lead"),
            ("attribution_window", "1-day-click"),
        ):
            plan = copy.deepcopy(self.plan)
            plan[field] = value
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "mismatch"):
                validate_plan(self.brief, plan, self.context)

    def test_competitor_claims_stay_context_only(self):
        context = copy.deepcopy(self.context)
        context["claim_state"] = "proved_customer_preference"
        with self.assertRaisesRegex(ValueError, "cannot prove customer preference"):
            validate_competitor(self.brief, context)
        context = copy.deepcopy(self.context)
        context["offer_id"] = "different-offer"
        with self.assertRaisesRegex(ValueError, "competitor offer_id mismatch"):
            validate_competitor(self.brief, context)
        context = copy.deepcopy(self.context)
        context["after_captured_at"] = "2026-08-30T10:00:00Z"
        with self.assertRaisesRegex(ValueError, "ordered dates"):
            validate_competitor(self.brief, context)

    def test_plan_cannot_claim_active_from_proposal(self):
        with self.assertRaisesRegex(ValueError, "separate approved route"):
            validate_plan(self.brief, fixture("invalid-growth-experiment-plan.json"), self.context)

    def test_optional_context_must_be_attached_if_cited(self):
        with self.assertRaisesRegex(ValueError, "missing competitor context"):
            validate_plan(self.brief, self.plan)


if __name__ == "__main__":
    unittest.main()
