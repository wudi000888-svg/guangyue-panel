import { onScopeDispose } from 'vue';
import { t } from '../i18n';

export class ApiError extends Error {
  constructor(public status: number, public data: { error?: string; warnings?: { index: number; reason: string }[] } = {}) {
    const warnings = Array.isArray(data.warnings) ? data.warnings.filter(w => w && typeof w.reason === 'string').slice(0, 5) : [];
    super((data.error || t('请求失败')) + (warnings.length ? '：' + warnings.map(w => t('第 ') + w.index + t(' 项 ') + w.reason).join('；') : ''));
  }
}
export const isCancelled = (error: unknown): boolean => error instanceof Error && error.name === 'AbortError';
const aborted = () => new DOMException('Request cancelled', 'AbortError');
let session = 0;
let remoteSite = '';
export function requestGeneration(): number { return session; }
export function getRemoteSite(): string { return remoteSite; }
export function setRemoteSite(id: string): void {
  if (id !== remoteSite) { invalidateSession(); remoteSite = id; }
}
const active = new Set<AbortController>();
const expired = new Set<() => void>();
export function invalidateSession(): void {
  session++;
  for (const request of active) request.abort();
  active.clear();
}
export function onSessionExpired(callback: () => void): () => void {
  expired.add(callback);
  return () => { expired.delete(callback); };
}

async function request<T>(url: string, options: RequestInit, decode: (r: Response) => Promise<T>): Promise<T> {
  const generation = session, controller = new AbortController();
  const cancel = () => controller.abort();
  options.signal?.addEventListener('abort', cancel, { once: true });
  if (options.signal?.aborted) cancel();
  active.add(controller);
  const check = () => { if (controller.signal.aborted || generation !== session) throw aborted(); };
  try {
    check();
    const localOnly = /^\/api\/(fleet(?:\/|$)|business-sites(?:\/|$)|login$|logout$|password$|site$)/.test(url);
    if (remoteSite && url.startsWith('/api/') && !localOnly && !new Headers(options.headers).has('X-Guangyue-Site')) {
      options = { ...options, headers: { ...options.headers, 'X-Guangyue-Site': remoteSite } };
    }
    const response = await fetch(url, { credentials: 'same-origin', cache: 'no-store', ...options, signal: controller.signal });
    check();
    if (!response.ok) {
      let data = {};
      try { data = await response.json(); } catch { /* Nginx may return an HTML error. */ }
      check();
      if (response.status === 401) {
        invalidateSession();
        expired.forEach(callback => callback());
      }
      throw new ApiError(response.status, data && typeof data === 'object' ? data : {});
    }
    let value: T;
    try { value = await decode(response); }
    catch (error) { check(); if (isCancelled(error)) throw error; throw new Error(t('服务器响应无效，请稍后重试')); }
    check();
    return value;
  } finally {
    active.delete(controller);
    options.signal?.removeEventListener('abort', cancel);
  }
}
export function api<T = any>(path: string, method = 'GET', body?: unknown, options: RequestInit = {}): Promise<T> {
  return request('/api' + path, {
    ...options, method,
    headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'guangyue', ...options.headers },
    body: body === undefined ? undefined : JSON.stringify(body),
  }, response => response.json());
}
export function downloadBlob(url: string, options: RequestInit = {}): Promise<Blob> {
  return request(url, options, response => response.blob());
}
// Every component owns its requests. Navigating away cancels reads and prevents
// late mutation responses from changing a later screen/session.
export function useApi(prefix = '') {
  const scope = new AbortController();
  onScopeDispose(() => scope.abort());
  return <T = any>(path = '', method = 'GET', body?: unknown, options: RequestInit = {}) => api<T>(prefix + path, method, body, {
    ...options, signal: options.signal ? AbortSignal.any([scope.signal, options.signal]) : scope.signal,
  });
}
