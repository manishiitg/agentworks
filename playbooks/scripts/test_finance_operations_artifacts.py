#!/usr/bin/env python3
"""Contract checks for the Finance Operations Review blocking handoff."""

from __future__ import annotations

import importlib.util
import json
import unittest
from pathlib import Path


PACKAGE = Path(__file__).resolve().parents[1] / "agentic-engineering-platform/finance/finance-operations-review"
SPEC = importlib.util.spec_from_file_location("finance_validator", PACKAGE / "scripts/validate_finance_artifact.py")
assert SPEC and SPEC.loader
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


class FinanceHandoffContractTest(unittest.TestCase):
    def setUp(self) -> None:
        self.queue = json.loads((PACKAGE / "examples/billing-exception-queue.json").read_text())
        self.readout = json.loads((PACKAGE / "examples/finance-impact-readout.json").read_text())

    def test_valid_artifacts(self) -> None:
        self.assertEqual(validator.validate_queue(self.queue), {"case-refund-001"})
        validator.validate_readout(self.readout, self.queue)
        self.readout["metrics"][0]["value_minor"] = -500
        validator.validate_readout(self.readout, self.queue)

    def test_over_refund_blocks_consumer(self) -> None:
        invalid = json.loads((PACKAGE / "examples/invalid-billing-exception-queue.json").read_text())
        with self.assertRaisesRegex(validator.InvalidArtifact, "exceeds refundable amount"):
            validator.validate_queue(invalid)

    def test_readout_cannot_cite_another_case_or_period(self) -> None:
        self.readout["impacts"][0]["case_id"] = "case-not-in-queue"
        with self.assertRaisesRegex(validator.InvalidArtifact, "unknown or duplicate case"):
            validator.validate_readout(self.readout, self.queue)
        self.readout["impacts"][0]["case_id"] = "case-refund-001"
        self.readout["period"] = "2026-08"
        with self.assertRaisesRegex(validator.InvalidArtifact, "period differs"):
            validator.validate_readout(self.readout, self.queue)


if __name__ == "__main__":
    unittest.main()
