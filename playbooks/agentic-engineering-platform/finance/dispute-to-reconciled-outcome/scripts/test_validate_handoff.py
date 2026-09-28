import copy
import json
import unittest
from pathlib import Path

from validate_handoff import validate


EXAMPLES = Path(__file__).resolve().parents[1] / "examples"


def fixture(name):
    return json.loads((EXAMPLES / name).read_text())


class DisputeContractTests(unittest.TestCase):
    def test_pending_case_preserves_unknown_submission_and_outcome(self):
        self.assertEqual(validate(fixture("dispute-case-pending.json"), fixture("dispute-finance-pending.json")), [])

    def test_invented_submission_is_rejected(self):
        failures = validate(fixture("invalid-dispute-case.json"), fixture("dispute-finance-pending.json"))
        self.assertTrue(any("submitted case lacks approval" in error for error in failures), failures)

    def test_approved_submission_waits_for_provider_outcome(self):
        self.assertEqual(validate(fixture("dispute-case-submitted.json"), fixture("dispute-finance-awaiting.json")), [])
        case = fixture("dispute-case-submitted.json")
        case["provider_submission_receipt"]["packet_hash"] = "different-packet"
        self.assertTrue(any("exact approved packet" in error for error in validate(case, fixture("dispute-finance-awaiting.json"))))

    def test_finance_cannot_claim_win_or_reconciliation_from_open_case(self):
        failures = validate(fixture("dispute-case-pending.json"), fixture("invalid-dispute-finance-won.json"))
        self.assertTrue(any("before outcome" in error for error in failures), failures)
        self.assertTrue(any("principal and fee ledger" in error for error in failures), failures)

    def test_wrong_account_or_stale_case_revision_cannot_reach_finance(self):
        case = fixture("dispute-case-pending.json")
        finance = fixture("dispute-finance-pending.json")
        finance["provider_account_id"] = "acct-other"
        finance["provider_case_revision"] = "rev2"
        finance["provider_observed_at"] = "2026-09-25T10:00:00Z"
        failures = validate(case, finance)
        self.assertTrue(any("provider_account_id" in error for error in failures), failures)
        self.assertTrue(any("provider case revision" in error for error in failures), failures)
        self.assertTrue(any("provider observation" in error for error in failures), failures)

    def test_observed_loss_with_exact_ledger_can_reconcile(self):
        case = fixture("dispute-case-closed-lost.json")
        finance = fixture("dispute-finance-reconciled.json")
        self.assertEqual(validate(case, finance), [])
        finance = copy.deepcopy(finance)
        finance["principal_ledger_match"]["dispute_id"] = "du_other"
        self.assertTrue(any("exact principal" in error for error in validate(case, finance)))


if __name__ == "__main__":
    unittest.main()
