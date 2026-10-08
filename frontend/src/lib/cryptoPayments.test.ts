import {describe,it,expect,vi,afterEach} from 'vitest';
import {confirmCryptoSetup,type CryptoSetupOperation,cryptoSetupDefaults,cryptoSetupSelection,type CryptoSettings,invoiceOperationID,clearInvoiceOperation,tokenAmount,tokenAtoms,invoiceStateText,invoiceCanPay,invoiceQR,supportsPaymentPurpose,type CryptoInvoice} from './cryptoPayments';
import type {CryptoWallet,WalletAPI} from './cryptoWallet';
const invoice={address:'0x9858EfFD232B4033E47d90003D41EC34EcaEda94',qr_uri:'ethereum:0xToken@1/transfer?address=0xRecipient&uint256=1230000',state:'waiting',order_state:'pending',expires:200} as CryptoInvoice;
describe('crypto amounts',()=>{
 it('preserves huge balances and 18-decimal dust exactly',()=>{expect(tokenAmount('900719925474099312345678901234567890',18)).toBe('900719925474099312.34567890123456789');expect(tokenAmount('1',18)).toBe('0.000000000000000001');expect(tokenAmount('1000000',6,2)).toBe('1.00');expect(tokenAtoms('900719925474099312.34567890123456789',18)).toBe('900719925474099312345678901234567890')});
 it('rejects exponent, sign, overprecision and invalid server amounts',()=>{for(const value of ['1e6','-1','1.0000001','01','NaN'])expect(()=>tokenAtoms(value,6)).toThrow();expect(()=>tokenAtoms('1.1',0)).toThrow();expect(tokenAmount('NaN',18)).toBe('—');expect(tokenAtoms('0',18)).toBe('0')});
});
it('payment completion depends on entitlement; late overpayment cannot replace active plan status',()=>{expect(invoiceStateText({...invoice,state:'review_required',order_state:'completed'})).toBe('套餐已生效');expect(invoiceStateText({...invoice,state:'paid'})).toBe('已到账，正在开通');expect(invoiceCanPay(invoice,100)).toBe(true);expect(invoiceCanPay(invoice,200)).toBe(false);expect(invoiceCanPay({...invoice,state:'confirming'},100)).toBe(false)});
it('exchange QR contains only address and wallet QR comes from frozen invoice',()=>{expect(invoiceQR(invoice,'exchange')).toBe(invoice.address);expect(invoiceQR(invoice,'wallet')).toBe(invoice.qr_uri);expect(invoiceQR({...invoice,qr_uri:'https://evil.test'},'wallet')).toBe('')});
it('crypto is excluded from fiat topups even if an older response omits purposes',()=>{expect(supportsPaymentPurpose({code:'crypto'},'topup')).toBe(false);expect(supportsPaymentPurpose({code:'evm_crypto'},'topup')).toBe(false);expect(supportsPaymentPurpose({code:'crypto',purposes:['order']},'topup')).toBe(false);expect(supportsPaymentPurpose({code:'epay'},'topup')).toBe(true)});

afterEach(()=>vi.unstubAllGlobals());
it('restores an uncertain invoice operation after reload without storing passwords or quotes',()=>{const values=new Map<string,string>();vi.stubGlobal('sessionStorage',{getItem:(k:string)=>values.get(k),setItem:(k:string,v:string)=>values.set(k,v),removeItem:(k:string)=>values.delete(k)});const generate=vi.fn(()=> 'operation-1234567890');expect(invoiceOperationID('order','ethereum-usdt',generate)).toBe('operation-1234567890');expect(invoiceOperationID('order','ethereum-usdt',()=> 'another-operation-123')).toBe('operation-1234567890');expect(JSON.parse([...values.values()][0]!)).toEqual({asset_id:'ethereum-usdt',id:'operation-1234567890'});clearInvoiceOperation('order');expect(values.size).toBe(0)});

const setupWallet={id:'bnb',mode:'hot',enabled:true,backup_confirmed:true,recovery_required:false,next_index:0,supported_chain_ids:[56]} as CryptoWallet;
const setupSettings={enabled:false,wallet_id:'',chains:[{chain_id:1},{chain_id:56},{chain_id:8453}],assets:[{id:'ethereum-usdt',chain_id:1,symbol:'USDT'},{id:'bsc-usdt',chain_id:56,symbol:'USDT'},{id:'bsc-usdc',chain_id:56,symbol:'USDC'},{id:'base-usdc',chain_id:8453,symbol:'USDC'}]} as CryptoSettings;
it('defaults setup to BNB and stablecoins without showing unsupported networks',()=>{
 expect(cryptoSetupDefaults(setupWallet,setupSettings)).toEqual({chain_ids:[56],asset_ids:['bsc-usdt','bsc-usdc']});
 expect(cryptoSetupDefaults({...setupWallet,supported_chain_ids:[1]},setupSettings)).toEqual({chain_ids:[1],asset_ids:['ethereum-usdt']});
 expect(cryptoSetupDefaults(undefined,setupSettings)).toEqual({chain_ids:[],asset_ids:[]});
});
it('quick setup carries only wallet/network/asset selection and cannot bypass backup or network gates',()=>{
 expect(cryptoSetupSelection(setupWallet,setupSettings,[56],['bsc-usdt','bsc-usdc'])).toEqual({wallet_id:'bnb',chain_ids:[56],asset_ids:['bsc-usdt','bsc-usdc']});
 for(const wallet of [{...setupWallet,backup_confirmed:false},{...setupWallet,recovery_required:true},{...setupWallet,enabled:false}])expect(()=>cryptoSetupSelection(wallet,setupSettings,[56],['bsc-usdt'])).toThrow();
 for(const [chains,assets] of [[[1],['ethereum-usdt']],[[56],['ethereum-usdt']],[[56],[]],[[8453],['base-usdc']]] as [number[],string[]][])expect(()=>cryptoSetupSelection(setupWallet,setupSettings,chains,assets)).toThrow();
 expect(()=>cryptoSetupSelection({...setupWallet,supported_chain_ids:[1,56]},setupSettings,[1,56],['bsc-usdt'])).toThrow();
});

it('reads current setup state after an older enabled operation is replayed',async()=>{
 const operation:CryptoSetupOperation={key:'same-selection',id:'operation-setup-1234',completed:false};
 const api=vi.fn().mockResolvedValueOnce({...setupSettings,enabled:true}).mockResolvedValueOnce({...setupSettings,enabled:false,readiness:{ready:false,checked_at:1,message:'管理员已停用'}});
 const current=await confirmCryptoSetup(api as WalletAPI,{wallet_id:'bnb',chain_ids:[56],asset_ids:['bsc-usdt']},operation,'password');
 expect(current.enabled).toBe(false);expect(current.readiness?.ready).toBe(false);expect(operation.completed).toBe(true);
 expect(api.mock.calls.map(call=>call[0])).toEqual(['/setup','/setup/status']);
});
it('after setup commits but status fails, retries only the read and retains no password in operation state',async()=>{
 const operation:CryptoSetupOperation={key:'same-selection',id:'operation-setup-1234',completed:false};
 const selection={wallet_id:'bnb',chain_ids:[56],asset_ids:['bsc-usdt']};
 const api=vi.fn().mockResolvedValueOnce({...setupSettings,enabled:true}).mockRejectedValueOnce(new Error('status unavailable')).mockResolvedValueOnce({...setupSettings,enabled:false});
 await expect(confirmCryptoSetup(api as WalletAPI,selection,operation,'sensitive-password')).rejects.toThrow('status unavailable');
 expect(operation).toEqual({key:'same-selection',id:'operation-setup-1234',completed:true});
 expect((await confirmCryptoSetup(api as WalletAPI,selection,operation,'')).enabled).toBe(false);
 expect(api.mock.calls.map(call=>call[0])).toEqual(['/setup','/setup/status','/setup/status']);
});
it('keeps the same setup operation when the POST result itself is uncertain',async()=>{
 const operation:CryptoSetupOperation={key:'same-selection',id:'operation-setup-1234',completed:false};
 const selection={wallet_id:'bnb',chain_ids:[56],asset_ids:['bsc-usdt']};
 const api=vi.fn().mockRejectedValueOnce(new Error('response lost')).mockResolvedValueOnce({...setupSettings,enabled:true}).mockResolvedValueOnce({...setupSettings,enabled:true,readiness:{ready:false,checked_at:1,message:'报价过期'}});
 await expect(confirmCryptoSetup(api as WalletAPI,selection,operation,'first-password')).rejects.toThrow('response lost');expect(operation.completed).toBe(false);
 const current=await confirmCryptoSetup(api as WalletAPI,selection,operation,'retry-password');expect(current.readiness?.ready).toBe(false);
 expect(api.mock.calls[0]?.[2].operation_id).toBe(api.mock.calls[1]?.[2].operation_id);expect(api.mock.calls).toHaveLength(3);
});
