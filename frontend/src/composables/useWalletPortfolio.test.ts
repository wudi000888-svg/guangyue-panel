import {effectScope,nextTick,ref} from 'vue';
import {expect,it,vi} from 'vitest';
import {useWalletPortfolio} from './useWalletPortfolio';
import type {CryptoChain} from '../lib/cryptoPayments';
import type {PortfolioAPI,PortfolioSnapshot} from '../lib/cryptoPortfolio';
const result=(chainID:string,filter='funded')=>({wallet_id:'wallet',chain_id:chainID,filter,items:[],next_after:null,native_symbol:chainID==='bsc'?'BNB':'ETH',tokens:[],complete:true,stale:false,funded_addresses:0,total_addresses:40} as unknown as PortfolioSnapshot);
it('reads all supported network summaries without enumerating pages and pauses when inactive or hidden',async()=>{
 let visible=false;const api=vi.fn(async(path:string)=>{const query=new URL('http://localhost'+path).searchParams;return result(query.get('chain_id')!,query.get('filter')!)}),scope=effectScope(),wallet=ref('wallet'),chain=ref('bsc'),chains=ref([{id:'bsc'},{id:'ethereum'}] as CryptoChain[]);
 const view=scope.run(()=>useWalletPortfolio(api as PortfolioAPI,wallet,chain,chains,()=>visible))!;
 await view.load();await view.refreshSummaries();expect(api).not.toHaveBeenCalled();visible=true;await view.refreshSummaries();expect(api).toHaveBeenCalledTimes(2);expect(Object.keys(view.summaries.value).sort()).toEqual(['bsc','ethereum']);expect(api.mock.calls.every(([path])=>!path.includes('after='))).toBe(true);
 visible=false;await view.load(true);await view.refreshSummaries();expect(api).toHaveBeenCalledTimes(2);scope.stop();
});
it('changing a wallet clears prior network totals rather than showing the other wallet balance',async()=>{
 const api=vi.fn(async(path:string)=>{const query=new URL('http://localhost'+path).searchParams;return result(query.get('chain_id')!,query.get('filter')!)}),scope=effectScope(),wallet=ref('wallet'),chain=ref('bsc'),chains=ref([{id:'bsc'}] as CryptoChain[]);let visible=true;
 const view=scope.run(()=>useWalletPortfolio(api as PortfolioAPI,wallet,chain,chains,()=>visible))!;await view.load();expect(view.summaries.value.bsc).toBeDefined();visible=false;wallet.value='different';await nextTick();expect(view.summaries.value).toEqual({});expect(view.snapshot.value).toBeUndefined();scope.stop();
});
