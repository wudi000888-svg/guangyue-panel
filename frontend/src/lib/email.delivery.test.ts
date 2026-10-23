import { expect, it } from 'vitest';
import { hasSettlingDelivery } from './email';

it('polls only while a delivery is still pending, sending or retrying', () => {
  expect(hasSettlingDelivery([])).toBe(false);
  expect(hasSettlingDelivery([{ state: 'sent' }, { state: 'failed' }, { state: 'expired' }, { state: 'cancelled' }])).toBe(false);
  for (const state of ['pending', 'sending', 'retry']) expect(hasSettlingDelivery([{ state: 'sent' }, { state }])).toBe(true);
});
