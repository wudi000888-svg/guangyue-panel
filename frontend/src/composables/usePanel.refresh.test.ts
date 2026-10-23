import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createRenderer, defineComponent, nextTick } from 'vue';
import { createPinia } from 'pinia';
import { usePanel } from './usePanel';
import { invalidateSession } from '../lib/api';
import type { State } from '../types';

vi.mock('vue-router', () => ({
  useRouter: () => ({replace: vi.fn(), isReady: () => new Promise(() => {})}),
  useRoute: () => ({fullPath: '/overview'}),
}));
const renderer = createRenderer<any, any>({
  patchProp() {}, insert() {}, remove() {}, createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
  setText() {}, setElementText() {}, parentNode: () => null, nextSibling: () => null,
});
let panel: ReturnType<typeof usePanel>, app: ReturnType<typeof renderer.createApp>;
const fixture = (): State => ({
  me: {id: 1, role: 'owner', username: 'root'}, nodes: [{id: 'n1', name: 'Node', protocol: 'vless', host: '', probe_ip: '', country: '', enabled: true}],
  users: [{id: 2, username: 'alice', role: 'member', enabled: true}], ip_pool: [],
  system: {version: '0.41.2', edition: 'lite'}, runtime: {}, site: {panel_name: 'Fixture', default_locale: 'zh-CN'},
  totals: {}, history: [], audit: [], unread_messages: 0,
} as unknown as State);
const urls = (fetch: ReturnType<typeof vi.fn>) => fetch.mock.calls.map(call => String(call[0]));
beforeEach(async () => {
  vi.useFakeTimers();
  vi.stubGlobal('document', {title: '', hidden: false, documentElement: {dataset: {}}});
  vi.stubGlobal('window', {scrollTo() {}});
  app = renderer.createApp(defineComponent({setup() { panel = usePanel(); return () => null; }}));
  app.use(createPinia()); app.mount({});
});
afterEach(() => { app.unmount(); invalidateSession(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it('skips the navigation refetch while the last full refresh is fresh', async () => {
  const fetch = vi.fn(async (url: string) => url === '/api/state' ? Response.json(fixture()) : Response.json({items: []}));
  vi.stubGlobal('fetch', fetch);
  await panel.refresh(true);
  panel.go('users');
  await vi.advanceTimersByTimeAsync(0);
  expect(urls(fetch).filter(url => url === '/api/state')).toHaveLength(1);
  vi.setSystemTime(Date.now() + 16000);
  panel.go('nodes');
  await vi.advanceTimersByTimeAsync(0);
  expect(urls(fetch).filter(url => url === '/api/state')).toHaveLength(2);
});

it('loads private sources once when navigating to a stale IP page', async () => {
  const fetch = vi.fn(async (url: string) => url === '/api/state' ? Response.json(fixture()) : Response.json({items: []}));
  vi.stubGlobal('fetch', fetch);
  await panel.refresh(true);
  vi.setSystemTime(Date.now() + 16000);
  panel.go('ips');
  await vi.advanceTimersByTimeAsync(0);
  expect(urls(fetch).filter(url => url === '/api/import-sources')).toHaveLength(1);
  expect(urls(fetch).filter(url => url === '/api/state')).toHaveLength(2);
});

it('patches polled fields without invalidating list computeds', async () => {
  const fetch = vi.fn(async (url: string) => url === '/api/state' ? Response.json(fixture())
    : Response.json({me: {id: 1, role: 'owner', username: 'root'}, site: {panel_name: 'Fixture'}, runtime: {connections: 3}, unread_messages: 4, system: {version: '0.41.3'}}));
  vi.stubGlobal('fetch', fetch);
  await panel.refresh(true);
  const state = panel.state.value, users = panel.users.value, nodes = panel.filteredNodes.value;
  panel.poll.start();
  await vi.advanceTimersByTimeAsync(10000);
  panel.poll.stop();
  await nextTick();
  expect(urls(fetch)).toContain('/api/operations');
  expect(panel.state.value).toBe(state);
  expect(panel.state.value?.unread_messages).toBe(4);
  expect(panel.state.value?.system.version).toBe('0.41.3');
  expect(panel.state.value?.system.edition).toBe('lite');
  expect(panel.users.value).toBe(users);
  expect(panel.filteredNodes.value).toBe(nodes);
});

it('downloads the backup with the verified administrator password', async () => {
  const click = vi.fn();
  vi.stubGlobal('document', {title: '', hidden: false, documentElement: {dataset: {}}, createElement: () => ({click})});
  vi.stubGlobal('URL', {createObjectURL: () => 'blob:x', revokeObjectURL() {}});
  const fetch = vi.fn(async (url: string, options: RequestInit) => url === '/api/backup' ? (JSON.parse(String(options.body)).password === 'secret' ? new Response(new Blob(['tar'])) : Response.json({error: '请验证管理员当前密码'}, {status: 403})) : Response.json(fixture()));
  vi.stubGlobal('fetch', fetch);
  await panel.refresh(true);
  await panel.backup();
  expect(panel.confirmation.value?.password).toBe(true);
  await panel.confirmed('wrong');
  expect(panel.error.value).toBe('请验证管理员当前密码');
  expect(panel.confirmation.value).not.toBeNull();
  await panel.confirmed('secret');
  const call = fetch.mock.calls.find(([url, options]) => url === '/api/backup' && JSON.parse(String(options.body)).password === 'secret')!;
  expect(call[1]).toMatchObject({method: 'POST', headers: {'Content-Type': 'application/json', 'X-Requested-With': 'guangyue'}});
  expect(click).toHaveBeenCalledTimes(1);
  expect(panel.confirmation.value).toBeNull();
});
