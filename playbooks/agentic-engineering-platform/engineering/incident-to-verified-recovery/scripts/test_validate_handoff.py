#!/usr/bin/env python3
"""Exercise incident identity, owner approval and delivery evidence."""

import json
import unittest
from pathlib import Path

from validate_handoff import validate, validate_incident

EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class IncidentContractTests(unittest.TestCase):
    def test_good_investigation_to_delivery(self):
        incident = fixture("incident-investigation.json")
        validate_incident(incident)
        validate(incident, fixture("engineering-blocker-ledger.json"))

    def test_rejected_delivery(self):
        with self.assertRaises(ValueError):
            validate(
                fixture("incident-investigation.json"),
                fixture("invalid-engineering-blocker-ledger.json"),
            )

    def test_wrong_service_cannot_join(self):
        delivery = fixture("engineering-blocker-ledger.json")
        delivery["service_id"] = "another-service"
        with self.assertRaisesRegex(ValueError, "service_id mismatch"):
            validate(fixture("incident-investigation.json"), delivery)


if __name__ == "__main__":
    unittest.main()
