import type {NativeFiatQuote} from './cryptoFiat';
import { errorMessage } from './api';
import {nativeDecimals,SOLANA_CHAIN_ID} from './cryptoNetworks';
import {tokenAmount} from './cryptoPayments';
export interface CryptoGasSnapshot extends NativeFiatQuote {
 wallet_id: string; chain_id: string; native_symbol: string;
 gas_price_atoms: string; reference_native_transfer_fee_atoms: string;
 gas_checked_at: number; expires_at: number; gas_error: string;
 funding_address: string; funding_balance_atoms: string; funding_checked_at: number; funding_error: string;
 block_number: number; finality: string; last_attempt_at: number;
 native_decimals?:number;fee_model?:'evm'|'solana';
}
export interface GasReadState { snapshot?: CryptoGasSnapshot; loading: boolean; error: string }
export type GasReadAPI = (path: string, method?: string, body?: unknown, options?: RequestInit) => Promise<CryptoGasSnapshot>;
export function gasSnapshotCurrent(value: CryptoGasSnapshot | undefined, now: number): boolean {
 return !!value && !value.gas_error && /^\d+$/.test(value.reference_native_transfer_fee_atoms) && BigInt(value.reference_native_transfer_fee_atoms) > 0n && ((value.fee_model==='solana'||value.chain_id==='solana')||(/^\d+$/.test(value.gas_price_atoms)&&BigInt(value.gas_price_atoms)>0n)) && value.gas_checked_at > 0 && value.gas_checked_at <= now + 5 && value.expires_at > now;
}
export function gasReferenceDisplay(gas:CryptoGasSnapshot|undefined,chain?:{chain_id?:number;native_decimals?:number;native_symbol?:string}):{amount:string;unit:string;solana:boolean}{
 const solana=gas?.fee_model==='solana'||chain?.chain_id===SOLANA_CHAIN_ID||gas?.chain_id==='solana';
 return {amount:tokenAmount(solana?gas?.reference_native_transfer_fee_atoms||'':gas?.gas_price_atoms||'',solana?nativeDecimals(chain):9),unit:solana?'SOL':'Gwei',solana};
}
/** Independent of address portfolio caching. One flight per current wallet/network, never a money endpoint. */
export function createGasReader(api: GasReadAPI, publish: (value: GasReadState) => void) {
 let walletID = '', chainID = '', generation = 0, controller: AbortController | undefined, flight: Promise<void> | undefined;
 let state: GasReadState = { loading: false, error: '' };
 const update = (changes: Partial<GasReadState>) => { state = { ...state, ...changes }; publish(state); };
 function cancel() { generation++; controller?.abort(); controller = undefined; flight = undefined; update({ loading: false }); }
 function setContext(wallet: string, chain: string) {
  if (wallet === walletID && chain === chainID) return;
  cancel(); walletID = wallet; chainID = chain; state = { loading: false, error: '' }; publish(state);
 }
 function read(): Promise<void> {
  if (!walletID || !chainID) return Promise.resolve();
  if (flight) return flight;
  const wid = walletID, cid = chainID, current = ++generation, request = new AbortController(); controller = request; update({ loading: true });
  const run = (async () => {
   try {
    const value = await api('/wallets/' + encodeURIComponent(wid) + '/gas?chain_id=' + encodeURIComponent(cid), 'GET', undefined, { signal: request.signal });
    if (current !== generation || request.signal.aborted) return;
    if (value.wallet_id !== wid || value.chain_id !== cid) throw new Error('Gas response does not match requested wallet and network');
    update({ snapshot: value, error: value.gas_error || '' });
   } catch (reason) { if (current === generation && !request.signal.aborted) update({ error: errorMessage(reason) }); }
   finally { if (current === generation) { controller = undefined; flight = undefined; update({ loading: false }); } }
  })(); flight = run; return run;
 }
 return { setContext, read, cancel, getState: () => state };
}
