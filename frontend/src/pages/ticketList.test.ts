import { expect, it } from 'vitest';
import { ticketListHasMore, ticketListPath } from './ticketList';

it('serializes ticket filters and search into server-side list queries', () => {
  expect(ticketListPath('all', '')).toBe('/tickets');
  expect(ticketListPath('active', '  payment issue  ')).toBe('/tickets?state=active&search=payment+issue');
  expect(ticketListPath('waiting_user', 'x/y', 'GYT-2')).toBe('/tickets?state=waiting_user&search=x%2Fy&before=GYT-2');
});

it('uses explicit server pagination metadata and keeps the legacy fallback', () => {
  expect(ticketListHasMore({ has_more: true, items: [] })).toBe(true);
  expect(ticketListHasMore({ has_more: false, items: new Array(50) })).toBe(false);
  expect(ticketListHasMore({ items: new Array(50) })).toBe(true);
  expect(ticketListHasMore({ items: new Array(2) })).toBe(false);
});
