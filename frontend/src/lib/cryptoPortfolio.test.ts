import {describe,expect,it,vi} from 'vitest';
import {addressHasAsset,chainExplorer,createPortfolioReader,gasReceiveURI,portfolioAmount,portfolioPath,portfolioStatus,type PortfolioAPI,type PortfolioBalance,type PortfolioSnapshot} from './cryptoPortfolio';
const row=(index:number,atoms='0'):PortfolioBalance=>({address_id:'address-'+index,index,address:'0x'+'a'.repeat(40),path:"m/44'/60'/0'/0/"+index,native_atoms:atoms,tokens:[{asset_id:'usdt',symbol:'USDT',decimals:6,balance_atoms:atoms}],label:'',checked_at:10,last_success_at:10,block_number:100,error:'',stale:false,funded:atoms!=='0'});
const page=(items:PortfolioBalance[]=[row(0)],patch:Partial<PortfolioSnapshot>={}):PortfolioSnapshot=>({wallet_id:'wallet',chain_id:'bsc',native_symbol:'BNB',watch_only:false,operations_supported:true,unavailable_reason:'',funding_address:'0x'+'b'.repeat(40),funding_path:"m/44'/60'/0'/1/0",funding_balance_atoms:'100',funding_checked_at:10,funding_error:'',native_balance_atoms:'9007199254740993123456789',tokens:[{asset_id:'usdt',symbol:'USDT',decimals:6,balance_atoms:'100000000'}],total_addresses:80,checked_addresses:80,funded_addresses:30,empty_addresses:50,failed_addresses:0,complete:true,stale:false,refreshing:false,checked_at:10,last_attempt_at:10,block_number:100,block_hash:'',finality:'finalized',gas_price_atoms:'1000000000',reference_native_transfer_fee_atoms:'21000000000000',gas_checked_at:10,gas_error:'',errors:[],items,next_after:null,filter:'funded',...patch});
const deferred=<T>()=>{let resolve!:(value:T)=>void,reject!:(reason:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}};

describe('exact portfolio data',()=>{
 it('keeps full-wallet counts server-owned and token/native amounts exact without assuming dollar value',async()=>{
  const api=vi.fn(async()=>page([row(0,'1'),row(1)]));const reader=createPortfolioReader(api as PortfolioAPI,()=>{});reader.reset({walletID:'wallet',chainID:'bsc',filter:'funded'});await reader.read();
  expect(reader.getState().items).toHaveLength(2);expect(reader.getState().snapshot?.total_addresses).toBe(80);expect(reader.getState().snapshot?.funded_addresses).toBe(30);
  expect(portfolioAmount(reader.getState().snapshot?.native_balance_atoms)).toBe('9007199.254740993123456789');
  expect(portfolioAmount('1',18)).toBe('0.000000000000000001');expect(portfolioAmount('0',18)).toBe('0');expect(portfolioAmount('',18)).toBe('—');expect(portfolioAmount(undefined,18)).toBe('—');
 });
 it('preserves the previous successful balances when refresh fails instead of substituting zero',async()=>{
  const api=vi.fn().mockResolvedValueOnce(page([row(0,'101')])).mockRejectedValueOnce(new Error('RPC unavailable'));const reader=createPortfolioReader(api as PortfolioAPI,()=>{});reader.reset({walletID:'wallet',chainID:'bsc',filter:'funded'});await reader.read();await reader.read();
  expect(reader.getState().items[0]?.native_atoms).toBe('101');expect(reader.getState().snapshot?.funded_addresses).toBe(30);expect(reader.getState().error).toBe('RPC unavailable');expect(reader.getState().loading).toBe(false);expect(portfolioStatus(reader.getState().snapshot)).toBe('stale');expect(reader.getState().items[0]?.stale).toBe(true);
 });
 it('distinguishes stale, partial and refreshing responses and never promises a successful balance read',()=>{
  expect(portfolioStatus()).toBe('loading');expect(portfolioStatus(undefined,'RPC down')).toBe('failed');expect(portfolioStatus(page(),'RPC down')).toBe('stale');expect(portfolioStatus(page())).toBe('ready');expect(portfolioStatus(page([],{complete:false}))).toBe('partial');expect(portfolioStatus(page([],{stale:true}))).toBe('stale');expect(portfolioStatus(page([],{stale:true,refreshing:true}))).toBe('refreshing');expect(portfolioStatus(page([],{complete:false,stale:true,checked_at:0,checked_addresses:0,funding_checked_at:0,gas_checked_at:0,native_balance_atoms:''}))).toBe('failed');expect(portfolioStatus(page([],{complete:false,stale:true,checked_addresses:20}))).toBe('partial');
 });
 it('uses positive atomic balance for current-list asset filters, including dust and unknown balances',()=>{
  expect(addressHasAsset(row(0,'1'),'usdt')).toBe(true);expect(addressHasAsset(row(0,'0'),'usdt')).toBe(false);expect(addressHasAsset(row(0,''),'native')).toBe(false);expect(addressHasAsset(row(0,'1'),'other')).toBe(false);expect(addressHasAsset(row(0),'')).toBe(true);
 });
 it('scopes Gas QR requests and explorer links to the selected network without trusting arbitrary URLs',()=>{
  const address='0x'+'a'.repeat(40);expect(gasReceiveURI(56,address)).toBe('ethereum:'+address+'@56');expect(gasReceiveURI(1,address)).toBe('ethereum:'+address+'@1');expect(gasReceiveURI(0,address)).toBe('');expect(chainExplorer(56,address)).toBe('https://bscscan.com/address/'+address);expect(chainExplorer(1,address)).toBe('https://etherscan.io/address/'+address);expect(chainExplorer(56,'https://attacker.invalid')).toBe('');expect(chainExplorer(999,address)).toBe('');expect(chainExplorer(56,'0x'+'b'.repeat(64),'tx')).toContain('/tx/');
 });
});

describe('portfolio read orchestration',()=>{
 it('deduplicates concurrent polling and prevents a cancelled network response overwriting the new network',async()=>{
  const old=deferred<PortfolioSnapshot>(),current=deferred<PortfolioSnapshot>();const api=vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);const reader=createPortfolioReader(api as PortfolioAPI,()=>{});reader.reset({walletID:'wallet',chainID:'bsc',filter:'funded'});const previous=reader.read();expect(reader.read('summary')).toBe(previous);expect(api).toHaveBeenCalledTimes(1);
  const signal=(api.mock.calls[0]?.[3] as RequestInit).signal!;reader.reset({walletID:'wallet',chainID:'ethereum',filter:'all'});expect(signal.aborted).toBe(true);const next=reader.read();current.resolve(page([row(3)],{chain_id:'ethereum',native_symbol:'ETH',filter:'all'}));await next;old.resolve(page([row(0,'999')]));await previous;
  expect(reader.getState().snapshot?.native_symbol).toBe('ETH');expect(reader.getState().items.map(item=>item.index)).toEqual([3]);
 });
 it('refreshes all loaded pages atomically, with a failed later page retaining the prior list',async()=>{
  const api=vi.fn().mockResolvedValueOnce(page([row(0)],{next_after:0})).mockResolvedValueOnce(page([row(1)],{next_after:1})).mockResolvedValueOnce(page([row(0,'10')],{next_after:0})).mockRejectedValueOnce(new Error('second page timeout'));
  const reader=createPortfolioReader(api as PortfolioAPI,()=>{});reader.reset({walletID:'wallet',chainID:'bsc',filter:'funded'});await reader.read();await reader.read('append');expect(reader.getState().items).toHaveLength(2);await reader.read('detail');expect(reader.getState().items.map(item=>item.native_atoms)).toEqual(['0','0']);expect(reader.getState().error).toBe('second page timeout');expect(api.mock.calls[3]?.[0]).toContain('after=0');
 });
 it('summary polls update aggregates without throwing away loaded pages or changing the detail cursor',async()=>{
  const api=vi.fn().mockResolvedValueOnce(page([row(0)],{next_after:0})).mockResolvedValueOnce(page([row(1)],{next_after:1})).mockResolvedValueOnce(page([row(0,'10')],{funded_addresses:40,next_after:0}));const reader=createPortfolioReader(api as PortfolioAPI,()=>{});reader.reset({walletID:'wallet',chainID:'bsc',filter:'funded'});await reader.read();await reader.read('append');await reader.read('summary');
  expect(reader.getState().snapshot?.funded_addresses).toBe(40);expect(reader.getState().items.map(item=>item.index)).toEqual([0,1]);expect(reader.getState().nextAfter).toBe(1);
 });
 it('rejects wrong-network or wrong-filter responses and safely encodes wallet IDs',async()=>{
  const api=vi.fn(async()=>page([],{chain_id:'ethereum'}));const reader=createPortfolioReader(api as PortfolioAPI,()=>{});reader.reset({walletID:'wallet',chainID:'bsc',filter:'funded'});await reader.read();expect(reader.getState().snapshot).toBeUndefined();expect(reader.getState().error).toContain('does not match');expect(portfolioPath({walletID:'w/a',chainID:'bsc',filter:'failed'},3)).toBe('/wallets/w%2Fa/portfolio?chain_id=bsc&filter=failed&after=3');
 });
});
