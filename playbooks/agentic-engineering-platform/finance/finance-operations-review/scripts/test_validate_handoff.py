#!/usr/bin/env python3
"""Exercise Finance fixture joins and refund arithmetic."""

import copy
import json
import unittest
from pathlib import Path

from validate_finance_artifact import InvalidArtifact, validate_queue, validate_readout

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class FinanceContractTests(unittest.TestCase):
    def test_good_queue_and_impact_handoff(self):
        queue = fixture("billing-exception-queue.json")
        validate_queue(queue)
        validate_readout(fixture("finance-impact-readout.json"), queue)

    def test_rejected_queue_and_refund_arithmetic(self):
        with self.assertRaises(InvalidArtifact):
            validate_queue(fixture("invalid-billing-exception-queue.json"))
        queue = fixture("billing-exception-queue.json")
        refund = next(case for case in queue["cases"] if case["type"] == "refund")
        refund["refund"]["proposed_minor"] = refund["refund"]["original_minor"] + 1
        with self.assertRaisesRegex(InvalidArtifact, "refundable amount"):
            validate_queue(queue)

    def test_impact_cannot_join_another_queue(self):
        readout = copy.deepcopy(fixture("finance-impact-readout.json"))
        readout["source_queue_id"] = "other-queue"
        with self.assertRaisesRegex(InvalidArtifact, "different queue"):
            validate_readout(readout, fixture("billing-exception-queue.json"))


if __name__ == "__main__":
    unittest.main()
