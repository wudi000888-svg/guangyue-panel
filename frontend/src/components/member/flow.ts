import type { Offer } from '../../lib/commerce';
import type { User } from '../../types';

/** Newest-first API refreshes must update, rather than discard, loaded history. */
export function mergeHistory<T extends { id: string }>(loaded: T[], incoming: T[]): T[] {
  return [...new Map([...loaded, ...incoming].map(row => [row.id, row])).values()]
    .sort((a, b) => a.id === b.id ? 0 : a.id < b.id ? 1 : -1);
}
export function offerState(user: User | undefined, offer: Offer, now = Date.now() / 1000) {
  const current = !!user?.entitlement && user.entitlement.plan_id === offer.plan.id &&
    user.entitlement.version === offer.plan.version && (!user.expires || user.expires > now);
  return { current, renew: current && !!user?.expires && offer.plan.valid_days > 0,
    permanent: current && (!user?.expires || !offer.plan.valid_days) };
}
