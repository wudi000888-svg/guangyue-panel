import {expect,it} from 'vitest';
import {hasCryptoBalance,previewFundingFees,previewGasFees,canSignSweep,sweepFinished,sweepJobStateText,previewFundingShortfall,treasuryChains,treasuryChainID,treasurySweepInput,treasuryPreviewMatches,type CryptoBalance,type SweepPreview,type SweepJob} from './cryptoTreasury';
import type {CryptoChain,CryptoConfiguredAsset} from './cryptoPayments';
it('keeps gas and token balances exact and never confuses dust with zero',()=>{expect(hasCryptoBalance({native_atoms:'0',tokens:[{balance_atoms:'1'}]} as CryptoBalance)).toBe(true);expect(hasCryptoBalance({native_atoms:'0',tokens:[]} as unknown as CryptoBalance)).toBe(false);const preview={items:[{funding_gas_atoms:'900719925474099300',gas_fee_atoms:'900719925474099301'},{funding_gas_atoms:'1',gas_fee_atoms:'2'}]} as SweepPreview;expect(previewFundingFees(preview)).toBe('900719925474099301');expect(previewGasFees(preview)).toBe('900719925474099303')});
it('watch-only and unsupported networks cannot sign even with a funded address',()=>{expect(canSignSweep('watch_only',true)).toBe(false);expect(canSignSweep('hot',false)).toBe(false);expect(canSignSweep('hot',true)).toBe(true)});
it('broadcast and waiting receipts are never presented as complete',()=>{for(const state of ['gas_pending','sweep_pending','queued'])expect(sweepFinished({state:'complete',items:[{state}]} as SweepJob)).toBe(false);expect(sweepFinished({state:'complete',items:[{state:'complete'}]} as SweepJob)).toBe(true)});

it('shows the exact funding shortage while retaining the full fee preview',()=>{expect(previewFundingShortfall({required_funding_atoms:'2000000000000000001',funding_balance_atoms:'1000000000000000000'} as SweepPreview)).toBe('1000000000000000001');expect(previewFundingShortfall({required_funding_atoms:'1',funding_balance_atoms:'2'} as SweepPreview)).toBe('0')});

it('does not label inconsistent task totals as complete when receipts are still pending',()=>{expect(sweepJobStateText({state:'complete',items:[{state:'sweep_pending'}]} as SweepJob)).toBe('需要核对')});

const chainOptions=[{id:'ethereum',chain_id:1,has_rpc:true},{id:'bsc',chain_id:56,has_rpc:true},{id:'base',chain_id:8453,has_rpc:true}] as CryptoChain[];
const bscAsset={id:'bsc-usdt',chain_id:56,decimals:18} as CryptoConfiguredAsset;
const destination='0x1234567890123456789012345678901234567890';
it('a BNB-only wallet never offers ETH gas, even when an Ethereum RPC is configured',()=>{
  const allowed=treasuryChains({supported_chain_ids:[56]},chainOptions);
  expect(allowed.map(chain=>chain.chain_id)).toEqual([56]);
  expect(treasuryChainID('ethereum',allowed)).toBe('bsc');
  expect(treasuryChains({supported_chain_ids:[]},chainOptions)).toEqual([]);
  expect(treasuryChains(undefined,chainOptions)).toEqual([]);
});
it('keeps an allowed choice but excludes unsupported finality adapters and duplicate wallet networks',()=>{
  const allowed=treasuryChains({supported_chain_ids:[56,1,56,8453]},chainOptions);
  expect(allowed.map(chain=>chain.chain_id)).toEqual([56,1]);
  expect(treasuryChainID('ethereum',allowed)).toBe('ethereum');
  expect(treasuryChainID('',allowed)).toBe('bsc');
});
it('asks the server for an automatic budget while preserving exact minimum and selected address scope',()=>{
  expect(treasurySweepInput(chainOptions[1],bscAsset,destination,'0.000000000000000001',['a','a','b'])).toEqual({chain_id:'bsc',asset_id:'bsc-usdt',destination,min_atoms:'1',max_gas_atoms:'',address_ids:['a','b']});
  expect(treasurySweepInput(chainOptions[1],bscAsset,destination,'0',[])).not.toHaveProperty('address_ids');
  expect(()=>treasurySweepInput(chainOptions[0],bscAsset,destination,'0',[])).toThrow();
  expect(()=>treasurySweepInput(chainOptions[1],bscAsset,'0x'+'0'.repeat(40),'0',[])).toThrow();
});
it('a confirmation cannot reuse a quote for a different wallet, network, recipient, amount or selection',()=>{
  const input=treasurySweepInput(chainOptions[1],bscAsset,destination,'0',['a']);
  const preview={wallet_id:'w',chain_id:'bsc',asset_id:'bsc-usdt',destination,min_atoms:'0',items:[{address_id:'a'}]} as SweepPreview;
  expect(treasuryPreviewMatches(preview,'w',input)).toBe(true);
  for(const changed of [{wallet_id:'other'},{chain_id:'ethereum'},{asset_id:'other'},{destination:'0x'+'b'.repeat(40)},{min_atoms:'1'},{items:[{address_id:'b'}]}]){
    expect(treasuryPreviewMatches({...preview,...changed} as SweepPreview,'w',input)).toBe(false);
  }
});
