#!/usr/bin/env python3
"""Contract tests for meeting decisions, accepted owners and observed task state."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_meeting, validate_review, validate_status

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class OperationsHandoffTests(unittest.TestCase):
    def setUp(self):
        self.register = fixture("meeting-action-register.json")
        self.status = fixture("project-action-status.json")
        self.review = fixture("operations-review-brief.json")

    def test_fictional_meeting_status_and_review(self):
        validate_meeting(self.register)
        validate_status(self.register, self.status)
        validate_review(self.register, self.status, self.review)

    def test_unaccepted_owner_cannot_be_done(self):
        with self.assertRaisesRegex(ValueError, "unaccepted owner"):
            validate_status(self.register, fixture("invalid-project-action-status.json"))

    def test_wrong_project_or_revision_cannot_join(self):
        status = copy.deepcopy(self.status)
        status["project_id"] = "other-project"
        with self.assertRaisesRegex(ValueError, "project_id mismatch"):
            validate_status(self.register, status)
        status = copy.deepcopy(self.status)
        status["meeting_revision"] = "notes-rev-4"
        with self.assertRaisesRegex(ValueError, "meeting_revision mismatch"):
            validate_status(self.register, status)

    def test_accepted_owner_requires_source_and_duplicate_reuse(self):
        register = copy.deepcopy(self.register)
        register["actions"][0].pop("owner_acceptance_ref")
        with self.assertRaisesRegex(ValueError, "owner_acceptance_ref"):
            validate_meeting(register)
        status = copy.deepcopy(self.status)
        status["entries"][0]["task_id"] = "task-new"
        with self.assertRaisesRegex(ValueError, "reuse linked task"):
            validate_status(self.register, status)

    def test_completion_requires_tracker_evidence(self):
        status = copy.deepcopy(self.status)
        status["entries"][0]["state"] = "done"
        with self.assertRaisesRegex(ValueError, "completion_ref"):
            validate_status(self.register, status)

    def test_review_must_link_same_status(self):
        review = copy.deepcopy(self.review)
        review["source_status_id"] = "another-status"
        with self.assertRaisesRegex(ValueError, "different project status"):
            validate_review(self.register, self.status, review)


if __name__ == "__main__":
    unittest.main()
