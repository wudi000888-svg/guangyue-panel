import { mergeCryptoWallets, type CryptoWallet } from './cryptoWallet';

/** A list is authoritative for presence. Revision comparison protects a newer local
 * mutation for an ID actually returned, but cannot retain absent/removed rows. */
export function applyWalletList(result:{items:CryptoWallet[]},current:CryptoWallet[],removed=false){
 const known=new Map(current.map(wallet=>[wallet.id,wallet]));
 const rows=[...new Map(result.items.map(wallet=>{const local=known.get(wallet.id);return [wallet.id,local&&local.revision>wallet.revision?local:wallet] as const})).values()];
 return {wallets:rows.filter(wallet=>!!wallet.removed_at===removed),loaded:true,listError:''};
}
/** One mutation moves a wallet between lists immediately; a lower-revision reply cannot resurrect it. */
export function applyWalletMutation(wallets:CryptoWallet[],deletedWallets:CryptoWallet[],incoming:CryptoWallet){
 const merged=mergeCryptoWallets([...wallets,...deletedWallets],[incoming]);
 return {wallets:merged.filter(wallet=>!wallet.removed_at),deletedWallets:merged.filter(wallet=>!!wallet.removed_at)};
}

/** Active and removed reads may straddle a concurrent transition. Resolve one
 * highest revision per ID before applying each list's authoritative presence. */
export function reconcileWalletLists(active:{items:CryptoWallet[]}|undefined,removed:{items:CryptoWallet[]}|undefined,wallets:CryptoWallet[],deletedWallets:CryptoWallet[]){
 const known=mergeCryptoWallets([],[...wallets,...deletedWallets,...(active?.items||[]),...(removed?.items||[])]);
 return {
  wallets:applyWalletList(active||{items:wallets},known).wallets,
  deletedWallets:applyWalletList(removed||{items:deletedWallets},known,true).wallets,
 };
}

export function shouldResumeWalletList(hidden: boolean, active: boolean, disposed: boolean, pending: boolean) {
  return pending && !hidden && active && !disposed;
}
