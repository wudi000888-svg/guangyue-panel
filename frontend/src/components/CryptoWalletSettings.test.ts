import { describe, expect, it } from 'vitest';
import { applyWalletList, applyWalletMutation, reconcileWalletLists, shouldResumeWalletList } from '../lib/cryptoWalletList';
import type { CryptoWallet } from '../lib/cryptoWallet';

const wallet = (id: string, revision: number): CryptoWallet => ({
  id, name: id, mode: 'watch_only', xpub: 'xpub', path: "m/44'/60'/0'", first_address: '',
  engine: 'external-xpub', engine_version: '4.8.4', enabled: true, revision, next_index: 0,
  created: 1, backup_confirmed: true, recovery_required: false, supported_chain_ids: [56],
  funding_address: '', funding_path: '', removed_at:0,
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


  it('prunes missing IDs from authoritative refreshes instead of keeping vanished wallets',()=>{
    const result=applyWalletList({items:[wallet('existing',2)]},[wallet('existing',3),wallet('removed-elsewhere',1)]);
    expect(result.wallets.map(item=>item.id)).toEqual(['existing']);expect(result.wallets[0]?.revision).toBe(3);
  });
  it('moves a removed wallet out immediately and never resurrects it from an older list response',()=>{
    const original=wallet('existing',1),removed={...original,revision:2,enabled:false,removed_at:100};
    const changed=applyWalletMutation([original],[],removed);
    expect(changed.wallets).toEqual([]);expect(changed.deletedWallets).toEqual([removed]);
    expect(applyWalletList({items:[original]},[...changed.wallets,...changed.deletedWallets]).wallets).toEqual([]);
    expect(applyWalletMutation(changed.wallets,changed.deletedWallets,original).deletedWallets).toEqual([removed]);
  });
  it('restores a removed wallet as disabled and prunes it from the deleted list',()=>{
    const removed={...wallet('existing',2),removed_at:100,enabled:false},restored={...removed,revision:3,removed_at:0};
    const changed=applyWalletMutation([], [removed],restored);
    expect(changed.wallets).toEqual([restored]);expect(changed.wallets[0]?.enabled).toBe(false);expect(changed.deletedWallets).toEqual([]);
    expect(applyWalletList({items:[removed]},changed.wallets,true).wallets).toEqual([]);
  });

  it('a delete between the active and removed reads resolves one ID into the newer removed list only',()=>{
    const active=wallet('existing',1),removed={...active,revision:2,removed_at:100,enabled:false};
    const result=reconcileWalletLists({items:[active]},{items:[removed]},[active],[]);
    expect(result.wallets).toEqual([]);expect(result.deletedWallets).toEqual([removed]);
  });
  it('a restore between list reads cannot place the same ID in both active and removed lists',()=>{
    const removed={...wallet('existing',2),removed_at:100,enabled:false},active={...removed,revision:3,removed_at:0};
    const result=reconcileWalletLists({items:[active]},{items:[removed]},[],[removed]);
    expect(result.wallets).toEqual([active]);expect(result.deletedWallets).toEqual([]);
  });
  it('one failed list read retains unrelated rows while a successful higher revision moves known IDs',()=>{
    const removed={...wallet('existing',2),removed_at:100,enabled:false},other={...wallet('other',1),removed_at:101},active={...removed,revision:3,removed_at:0};
    const result=reconcileWalletLists({items:[active]},undefined,[],[removed,other]);
    expect(result.wallets).toEqual([active]);expect(result.deletedWallets).toEqual([other]);
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
