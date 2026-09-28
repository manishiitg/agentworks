#!/usr/bin/env python3
"""Contract tests for support case joins, evidence, delivery, and closure."""

import copy
import json
import unittest
from pathlib import Path

from validate_handoff import (
    validate_delivery, validate_escalation, validate_outcome,
    validate_reply, validate_triage,
)


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def sample(name):
    return json.loads((EXAMPLES / name).read_text())


class SupportHandoffTests(unittest.TestCase):
    def setUp(self):
        self.triage = sample("support-case-triage.json")
        self.reply = sample("support-reply-draft.json")
        self.escalation = sample("support-escalation-brief.json")
        self.delivery = sample("provider-delivery.json")
        self.outcome = sample("case-outcome.json")

    def test_fictional_contracts(self):
        validate_triage(self.triage)
        validate_reply(self.triage, self.reply)
        validate_escalation(self.triage, self.escalation)
        approved = sample("approved-support-reply.json")
        validate_delivery(self.triage, approved, self.delivery)
        validate_outcome(self.triage, approved, self.delivery, self.outcome)

    def test_wrong_case_and_unsupported_claim_fail(self):
        with self.assertRaises(ValueError):
            validate_reply(self.triage, sample("invalid-support-reply-draft.json"))
        reply = copy.deepcopy(self.reply)
        reply["claim_refs"].append("unverified-fix")
        with self.assertRaisesRegex(ValueError, "lacks source"):
            validate_reply(self.triage, reply)

    def test_new_thread_and_sent_draft_fail(self):
        reply = copy.deepcopy(self.reply)
        reply["thread_revision"] = "rev-8"
        with self.assertRaisesRegex(ValueError, "thread_revision"):
            validate_reply(self.triage, reply)
        reply = copy.deepcopy(self.reply)
        reply["delivery_state"] = "sent"
        with self.assertRaisesRegex(ValueError, "unsent"):
            validate_reply(self.triage, reply)

    def test_escalation_acceptance_requires_receipt(self):
        escalation = copy.deepcopy(self.escalation)
        escalation.pop("acceptance_ref")
        with self.assertRaisesRegex(ValueError, "acceptance_ref"):
            validate_escalation(self.triage, escalation)

    def test_delivery_requires_exact_approval_and_recipient(self):
        with self.assertRaisesRegex(ValueError, "approved"):
            validate_delivery(self.triage, self.reply, self.delivery)
        approved = sample("approved-support-reply.json")
        delivery = copy.deepcopy(self.delivery)
        delivery["recipient_id"] = "contact-other"
        with self.assertRaisesRegex(ValueError, "recipient_id"):
            validate_delivery(self.triage, approved, delivery)
        delivery = copy.deepcopy(self.delivery)
        delivery["message_fingerprint"] = "sha256:other"
        with self.assertRaisesRegex(ValueError, "fingerprint mismatch"):
            validate_delivery(self.triage, approved, delivery)
        changed = copy.deepcopy(approved)
        changed["message"] = "We fixed it."
        with self.assertRaisesRegex(ValueError, "bind exact message"):
            validate_delivery(self.triage, changed, self.delivery)

    def test_resolution_requires_observed_matching_receipt(self):
        approved = sample("approved-support-reply.json")
        outcome = copy.deepcopy(self.outcome)
        outcome["provider_message_id"] = "other-message"
        with self.assertRaisesRegex(ValueError, "reference mismatch"):
            validate_outcome(self.triage, approved, self.delivery, outcome)
        outcome = copy.deepcopy(self.outcome)
        outcome["observed_at"] = "2026-09-25T09:00:00Z"
        with self.assertRaisesRegex(ValueError, "predates"):
            validate_outcome(self.triage, approved, self.delivery, outcome)


if __name__ == "__main__":
    unittest.main()
