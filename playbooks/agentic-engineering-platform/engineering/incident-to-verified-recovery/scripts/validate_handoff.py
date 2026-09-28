#!/usr/bin/env python3
"""Validate the structural incident-to-delivery handoff; source truth needs review."""

import json
import re
import sys
from pathlib import Path


def require(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def validate_incident(incident):
    if incident.get("artifact_type") != "incident-investigation/v1":
        raise ValueError("invalid investigation artifact type")
    for field in ("incident_id", "service_id", "environment", "owner_id"):
        require(incident.get(field), f"investigation.{field}")
    for field in ("window_start", "window_end"):
        require(incident.get(field), f"investigation.{field}")
    timeline = incident.get("timeline")
    if not isinstance(timeline, list) or not timeline:
        raise ValueError("investigation.timeline must contain sourced events")
    for index, event in enumerate(timeline):
        if not isinstance(event, dict):
            raise ValueError(f"timeline[{index}] must be an object")
        for field in ("event_id", "observed_at", "source_ref", "summary"):
            require(event.get(field), f"timeline[{index}].{field}")
    hypotheses = incident.get("hypotheses")
    if not isinstance(hypotheses, list):
        raise ValueError("investigation.hypotheses must be a list")
    for index, hypothesis in enumerate(hypotheses):
        if not isinstance(hypothesis, dict):
            raise ValueError(f"hypotheses[{index}] must be an object")
        require(hypothesis.get("statement"), f"hypotheses[{index}].statement")
        if hypothesis.get("confidence") not in ("low", "medium", "high"):
            raise ValueError(f"hypotheses[{index}].confidence is invalid")
        if not isinstance(hypothesis.get("evidence_refs"), list) or not hypothesis["evidence_refs"]:
            raise ValueError(f"hypotheses[{index}] needs evidence refs")
    actions = incident.get("proposed_actions")
    if not isinstance(actions, list) or not actions:
        raise ValueError("investigation.proposed_actions must contain an owned next action")
    for index, action in enumerate(actions):
        if not isinstance(action, dict):
            raise ValueError(f"proposed_actions[{index}] must be an object")
        for field in ("action_id", "owner_id", "approval_state", "next_evidence"):
            require(action.get(field), f"proposed_actions[{index}].{field}")
        if action["approval_state"] not in ("pending", "approved", "rejected"):
            raise ValueError(f"proposed_actions[{index}].approval_state is invalid")


def validate(incident, delivery):
    validate_incident(incident)
    if delivery.get("artifact_type") != "engineering-blocker-ledger/v1":
        raise ValueError("invalid delivery artifact type")
    for field in ("incident_id", "service_id", "environment", "owner_id"):
        require(delivery.get(field), f"delivery.{field}")
    for field in ("incident_id", "service_id", "environment"):
        if incident[field] != delivery[field]:
            raise ValueError(f"handoff {field} mismatch")
    actions = incident["proposed_actions"]
    for field in ("change_id", "delivery_state", "approval_state", "next_evidence"):
        require(delivery.get(field), f"delivery.{field}")
    if delivery["delivery_state"] not in ("proposed", "in_progress", "ci_passed", "deployed", "verified"):
        raise ValueError("delivery.delivery_state is invalid")
    if delivery["approval_state"] not in ("pending", "approved", "rejected"):
        raise ValueError("delivery.approval_state is invalid")
    if delivery["approval_state"] == "approved" and not any(
        action["approval_state"] == "approved" for action in actions
    ):
        raise ValueError("delivery approval has no approved investigation action")
    for field in ("issue_ref", "commit_sha", "ci_ref", "deployment_ref"):
        if delivery.get(field) is not None:
            require(delivery[field], f"delivery.{field}")
    if delivery.get("commit_sha") is not None and not re.fullmatch(r"[0-9a-fA-F]{7,40}", delivery["commit_sha"]):
        raise ValueError("delivery.commit_sha must be a Git SHA")
    refs = delivery.get("source_refs")
    if not isinstance(refs, list) or not refs or any(not isinstance(ref, str) or not ref.strip() for ref in refs):
        raise ValueError("delivery.source_refs must contain source IDs")
    if delivery["delivery_state"] in ("ci_passed", "deployed", "verified") and not delivery.get("ci_ref"):
        raise ValueError("advanced delivery state needs CI evidence")
    if delivery["delivery_state"] in ("deployed", "verified") and not delivery.get("deployment_ref"):
        raise ValueError("deployment state needs deployment evidence")


if __name__ == "__main__":
    incident_only = len(sys.argv) == 3 and sys.argv[1] == "--incident-only"
    if len(sys.argv) != 3:
        raise SystemExit("usage: validate_handoff.py [--incident-only] incident.json [delivery.json]")
    try:
        if incident_only:
            validate_incident(json.loads(Path(sys.argv[2]).read_text()))
        else:
            validate(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print("valid incident artifact" if incident_only else "valid incident-to-delivery handoff")
