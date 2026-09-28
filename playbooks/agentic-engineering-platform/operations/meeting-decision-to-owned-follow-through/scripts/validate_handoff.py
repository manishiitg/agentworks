#!/usr/bin/env python3
"""Validate meeting-to-status joins; source and provider truth still need reads."""

import json
import sys
from datetime import datetime
from pathlib import Path


IDENTITY = ("tenant_id", "project_id")
MEETING_IDENTITY = (*IDENTITY, "meeting_id", "meeting_revision")


def nonempty(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a nonempty string")


def timestamp(value, label):
    nonempty(value, label)
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError(f"{label} must be an ISO timestamp") from exc
    if parsed.tzinfo is None:
        raise ValueError(f"{label} needs a timezone")
    return parsed


def refs(value, label):
    if not isinstance(value, list) or not value:
        raise ValueError(f"{label} needs source IDs")
    for index, ref in enumerate(value):
        nonempty(ref, f"{label}[{index}]")
    if len(set(value)) != len(value):
        raise ValueError(f"{label} IDs must be unique")


def require_ref(ref, sources, label):
    nonempty(ref, label)
    if ref not in sources:
        raise ValueError(f"{label} lacks source citation")


def artifact(value, kind, fields):
    if not isinstance(value, dict) or value.get("artifact_type") != kind:
        raise ValueError(f"expected {kind}")
    for field in fields:
        nonempty(value.get(field), f"{kind}.{field}")
    refs(value.get("source_refs"), f"{kind}.source_refs")
    return timestamp(value.get("observed_at"), f"{kind}.observed_at")


def joined(first, second, keys):
    for key in keys:
        if first.get(key) != second.get(key):
            raise ValueError(f"handoff {key} mismatch")


def validate_meeting(register):
    artifact(register, "meeting-action-register/v1", (*MEETING_IDENTITY, "register_id"))
    actions = register.get("actions")
    if not isinstance(actions, list) or not actions:
        raise ValueError("meeting needs actions")
    ids = set()
    for index, action in enumerate(actions):
        if not isinstance(action, dict):
            raise ValueError("meeting action must be an object")
        for field in ("action_id", "summary", "source_ref", "owner_id", "due_at"):
            nonempty(action.get(field), f"action[{index}].{field}")
        if action["action_id"] in ids:
            raise ValueError("duplicate meeting action ID")
        ids.add(action["action_id"])
        require_ref(action["source_ref"], register["source_refs"], "action.source_ref")
        timestamp(action["due_at"], "action.due_at")
        if action.get("owner_state") not in ("accepted", "pending"):
            raise ValueError("owner_state must be accepted or pending")
        if action["owner_state"] == "accepted":
            require_ref(action.get("owner_acceptance_ref"), register["source_refs"], "action.owner_acceptance_ref")
        if action.get("duplicate_state") not in ("none", "linked_existing"):
            raise ValueError("duplicate_state is invalid")
        if action["duplicate_state"] == "linked_existing":
            nonempty(action.get("linked_task_id"), "action.linked_task_id")
            require_ref("tracker:" + action["linked_task_id"], register["source_refs"], "action.linked_task_id")
        elif action.get("linked_task_id"):
            raise ValueError("new action cannot claim existing task")


def validate_status(register, status):
    validate_meeting(register)
    observed = artifact(status, "project-action-status/v1", (*MEETING_IDENTITY, "source_register_id", "status_id", "coverage"))
    joined(register, status, MEETING_IDENTITY)
    if status["source_register_id"] != register["register_id"]:
        raise ValueError("status cites different meeting register")
    require_ref(status["source_register_id"], status["source_refs"], "status.source_register_id")
    if observed < timestamp(register["observed_at"], "register.observed_at"):
        raise ValueError("status predates meeting register")
    if status["coverage"] not in ("complete", "partial", "unavailable"):
        raise ValueError("status coverage is invalid")
    if status["coverage"] != "complete":
        nonempty(status.get("coverage_gap"), "status.coverage_gap")
    entries = status.get("entries")
    if not isinstance(entries, list):
        raise ValueError("status entries must be a list")
    actions = {action["action_id"]: action for action in register["actions"]}
    if len(entries) != len(actions):
        raise ValueError("status must account for every meeting action")
    seen = set()
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict):
            raise ValueError("status entry must be an object")
        action_id = entry.get("action_id")
        nonempty(action_id, f"entry[{index}].action_id")
        if action_id not in actions or action_id in seen:
            raise ValueError("status cites unknown or duplicate action")
        seen.add(action_id)
        action = actions[action_id]
        if entry.get("owner_id") != action["owner_id"]:
            raise ValueError("status owner mismatch")
        state = entry.get("state")
        if state not in ("pending_confirmation", "awaiting_task_write", "open", "blocked", "done"):
            raise ValueError("status state is invalid")
        nonempty(entry.get("next_action"), "entry.next_action")
        require_ref(entry.get("source_ref"), status["source_refs"], "entry.source_ref")
        if action["owner_state"] == "pending":
            if state != "pending_confirmation" or entry.get("task_id"):
                raise ValueError("unaccepted owner cannot have task or completed status")
        elif state == "pending_confirmation":
            raise ValueError("accepted owner must have an action status")
        if state == "awaiting_task_write" and entry.get("task_id"):
            raise ValueError("awaiting task write cannot claim task")
        if state in ("open", "blocked", "done"):
            nonempty(entry.get("task_id"), "entry.task_id")
        if action["duplicate_state"] == "linked_existing" and entry.get("task_id") != action["linked_task_id"]:
            raise ValueError("duplicate action must reuse linked task")
        if state == "done":
            require_ref(entry.get("completion_ref"), status["source_refs"], "entry.completion_ref")
            if status["coverage"] == "unavailable":
                raise ValueError("done needs available task source")


def validate_review(register, status, review):
    validate_status(register, status)
    observed = artifact(review, "operations-review-brief/v1", (*IDENTITY, "source_status_id", "brief_id", "owner_id"))
    joined(status, review, IDENTITY)
    if review["source_status_id"] != status["status_id"]:
        raise ValueError("review cites different project status")
    require_ref(review["source_status_id"], review["source_refs"], "review.source_status_id")
    if observed < timestamp(status["observed_at"], "status.observed_at"):
        raise ValueError("review predates status")
    requests = review.get("decision_requests")
    if not isinstance(requests, list):
        raise ValueError("review decision_requests must be a list")
    entries = {entry["action_id"]: entry for entry in status["entries"]}
    ids = set()
    for item in requests:
        if not isinstance(item, dict):
            raise ValueError("decision request must be an object")
        action_id = item.get("action_id")
        nonempty(action_id, "decision.action_id")
        if action_id not in entries or action_id in ids:
            raise ValueError("review cites unknown or duplicate action")
        ids.add(action_id)
        nonempty(item.get("question"), "decision.question")
        nonempty(item.get("owner_id"), "decision.owner_id")
        require_ref(item.get("source_ref"), review["source_refs"], "decision.source_ref")


def main(argv):
    modes = {
        "meeting": (1, validate_meeting),
        "status": (2, validate_status),
        "review": (3, validate_review),
    }
    if not argv or argv[0] not in modes or len(argv) != modes[argv[0]][0] + 1:
        raise SystemExit("usage: validate_handoff.py meeting|status|review artifact.json ...")
    try:
        artifacts = [json.loads(Path(path).read_text()) for path in argv[1:]]
        modes[argv[0]][1](*artifacts)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise SystemExit(f"invalid handoff: {exc}") from exc
    print(f"valid operations {argv[0]} contract")


if __name__ == "__main__":
    main(sys.argv[1:])
