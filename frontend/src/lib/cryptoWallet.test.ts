import { describe, expect, it, vi } from 'vitest';
import { canAllocate, changeWalletRemoval, walletFormOperation, walletChains, walletSupportsChain, validateWalletChains, mergeCryptoWallets, CRYPTO_BACKUP_FILE_LIMIT, MAX_WALLET_NEXT_INDEX, parseCryptoBackup, saveGeneratedWallet, validateBackupPassword, type CryptoWallet, type WalletAPI } from './cryptoWallet';
import type { HotWalletMaterial } from './walletCore';

const wallet = { id: 'wallet-test', mode: 'hot', enabled: true, backup_confirmed: false, recovery_required: false, next_index: 0 } as CryptoWallet;
const material = (): HotWalletMaterial => ({ mnemonic: 'public test words', xpub: 'xpub-test', path: "m/44'/60'/0'", first_address: '0x-test', engine_version: '4.8.4' });

describe('wallet secrets and request lifecycle', () => {
  it('sends only the explicit creation request and drops mnemonic/password references after success', async () => {
    const generated = material(), input = { name: 'test', password: 'admin-test', operation_id: 'operation-test', risk_ack: true, supported_chain_ids: [56] };
    let sent: Record<string, unknown> | undefined, wire: Record<string, unknown> | undefined;
    const api = vi.fn(async (_path: string, _method: string, body: Record<string, unknown>) => { sent = body; wire = JSON.parse(JSON.stringify(body)); return wallet; });
    const result = await saveGeneratedWallet(api as WalletAPI, input, async () => generated);
    expect(result).toBe(wallet); expect(api).toHaveBeenCalledOnce();
    expect(wire?.supported_chain_ids).toEqual([56]); expect(wire?.mnemonic).toBe('public test words'); expect(wire?.operation_id).toBe('operation-test');
    expect(sent?.mnemonic).toBe(''); expect(sent?.password).toBe(''); expect(generated.mnemonic).toBe(''); expect(input.password).toBe('');
  });
  it('clears secrets after a lost response and does not automatically create a second wallet', async () => {
    const generated = material(), input = { name: 'test', password: 'admin-test', operation_id: 'operation-test', risk_ack: true, supported_chain_ids: [56] };
    let retained: Record<string, unknown> | undefined;
    const api = vi.fn(async (_path: string, _method: string, body: Record<string, unknown>) => { retained = body; throw new Error('network interrupted'); });
    await expect(saveGeneratedWallet(api as WalletAPI, input, async () => generated)).rejects.toThrow('network interrupted');
    expect(api).toHaveBeenCalledOnce(); expect(retained?.mnemonic).toBe(''); expect(retained?.password).toBe(''); expect(generated.mnemonic).toBe(''); expect(input.password).toBe('');
  });
  it('does not create keys before the custody acknowledgement and clears authentication after loader failures', async () => {
    const api = vi.fn(), generate = vi.fn(async () => material());
    const input = { name: 'test', password: 'admin-test', operation_id: 'operation-test', risk_ack: false, supported_chain_ids: [56] };
    await expect(saveGeneratedWallet(api as WalletAPI, input, generate)).rejects.toThrow();
    expect(generate).not.toHaveBeenCalled(); expect(api).not.toHaveBeenCalled(); expect(input.password).toBe('');
    const next = { ...input, password: 'other-admin-test', risk_ack: true, supported_chain_ids: [56] };
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


it('shows only the networks selected for this wallet and never falls back to all chains', () => {
  const bnb = {...wallet, supported_chain_ids:[56]};
  expect(walletChains(bnb).map(chain=>chain.native_symbol)).toEqual(['BNB']);
  expect(walletSupportsChain(bnb,1)).toBe(false);
  expect(walletChains({...wallet, supported_chain_ids:[]})).toEqual([]);
  expect(validateWalletChains([56,56,1])).toEqual([56,1]);
  expect(()=>validateWalletChains([])).toThrow();
  expect(()=>validateWalletChains([56,8453])).toThrow();
});

it('rejects an empty or unsupported network selection before creating keys', async () => {
  const api=vi.fn(),generate=vi.fn(async()=>material());
  for(const ids of [[],[56,8453]]){
    const input={name:'test',password:'admin-test',operation_id:'operation-test',risk_ack:true,supported_chain_ids:ids};
    await expect(saveGeneratedWallet(api as WalletAPI,input,generate)).rejects.toThrow();
    expect(input.password).toBe('');
  }
  expect(generate).not.toHaveBeenCalled();expect(api).not.toHaveBeenCalled();
});


describe('wallet removal reauthentication',()=>{
 it('sends exactly one acknowledged, revision-scoped removal and drops the password reference',async()=>{
  const input={kind:'delete' as const,wallet:{id:'wallet/id',revision:8,removed_at:0},password:'admin-test',operation_id:'same-operation',delete_ack:true};let body:Record<string,unknown>|undefined,wire:unknown;
  const api=vi.fn(async(_route:string,_method:string,payload:Record<string,unknown>)=>{body=payload;wire=JSON.parse(JSON.stringify(payload));return {...wallet,revision:9,removed_at:100,enabled:false}});
  const result=await changeWalletRemoval(api as WalletAPI,input);expect(api.mock.calls[0]?.[0]).toBe('/wallets/wallet%2Fid/delete');expect(wire).toEqual({password:'admin-test',revision:8,operation_id:'same-operation',delete_ack:true});expect(result.removed_at).toBe(100);expect(input.password).toBe('');expect(body?.password).toBe('');
 });
 it('keeps the same public operation key after a lost response without automatically replaying deletion',async()=>{
  const input={kind:'delete' as const,wallet:{id:'wallet',revision:8,removed_at:0},password:'admin-test',operation_id:'same-operation',delete_ack:true};let retained:Record<string,unknown>|undefined;
  const api=vi.fn(async(_route:string,_method:string,payload:Record<string,unknown>)=>{retained=payload;throw new Error('response lost')});await expect(changeWalletRemoval(api as WalletAPI,input)).rejects.toThrow('response lost');expect(api).toHaveBeenCalledTimes(1);expect(input.operation_id).toBe('same-operation');expect(input.password).toBe('');expect(retained?.password).toBe('');
 });
 it('requires removal acknowledgement and keeps recovered wallets unable to allocate until explicitly enabled',async()=>{
  const api=vi.fn(async(_route:string,_method:string,_payload:Record<string,unknown>)=>({...wallet,revision:9,removed_at:0,enabled:false}));const missing={kind:'delete' as const,wallet:{id:'wallet',revision:8,removed_at:0},password:'admin-test',operation_id:'operation',delete_ack:false};await expect(changeWalletRemoval(api as WalletAPI,missing)).rejects.toThrow();expect(api).not.toHaveBeenCalled();expect(missing.password).toBe('');
  const recovered=await changeWalletRemoval(api as WalletAPI,{kind:'undelete',wallet:{id:'wallet',revision:8,removed_at:100},password:'admin-test',operation_id:'restore-operation'});expect(api.mock.calls[0]?.[0]).toBe('/wallets/wallet/undelete');expect(canAllocate(recovered)).toBe(false);expect(canAllocate({...wallet,backup_confirmed:true,enabled:true,removed_at:100})).toBe(false);
 });
});


describe('wallet dialog intent and CAS',()=>{
 it('a pause dialog does not silently become enable after another admin pauses the live list row',()=>{
  const live={...wallet,revision:8,enabled:true},reviewed={...live},generate=vi.fn(()=>'operation-original');
  // A background list response changes the current row before the first submission.
  live.revision=9;live.enabled=false;
  const operation=walletFormOperation('pause-dialog',reviewed,generate);
  expect({revision:operation.revision,enabled:!operation.enabled}).toEqual({revision:8,enabled:false});
  // Revision 8 must conflict with the server's 9, rather than re-enable a now-paused wallet.
  expect(operation.revision).not.toBe(live.revision);expect(!operation.enabled).toBe(live.enabled);
 });
 it('uncertain replies preserve the original id, revision and intent until the dialog is explicitly reopened',()=>{
  const reviewed={...wallet,revision:8,enabled:true},generate=vi.fn().mockReturnValueOnce('first').mockReturnValueOnce('reconfirmed');
  const operation=walletFormOperation('pause-dialog',reviewed,generate),changed={...reviewed,revision:9,enabled:false};
  const retry=walletFormOperation('pause-dialog',changed,generate,operation);expect(retry).toBe(operation);expect(generate).toHaveBeenCalledTimes(1);expect({revision:retry.revision,enabled:!retry.enabled}).toEqual({revision:8,enabled:false});
  const reconfirmed=walletFormOperation('new-dialog',changed,generate);expect(reconfirmed.id).toBe('reconfirmed');expect(reconfirmed.revision).toBe(9);expect(reconfirmed.enabled).toBe(false);
 });
});
