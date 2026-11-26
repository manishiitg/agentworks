import { expect, it } from 'vitest'
import { deliveryTickLabel, deliveryTickState } from './deliveryTickState'

it('PLAT-746 never calls queued injection sent, or a durability timeout known non-delivery', () => {
  expect(deliveryTickState({ delivery_status: 'queued_for_injection', confirmation: 'fast' })).toBe('queued')
  expect(deliveryTickState({ delivery_status: 'sent_to_cli' })).toBe('fast')
  expect(deliveryTickState({ provisional: true })).toBe('sending')
  expect(deliveryTickLabel(deliveryTickState({ confirmation: 'failed' })!)).toBe('Delivery unconfirmed')
  expect(deliveryTickState(undefined)).toBeNull()
})
