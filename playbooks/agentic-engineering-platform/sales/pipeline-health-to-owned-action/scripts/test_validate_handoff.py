import json
import unittest
from pathlib import Path
from validate_handoff import validate

ROOT = Path(__file__).resolve().parents[1] / 'examples'


class PipelineContractTests(unittest.TestCase):
    def setUp(self):
        self.exception = json.loads((ROOT / 'pipeline-exception-brief.json').read_text())
        self.action = json.loads((ROOT / 'deal-action-register.json').read_text())

    def test_reviewable_pending_register_passes(self):
        self.assertEqual(validate(self.exception, self.action), [])

    def test_wrong_opportunity_suppressed_contact_and_false_send_fail(self):
        invalid = json.loads((ROOT / 'invalid-deal-action-register.json').read_text())
        errors = validate(self.exception, invalid)
        self.assertTrue(any('opportunity_id' in error for error in errors))
        self.assertTrue(any('duplicate' in error for error in errors))
        self.assertTrue(any('contact review blocked' in error for error in errors))
        self.assertTrue(any('provider receipt' in error for error in errors))

    def test_wrong_stale_math_and_incomparable_snapshots_fail(self):
        self.exception['stale_age_days'] = 90
        self.exception['snapshot_comparable'] = False
        errors = validate(self.exception, self.action)
        self.assertTrue(any('stale calculation' in error for error in errors))
        self.assertTrue(any('comparability' in error for error in errors))

    def test_intervening_stage_or_activity_requires_recheck(self):
        self.action['current_stage'] = 'Proposal'
        self.action['current_last_activity_at'] = '2026-09-25T12:00:00Z'
        self.assertTrue(any('requires recheck' in error for error in validate(self.exception, self.action)))

    def test_provider_receipt_without_exact_approval_fails(self):
        self.action['resolution_state'] = 'owner_reviewed'
        self.action['owner_decided_at'] = '2026-09-26T11:00:00Z'
        self.action['external_action_state'] = 'crm_updated'
        self.action['external_action_receipt'] = {'opportunity_id': 'opp_12', 'action_key': self.action['action_key'], 'action_type': 'crm_updated', 'provider_id': 'crm-receipt-1'}
        self.assertTrue(any('provider receipt' in error for error in validate(self.exception, self.action)))

    def test_clear_contact_state_requires_source_and_malformed_refs_do_not_crash(self):
        self.action['current_contact_status'] = 'clear'
        self.exception['source_refs'] = [{'bad': 'ref'}, 'valid']
        errors = validate(self.exception, self.action)
        self.assertTrue(any('clear contact state' in error for error in errors))
        self.assertTrue(any('source references' in error for error in errors))

    def test_exactly_approved_and_receipted_contact_action_can_pass(self):
        self.action['current_contact_status'] = 'clear'
        self.action['current_contact_source_id'] = 'opp_12:email-status@rev3'
        self.action['proposed_action'] = 'contact_review'
        self.action['resolution_state'] = 'owner_reviewed'
        self.action['owner_decided_at'] = '2026-09-26T11:00:00Z'
        self.action['external_action_state'] = 'message_sent'
        self.action['external_action_approval'] = {'owner_id': 'seller_3', 'opportunity_id': 'opp_12', 'action_key': self.action['action_key'], 'action_type': 'message_sent', 'approved_at': '2026-09-26T11:00:00Z'}
        self.action['external_action_receipt'] = {'opportunity_id': 'opp_12', 'action_key': self.action['action_key'], 'action_type': 'message_sent', 'provider_id': 'mail-123'}
        self.assertEqual(validate(self.exception, self.action), [])


if __name__ == '__main__':
    unittest.main()
