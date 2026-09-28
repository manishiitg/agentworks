#!/usr/bin/env python3
"""Validate Finance Operations Review v1 artifacts and their bounded handoff.

This checks structure, references and basic refund arithmetic, not source truth.
Use as a blocking Workflow script step before routing to a consumer Crew.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from datetime import datetime
from pathlib import Path


class InvalidArtifact(ValueError):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise InvalidArtifact(message)


def object_value(value: object, label: str) -> dict:
    require(isinstance(value, dict), f"{label} must be an object")
    return value


def text(value: object, label: str) -> str:
    require(isinstance(value, str) and bool(value.strip()), f"{label} must be non-empty text")
    return value.strip()


def nonnegative(value: object, label: str) -> int:
    require(type(value) is int and value >= 0, f"{label} must be a non-negative integer")
    return value


def integer(value: object, label: str) -> int:
    require(type(value) is int, f"{label} must be an integer")
    return value


def array(value: object, label: str, *, allow_empty: bool = False) -> list:
    require(isinstance(value, list) and (allow_empty or bool(value)), f"{label} must be a {'list' if allow_empty else 'non-empty list'}")
    return value


def timestamp(value: object, label: str) -> None:
    try:
        parsed = datetime.fromisoformat(text(value, label).replace("Z", "+00:00"))
    except ValueError as exc:
        raise InvalidArtifact(f"{label} must be an ISO timestamp") from exc
    require(parsed.tzinfo is not None, f"{label} needs a timezone")


def base(raw: object, kind: str) -> dict:
    doc = object_value(raw, kind)
    require(doc.get("schema_version") == 1, f"{kind}.schema_version must be 1")
    require(doc.get("artifact_type") == f"{kind}/v1", f"{kind}.artifact_type is wrong")
    for field in ("entity", "period", "currency"):
        text(doc.get(field), f"{kind}.{field}")
    require(bool(re.fullmatch(r"[A-Z]{3}", doc["currency"])), f"{kind}.currency must be an ISO 4217 code")
    timestamp(doc.get("created_at"), f"{kind}.created_at")
    return doc


def references(raw: object) -> set[str]:
    refs: set[str] = set()
    for index, value in enumerate(array(raw, "source_refs")):
        ref = object_value(value, f"source_refs[{index}]")
        ref_id = text(ref.get("id"), f"source_refs[{index}].id")
        require(ref_id not in refs, f"duplicate source reference {ref_id}")
        refs.add(ref_id)
        text(ref.get("uri"), f"source_refs[{index}].uri")
        timestamp(ref.get("observed_at"), f"source_refs[{index}].observed_at")
    return refs


def known_refs(raw: object, available: set[str], label: str) -> None:
    refs = array(raw, label)
    for index, value in enumerate(refs):
        require(text(value, f"{label}[{index}]") in available, f"{label} cites an unknown source")


def validate_queue(raw: object) -> set[str]:
    queue = base(raw, "billing-exception-queue")
    text(queue.get("queue_id"), "queue_id")
    available = references(queue.get("source_refs"))
    case_ids: set[str] = set()
    for index, value in enumerate(array(queue.get("cases"), "cases")):
        label = f"cases[{index}]"
        case = object_value(value, label)
        case_id = text(case.get("id"), f"{label}.id")
        require(case_id not in case_ids, f"duplicate case ID {case_id}")
        case_ids.add(case_id)
        require(case.get("type") in {"invoice", "payment", "refund", "dispute", "subscription"}, f"{label}.type is invalid")
        for field in ("source_object_id", "current_status", "proposed_action", "owner"):
            text(case.get(field), f"{label}.{field}")
        nonnegative(case.get("amount_minor"), f"{label}.amount_minor")
        require(case.get("currency") == queue["currency"], f"{label}.currency does not match queue")
        require(type(case.get("approval_required")) is bool, f"{label}.approval_required must be boolean")
        known_refs(case.get("evidence_refs"), available, f"{label}.evidence_refs")
        if case["type"] == "refund":
            refund = object_value(case.get("refund"), f"{label}.refund")
            original = nonnegative(refund.get("original_minor"), f"{label}.refund.original_minor")
            previous = nonnegative(refund.get("previously_refunded_minor"), f"{label}.refund.previously_refunded_minor")
            proposed = nonnegative(refund.get("proposed_minor"), f"{label}.refund.proposed_minor")
            remaining = nonnegative(refund.get("remaining_after_proposal_minor"), f"{label}.refund.remaining_after_proposal_minor")
            require(previous + proposed <= original, f"{label} exceeds refundable amount")
            require(remaining == original - previous - proposed, f"{label} remaining refund arithmetic is wrong")
            require(proposed == case["amount_minor"], f"{label} refund amount differs from case amount")
            require(case["approval_required"], f"{label} refund proposal requires approval")
    array(queue.get("limitations"), "limitations", allow_empty=True)
    return case_ids


def validate_readout(raw: object, queue_raw: object) -> None:
    readout = base(raw, "finance-impact-readout")
    queue = base(queue_raw, "billing-exception-queue")
    case_ids = validate_queue(queue_raw)
    require(readout.get("source_queue_id") == queue.get("queue_id"), "readout cites a different queue")
    for field in ("entity", "period", "currency"):
        require(readout[field] == queue[field], f"readout.{field} differs from queue")
    available = references(readout.get("source_refs")) | references(queue.get("source_refs"))
    metric_names: set[str] = set()
    for index, value in enumerate(array(readout.get("metrics"), "metrics")):
        label = f"metrics[{index}]"
        metric = object_value(value, label)
        name = text(metric.get("name"), f"{label}.name")
        require(name not in metric_names, f"duplicate metric {name}")
        metric_names.add(name)
        integer(metric.get("value_minor"), f"{label}.value_minor")
        text(metric.get("formula"), f"{label}.formula")
        known_refs(metric.get("source_refs"), available, f"{label}.source_refs")
    impact_ids: set[str] = set()
    for index, value in enumerate(array(readout.get("impacts"), "impacts")):
        label = f"impacts[{index}]"
        impact = object_value(value, label)
        case_id = text(impact.get("case_id"), f"{label}.case_id")
        require(case_id in case_ids and case_id not in impact_ids, f"{label} cites an unknown or duplicate case")
        impact_ids.add(case_id)
        for field in ("assessment", "next_action", "owner"):
            text(impact.get(field), f"{label}.{field}")
        known_refs(impact.get("source_refs"), available, f"{label}.source_refs")
    array(readout.get("limitations"), "limitations", allow_empty=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("queue", "readout"))
    parser.add_argument("artifact", type=Path)
    parser.add_argument("--queue", type=Path, help="required billing queue when validating a readout")
    args = parser.parse_args()
    if args.kind == "readout" and args.queue is None:
        parser.error("readout validation requires --queue")
    if args.kind == "queue" and args.queue is not None:
        parser.error("--queue is only for readout validation")
    try:
        artifact = json.loads(args.artifact.read_text())
        if args.kind == "queue":
            validate_queue(artifact)
        else:
            validate_readout(artifact, json.loads(args.queue.read_text()))
    except (OSError, json.JSONDecodeError, InvalidArtifact) as exc:
        print(f"INVALID {args.kind}: {exc}", file=sys.stderr)
        return 1
    print(f"VALID {args.kind}: {args.artifact}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
