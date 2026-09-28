import copy
import json
import unittest
from pathlib import Path

from validate_handoff import InvalidArtifact, validate_outcome, validate_review


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class ReceivableContractTests(unittest.TestCase):
    def test_open_and_collected_but_unsettled_are_honest(self):
        review = fixture("receivable-review.json")
        validate_review(review)
        validate_outcome(fixture("receivable-outcome.json"), review)
        validate_outcome(fixture("collected-outcome.json"), review)

    def test_wrong_balance_and_false_send_fail(self):
        with self.assertRaises(InvalidArtifact):
            validate_review(fixture("invalid-receivable-review.json"))
        review = fixture("receivable-review.json")
        review.update(proposed_action="draft_reminder", draft_message="Pay invoice in_17", delivery_state="sent")
        with self.assertRaisesRegex(InvalidArtifact, "sent reminder needs approved"):
            validate_review(review)

    def test_suppression_and_wrong_invoice_block_contact(self):
        review = fixture("receivable-review.json")
        review.update(suppression_reason="active dispute", proposed_action="draft_reminder", draft_message="Pay invoice in_17")
        with self.assertRaisesRegex(InvalidArtifact, "suppressed or disputed"):
            validate_review(review)
        outcome = fixture("collected-outcome.json")
        outcome["invoice_id"] = "in_other"
        with self.assertRaisesRegex(InvalidArtifact, "invoice_id differs"):
            validate_outcome(outcome, fixture("receivable-review.json"))

    def test_sent_reminder_needs_exact_approval_and_provider_receipt(self):
        review = fixture("receivable-review.json")
        review.update(proposed_action="draft_reminder", draft_message="Please review invoice in_17.",
                      approval_state="approved", approval_ref="owner:ar-17",
                      delivery_state="sent", delivery_action_key="ar-case-acme-inv-17:reminder-2")
        review["delivery_receipt"] = {"id": "msg_17", "invoice_id": "in_17", "customer_id": "cus_17",
                                      "action_key": "ar-case-acme-inv-17:reminder-2", "status": "sent",
                                      "observed_at": "2026-09-26T09:05:00Z", "source_ref": "message-17"}
        review["source_refs"].append({"id": "message-17", "uri": "mail:msg_17", "observed_at": "2026-09-26T09:05:00Z"})
        # A post-cutoff delivery requires a later reviewed cutoff, not a retroactive claim.
        review["cutoff_at"] = "2026-09-26T09:06:00Z"
        validate_review(review)
        review["delivery_receipt"]["invoice_id"] = "in_other"
        with self.assertRaisesRegex(InvalidArtifact, "delivery receipt invoice_id"):
            validate_review(review)

    def test_false_deposit_and_wrong_payment_receipt_fail(self):
        review = fixture("receivable-review.json")
        with self.assertRaises(InvalidArtifact):
            validate_outcome(fixture("invalid-receivable-outcome.json"), review)
        outcome = fixture("collected-outcome.json")
        outcome["provider_payment_receipt"]["invoice_id"] = "in_other"
        with self.assertRaisesRegex(InvalidArtifact, "payment receipt invoice_id"):
            validate_outcome(outcome, review)

    def test_partial_collection_keeps_remaining_due(self):
        outcome = fixture("collected-outcome.json")
        outcome["recovery_state"] = "partially_collected"
        outcome["new_collected_minor"] = 5000
        outcome["current_remaining_minor"] = 7000
        outcome["provider_payment_receipt"]["amount_minor"] = 5000
        validate_outcome(outcome, fixture("receivable-review.json"))
        outcome["current_remaining_minor"] = 0
        with self.assertRaisesRegex(InvalidArtifact, "do not reconcile"):
            validate_outcome(outcome, fixture("receivable-review.json"))

    def test_exact_payment_payout_and_bank_match_can_verify_deposit(self):
        review = fixture("receivable-review.json")
        outcome = fixture("deposited-outcome.json")
        validate_outcome(outcome, review)
        wrong = copy.deepcopy(outcome)
        wrong["bank_ledger_match"]["amount_minor"] = 12000
        with self.assertRaisesRegex(InvalidArtifact, "bank/ledger amount_minor"):
            validate_outcome(wrong, review)
        wrong = copy.deepcopy(outcome)
        wrong["payout_allocation"]["net_minor"] = 12000
        with self.assertRaisesRegex(InvalidArtifact, "payout gross, fee, net"):
            validate_outcome(wrong, review)

    def test_outcome_cannot_claim_collection_before_review_cutoff(self):
        outcome = fixture("collected-outcome.json")
        outcome["provider_payment_receipt"]["observed_at"] = "2026-09-25T11:00:00Z"
        with self.assertRaisesRegex(InvalidArtifact, "outside review"):
            validate_outcome(outcome, fixture("receivable-review.json"))


if __name__ == "__main__":
    unittest.main()
