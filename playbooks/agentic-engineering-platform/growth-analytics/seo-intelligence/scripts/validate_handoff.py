#!/usr/bin/env python3
"""Check the typed SEO Crew handoff; source truth still needs a human read."""

import json
import sys
from datetime import date, datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path
from urllib.parse import urlparse


IDENTITY = ("site_url", "market", "locale", "device")


def required(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def timestamp(value, label):
    required(value, label)
    try:
        result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if result.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return result


def site_page(site, page, label):
    host = urlparse(required(site, "site_url"))
    target = urlparse(required(page, label))
    if host.scheme != "https" or not host.netloc or target.scheme != "https" or target.netloc != host.netloc:
        raise ValueError(f"{label} must be on the approved HTTPS site")
    return target


def in_scope(site, scope, page, label):
    approved = site_page(site, scope, "crawl_scope")
    target = site_page(site, page, label)
    prefix = approved.path.rstrip("/")
    if prefix and target.path != prefix and not target.path.startswith(prefix + "/"):
        raise ValueError(f"{label} is outside approved crawl scope")


def sources(value, observed):
    if not isinstance(value, list) or not value:
        raise ValueError("source_refs need dated references")
    ids = set()
    for source in value:
        if not isinstance(source, dict):
            raise ValueError("source_ref must be an object")
        source_id = required(source.get("id"), "source id")
        required(source.get("uri"), "source uri")
        if source_id in ids:
            raise ValueError("source IDs must be unique")
        ids.add(source_id)
        if timestamp(source.get("observed_at"), "source observed_at") > observed:
            raise ValueError("source postdates artifact")
    return {source["id"]: source for source in value}


def citations(value, available, label):
    if not isinstance(value, list) or not value or any(not isinstance(ref, str) for ref in value) or len(value) != len(set(value)):
        raise ValueError(f"{label} needs unique citations")
    if any(ref not in available for ref in value):
        raise ValueError(f"{label} cites a missing source")


def header(value, kind):
    if not isinstance(value, dict) or value.get("artifact_type") != kind:
        raise ValueError(f"expected {kind}")
    for field in ("artifact_id", *IDENTITY):
        required(value.get(field), field)
    site_page(value["site_url"], value["site_url"], "site_url")
    observed = timestamp(value.get("observed_at"), "observed_at")
    return observed, sources(value.get("source_refs"), observed)


def validate_issue(value):
    observed, source_map = header(value, "seo-issue-list/v1")
    site_page(value["site_url"], value.get("crawl_scope"), "crawl_scope")
    issues = value.get("issues")
    if not isinstance(issues, list) or not issues:
        raise ValueError("issue list needs a real observation")
    seen = set()
    for issue in issues:
        if not isinstance(issue, dict):
            raise ValueError("issue must be an object")
        issue_id = required(issue.get("id"), "issue id")
        if issue_id in seen:
            raise ValueError("issue IDs must be unique")
        seen.add(issue_id)
        in_scope(value["site_url"], value["crawl_scope"], issue.get("page_url"), "issue page_url")
        for field in ("observation", "owner_action", "retest"):
            required(issue.get(field), field)
        citations(issue.get("evidence_refs"), source_map, "issue evidence")
        if issue.get("confidence") not in ("low", "medium", "high"):
            raise ValueError("issue confidence must be explicit")
        if issue.get("index_state") not in ("unknown", "indexed", "not_indexed"):
            raise ValueError("issue index_state must be explicit")
        if issue["index_state"] != "unknown" and not any(source_map[ref]["uri"].startswith("gsc:") for ref in issue["evidence_refs"]):
            raise ValueError("public crawl cannot prove indexation")
        if issue.get("change_state") != "observed_unresolved":
            raise ValueError("issue cannot claim an unverified fix")
    if not isinstance(value.get("limitations"), list) or not value["limitations"]:
        raise ValueError("issue limitations must be listed")
    return observed, {issue["id"]: issue for issue in issues}


def measured_metrics(metric, opportunity, source_map):
    if not isinstance(metric, dict):
        raise ValueError("measured demand needs search_metrics")
    for field in ("property", "query", "page_url", "market", "locale", "device", "source_ref"):
        required(metric.get(field), "search_metrics." + field)
    if any(metric[field] != opportunity[field] for field in ("market", "locale", "device")):
        raise ValueError("search metric market/locale/device mismatch")
    if metric["page_url"] != opportunity["page_url"] or metric["source_ref"] not in source_map:
        raise ValueError("search metric page or source mismatch")
    if not source_map[metric["source_ref"]]["uri"].startswith("gsc:"):
        raise ValueError("measured demand needs an authorized Search Console source")
    try:
        current = (date.fromisoformat(metric["current_start"]), date.fromisoformat(metric["current_end"]))
        baseline = (date.fromisoformat(metric["baseline_start"]), date.fromisoformat(metric["baseline_end"]))
    except (KeyError, ValueError) as exc:
        raise ValueError("search metric windows need ISO dates") from exc
    if current[0] > current[1] or baseline[0] > baseline[1] or baseline[1] >= current[0] or (current[1] - current[0]) != (baseline[1] - baseline[0]):
        raise ValueError("search metric windows must be ordered and equally long")
    if current[1] > date.fromisoformat(opportunity["observed_at"][:10]):
        raise ValueError("search metric window cannot end after observation")
    for period in ("current", "baseline"):
        impressions = metric.get(period + "_impressions")
        clicks = metric.get(period + "_clicks")
        if any(isinstance(v, bool) or not isinstance(v, int) or v < 0 for v in (impressions, clicks)) or impressions == 0 or clicks > impressions:
            raise ValueError("search clicks need a valid impressions denominator")
        try:
            ctr = Decimal(metric[period + "_ctr"])
        except (KeyError, InvalidOperation, TypeError) as exc:
            raise ValueError("search CTR needs a decimal value") from exc
        if not ctr.is_finite() or ctr != Decimal(clicks) / Decimal(impressions):
            raise ValueError("search CTR does not match source counts")
    if metric.get("coverage_state") not in ("complete", "partial"):
        raise ValueError("search metric coverage_state is required")
    if metric["coverage_state"] == "partial" and not required(metric.get("coverage_gap"), "coverage_gap"):
        raise ValueError("partial search coverage needs a gap")


def validate_opportunity(issue_list, value):
    issue_observed, issue_map = validate_issue(issue_list)
    observed, source_map = header(value, "seo-opportunity-list/v1")
    if observed < issue_observed:
        raise ValueError("opportunity predates issue list")
    for field in IDENTITY:
        if value[field] != issue_list[field]:
            raise ValueError(f"opportunity {field} mismatch")
    if value.get("source_issue_artifact_id") != issue_list["artifact_id"]:
        raise ValueError("opportunity cites a different issue artifact")
    if not any(ref["uri"] == "artifact:" + issue_list["artifact_id"] for ref in source_map.values()):
        raise ValueError("opportunity lacks issue artifact citation")
    opportunities = value.get("opportunities")
    if not isinstance(opportunities, list) or not opportunities:
        raise ValueError("opportunity list needs a buyer question")
    seen = set()
    for item in opportunities:
        if not isinstance(item, dict):
            raise ValueError("opportunity must be an object")
        item_id = required(item.get("id"), "opportunity id")
        if item_id in seen:
            raise ValueError("opportunity IDs must be unique")
        seen.add(item_id)
        required(item.get("buyer_question"), "buyer_question")
        in_scope(value["site_url"], issue_list["crawl_scope"], item.get("page_url"), "opportunity page_url")
        if item.get("question_source_ref") not in source_map:
            raise ValueError("buyer question lacks a source")
        citations(item.get("evidence_refs"), source_map, "opportunity evidence")
        if item.get("page_decision") not in ("answered", "weak_answer", "gap"):
            raise ValueError("page decision must be explicit")
        blockers = item.get("blocking_issue_ids")
        if not isinstance(blockers, list) or any(ref not in issue_map or issue_map[ref]["page_url"] != item["page_url"] for ref in blockers):
            raise ValueError("blocking issue must cite an issue on the same page")
        if blockers and "first" not in required(item.get("next_action"), "next_action").lower():
            raise ValueError("technical blocker must be reviewed first")
        required(item.get("owner"), "owner")
        if item.get("approval_state") != "pending_owner_review" or item.get("publication_state") != "not_published":
            raise ValueError("publication needs a separate approved route and receipt")
        if item.get("demand_state") == "unknown":
            if item.get("search_metrics") is not None:
                raise ValueError("unknown demand cannot carry measured metrics")
        elif item.get("demand_state") == "measured":
            measured_metrics(item.get("search_metrics"), {**value, **item}, source_map)
        else:
            raise ValueError("demand state must be unknown or measured")
    if not isinstance(value.get("limitations"), list) or not value["limitations"]:
        raise ValueError("opportunity limitations must be listed")


def main(argv):
    if not argv or argv[0] not in ("issue", "opportunity") or len(argv) != (2 if argv[0] == "issue" else 3):
        raise SystemExit("usage: validate_handoff.py issue issue.json | opportunity issue.json opportunity.json")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        if argv[0] == "issue":
            validate_issue(artifacts[0])
        else:
            validate_opportunity(*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid SEO handoff: {exc}") from exc
    print("valid SEO " + argv[0] + " contract")


if __name__ == "__main__":
    main(sys.argv[1:])
