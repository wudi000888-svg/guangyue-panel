import { afterEach, describe, expect, it, vi } from 'vitest';
import { effectScope } from 'vue';
import { api, ApiError, invalidateSession, isSiteAccessStatus, onAccessDenied, onSessionExpired, requestGeneration, setRemoteSite, useApi } from './api';
const deferred = () => { let resolve!: (value: Response) => void; const promise = new Promise<Response>(r => resolve = r); return { promise, resolve }; };
afterEach(() => { setRemoteSite(''); invalidateSession(); vi.unstubAllGlobals(); });

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

describe('entry-site account and payment requests', () => {
  it.each([
    ['/email/status', 'GET'],
    ['/email/registration', 'POST'],
    ['/email/verify', 'POST'],
    ['/email/reset/request', 'POST'],
    ['/email/reset/confirm', 'POST'],
    ['/email/settings', 'GET'],
    ['/email/settings', 'PUT'],
    ['/email/test', 'POST'],
    ['/email/deliveries?state=failed', 'GET'],
    ['/email/deliveries/retry', 'POST'],
    ['/account/email', 'GET'],
    ['/account/email', 'POST'],
    ['/account/email', 'DELETE'],
    ['/commerce/payment-methods', 'GET'],
    ['/commerce/crypto/admin/wallets', 'GET'],
    ['/commerce/crypto/admin/wallets/hot', 'POST'],
    ['/commerce/crypto/admin/wallets/xpub', 'POST'],
    ['/commerce/crypto/admin/wallets/restore', 'POST'],
    ['/commerce/crypto/admin/wallets/test/addresses', 'GET'],
    ['/commerce/crypto/admin/wallets/test/addresses', 'POST'],
    ['/commerce/crypto/admin/wallets/test/backup', 'POST'],
    ['/commerce/crypto/admin/wallets/test/reveal', 'POST'],
    ['/commerce/crypto/admin/wallets/test/recovery', 'POST'],
    ['/commerce/crypto/admin/wallets/test/status', 'POST'],
    ['/commerce/payment-methods', 'POST'],
    ['/commerce/payment-methods/action', 'POST'],
    ['/commerce/payments?state=refund_required', 'GET'],
    ['/commerce/payments/action', 'POST'],
    ['/commerce/wallet/topup', 'POST'],
  ])('keeps %s %s on the entry site while a child is selected', async (path, method) => {
    setRemoteSite('child-account-must-not-use');
    const fetch = vi.fn(async (_url: string, _options: RequestInit) => Response.json({ ok: true }));
    vi.stubGlobal('fetch', fetch);
    const body = method === 'GET' ? undefined : { operation_id: 'request-1' };

    await api(path, method, body);
    await api('/state');

    const [url, options] = fetch.mock.calls[0]!;
    expect(url).toBe('/api' + path);
    expect(options.method).toBe(method);
    expect(options.credentials).toBe('same-origin');
    const localHeaders = new Headers(options.headers);
    expect(localHeaders.has('X-Guangyue-Site')).toBe(false);
    expect(localHeaders.get('X-Requested-With')).toBe('guangyue');
    expect(options.body).toBe(body ? JSON.stringify(body) : undefined);
    // Performing local account work must not clear the user's selected site.
    expect(new Headers(fetch.mock.calls[1]![1].headers).get('X-Guangyue-Site')).toBe('child-account-must-not-use');
  });
});

const deniedStatus = {
  enabled: true, allowed: false, access_denied: true, ip: '203.0.113.24',
  country_code: 'CN', country_name: '中国', reason: '该地区暂不提供访问',
  database_version: '2026-10', error: '该地区暂不提供访问',
};

describe('site access denial', () => {
  it('notifies on a valid 403 and prevents other in-flight responses from restoring private state', async () => {
    const pending = deferred();
    const fetch = vi.fn().mockResolvedValueOnce(Response.json(deniedStatus, { status: 403 })).mockReturnValueOnce(pending.promise);
    vi.stubGlobal('fetch', fetch);
    const denied = vi.fn(), expired = vi.fn(), remove = onAccessDenied(denied), removeExpired = onSessionExpired(expired);
    try {
      const generation = requestGeneration(), first = api('/subscription'), other = api('/state');
      const rejectedOther = expect(other).rejects.toMatchObject({ name: 'AbortError' });
      await expect(first).rejects.toMatchObject({ status: 403, data: deniedStatus });
      expect(requestGeneration()).toBe(generation + 1);
      expect(denied).toHaveBeenCalledExactlyOnceWith(deniedStatus);
      expect(expired).not.toHaveBeenCalled();
      expect(fetch.mock.calls[1][1].signal.aborted).toBe(true);
      pending.resolve(Response.json({ me: { id: 1 } }));
      await rejectedOther;
    } finally { remove(); removeExpired(); }
  });

  it('keeps ordinary role-based 403 errors local to the request', async () => {
    const pending = deferred(), denied = vi.fn(), remove = onAccessDenied(denied);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(Response.json({ error: '需要管理员权限' }, { status: 403 })).mockReturnValueOnce(pending.promise));
    try {
      const generation = requestGeneration(), first = api('/settings'), other = api('/state');
      await expect(first).rejects.toMatchObject({ status: 403 });
      pending.resolve(Response.json({ me: { id: 2 } }));
      await expect(other).resolves.toEqual({ me: { id: 2 } });
      expect(requestGeneration()).toBe(generation);
      expect(denied).not.toHaveBeenCalled();
    } finally { remove(); }
  });

  it('does not accept an incomplete or malformed denial marker', async () => {
    const denied = vi.fn(), remove = onAccessDenied(denied), generation = requestGeneration();
    const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
    const invalid = [
      { error: 'blocked', access_denied: true, allowed: false },
      { ...deniedStatus, access_denied: 'true' },
      { ...deniedStatus, access_denied: false },
      { ...deniedStatus, allowed: true },
      { ...deniedStatus, enabled: false },
      { ...deniedStatus, country_code: 'China' },
      { ...deniedStatus, country_code: '' },
      { ...deniedStatus, ip: '999.0.0.1' },
      { ...deniedStatus, ip: '' },
      { ...deniedStatus, country_name: null },
      { ...deniedStatus, database_version: 202610 },
      { ...deniedStatus, reason: { html: '<p>blocked</p>' } },
      [deniedStatus],
    ];
    try {
      for (const value of invalid) {
        fetch.mockResolvedValueOnce(Response.json(value, { status: 403 }));
        await expect(api('/state')).rejects.toBeInstanceOf(ApiError);
      }
      expect(denied).not.toHaveBeenCalled();
      expect(requestGeneration()).toBe(generation);
    } finally { remove(); }
  });

  it('requires an actual HTTP 403 rather than just a JSON field', async () => {
    const denied = vi.fn(), remove = onAccessDenied(denied), generation = requestGeneration();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(Response.json(deniedStatus)).mockResolvedValueOnce(Response.json(deniedStatus, { status: 502 })));
    try {
      await expect(api('/access-status')).resolves.toEqual(deniedStatus);
      await expect(api('/state')).rejects.toMatchObject({ status: 502 });
      expect(denied).not.toHaveBeenCalled();
      expect(requestGeneration()).toBe(generation);
    } finally { remove(); }
  });

  it('ignores a blocked response from a previous session', async () => {
    const pending = deferred(), denied = vi.fn(), remove = onAccessDenied(denied);
    vi.stubGlobal('fetch', vi.fn(() => pending.promise));
    try {
      const result = api('/state'), rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
      invalidateSession();
      pending.resolve(Response.json(deniedStatus, { status: 403 }));
      await rejected;
      expect(denied).not.toHaveBeenCalled();
    } finally { remove(); }
  });

  it('delivers one denial when several requests fail together', async () => {
    const denied = vi.fn(), remove = onAccessDenied(denied);
    vi.stubGlobal('fetch', vi.fn(async () => Response.json(deniedStatus, { status: 403 })));
    try {
      const result = await Promise.allSettled([api('/state'), api('/orders')]);
      expect(result.every(value => value.status === 'rejected')).toBe(true);
      expect(denied).toHaveBeenCalledTimes(1);
    } finally { remove(); }
  });

  it('removes a listener when its owner is disposed', async () => {
    const denied = vi.fn(), remove = onAccessDenied(denied); remove();
    vi.stubGlobal('fetch', vi.fn(async () => Response.json(deniedStatus, { status: 403 })));
    await expect(api('/state')).rejects.toMatchObject({ status: 403 });
    expect(denied).not.toHaveBeenCalled();
  });

  it('always checks the entry site, even with a selected child or an explicit remote header', async () => {
    setRemoteSite('child-1');
    const fetch = vi.fn(async () => Response.json({ ok: true })); vi.stubGlobal('fetch', fetch);
    await api('/access-status?refresh=1', 'GET', undefined, { headers: { 'X-Guangyue-Site': 'child-2' } });
    await api('/state');
    const localHeaders = new Headers((fetch.mock.calls[0] as unknown as [string, RequestInit])[1].headers);
    const remoteHeaders = new Headers((fetch.mock.calls[1] as unknown as [string, RequestInit])[1].headers);
    expect(localHeaders.has('X-Guangyue-Site')).toBe(false);
    expect(localHeaders.get('X-Requested-With')).toBe('guangyue');
    expect(remoteHeaders.get('X-Guangyue-Site')).toBe('child-1');
  });

  it('accepts unknown locations without fabricating a country and validates IPv6 locations', () => {
    expect(isSiteAccessStatus({ ...deniedStatus, access_denied: undefined, enabled: false, allowed: true, ip: '', country_code: '', country_name: '', reason: '' })).toBe(true);
    expect(isSiteAccessStatus({ ...deniedStatus, ip: '2001:db8::24' })).toBe(true);
    expect(isSiteAccessStatus({ ...deniedStatus, ip: '2001:::db8' })).toBe(false);
    expect(isSiteAccessStatus({ allowed: true })).toBe(false);
  });
});
