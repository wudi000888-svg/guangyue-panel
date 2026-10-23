import { expect, it, vi } from 'vitest';
import { computed, effectScope, ref } from 'vue';
import { usePrivatePool } from './usePrivatePool';
import type { State } from '../../types';

it('splits subscribed and manual exits and filters the visible tab', async () => {
  const state = ref({ me: { role: 'owner' }, system: { edition: 'lite' }, ip_pool: [
    { id: 'a', label: 'Tokyo', enabled: true, reachable: true, exit: 'socks5', node_ids: [] },
    { id: 'b', label: 'Osaka', enabled: false, reachable: false, exit: 'http', node_ids: ['n'] },
    { id: 'c', label: 'Public', pool_group: 'public', enabled: true, reachable: true, exit: 'http' },
  ] } as unknown as State);
  const api = vi.fn(async () => ({ items: [{ source: { id: 's', name: 'Feed' }, resource_ids: ['a'] }] }));
  const router = { replace: vi.fn() };
  const pool = effectScope().run(() => usePrivatePool({ state, owner: computed(() => true), selectedSite: ref(''), api: api as never, router: router as never,
    error: ref(''), modal: ref(''), task: async fn => fn(), refresh: async () => true, go: vi.fn(), toast: vi.fn() }))!;
  expect(pool.filteredIPs.value).toEqual([]);
  await pool.loadPrivateSources();
  expect(pool.privateTabs.value.map(tab => [tab.id, 'count' in tab ? tab.count : undefined])).toEqual([['subscriptions', 1], ['manual', 1], ['sources', 1]]);
  expect(pool.filteredIPs.value.map(p => p.id)).toEqual(['a']);
  pool.selectPrivateTab('manual');
  expect(router.replace).toHaveBeenCalledWith('/ips/manual');
  expect(pool.filteredIPs.value.map(p => p.id)).toEqual(['b']);
  expect(pool.sourceLabel(state.value!.ip_pool[0]!)).toBe('Feed');
  expect(pool.poolStats.value).toEqual({ total: 2, ready: 1, used: 1, attention: 0 });
  pool.resetPrivatePool();
  expect(pool.privateSourcesReady.value).toBe(false);
});
