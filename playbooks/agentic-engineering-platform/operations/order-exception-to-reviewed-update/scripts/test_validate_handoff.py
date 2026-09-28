import json
import unittest
from pathlib import Path

from validate_handoff import validate_order, validate_update

EXAMPLES = Path(__file__).resolve().parents[1] / 'examples'


def fixture(name: str) -> dict:
    return json.loads((EXAMPLES / name).read_text())


class OrderUpdateContractTests(unittest.TestCase):
    def test_realistic_label_only_case_stays_open_and_unsent(self):
        order = fixture('order-exception.json')
        update = fixture('order-customer-update-review.json')
        self.assertEqual(validate_order(order), [])
        self.assertEqual(validate_update(order, update), [])

    def test_rejects_false_resolution_and_wrong_order(self):
        errors = validate_update(fixture('order-exception.json'), fixture('invalid-order-customer-update-review.json'))
        self.assertTrue(any('identity mismatch order_id' in error for error in errors))
        self.assertTrue(any('customer claim exceeds' in error for error in errors))
        self.assertTrue(any('approval or delivery' in error for error in errors))
        self.assertTrue(any('case resolution' in error for error in errors))

    def test_rejects_label_as_delivery_and_unsupported_paid_state(self):
        order = fixture('order-exception.json')
        order['exception_state'] = 'delivered'
        order['payment_state'] = 'authorized'
        errors = validate_order(order)
        self.assertTrue(any('exception state' in error for error in errors))
        self.assertTrue(any('captured or sale' in error for error in errors))

    def test_rejects_source_records_joined_to_other_order_or_shipment(self):
        order = fixture('order-exception.json')
        order['payment_record_order_id'] = 'ord-99'
        order['carrier_record_shipment_id'] = 'track-99'
        errors = validate_order(order)
        self.assertTrue(any('payment_record_order_id' in error for error in errors))
        self.assertTrue(any('carrier_record_shipment_id' in error for error in errors))

    def test_rejects_stale_case_and_unverified_message_claim(self):
        order = fixture('order-exception.json')
        update = fixture('order-customer-update-review.json')
        update['case_observed_at'] = '2026-09-26T11:00:00Z'
        update['draft_text'] = 'Your order was delivered.'
        errors = validate_update(order, update)
        self.assertTrue(any('predates' in error for error in errors))
        self.assertTrue(any('delivery without carrier' in error for error in errors))

    def test_no_message_requires_reason_and_current_case_reference(self):
        order = fixture('order-exception.json')
        update = fixture('order-customer-update-review.json')
        update.update(contact_decision='no_message', draft_text='', no_message_reason='The contact owner chose an internal investigation first.')
        self.assertEqual(validate_update(order, update), [])
        update['source_refs'].remove(update['case_source_id'])
        self.assertTrue(any('current case' in error for error in validate_update(order, update)))


if __name__ == '__main__':
    unittest.main()
