#!/usr/bin/env python3
"""QA route contract tests for exact identity, required suites and publication."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate_flake, validate_gate, validate_journey, validate_publication


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def sample(name):
    return json.loads((EXAMPLES / name).read_text())


class QAHandoffTests(unittest.TestCase):
    def setUp(self):
        self.journey = sample("journey-result.json")
        self.gate = sample("release-quality-brief.json")
        self.flake = sample("flake-investigation.json")

    def test_fictional_route(self):
        validate_journey(self.journey)
        validate_gate(self.journey, self.gate)
        validate_flake(self.journey, self.flake)
        validate_gate(self.journey, sample("flake-needs-review-gate.json"), self.flake)
        validate_publication(
            self.journey, sample("approved-release-quality-brief.json"),
            sample("status-publication.json"),
        )

    def test_wrong_build_and_missing_suite_cannot_pass(self):
        with self.assertRaisesRegex(ValueError, "sha mismatch"):
            validate_gate(self.journey, sample("invalid-release-quality-brief.json"))
        gate = copy.deepcopy(self.gate)
        gate["suite_results"].pop()
        with self.assertRaisesRegex(ValueError, "pass needs all required"):
            validate_gate(self.journey, gate)

    def test_journey_attempt_and_status_must_match(self):
        gate = copy.deepcopy(self.gate)
        gate["suite_results"][0]["attempt_ids"] = ["other-attempt"]
        with self.assertRaisesRegex(ValueError, "exact journey attempt"):
            validate_gate(self.journey, gate)
        journey = copy.deepcopy(self.journey)
        journey["evidence_refs"] = []
        with self.assertRaisesRegex(ValueError, "evidence_refs"):
            validate_journey(journey)
        gate = copy.deepcopy(self.gate)
        gate["suite_results"][1]["build_id"] = "artifact:other-build"
        with self.assertRaisesRegex(ValueError, "suite auth build_id mismatch"):
            validate_gate(self.journey, gate)
        gate = copy.deepcopy(self.gate)
        gate["suite_results"][0]["source_refs"] = ["ci:other-attempt"]
        with self.assertRaisesRegex(ValueError, "source citation"):
            validate_gate(self.journey, gate)

    def test_unresolved_flake_blocks_pass_and_keeps_failures(self):
        with self.assertRaisesRegex(ValueError, "all flaky attempts as blocked"):
            validate_gate(self.journey, self.gate, self.flake)
        gate = sample("flake-needs-review-gate.json")
        gate["verdict"] = "pass"
        with self.assertRaisesRegex(ValueError, "pass needs all required"):
            validate_gate(self.journey, gate, self.flake)
        flake = copy.deepcopy(self.flake)
        flake["attempts"] = [flake["attempts"][1]]
        with self.assertRaisesRegex(ValueError, "at least two"):
            validate_flake(self.journey, flake)

    def test_publication_needs_approval_and_exact_status(self):
        with self.assertRaisesRegex(ValueError, "owner-approved"):
            validate_publication(self.journey, self.gate, sample("status-publication.json"))
        receipt = sample("status-publication.json")
        receipt["sha"] = "wrong-sha"
        with self.assertRaisesRegex(ValueError, "sha mismatch"):
            validate_publication(self.journey, sample("approved-release-quality-brief.json"), receipt)


if __name__ == "__main__":
    unittest.main()
