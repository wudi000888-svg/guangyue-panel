import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearEmailProof, readEmailProof, saveEmailProof } from './email';

const key = 'guangyue-email-registration';
const email = 'member@example.com';
const token = 'registration-proof-'.padEnd(48, 'x');
const now = Date.UTC(2026, 9, 8, 12);
let values: Map<string, string>;
let storage: { getItem: ReturnType<typeof vi.fn>; setItem: ReturnType<typeof vi.fn>; removeItem: ReturnType<typeof vi.fn> };

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
  values = new Map();
  storage = {
    getItem: vi.fn((name: string) => values.get(name) ?? null),
    setItem: vi.fn((name: string, value: string) => { values.set(name, value); }),
    removeItem: vi.fn((name: string) => { values.delete(name); }),
  };
  vi.stubGlobal('sessionStorage', storage);
  clearEmailProof();
});
afterEach(() => { clearEmailProof(); vi.unstubAllGlobals(); vi.useRealTimers(); });

describe('email registration proof', () => {
  it('keeps the verified proof in this tab for ten minutes without persistent storage', () => {
    const persistentStorage = { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() };
    vi.stubGlobal('localStorage', persistentStorage);
    saveEmailProof(email, token);

    const expected = { email, token, expires: now + 600_000 };
    expect(readEmailProof()).toEqual(expected);
    expect(JSON.parse(values.get(key)!)).toEqual(expected);
    expect(persistentStorage.getItem).not.toHaveBeenCalled();
    expect(persistentStorage.setItem).not.toHaveBeenCalled();
    expect(persistentStorage.removeItem).not.toHaveBeenCalled();
  });

  it('expires at the exact deadline and removes the saved credential', () => {
    saveEmailProof(email, token);
    vi.setSystemTime(now + 599_999);
    expect(readEmailProof()?.token).toBe(token);
    vi.setSystemTime(now + 600_000);
    expect(readEmailProof()).toBeNull();
    expect(values.has(key)).toBe(false);
    vi.setSystemTime(now);
    expect(readEmailProof()).toBeNull();
  });

  it('reads a valid proof left by an earlier page load', () => {
    values.set(key, JSON.stringify({ email, token, expires: now + 120_000 }));
    expect(readEmailProof()).toEqual({ email, token, expires: now + 120_000 });
  });

  it('replaces the earlier registration identity after a new verification', () => {
    saveEmailProof(email, token);
    vi.setSystemTime(now + 60_000);
    saveEmailProof('new@example.com', 'n'.repeat(48));
    expect(readEmailProof()).toEqual({ email: 'new@example.com', token: 'n'.repeat(48), expires: now + 660_000 });
  });

  it.each([
    ['malformed JSON', '{'],
    ['null', 'null'],
    ['array', '[]'],
    ['missing fields', '{}'],
    ['empty email', JSON.stringify({ email: '', token, expires: now + 1 })],
    ['blank email', JSON.stringify({ email: '  ', token, expires: now + 1 })],
    ['non-string email', JSON.stringify({ email: 12, token, expires: now + 1 })],
    ['short token', JSON.stringify({ email, token: 'short', expires: now + 1 })],
    ['non-string token', JSON.stringify({ email, token: 123, expires: now + 1 })],
    ['expired proof', JSON.stringify({ email, token, expires: now - 1 })],
    ['deadline as text', JSON.stringify({ email, token, expires: String(now + 1) })],
    ['missing deadline', JSON.stringify({ email, token })],
    ['extended lifetime', JSON.stringify({ email, token, expires: now + 600_001 })],
  ])('rejects and clears %s instead of reusing it', (_label, raw) => {
    values.set(key, raw);
    expect(readEmailProof()).toBeNull();
    expect(values.has(key)).toBe(false);
  });

  it('clears only the registration proof after use', () => {
    values.set('unrelated-session-preference', 'keep');
    saveEmailProof(email, token);
    clearEmailProof();
    expect(readEmailProof()).toBeNull();
    expect(values.get('unrelated-session-preference')).toBe('keep');
  });

  it('continues within the page when browser storage is disabled', () => {
    const denied = () => { throw new DOMException('Storage denied', 'SecurityError'); };
    vi.stubGlobal('sessionStorage', { getItem: denied, setItem: denied, removeItem: denied });
    expect(readEmailProof()).toBeNull();
    expect(() => saveEmailProof(email, token)).not.toThrow();
    expect(readEmailProof()).toEqual({ email, token, expires: now + 600_000 });
    expect(() => clearEmailProof()).not.toThrow();
    expect(readEmailProof()).toBeNull();
  });

  it.each([false, true])('uses the new proof when storage is full (old stored proof: %s)', oldStoredProof => {
    if (oldStoredProof) values.set(key, JSON.stringify({ email: 'old@example.com', token: 'o'.repeat(48), expires: now + 60_000 }));
    storage.setItem.mockImplementation(() => { throw new DOMException('Storage full', 'QuotaExceededError'); });
    saveEmailProof(email, token);
    expect(readEmailProof()).toEqual({ email, token, expires: now + 600_000 });
    vi.setSystemTime(now + 600_000);
    expect(readEmailProof()).toBeNull();
  });
});
