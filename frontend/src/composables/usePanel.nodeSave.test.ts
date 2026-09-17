import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createRenderer, defineComponent, nextTick } from 'vue';
import { createPinia } from 'pinia';
import { usePanel } from './usePanel';
import { invalidateSession } from '../lib/api';
import type { State } from '../types';

vi.mock('vue-router', () => ({
  useRouter: () => ({replace: vi.fn(), isReady: () => new Promise(() => {})}),
  useRoute: () => ({fullPath: '/nodes'}),
}));
const renderer = createRenderer<any, any>({
  patchProp() {}, insert() {}, remove() {}, createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
  setText() {}, setElementText() {}, parentNode: () => null, nextSibling: () => null,
});
let panel: ReturnType<typeof usePanel>, app: ReturnType<typeof renderer.createApp>;
const fixture = (): State => ({
  me: {id: 1, role: 'owner'}, nodes: [], users: [], ip_pool: [], system: {version: '0.22.1', node_save_receipts: true},
  site: {panel_name: 'Fixture', default_locale: 'zh-CN'}, totals: {}, history: [], audit: [], unread_messages: 0,
} as unknown as State);
beforeEach(async () => {
  vi.useFakeTimers();
  vi.stubGlobal('document', {title: '', hidden: false, documentElement: {dataset: {}}});
  vi.stubGlobal('window', {scrollTo() {}});
  app = renderer.createApp(defineComponent({setup() { panel = usePanel(); return () => null; }}));
  app.use(createPinia()); app.mount({});
  panel.state.value = fixture();
  panel.modal.value = 'node';
  await nextTick();
});
afterEach(() => { app.unmount(); invalidateSession(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it('shows a saved node immediately despite a failed follow-up refresh, then retries automatically', async () => {
  let saved: Record<string, unknown>;
  let listReads = 0;
  const fetch = vi.fn(async (url, options) => {
    if (url === '/api/nodes') {
      saved = {...JSON.parse(options.body), id: 'vless-new', name: 'New node'};
      return Response.json(saved);
    }
    if (url === '/api/state') {
      if (++listReads === 1) throw new TypeError('Failed to fetch');
      return Response.json({...fixture(), nodes: [saved, {id: 'hy2-other', name: 'Other node'}]});
    }
    throw new Error('unexpected request '+url);
  });
  vi.stubGlobal('fetch', fetch);
  await panel.saveNode();
  expect(panel.modal.value).toBe('');
  expect(panel.error.value).toBe('');
  expect(panel.state.value?.nodes.map(node => node.id)).toEqual(['vless-new']);
  expect(panel.notice.value).toContain('节点已应用');
  panel.poll.start();
  await vi.advanceTimersByTimeAsync(10000);
  expect(panel.state.value?.nodes.map(node => node.id)).toEqual(['vless-new', 'hy2-other']);
  expect(fetch.mock.calls.filter(call => call[1].method === 'POST')).toHaveLength(1);
});

it('rejects an older in-flight list response after accepting a new node', async () => {
  let finish!: (response: Response) => void;
  const old = new Promise<Response>(resolve => { finish = resolve; });
  const fetch = vi.fn().mockReturnValueOnce(old)
    .mockResolvedValueOnce(Response.json({id: 'vless-new', name: 'New node'}))
    .mockRejectedValueOnce(new TypeError('Failed to fetch'));
  vi.stubGlobal('fetch', fetch);
  const earlier = panel.refresh(true);
  await panel.saveNode();
  finish(Response.json(fixture()));
  await earlier;
  expect(panel.state.value?.nodes.map(node => node.id)).toEqual(['vless-new']);
  expect(panel.error.value).toBe('');
});

it('does not fabricate a new node after a rejected save', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({error: 'invalid DNS'}, {status: 400})));
  await panel.saveNode();
  expect(panel.state.value?.nodes).toEqual([]);
  expect(panel.modal.value).toBe('node');
  expect(panel.error.value).toBe('invalid DNS');
});

it('uses a new receipt for each edit so a rollback cannot match an older successful edit', async () => {
  panel.nodeForm.id = 'vless-existing';
  const fetch = vi.fn(async (url, options) => url === '/api/nodes'
    ? Response.json({...JSON.parse(options.body), name: 'Edited node'}) : Response.json(fixture()));
  vi.stubGlobal('fetch', fetch);
  await panel.saveNode();
  panel.nodeForm.enabled = false;
  await panel.saveNode();
  const writes = fetch.mock.calls.filter(call => call[0] === '/api/nodes');
  expect(writes).toHaveLength(2);
  expect(JSON.parse(writes[0][1].body).save_request_id).not.toBe(JSON.parse(writes[1][1].body).save_request_id);
});
