import { expect, it, vi } from 'vitest';
import { createGasReader, gasSnapshotCurrent, type CryptoGasSnapshot } from './cryptoGas';
const value=():CryptoGasSnapshot=>({wallet_id:'w',chain_id:'bsc',native_symbol:'BNB',gas_price_atoms:'1000000001',reference_native_transfer_fee_atoms:'21000000021000',gas_checked_at:100,expires_at:110,gas_error:'',funding_address:'',funding_balance_atoms:'0',funding_checked_at:100,funding_error:'',block_number:99,finality:'finalized',last_attempt_at:100});
it('uses a lightweight market read and coalesces concurrent refresh requests',async()=>{
 let resolve!:(value:CryptoGasSnapshot)=>void;const api=vi.fn((_path:string)=>new Promise<CryptoGasSnapshot>(r=>{resolve=r}));const reader=createGasReader(api,()=>{});reader.setContext('w','bsc');const first=reader.read(),second=reader.read();
 expect(first).toBe(second);expect(api).toHaveBeenCalledTimes(1);expect(api.mock.calls[0]?.[0]).toBe('/wallets/w/gas?chain_id=bsc');resolve(value());await first;expect(reader.getState().snapshot?.gas_price_atoms).toBe('1000000001');
});
it('cancels and clears the old network instead of displaying a cross-network fee',async()=>{
 let resolve!:(value:CryptoGasSnapshot)=>void,signal:AbortSignal|undefined;const api=vi.fn((_path:string,_method?:string,_body?:unknown,options?:RequestInit)=>{signal=options?.signal as AbortSignal;return new Promise<CryptoGasSnapshot>(r=>{resolve=r})});const reader=createGasReader(api,()=>{});reader.setContext('w','bsc');const pending=reader.read();reader.setContext('w','ethereum');expect(signal?.aborted).toBe(true);resolve(value());await pending;expect(reader.getState().snapshot).toBeUndefined();
});
it('retains a successful market value on outage while freshness remains explicit',async()=>{
 let failure=false;const reader=createGasReader(async()=>{if(failure)throw Error('network down');return value()},()=>{});reader.setContext('w','bsc');await reader.read();failure=true;await reader.read();expect(reader.getState().snapshot?.gas_price_atoms).toBe('1000000001');expect(reader.getState().error).toBe('network down');
 expect(gasSnapshotCurrent(value(),100)).toBe(true);expect(gasSnapshotCurrent(value(),110)).toBe(false);expect(gasSnapshotCurrent({...value(),gas_error:'RPC unavailable'},100)).toBe(false);expect(gasSnapshotCurrent({...value(),gas_price_atoms:''},100)).toBe(false);
});
