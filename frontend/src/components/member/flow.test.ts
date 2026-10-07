import { describe, expect, it } from 'vitest';
import type { Offer } from '../../lib/commerce';
import type { User } from '../../types';
import { mergeHistory, offerState } from './flow';

const offer: Offer = {
  id: 'offer-a', version: 2, enabled: true, price: '1000',
  plan: {
    id: 'plan-a', version: 3, name: 'Plan A', description: '', category: '', notes: '',
    archived: false, sort: 0, quota: 1000, valid_days: 30, cycle: 'month', timezone: 'UTC',
    group_ids: [], node_ids: [], vless: true, hy2: true,
  },
};
const user: User = {
  id: 2, username: 'member', role: 'member', enabled: true, vless: true, hy2: true,
  expires: 2000, quota: 1000, upload: 0, download: 0, vless_traffic: 0, hy2_traffic: 0,
  created: 1, last_sub: 0,
  entitlement: { plan_id: 'plan-a', version: 3, name: 'Plan A', group_ids: [], node_ids: [],
    cycle: 'month', timezone: 'UTC', assigned_at: 1, revision: 'revision-1' },
};

describe('member order eligibility', () => {
  it('offers renewal only for the exact active entitlement snapshot', () => {
    expect(offerState(user, offer, 1000)).toEqual({ current: true, renew: true, permanent: false });
    const revised = { ...offer, plan: { ...offer.plan, version: 4 } };
    expect(offerState(user, revised, 1000)).toEqual({ current: false, renew: false, permanent: false });
    expect(offerState(user, { ...offer, plan: { ...offer.plan, id: 'plan-b' } }, 1000).renew).toBe(false);
  });
  it('does not label expired or absent entitlements as a renewal', () => {
    expect(offerState(user, offer, user.expires)).toEqual({ current: false, renew: false, permanent: false });
    expect(offerState(undefined, offer, 1000).current).toBe(false);
    expect(offerState({ ...user, entitlement: undefined }, offer, 1000).renew).toBe(false);
  });
  it('prevents needless orders for permanent entitlements', () => {
    expect(offerState({ ...user, expires: 0 }, offer, 1000)).toEqual({ current: true, renew: false, permanent: true });
    expect(offerState(user, { ...offer, plan: { ...offer.plan, valid_days: 0 } }, 1000))
      .toEqual({ current: true, renew: false, permanent: true });
  });
});

describe('member history across polling and pagination', () => {
  it('retains older pages while accepting updated server state and newly created records', () => {
    const loaded = [{ id: '003', state: 'pending' }, { id: '001', state: 'completed' }];
    const incoming = [{ id: '004', state: 'pending' }, { id: '003', state: 'completed' }];
    expect(mergeHistory(loaded, incoming)).toEqual([
      { id: '004', state: 'pending' }, { id: '003', state: 'completed' }, { id: '001', state: 'completed' },
    ]);
    expect(loaded[0]?.state).toBe('pending');
    expect(incoming).toHaveLength(2);
  });
  it('uses the database binary ID order independent of browser locale', () => {
    expect(mergeHistory([{ id: 'GYZ' }, { id: 'GY2' }], [{ id: 'GYa' }, { id: 'GYZ' }]).map(row => row.id))
      .toEqual(['GYa', 'GYZ', 'GY2']);
  });
});
