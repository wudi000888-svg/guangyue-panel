import { describe, expect, it } from 'vitest';
import { applyWalletList, shouldResumeWalletList } from '../lib/cryptoWalletList';
import type { CryptoWallet } from '../lib/cryptoWallet';

const wallet = (id: string, revision: number): CryptoWallet => ({
  id, name: id, mode: 'watch_only', xpub: 'xpub', path: "m/44'/60'/0'", first_address: '',
  engine: 'external-xpub', engine_version: '4.8.4', enabled: true, revision, next_index: 0,
  created: 1, backup_confirmed: true, recovery_required: false, supported_chain_ids: [56],
  funding_address: '', funding_path: '',
});

describe('wallet list lifecycle', () => {
  it('clears a stale Failed to fetch warning after a successful list response while retaining newer rows', () => {
    const current = [wallet('existing', 3)];
    const result = applyWalletList({ items: [wallet('existing', 2), wallet('new', 1)] }, current);
    expect(result.listError).toBe('');
    expect(result.loaded).toBe(true);
    expect(result.wallets.map(item => item.id)).toEqual(['existing', 'new']);
    expect(result.wallets[0]?.revision).toBe(3);
  });

  it.each([
    [true, true, false, true, false],
    [false, false, false, true, false],
    [false, true, true, true, false],
    [false, true, false, false, false],
    [false, true, false, true, true],
  ])('resumes an interrupted read only in an active, visible, mounted view', (hidden, active, disposed, pending, expected) => {
    expect(shouldResumeWalletList(hidden, active, disposed, pending)).toBe(expected);
  });
});
