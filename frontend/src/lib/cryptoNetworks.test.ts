import {expect,it} from 'vitest';
import {cryptoAddress,solanaAddress,nativeDecimals,SOLANA_CHAIN_ID,PLASMA_CHAIN_ID} from './cryptoNetworks';
import {chainExplorer,gasReceiveURI} from './cryptoPortfolio';
import {invoiceQR,type CryptoInvoice} from './cryptoPayments';
import {walletFundingAddress,walletFundingPath,walletSupportsChain,type CryptoWallet} from './cryptoWallet';
import {treasurySweepInput,treasuryPreviewMatches,treasuryChains,type SweepPreview} from './cryptoTreasury';
import type {CryptoChain,CryptoConfiguredAsset} from './cryptoPayments';
const sol='9MUcDfm7gtccvAVUMMdUZWzW9DbY4pNzBDHTcwcEvBW3';
it('validates decoded Solana key length and keeps base58 case significant',()=>{
 expect(solanaAddress(sol)).toBe(true);expect(nativeDecimals({chain_id:101})).toBe(9);expect(nativeDecimals({chain_id:9745})).toBe(18);
 for(const address of ['0x'+'a'.repeat(40),'A'.repeat(32),'z'.repeat(44),'0'.repeat(44),'1'.repeat(31)])expect(solanaAddress(address)).toBe(false);
 expect(cryptoAddress(101,'1'.repeat(32))).toBe(true);expect(cryptoAddress(101,'1'.repeat(32),true)).toBe(false);
 const chain={id:'solana',chain_id:101} as CryptoChain,asset={id:'solana-usdc',chain_id:101,decimals:6} as CryptoConfiguredAsset,input=treasurySweepInput(chain,asset,sol,'0',[]);
 const preview={wallet_id:'w',...input,items:[]} as unknown as SweepPreview;expect(treasuryPreviewMatches(preview,'w',input)).toBe(true);expect(treasuryPreviewMatches({...preview,destination:sol.toLowerCase()},'w',input)).toBe(false);
 expect(()=>treasurySweepInput(chain,asset,'0x'+'a'.repeat(40),'0',[])).toThrow();
});
it('keeps Solana Pay and trusted explorers separate from EIP-681 Plasma',()=>{
 expect(gasReceiveURI(101,sol)).toBe('solana:'+sol);expect(gasReceiveURI(9745,'0x'+'a'.repeat(40))).toBe('ethereum:0x'+'a'.repeat(40)+'@9745');
 expect(chainExplorer(101,sol)).toBe('https://solscan.io/account/'+sol);expect(chainExplorer(9745,'0x'+'a'.repeat(40))).toBe('https://plasmascan.to/address/0x'+'a'.repeat(40));
 expect(chainExplorer(101,'https://attacker.invalid')).toBe('');expect(gasReceiveURI(101,'0x'+'a'.repeat(40))).toBe('');
 const invoice={chain_id:101,address:sol,qr_uri:'solana:'+sol+'?amount=2.13&spl-token=EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v'} as CryptoInvoice;
 expect(invoiceQR(invoice,'wallet')).toBe(invoice.qr_uri);expect(invoiceQR(invoice,'exchange')).toBe(sol);expect(invoiceQR({...invoice,qr_uri:'ethereum:0x'+'a'.repeat(40)},'wallet')).toBe('');
});
it('a hot wallet reads its Solana fee payer while an external EVM descriptor cannot activate Solana',()=>{
 const wallet={mode:'hot',supported_chain_ids:[1,56,101,9745],funding_address:'0x'+'a'.repeat(40),funding_path:'evm',solana_funding_address:sol,solana_funding_path:"m/44'/501'/2147483647'/0'"} as CryptoWallet;
 expect(walletFundingAddress(wallet,101)).toBe(sol);expect(walletFundingAddress(wallet,9745)).toBe(wallet.funding_address);expect(walletFundingPath(wallet,101)).toBe(wallet.solana_funding_path);
 expect(walletFundingAddress({...wallet,solana_funding_address:undefined},101)).toBe('');expect(walletSupportsChain({...wallet,mode:'watch_only'},101)).toBe(false);
 const chains=[101,9745,56,8453].map(chain_id=>({chain_id,id:String(chain_id)})) as CryptoChain[];
 expect(treasuryChains(wallet,chains).map(chain=>chain.chain_id)).toEqual([56,SOLANA_CHAIN_ID,PLASMA_CHAIN_ID]);expect(treasuryChains({...wallet,mode:'watch_only'},chains).map(chain=>chain.chain_id)).toEqual([56,9745]);
});
