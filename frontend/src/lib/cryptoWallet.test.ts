import { describe, expect, it, vi } from 'vitest';
import { canAllocate, mergeCryptoWallets, CRYPTO_BACKUP_FILE_LIMIT, MAX_WALLET_NEXT_INDEX, parseCryptoBackup, saveGeneratedWallet, validateBackupPassword, type CryptoWallet, type WalletAPI } from './cryptoWallet';
import type { HotWalletMaterial } from './walletCore';

const wallet = { id: 'wallet-test', mode: 'hot', enabled: true, backup_confirmed: false, recovery_required: false, next_index: 0 } as CryptoWallet;
const material = (): HotWalletMaterial => ({ mnemonic: 'public test words', xpub: 'xpub-test', path: "m/44'/60'/0'", first_address: '0x-test', engine_version: '4.8.4' });

describe('wallet secrets and request lifecycle', () => {
  it('sends only the explicit creation request and drops mnemonic/password references after success', async () => {
    const generated = material(), input = { name: 'test', password: 'admin-test', operation_id: 'operation-test', risk_ack: true };
    let sent: Record<string, unknown> | undefined, wire: Record<string, unknown> | undefined;
    const api = vi.fn(async (_path: string, _method: string, body: Record<string, unknown>) => { sent = body; wire = JSON.parse(JSON.stringify(body)); return wallet; });
    const result = await saveGeneratedWallet(api as WalletAPI, input, async () => generated);
    expect(result).toBe(wallet); expect(api).toHaveBeenCalledOnce();
    expect(wire?.mnemonic).toBe('public test words'); expect(wire?.operation_id).toBe('operation-test');
    expect(sent?.mnemonic).toBe(''); expect(sent?.password).toBe(''); expect(generated.mnemonic).toBe(''); expect(input.password).toBe('');
  });
  it('clears secrets after a lost response and does not automatically create a second wallet', async () => {
    const generated = material(), input = { name: 'test', password: 'admin-test', operation_id: 'operation-test', risk_ack: true };
    let retained: Record<string, unknown> | undefined;
    const api = vi.fn(async (_path: string, _method: string, body: Record<string, unknown>) => { retained = body; throw new Error('network interrupted'); });
    await expect(saveGeneratedWallet(api as WalletAPI, input, async () => generated)).rejects.toThrow('network interrupted');
    expect(api).toHaveBeenCalledOnce(); expect(retained?.mnemonic).toBe(''); expect(retained?.password).toBe(''); expect(generated.mnemonic).toBe(''); expect(input.password).toBe('');
  });
  it('does not create keys before the custody acknowledgement and clears authentication after loader failures', async () => {
    const api = vi.fn(), generate = vi.fn(async () => material());
    const input = { name: 'test', password: 'admin-test', operation_id: 'operation-test', risk_ack: false };
    await expect(saveGeneratedWallet(api as WalletAPI, input, generate)).rejects.toThrow();
    expect(generate).not.toHaveBeenCalled(); expect(api).not.toHaveBeenCalled(); expect(input.password).toBe('');
    const next = { ...input, password: 'other-admin-test', risk_ack: true };
    await expect(saveGeneratedWallet(api as WalletAPI, next, async () => { throw new Error('WASM unavailable'); })).rejects.toThrow('WASM unavailable');
    expect(next.password).toBe('');
  });
});

it('keeps address allocation blocked until hot backup and recovery checks have completed', () => {
  expect(canAllocate(wallet)).toBe(false);
  expect(canAllocate({ ...wallet, backup_confirmed: true })).toBe(true);
  expect(canAllocate({ ...wallet, mode: 'watch_only' })).toBe(true);
  expect(canAllocate({ ...wallet, mode: 'watch_only', recovery_required: true })).toBe(false);
  expect(canAllocate({ ...wallet, backup_confirmed: true, enabled: false })).toBe(false);
  expect(canAllocate({ ...wallet, backup_confirmed: true, next_index: MAX_WALLET_NEXT_INDEX })).toBe(false);
});

it('accepts only the portable encrypted backup envelope and discards extra fields', () => {
  expect(parseCryptoBackup('{"format":"guangyue-crypto-wallet-v1","data":"encrypted","mnemonic":"must-not-retain"}'))
    .toEqual({ format: 'guangyue-crypto-wallet-v1', data: 'encrypted' });
  for (const text of ['', 'null', '[]', '{"format":"other","data":"x"}', '{"format":"guangyue-crypto-wallet-v1","data":12}', 'x'.repeat(CRYPTO_BACKUP_FILE_LIMIT + 1)]) expect(() => parseCryptoBackup(text)).toThrow();
});

it('requires a distinct confirmed backup password', () => {
  expect(() => validateBackupPassword('short', 'short', 'admin')).toThrow();
  expect(() => validateBackupPassword('long-backup-password', 'different-password', 'admin')).toThrow();
  expect(() => validateBackupPassword('long-backup-password', 'long-backup-password', 'long-backup-password')).toThrow();
  expect(() => validateBackupPassword('long-backup-password', 'long-backup-password', 'admin')).not.toThrow();
});

it('accepts a large exported backup and rejects a stale wallet read after mutation', () => {
  const large = { format: 'guangyue-crypto-wallet-v1', data: 'a'.repeat(3_000_000) };
  expect(parseCryptoBackup(JSON.stringify(large))).toEqual(large);
  const changed = { ...wallet, revision: 5, next_index: 3, backup_confirmed: true };
  const created = { ...wallet, id: 'new-wallet', revision: 1 };
  const merged = mergeCryptoWallets([changed, created], [{ ...wallet, revision: 4, next_index: 2 }]);
  expect(merged).toEqual([changed, created]);
  expect(mergeCryptoWallets(merged, [{ ...changed, revision: 6, enabled: false }])[0]?.enabled).toBe(false);
});
