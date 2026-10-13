import {mergeCryptoWallets,type CryptoWallet,type CryptoWalletDeletion} from './cryptoWallet';
/** A list is authoritative for presence. A missing wallet cannot survive a successful refresh. */
export function applyWalletList(result:{items:CryptoWallet[]},current:CryptoWallet[]){
 const known=new Map(current.map(wallet=>[wallet.id,wallet]));
 const wallets=[...new Map(result.items.map(wallet=>{const local=known.get(wallet.id);return [wallet.id,local&&local.revision>wallet.revision?local:wallet] as const})).values()];
 return {wallets,loaded:true,listError:''};
}
export function applyWalletMutation(wallets:CryptoWallet[],incoming:CryptoWallet){return mergeCryptoWallets(wallets,[incoming])}
export function applyPermanentWalletDeletion(wallets:CryptoWallet[],result:CryptoWalletDeletion){return result.deleted===true?wallets.filter(wallet=>wallet.id!==result.id):wallets}
export interface WalletListChange {items:CryptoWallet[];authoritative:boolean;deletedID?:string}
/** A committed DTO reaches the parent even if a follow-up GET fails. Mutation
 * events merge partial local knowledge; only successful lists prune absence. */
export function applyWalletListChange(current:CryptoWallet[],change:WalletListChange){
 const next=change.authoritative?applyWalletList(change,current).wallets:mergeCryptoWallets(current,change.items);
 return change.deletedID?next.filter(wallet=>wallet.id!==change.deletedID):next;
}
export function shouldResumeWalletList(hidden:boolean,active:boolean,disposed:boolean,pending:boolean){return pending&&!hidden&&active&&!disposed}
