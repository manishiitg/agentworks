#!/usr/bin/env python3
"""Validate sampled AI-answer observations and their exact Crew opportunity handoff."""

import json
import sys
from datetime import datetime
from pathlib import Path
from urllib.parse import urlparse


def require(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")
    return value


def timestamp(value, label):
    require(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def nonnegative(value, label):
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError(f"{label} must be a nonnegative integer")
    return value


def url(value, label):
    require(value, label)
    parsed = urlparse(value)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError(f"{label} must be an HTTPS URL")
    return parsed


def sources(artifact):
    observed = timestamp(artifact.get("observed_at"), "observed_at")
    entries = artifact.get("source_refs")
    if not isinstance(entries, list) or not entries:
        raise ValueError("dated source_refs are required")
    refs = {}
    for entry in entries:
        if not isinstance(entry, dict):
            raise ValueError("source_ref must be an object")
        key = require(entry.get("id"), "source id")
        require(entry.get("uri"), "source uri")
        if key in refs or timestamp(entry.get("observed_at"), "source observed_at") > observed:
            raise ValueError("source IDs must be unique and no later than the artifact")
        refs[key] = entry
    return observed, refs


def identity(snapshot, opportunity):
    for key in ("site", "question_id", "question_version", "engine", "surface", "locale"):
        if snapshot[key] != opportunity.get(key):
            raise ValueError(f"opportunity {key} differs from snapshot")


def domain_matches(candidate, approved):
    host = url(candidate, "cited URL").hostname.lower()
    return any(host == domain or host.endswith("." + domain) for domain in approved)


def validate_snapshot(snapshot):
    if not isinstance(snapshot, dict) or snapshot.get("artifact_type") != "ai-visibility-snapshot/v1":
        raise ValueError("expected ai-visibility-snapshot/v1")
    for key in ("artifact_id", "brand", "question_id", "question_version", "question", "question_source_ref", "engine", "surface", "locale", "session_mode", "limitation"):
        require(snapshot.get(key), key)
    if snapshot.get("claim_state") != "sample_observation":
        raise ValueError("a sampled answer cannot claim a stable rank or outcome")
    site_host = url(snapshot.get("site"), "site").hostname.lower()
    domains = snapshot.get("brand_domains")
    if not isinstance(domains, list) or not domains or any(not isinstance(d, str) or not d or d.lower() != d or "/" in d for d in domains) or site_host not in domains:
        raise ValueError("brand_domains must include the canonical site host")
    competitors = snapshot.get("competitors")
    if not isinstance(competitors, list) or any(not isinstance(x, str) or not x.strip() for x in competitors):
        raise ValueError("competitors must be a name list")
    observed, refs = sources(snapshot)
    if snapshot["question_source_ref"] not in refs:
        raise ValueError("question needs an approved source")
    attempts = snapshot.get("attempts")
    if not isinstance(attempts, list) or len(attempts) < 2:
        raise ValueError("at least two explicit attempts are required")
    if len({a.get("attempt_id") for a in attempts if isinstance(a, dict)}) != len(attempts):
        raise ValueError("attempt IDs must be unique")
    valid = failed = mentions = brand_citations = competitor_citations = 0
    attempt_sources = set()
    for attempt in attempts:
        if not isinstance(attempt, dict):
            raise ValueError("attempt must be an object")
        require(attempt.get("attempt_id"), "attempt_id")
        ref = require(attempt.get("source_ref"), "attempt source_ref")
        if ref not in refs:
            raise ValueError("attempt source is missing")
        if ref == snapshot["question_source_ref"] or ref in attempt_sources:
            raise ValueError("each attempt needs a distinct answer capture")
        attempt_sources.add(ref)
        if timestamp(refs[ref]["observed_at"], "attempt source observed_at") > observed:
            raise ValueError("attempt source postdates snapshot")
        if attempt.get("status") == "failed":
            failed += 1
            if any(attempt.get(key) is not None for key in ("brand_mentioned", "brand_cited_urls", "competitor_cited_urls")):
                raise ValueError("failed run cannot be counted as a negative answer")
            require(attempt.get("failure_reason"), "failure_reason")
            continue
        if attempt.get("status") != "valid" or not isinstance(attempt.get("brand_mentioned"), bool):
            raise ValueError("attempt needs valid or failed status and explicit mention state")
        brand_urls = attempt.get("brand_cited_urls")
        competitor_urls = attempt.get("competitor_cited_urls")
        if not isinstance(brand_urls, list) or not isinstance(competitor_urls, list) or any(not isinstance(u, str) for u in brand_urls + competitor_urls) or len(brand_urls) != len(set(brand_urls)) or len(competitor_urls) != len(set(competitor_urls)):
            raise ValueError("citation URLs must be distinct lists")
        if any(not domain_matches(u, domains) for u in brand_urls):
            raise ValueError("brand citation host is not approved")
        if any(domain_matches(u, domains) for u in competitor_urls):
            raise ValueError("competitor citation includes brand host")
        valid += 1
        mentions += int(attempt["brand_mentioned"])
        brand_citations += int(bool(brand_urls))
        competitor_citations += int(bool(competitor_urls))
    if valid == 0:
        raise ValueError("no valid answers were observed")
    for key, expected in (("valid_attempts", valid), ("failed_attempts", failed), ("brand_mention_count", mentions), ("brand_citation_count", brand_citations), ("competitor_citation_count", competitor_citations)):
        if nonnegative(snapshot.get(key), key) != expected:
            raise ValueError(f"{key} does not reconcile to attempts")
    return observed


def validate_opportunity(snapshot, opportunity):
    snapshot_observed = validate_snapshot(snapshot)
    if not isinstance(opportunity, dict) or opportunity.get("artifact_type") != "ai-citation-opportunity/v1":
        raise ValueError("expected ai-citation-opportunity/v1")
    identity(snapshot, opportunity)
    if opportunity.get("source_snapshot_artifact_id") != snapshot["artifact_id"]:
        raise ValueError("opportunity references another snapshot")
    for key in ("artifact_id", "finding", "recommendation", "owner", "next_check"):
        require(opportunity.get(key), key)
    observed, refs = sources(opportunity)
    if observed < snapshot_observed or not any(ref["uri"] == "artifact:" + snapshot["artifact_id"] for ref in refs.values()):
        raise ValueError("opportunity needs the exact prior snapshot as a dated source")
    if nonnegative(opportunity.get("observed_brand_citations"), "observed_brand_citations") != snapshot["brand_citation_count"] or nonnegative(opportunity.get("observed_valid_attempts"), "observed_valid_attempts") != snapshot["valid_attempts"]:
        raise ValueError("opportunity citation counts differ from snapshot")
    target = url(opportunity.get("target_page"), "target_page")
    site_host = url(snapshot["site"], "site").hostname.lower()
    if target.hostname.lower() != site_host:
        raise ValueError("target page is outside the canonical site")
    for key in ("page_observation_ref", "approved_fact_ref"):
        if require(opportunity.get(key), key) not in refs:
            raise ValueError(f"{key} needs a dated source")
    page_observation = url(refs[opportunity["page_observation_ref"]]["uri"], "page observation")
    if page_observation._replace(fragment="").geturl() != target.geturl():
        raise ValueError("page observation must show the exact target page")
    if opportunity["approved_fact_ref"] == opportunity["page_observation_ref"]:
        raise ValueError("product fact needs evidence separate from page observation")
    if opportunity.get("decision_state") != "pending_owner_review" or opportunity.get("publication_state") != "not_published":
        raise ValueError("this route yields an owner review, not an approved or published change")


def main(argv):
    if not argv or argv[0] not in ("snapshot", "opportunity") or len(argv) != (2 if argv[0] == "snapshot" else 3):
        raise SystemExit("usage: validate_handoff.py snapshot <snapshot.json> | opportunity <snapshot.json> <opportunity.json>")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        (validate_snapshot if argv[0] == "snapshot" else validate_opportunity)(*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid AI visibility handoff: {exc}") from exc
    print(f"valid AI visibility {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
