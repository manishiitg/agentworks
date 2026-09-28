import json
import unittest
from pathlib import Path
from validate_handoff import validate

ROOT = Path(__file__).resolve().parents[1] / 'examples'


class RefundContractTests(unittest.TestCase):
    def test_reviewed_proposal_passes_without_claiming_refund(self):
        decision = json.loads((ROOT / 'refund-decision.json').read_text())
        finance = json.loads((ROOT / 'refund-reconciliation.json').read_text())
        self.assertEqual(validate(decision, finance), [])

    def test_invalid_amount_and_fake_receipt_fail(self):
        decision = json.loads((ROOT / 'invalid-refund-decision.json').read_text())
        finance = json.loads((ROOT / 'refund-reconciliation.json').read_text())
        failures = validate(decision, finance)
        self.assertTrue(any('remaining amount' in error for error in failures))
        self.assertTrue(any('provider result' in error for error in failures))

    def test_reconciled_requires_provider_and_ledger(self):
        decision = json.loads((ROOT / 'refund-decision.json').read_text())
        finance = json.loads((ROOT / 'refund-reconciliation.json').read_text())
        finance['state'] = 'reconciled'
        self.assertTrue(any('provider refund' in error for error in validate(decision, finance)))
        self.assertTrue(any('ledger evidence' in error for error in validate(decision, finance)))

    def test_reconciled_requires_exact_receipt_and_matching_ledger(self):
        decision = json.loads((ROOT / 'refund-decision.json').read_text())
        finance = json.loads((ROOT / 'refund-reconciliation.json').read_text())
        decision.update(action_status='provider_succeeded', action_key='req_88:ch_88:5000', provider_refund_id='re_2', provider_receipt={
            'status': 'succeeded', 'refund_id': 're_2', 'payment_id': 'ch_88', 'account_id': 'acct_example',
            'mode': 'test', 'amount_minor': 5000, 'currency': 'USD',
        })
        finance.update(state='reconciled', provider_refund_id='re_2', ledger_match={
            'entry_id': 'je_2', 'refund_id': 're_2', 'amount_minor': 5000, 'currency': 'USD',
            'observed_at': '2026-09-27T08:00:00Z',
        })
        self.assertEqual(validate(decision, finance), [])
        finance['ledger_match']['amount_minor'] = 10000
        self.assertTrue(any('exact ledger evidence' in error for error in validate(decision, finance)))


if __name__ == '__main__':
    unittest.main()
