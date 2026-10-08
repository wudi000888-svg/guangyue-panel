import { onScopeDispose } from 'vue';
import { t } from '../i18n';

export interface SiteAccessStatus {
  enabled: boolean;
  allowed: boolean;
  ip: string;
  country_code: string;
  country_name: string;
  reason: string;
  database_version: string;
}
export type AccessDenial = SiteAccessStatus & { access_denied: true; enabled: true; allowed: false; error?: string };
function isIPAddress(value: string): boolean {
  if (value.includes(':')) {
    if (!/^[\da-f:.]+$/i.test(value)) return false;
    try { new URL(`http://[${value}]/`); return true; } catch { return false; }
  }
  const octets = value.split('.');
  return octets.length === 4 && octets.every(part => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255);
}
export function isSiteAccessStatus(value: unknown): value is SiteAccessStatus {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const status = value as Record<string, unknown>;
  return typeof status.enabled === 'boolean' && typeof status.allowed === 'boolean' &&
    ['ip', 'country_code', 'country_name', 'reason', 'database_version'].every(key => typeof status[key] === 'string') &&
    (status.country_code === '' || /^[A-Z]{2}$/.test(status.country_code as string)) &&
    (status.ip === '' || isIPAddress(status.ip as string));
}
function isAccessDenial(value: unknown): value is AccessDenial {
  return isSiteAccessStatus(value) && value.enabled === true && value.allowed === false &&
    (value as Partial<AccessDenial>).access_denied === true && !!value.ip && !!value.country_code &&
    (!('error' in value) || typeof value.error === 'string');
}

export class ApiError extends Error {
  constructor(public status: number, public data: Partial<SiteAccessStatus> & { access_denied?: boolean; error?: string; warnings?: { index: number; reason: string }[] } = {}) {
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
const accessDenied = new Set<(status: AccessDenial) => void>();
export function invalidateSession(): void {
  session++;
  for (const request of active) request.abort();
  active.clear();
}
export function onSessionExpired(callback: () => void): () => void {
  expired.add(callback);
  return () => { expired.delete(callback); };
}
export function onAccessDenied(callback: (status: AccessDenial) => void): () => void {
  accessDenied.add(callback);
  return () => { accessDenied.delete(callback); };
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
    const path = url.split(/[?#]/, 1)[0];
    const localOnly = /^\/api\/(commerce(?:\/|$)|support(?:\/|$)|fleet(?:\/|$)|business-sites(?:\/|$)|updates(?:\/|$)|login$|logout$|password$|site$|access-status$|register(?:\/|$)|email(?:\/|$)|account\/email$)/.test(path || '');
    // Access decisions concern the browser's entry site, never the selected child site.
    if (path === '/api/access-status') {
      const headers = new Headers(options.headers);
      headers.delete('X-Guangyue-Site');
      options = { ...options, headers };
    }
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
      } else if (response.status === 403 && isAccessDenial(data)) {
        invalidateSession();
        accessDenied.forEach(callback => callback(data));
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
