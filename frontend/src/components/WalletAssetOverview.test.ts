import { describe, expect, it } from 'vitest';
import { createSSRApp } from 'vue';
import { renderToString } from 'vue/server-renderer';
import WalletAssetOverview from './WalletAssetOverview.vue';
import type { CryptoChain, CryptoConfiguredAsset } from '../lib/cryptoPayments';
import type { PortfolioSnapshot } from '../lib/cryptoPortfolio';
import type { CryptoGasSnapshot } from '../lib/cryptoGas';

const chains = [{ id: 'bsc', chain_id: 56, name: 'BNB Smart Chain', native_symbol: 'BNB' }, { id: 'ethereum', chain_id: 1, name: 'Ethereum', native_symbol: 'ETH' }] as CryptoChain[];
const assets = [{ id: 'bsc-usdt', chain_id: 56, symbol: 'USDT', name: 'Binance-Peg USDT', decimals: 18 }, { id: 'ethereum-usdt', chain_id: 1, symbol: 'USDT', name: 'USDT', decimals: 6 }] as CryptoConfiguredAsset[];
const snapshot = (chain_id: string, balance: string): PortfolioSnapshot => ({
 wallet_id: 'wallet', chain_id, tokens: [{ asset_id: chain_id + '-usdt', symbol: 'USDT', decimals: chain_id === 'bsc' ? 18 : 6, balance_atoms: balance }], native_balance_atoms: '1', funding_balance_atoms: '2', gas_price_atoms: '3', checked_addresses: 1, total_addresses: 1, funded_addresses: 1, complete: true, stale: false, refreshing: false, funding_checked_at: 1, checked_at: 1, items: [], errors: [],
} as unknown as PortfolioSnapshot);
const props = { chains, assets, errors: {}, selectedChain: 'bsc', selectedAsset: 'bsc-usdt', walletName: '收款钱包', loading: false, writable: true, busy: false };

describe('wallet asset overview', () => {
 it('denominates its primary balance in the chosen network and preserves every fraction digit', async () => {
  const html = await renderToString(createSSRApp(WalletAssetOverview, { ...props, summaries: { bsc: snapshot('bsc', '12345678901234567890'), ethereum: snapshot('ethereum', '987654321098765432') } }));
  expect(html).toContain('aria-label="12.34567890123456789 USDT · BNB Smart Chain"');
  expect(html).toContain('.34567890123456789');
  expect(html).not.toContain('987654321098.765432');
  expect(html).not.toContain('USD 估值');
 });
 it('a first Gas request failure shows the error and never substitutes portfolio Gas values',async()=>{
  const html=await renderToString(createSSRApp(WalletAssetOverview,{...props,summaries:{bsc:snapshot('bsc','0')},gasError:'RPC unavailable'}));
  expect(html).toContain('Gas 查询失败，当前费用未知。');expect(html).toContain('RPC unavailable');expect(html).toContain('role="alert"');expect(html).not.toContain('0.000000003');expect(html).not.toContain('0.000000000000000002');
 });
 it('an HTTP-success snapshot with no successful Gas read shows the cause instead of a fictitious old estimate',async()=>{
  const gas={wallet_id:'wallet',chain_id:'bsc',gas_checked_at:0,expires_at:0,gas_error:'请配置两个独立的 HTTPS RPC',gas_price_atoms:'',reference_native_transfer_fee_atoms:'',funding_balance_atoms:''} as CryptoGasSnapshot;
  const html=await renderToString(createSSRApp(WalletAssetOverview,{...props,summaries:{},gas}));
  expect(html).toContain('Gas 查询失败，当前费用未知。');expect(html).toContain('请配置两个独立的 HTTPS RPC');expect(html).toContain('role="alert"');expect(html).not.toContain('上次估计');expect(html).not.toContain('1970');
 });
 it('shows the absence of a successful snapshot instead of a zero balance', async () => {
  const html = await renderToString(createSSRApp(WalletAssetOverview, { ...props, summaries: {} }));
  expect(html).toContain('aria-label="— USDT · BNB Smart Chain"');
  expect(html).toContain('等待链上查询');
 });
});
