import { afterEach, expect, it, vi } from 'vitest';
import { readPreference, writePreference } from './preferences';
afterEach(() => vi.unstubAllGlobals());
it('works when browser storage is denied or full', () => {
 vi.stubGlobal('localStorage', { getItem() { throw new Error('denied'); }, setItem() { throw new Error('full'); } });
 expect(readPreference('guangyue-locale')).toBeNull();
 expect(() => writePreference('guangyue-locale', 'en')).not.toThrow();
});
