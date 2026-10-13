import {expect,it} from 'vitest';
import {createSSRApp} from 'vue';
import {renderToString} from 'vue/server-renderer';
import WalletGasPanel from './WalletGasPanel.vue';
import WalletAssetOverview from './WalletAssetOverview.vue';
import WalletFeeFiat from './WalletFeeFiat.vue';
import type {CryptoGasSnapshot} from '../lib/cryptoGas';
import type {CryptoChain} from '../lib/cryptoPayments';
const chain={id:'solana',chain_id:101,name:'Solana',native_symbol:'SOL',native_decimals:9} as CryptoChain;
const gas={wallet_id:'w',chain_id:'solana',native_symbol:'SOL',native_decimals:9,fee_model:'solana',gas_price_atoms:'0',reference_native_transfer_fee_atoms:'5000',gas_checked_at:100,expires_at:110,gas_error:'',funding_address:'',funding_balance_atoms:'1234567890',funding_checked_at:100,funding_error:'',block_number:321,finality:'finalized',last_attempt_at:100,native_price_usd:'150',native_price_cny:'1000',price_checked_at:98,price_expires_at:150,fiat_source:'market-test'} as CryptoGasSnapshot;
it('the real Solana Gas component renders SOL precision and small USD/CNY fees with no EVM units',async()=>{
 const html=await renderToString(createSSRApp(WalletGasPanel,{chain,gas,fundingAddress:'',watchOnly:false,writable:true,now:100}));
 expect(html).toContain('1.23456789');expect(html).toContain('0.000005');expect(html).toContain('≈ $0.0008 / ¥0.0050');expect(html).toContain('market-test');expect(html).toContain('每 5 秒更新');expect(html).not.toContain('Gwei');expect(html).toContain('data-price-state="current"');
});
it('the real overview displays the selected chain reference fee while fiat failure stays unknown',async()=>{
 const html=await renderToString(createSSRApp(WalletAssetOverview,{chains:[chain],assets:[],summaries:{},errors:{},selectedChain:'solana',selectedAsset:'',walletName:'wallet',loading:false,writable:true,busy:false,gas:{...gas,native_price_usd:undefined,native_price_cny:undefined,price_checked_at:0},now:100}));
 expect(html).toContain('0.000005');expect(html).toContain('≈ $— / ¥—');expect(html).toContain('等待费用汇率查询');expect(html).not.toContain('Gwei');
});
it('stale fiat values are explicitly identified independently of a current Gas estimate',async()=>{
 const html=await renderToString(createSSRApp(WalletFeeFiat,{atoms:'5000',decimals:9,quote:{...gas,price_expires_at:99},now:100}));expect(html).toContain('data-price-state="stale"');expect(html).toContain('上次行情估值');expect(html).toContain('≈ $0.0008 / ¥0.0050');
});
it('a finalized commitment without a known slot never displays fictitious block zero',async()=>{
 const html=await renderToString(createSSRApp(WalletGasPanel,{chain,gas:{...gas,block_number:0},fundingAddress:'',watchOnly:false,writable:true,now:100}));
 expect(html).toContain('每 5 秒更新');expect(html).not.toContain('#0');
});
