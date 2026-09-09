import { afterEach, describe, expect, it, vi } from 'vitest';
import { effectScope } from 'vue';
import { api, ApiError, invalidateSession, onSessionExpired, useApi } from './api';
const deferred = () => { let resolve!: (value: Response) => void; const promise = new Promise<Response>(r => resolve = r); return { promise, resolve }; };
afterEach(() => { invalidateSession(); vi.unstubAllGlobals(); });

describe('session and request isolation', () => {
  it('rejects a state response arriving after logout even if transport ignores abort', async () => {
    const pending = deferred(); vi.stubGlobal('fetch', vi.fn(() => pending.promise));
    let state = null;
    const result = api('/state').then(value => { state = value; });
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
    invalidateSession(); pending.resolve(Response.json({ me: { id: 1 } }));
    await rejected; expect(state).toBeNull();
  });
  it('ignores an old 401 after the session has changed', async () => {
    const pending = deferred(); vi.stubGlobal('fetch', vi.fn(() => pending.promise));
    const expired = vi.fn(), remove = onSessionExpired(expired);
    try {
      const result = api('/state'), rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
      invalidateSession(); pending.resolve(Response.json({ error: 'expired' }, { status: 401 }));
      await rejected; expect(expired).not.toHaveBeenCalled();
    } finally { remove(); }
  });
  it('invalidates other requests when any mounted module receives a 401', async () => {
    const pending = deferred(); const fetch = vi.fn().mockResolvedValueOnce(Response.json({ error: 'expired' }, { status: 401 })).mockReturnValueOnce(pending.promise);
    vi.stubGlobal('fetch', fetch);
    const expired = vi.fn(), remove = onSessionExpired(expired);
    try {
      const first = api('/public-pool'), other = api('/state');
      const rejected = expect(other).rejects.toMatchObject({ name: 'AbortError' });
      await expect(first).rejects.toBeInstanceOf(ApiError);
      pending.resolve(Response.json({ me: { id: 1 } }));
      await rejected; expect(expired).toHaveBeenCalledTimes(1);
    } finally { remove(); }
  });
  it('cancels requests when their view is disposed', async () => {
    const pending = deferred(); const fetch = vi.fn(() => pending.promise); vi.stubGlobal('fetch', fetch);
    const scope = effectScope(); const client = scope.run(() => useApi('/import-sources'))!;
    const result = client(), rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
    scope.stop(); expect((fetch.mock.calls[0] as unknown as [string, RequestInit])[1].signal?.aborted).toBe(true);
    pending.resolve(Response.json({ items: [] })); await rejected;
  });
  it('keeps the CSRF header and preserves structured import errors', async () => {
    const fetch = vi.fn().mockResolvedValue(Response.json({ error: 'invalid input', warnings: [{ index: 2, reason: 'invalid port' }] }, { status: 400 }));
    vi.stubGlobal('fetch', fetch);
    await expect(api('/ips/import', 'POST', { content: 'invalid' })).rejects.toMatchObject({ status: 400, data: { warnings: [{ index: 2, reason: 'invalid port' }] } });
    expect(fetch.mock.calls[0][1]).toMatchObject({ credentials: 'same-origin', headers: { 'X-Requested-With': 'guangyue' }, method: 'POST', body: '{"content":"invalid"}' });
  });
  it('reports a proxy HTML error without leaking the response body', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>internal host</html>', { status: 502 })));
    await expect(api('/state')).rejects.toMatchObject({ status: 502, message: '请求失败' });
  });
  it('reports invalid JSON on an otherwise successful response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not json')));
    await expect(api('/state')).rejects.toThrow('服务器响应无效');
  });
});
