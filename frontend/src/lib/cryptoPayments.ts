import { t } from '../i18n';
export interface CryptoAssetOption { id:string; chain_id:number; chain_name:string; name:string; symbol:string; contract:string; decimals:number; payment_decimals:number; cny_per_token:string; rate_updated_at:number; rate_expires_at:number; available:boolean; unavailable_reason:string }
export interface CryptoOptions { enabled:boolean; assets:CryptoAssetOption[] }
export interface CryptoTransfer {chain_id:number;tx_hash:string;log_index:number;contract:string;atoms:string;block_number:number;block_time:number;state:string;reason:string}
export interface CryptoInvoice {transfers?:CryptoTransfer[]; id:string; order_id:string; attempt_id:string; chain_id:number; chain_name:string; asset_id:string; symbol:string; asset_name:string; contract:string; decimals:number; payment_decimals:number; address:string; expected_atoms:string; received_atoms:string; confirmed_atoms:string; remaining_atoms:string; overpaid_atoms:string; state:string; message:string; created:number; expires:number; rate_source:string; cny_per_token:string; rate_updated_at:number; rate_expires_at:number; qr_uri:string; order_state:string; receipt_state:string; scan_error:string; last_scan:number; finalized_block:number }
/** Never convert token amounts to Number: 18-decimal balances routinely exceed safe integers. */
export function tokenAmount(atoms:string, decimals:number, minimumDecimals=0):string {
 if(!/^\d+$/.test(atoms)||!Number.isInteger(decimals)||decimals<0||decimals>36) return '—';
 const digits=BigInt(atoms).toString().padStart(decimals+1,'0');
 if(!decimals)return digits;
 const whole=digits.slice(0,-decimals),fraction=digits.slice(-decimals).replace(/0+$/,'').padEnd(Math.min(minimumDecimals,decimals),'0');
 return whole+(fraction?'.'+fraction:'');
}
export function tokenAtoms(value:string,decimals:number):string {
 if(!Number.isInteger(decimals)||decimals<0||decimals>36||!new RegExp('^(0|[1-9]\\d*)(?:\\.\\d{1,'+Math.max(1,decimals)+'})?$').test(value)||(!decimals&&value.includes('.')))throw new Error(t('请输入有效金额，且不要超过币种的小数位数'));
 const [whole,fraction='']=value.split('.');return (BigInt(whole)*10n**BigInt(decimals)+BigInt(fraction.padEnd(decimals,'0')||'0')).toString();
}
export function invoiceStateText(invoice:CryptoInvoice):string {
 if(invoice.order_state==='completed')return t('套餐已生效');
 const states:Record<string,string>={waiting:'等待付款',confirming:'已检测到付款，等待网络确认',partial:'已收到部分款项',paid:'已到账，正在开通',expired:'付款报价已过期',superseded:'已更换付款方式',review_required:'款项待管理员核对'};
 return t(states[invoice.state]||invoice.state);
}
export function invoiceCanPay(invoice:CryptoInvoice,now=Date.now()/1000):boolean{return ['waiting','partial'].includes(invoice.state)&&invoice.expires>now&&invoice.order_state==='pending';}
export function invoiceQR(invoice:CryptoInvoice,mode:'wallet'|'exchange'):string {
 if(mode==='exchange')return /^0x[0-9a-fA-F]{40}$/.test(invoice.address)?invoice.address:'';
 // Only frozen server invoice data can become a wallet request. No client quote arithmetic.
 return /^ethereum:[^\s]+$/i.test(invoice.qr_uri)?invoice.qr_uri:'';
}
export const CRYPTO_PROVIDER='crypto';
export function supportsPaymentPurpose(method:{code:string;purposes?:string[]},purpose:'order'|'topup'):boolean{return method.purposes?method.purposes.includes(purpose):purpose==='order'||!['crypto','evm_crypto'].includes(method.code);}
export interface CryptoChain {id:string;name:string;chain_id:number;rpc_url:string;rpc_backup_url:string;has_rpc:boolean;has_rpc_backup:boolean;native_symbol:string;enabled:boolean;finality_verified:boolean}
export interface CryptoConfiguredAsset extends CryptoAssetOption {enabled:boolean}
export interface CryptoSettings {revision:number;enabled:boolean;wallet_id:string;invoice_minutes:number;chains:CryptoChain[];assets:CryptoConfiguredAsset[]}
export const cryptoOperationsSupported=(chain:number)=>chain===1||chain===56;

export function transferStateText(state:string){const labels:Record<string,string>={observed:'已检测，待网络确认',confirmed:'已最终确认',review_required:'待管理员核对',orphaned:'已被链重组撤回'};return t(labels[state]||state)}

/** Persist only a public idempotency key so a page reload cannot turn an uncertain request into a new invoice. */
export function invoiceOperationID(orderID:string,assetID:string,generate:()=>string):string {
 const key='guangyue.crypto.invoice-operation.'+orderID;
 try {const raw=sessionStorage.getItem(key);if(raw){const saved=JSON.parse(raw);if(saved.asset_id===assetID&&typeof saved.id==='string'&&/^[A-Za-z0-9_-]{16,80}$/.test(saved.id))return saved.id}}
 catch {/* Private browsing may disable session storage. In-memory retry remains available. */}
 const id=generate();try{sessionStorage.setItem(key,JSON.stringify({asset_id:assetID,id}))}catch{}return id;
}
export function clearInvoiceOperation(orderID:string){try{sessionStorage.removeItem('guangyue.crypto.invoice-operation.'+orderID)}catch{}}
