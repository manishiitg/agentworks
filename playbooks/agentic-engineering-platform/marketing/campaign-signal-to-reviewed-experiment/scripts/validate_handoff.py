#!/usr/bin/env python3
"""Validate campaign evidence to experiment proposal; actual source reads remain required."""

import json
import re
import sys
from datetime import date, datetime, timedelta
from decimal import Decimal, InvalidOperation
from pathlib import Path


IDENTITY = ("tenant_id", "account_id", "campaign_id", "offer_id", "market", "metric_id", "attribution_window", "qualified_event_id")


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def when(value, label):
    nonempty(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def day(value, label):
    nonempty(value, label)
    try:
        return date.fromisoformat(value)
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO date") from exc


def decimal(value, label):
    nonempty(value, label)
    try:
        number = Decimal(value)
    except InvalidOperation as exc:
        raise ValueError(f"{label} must be decimal") from exc
    if not number.is_finite() or number < 0:
        raise ValueError(f"{label} must be finite and nonnegative")
    return number


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} needs source IDs")
    for index, ref in enumerate(value):
        nonempty(ref, f"{label}[{index}]")
    if len(value) != len(set(value)):
        raise ValueError(f"{label} source IDs must be unique")


def cite(ref, available, label):
    nonempty(ref, label)
    if ref not in available:
        raise ValueError(f"{label} lacks source citation")


def artifact(value, kind, fields):
    if not isinstance(value, dict) or value.get("artifact_type") != kind:
        raise ValueError(f"expected {kind}")
    for field in fields:
        nonempty(value.get(field), f"{kind}.{field}")
    refs(value.get("source_refs"), f"{kind}.source_refs")
    return when(value.get("observed_at"), f"{kind}.observed_at")


def count(value, label):
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError(f"{label} must be a nonnegative integer")
    return value


def validate_performance(brief):
    observed = artifact(brief, "campaign-performance-brief/v1", (*IDENTITY, "brief_id", "currency", "period_start", "period_end", "baseline_start", "baseline_end"))
    if brief["metric_id"] != "qualified_events_per_click":
        raise ValueError("this route needs qualified_events_per_click")
    if len(brief["currency"]) != 3 or not brief["currency"].isalpha() or brief["currency"].upper() != brief["currency"]:
        raise ValueError("currency must be an uppercase ISO code")
    current_start = day(brief["period_start"], "period_start")
    current_end = day(brief["period_end"], "period_end")
    baseline_start = day(brief["baseline_start"], "baseline_start")
    baseline_end = day(brief["baseline_end"], "baseline_end")
    if current_start > current_end or baseline_start > baseline_end or baseline_end >= current_start:
        raise ValueError("baseline must precede a valid current period")
    if (current_end - current_start).days != (baseline_end - baseline_start).days:
        raise ValueError("current and baseline periods must have equal duration")
    window = re.fullmatch(r"(\d+)-day-click", brief["attribution_window"])
    if not window:
        raise ValueError("attribution_window must name a day-count click window")
    if observed.date() < current_end + timedelta(days=int(window.group(1))):
        raise ValueError("attribution window has not elapsed")
    for period in ("current", "baseline"):
        clicks = count(brief.get(period + "_clicks"), period + "_clicks")
        qualified = count(brief.get(period + "_qualified_events"), period + "_qualified_events")
        if clicks == 0 or qualified > clicks:
            raise ValueError(f"{period} qualified events need a valid click denominator")
        expected = Decimal(qualified) / Decimal(clicks)
        if decimal(brief.get(period + "_rate"), period + "_rate") != expected:
            raise ValueError(f"{period} rate does not match numerator and denominator")
        decimal(brief.get(period + "_spend"), period + "_spend")
        coverage = decimal(brief.get(period + "_coverage"), period + "_coverage")
        if coverage <= 0 or coverage > 1:
            raise ValueError(f"{period} coverage must be between zero and one")
        cite(brief.get(period + "_platform_ref"), brief["source_refs"], period + "_platform_ref")
        cite(brief.get(period + "_conversion_ref"), brief["source_refs"], period + "_conversion_ref")
    if brief.get("claim_state") != "observed_only":
        raise ValueError("performance cannot claim causality")
    if brief.get("lag_state") != "settled":
        raise ValueError("comparison requires settled attribution window")
    if not isinstance(brief.get("coverage_gaps"), list):
        raise ValueError("coverage_gaps must be listed")
    if (decimal(brief["current_coverage"], "current_coverage") < 1 or decimal(brief["baseline_coverage"], "baseline_coverage") < 1) and not brief["coverage_gaps"]:
        raise ValueError("partial coverage needs an explicit gap")


def validate_competitor(brief, context):
    validate_performance(brief)
    observed = artifact(context, "competitor-context/v1", ("tenant_id", "offer_id", "market", "context_id", "competitor_product_id", "before_ref", "after_ref", "before_captured_at", "after_captured_at"))
    for field in ("tenant_id", "offer_id", "market"):
        if context[field] != brief[field]:
            raise ValueError(f"competitor {field} mismatch")
    cite(context["before_ref"], context["source_refs"], "before_ref")
    cite(context["after_ref"], context["source_refs"], "after_ref")
    if context["before_ref"] == context["after_ref"]:
        raise ValueError("competitor change needs distinct before and after evidence")
    before = when(context["before_captured_at"], "before_captured_at")
    after = when(context["after_captured_at"], "after_captured_at")
    if before >= after or after > observed:
        raise ValueError("competitor captures need ordered dates before observation")
    if observed > when(brief["observed_at"], "brief.observed_at"):
        raise ValueError("competitor context postdates the campaign brief")
    nonempty(context.get("observed_change"), "observed_change")
    if context.get("claim_state") != "vendor_source_only":
        raise ValueError("competitor source cannot prove customer preference")


def validate_plan(brief, plan, competitor=None):
    validate_performance(brief)
    observed = artifact(plan, "growth-experiment-plan/v1", (*IDENTITY, "plan_id", "source_brief_id", "owner_id", "hypothesis", "eligible_unit", "treatment", "primary_metric", "guardrail_metric", "sample_rule", "stop_rule", "next_action"))
    for field in IDENTITY:
        if plan[field] != brief[field]:
            raise ValueError(f"experiment {field} mismatch")
    if plan["source_brief_id"] != brief["brief_id"]:
        raise ValueError("experiment cites a different performance brief")
    cite(plan["source_brief_id"], plan["source_refs"], "source_brief_id")
    if observed < when(brief["observed_at"], "brief.observed_at"):
        raise ValueError("experiment predates campaign brief")
    if plan["primary_metric"] != brief["metric_id"]:
        raise ValueError("experiment primary metric differs from measured metric")
    if decimal(plan.get("baseline_rate"), "baseline_rate") != decimal(brief["baseline_rate"], "brief.baseline_rate"):
        raise ValueError("experiment baseline rate mismatch")
    if plan.get("activation_state") != "proposal" or plan.get("provider_receipt_ref"):
        raise ValueError("experiment launch needs a separate approved route")
    if plan.get("decision_state") != "pending_owner_review":
        raise ValueError("experiment plan must await owner review")
    if competitor is None:
        if plan.get("source_context_id"):
            raise ValueError("experiment cites missing competitor context")
    else:
        validate_competitor(brief, competitor)
        if plan.get("source_context_id") != competitor["context_id"]:
            raise ValueError("experiment cites a different competitor context")
        cite(plan["source_context_id"], plan["source_refs"], "source_context_id")


def main(argv):
    modes = {"performance": (1, validate_performance), "competitor": (2, validate_competitor), "plan": (2, validate_plan), "plan-with-context": (3, validate_plan)}
    if not argv or argv[0] not in modes or len(argv) != modes[argv[0]][0] + 1:
        raise SystemExit("usage: validate_handoff.py performance|competitor|plan|plan-with-context artifact.json ...")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        modes[argv[0]][1](*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid marketing handoff: {exc}") from exc
    print(f"valid marketing {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
