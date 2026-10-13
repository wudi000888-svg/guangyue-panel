import {describe,expect,it} from 'vitest';
import {applyWalletList,applyWalletMutation,applyWalletListChange,applyPermanentWalletDeletion,shouldResumeWalletList} from '../lib/cryptoWalletList';
import type {CryptoWallet} from '../lib/cryptoWallet';
const wallet=(id:string,revision:number):CryptoWallet=>({id,name:id,mode:'watch_only',xpub:'xpub',path:"m/44'/60'/0'",first_address:'',engine:'external-xpub',engine_version:'4.8.4',enabled:true,revision,next_index:0,created:1,backup_confirmed:true,recovery_required:false,supported_chain_ids:[56],funding_address:'',funding_path:''});
describe('permanent wallet list lifecycle',()=>{
 it('clears list transport errors and preserves the newer revision for IDs actually present',()=>{const result=applyWalletList({items:[wallet('existing',2),wallet('new',1)]},[wallet('existing',3)]);expect(result.listError).toBe('');expect(result.loaded).toBe(true);expect(result.wallets.map(value=>value.id)).toEqual(['existing','new']);expect(result.wallets[0]?.revision).toBe(3)});
 it('a permanent deletion removes the wallet immediately and successful list reads cannot retain missing IDs',()=>{const original=[wallet('delete-me',3),wallet('keep',1)];const changed=applyPermanentWalletDeletion(original,{id:'delete-me',deleted:true});expect(changed.map(value=>value.id)).toEqual(['keep']);expect(applyWalletList({items:[wallet('keep',2)]},original).wallets.map(value=>value.id)).toEqual(['keep'])});
 it('a committed creation or import reaches the Gallery without relying on a follow-up list GET',()=>{
  const existing=wallet('existing',3),created=wallet('created',1),imported={...wallet('imported',1),enabled:false,recovery_required:true};
  let parent=[existing];parent=applyWalletListChange(parent,{items:[created],authoritative:false});parent=applyWalletListChange(parent,{items:[created,imported],authoritative:false});
  // A transport failure after these commits produces no successful list event.
  expect(parent.map(value=>value.id)).toEqual(['existing','created','imported']);expect(parent.find(value=>value.id==='imported')?.recovery_required).toBe(true);
 });
 it('confirmed deletion updates the parent immediately and does not retain its stale DTO from the event',()=>{
  const deleted=wallet('delete-me',3),keep=wallet('keep',1);const result=applyWalletListChange([deleted,keep],{items:[deleted,keep],authoritative:false,deletedID:deleted.id});expect(result).toEqual([keep]);
  expect(applyWalletListChange(result,{items:[wallet('new',1)],authoritative:true}).map(value=>value.id)).toEqual(['new']);
 });
 it('never interprets an uncertain or false deletion response as deletion',()=>{const original=[wallet('keep',1)];expect(applyPermanentWalletDeletion(original,{id:'keep',deleted:false} as unknown as {id:string;deleted:true})).toBe(original)});
 it('re-import is a distinct new ID and cannot silently recreate the removed old ID',()=>{const old=wallet('old-wallet',1),newWallet=wallet('new-wallet',1);const deleted=applyPermanentWalletDeletion([old],{id:old.id,deleted:true});expect(applyWalletMutation(deleted,newWallet)).toEqual([newWallet]);expect(applyWalletList({items:[newWallet]},[old,newWallet]).wallets).toEqual([newWallet])});
 it.each([[true,true,false,true,false],[false,false,false,true,false],[false,true,true,true,false],[false,true,false,false,false],[false,true,false,true,true]])('reads resume only while active, visible and mounted',(hidden,active,disposed,pending,expected)=>{expect(shouldResumeWalletList(hidden,active,disposed,pending)).toBe(expected)});
});
