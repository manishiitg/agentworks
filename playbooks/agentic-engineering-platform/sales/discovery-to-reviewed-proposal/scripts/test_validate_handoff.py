import json
import unittest
from pathlib import Path
from validate_handoff import validate

ROOT = Path(__file__).resolve().parents[1] / 'examples'


class ProposalContractTests(unittest.TestCase):
    def setUp(self):
        self.brief = json.loads((ROOT / 'sales-call-brief.json').read_text())
        self.proposal = json.loads((ROOT / 'proposal-draft.json').read_text())

    def test_reviewable_unsent_proposal_passes(self):
        self.assertEqual(validate(self.brief, self.proposal), [])

    def test_unapproved_discovery_and_false_send_fail(self):
        bad = json.loads((ROOT / 'invalid-proposal-draft.json').read_text())
        errors = validate(self.brief, bad)
        self.assertTrue(any('approved post-call discovery' in error for error in errors))
        self.assertTrue(any('delivery state' in error for error in errors))
        self.assertTrue(any('discount' in error for error in errors))

    def test_wrong_opportunity_and_price_fail(self):
        self.proposal['opportunity_id'] = 'opp-other'
        self.proposal['line_items'][0]['line_total_minor'] = 1
        errors = validate(self.brief, self.proposal)
        self.assertTrue(any('opportunity_id' in error for error in errors))
        self.assertTrue(any('line item' in error for error in errors))

    def test_claim_and_price_versions_must_match(self):
        self.proposal['scope_claims'][0]['discovery_ref'] = 'old-note:line-4'
        self.proposal['line_items'][0]['price_source_id'] = 'old-price:seat-monthly'
        errors = validate(self.brief, self.proposal)
        self.assertTrue(any('different discovery' in error for error in errors))
        self.assertTrue(any('different price version' in error for error in errors))


if __name__ == '__main__':
    unittest.main()
