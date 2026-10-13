import { afterEach, expect, it, vi } from 'vitest';
import { createSweepEstimator, sweepEstimateCurrent, WALLET_GAS_POLL_MS } from './sweepEstimates';
import { serialPoll } from './requests';
import type { SweepInput, SweepPreview } from './cryptoTreasury';
const input: SweepInput = { chain_id:'ethereum',asset_id:'usdt',destination:'0x'+'a'.repeat(40),min_atoms:'0',max_gas_atoms:'',address_ids:['address'] };
const preview = (): SweepPreview => ({wallet_id:'wallet',...input,checked_at:100,expires_at:220,can_submit:true,quote:'authenticated-quote',items:[{address_id:'address',amount_atoms:'1'}]} as SweepPreview);
const deferred=<T,>()=>{let resolve!:(value:T)=>void,reject!:(reason:Error)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no});return{promise,resolve,reject};};
afterEach(()=>vi.useRealTimers());
it('coalesces a slow estimate and never issues a transfer or funding operation',async()=>{
 const pending=deferred<SweepPreview>();const api=vi.fn((_path:string)=>pending.promise);const reader=createSweepEstimator(api,()=>{},()=>100);
 reader.setContext({walletID:'wallet',input});const first=reader.read();const second=reader.read();
 expect(api).toHaveBeenCalledTimes(1);expect(first).toBe(second);
 expect(api.mock.calls[0]?.[0]).toBe('/wallets/wallet/sweeps/preview');
 pending.resolve(preview());await first;expect(reader.freeze()?.quote).toBe('authenticated-quote');
});
it('cancels an old wallet intent and ignores even an uncooperative late response',async()=>{
 const old=deferred<SweepPreview>();let signal:AbortSignal|undefined;const api=vi.fn((_path:string,_method?:string,_body?:unknown,options?:RequestInit)=>{signal=options?.signal as AbortSignal;return old.promise});
 const reader=createSweepEstimator(api,()=>{},()=>100);reader.setContext({walletID:'wallet',input});const first=reader.read();
 reader.setContext({walletID:'other',input});expect(signal?.aborted).toBe(true);old.resolve(preview());await first;
 expect(reader.getState().preview).toBeUndefined();expect(reader.freeze()).toBeUndefined();
});
it('retains the last successful estimate on outage while disabling stale authorization',async()=>{
 let fail=false;const reader=createSweepEstimator(async()=>{if(fail)throw Error('RPC unavailable');return preview()},()=>{},()=>100);
 reader.setContext({walletID:'wallet',input});await reader.read();expect(reader.freeze()).toBeDefined();
 fail=true;await reader.read();expect(reader.getState().preview?.quote).toBe('authenticated-quote');expect(reader.getState().error).toBe('RPC unavailable');expect(reader.freeze()).toBeUndefined();
});
it('freezes an exact reviewed quote and rejects expiration or mismatched intent',async()=>{
 let now=100;const value=preview();const reader=createSweepEstimator(async()=>value,()=>{},()=>now);
 reader.setContext({walletID:'wallet',input});await reader.read();const frozen=reader.freeze()!;
 value.items[0]!.amount_atoms='2';expect(frozen.items[0]!.amount_atoms).toBe('1');now=220;expect(reader.freeze()).toBeUndefined();
 expect(sweepEstimateCurrent({...preview(),destination:'0x'+'b'.repeat(40)},'wallet',input,100)).toBe(false);
});
it('runs the next estimate after five seconds and waits for a slow query without overlap',async()=>{
 vi.useFakeTimers();let visible=true;const pending=deferred<SweepPreview>();const api=vi.fn((_path:string)=>pending.promise);const reader=createSweepEstimator(api,()=>{},()=>100);
 reader.setContext({walletID:'wallet',input});const poll=serialPoll(()=>visible?reader.read():Promise.resolve(),()=>WALLET_GAS_POLL_MS);poll.start();
 await vi.advanceTimersByTimeAsync(4999);expect(api).not.toHaveBeenCalled();
 await vi.advanceTimersByTimeAsync(1);expect(api).toHaveBeenCalledTimes(1);
 await vi.advanceTimersByTimeAsync(20000);expect(api).toHaveBeenCalledTimes(1);
 pending.resolve(preview());await Promise.resolve();await vi.advanceTimersByTimeAsync(5000);expect(api).toHaveBeenCalledTimes(2);
 visible=false;reader.cancel();expect(reader.freeze()).toBeUndefined();await vi.advanceTimersByTimeAsync(15000);expect(api).toHaveBeenCalledTimes(2);
 poll.stop();await vi.advanceTimersByTimeAsync(30000);expect(api).toHaveBeenCalledTimes(2);
});
