import { t } from '../i18n';
import { canAllocate, walletSupportsChain, type CryptoWallet, type WalletAPI } from './cryptoWallet';
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
export interface CryptoChain {id:string;name:string;chain_id:number;rpc_url:string;rpc_backup_url:string;has_rpc:boolean;has_rpc_backup:boolean;native_symbol:string;enabled:boolean;finality_verified:boolean;rpc_source?:'built_in'|'custom'}
export interface CryptoConfiguredAsset extends CryptoAssetOption {enabled:boolean;rate_source?:string}
export interface CryptoSetupReadiness {ready:boolean;checked_at:number;message:string}
export interface CryptoSettings {revision:number;enabled:boolean;wallet_id:string;invoice_minutes:number;chains:CryptoChain[];assets:CryptoConfiguredAsset[];rate_mode?:'automatic'|'manual';rate_source?:string;rate_checked_at?:number;rate_error?:string;setup_checked_at?:number;readiness?:CryptoSetupReadiness}
export const cryptoOperationsSupported=(chain:number)=>chain===1||chain===56;

export function cryptoSetupDefaults(wallet:CryptoWallet|undefined,settings:CryptoSettings){
 const available=settings.chains.filter(chain=>cryptoOperationsSupported(chain.chain_id)&&walletSupportsChain(wallet,chain.chain_id));
 const configured=settings.wallet_id===wallet?.id&&settings.enabled?available.filter(chain=>chain.enabled):[];
 const chain_ids=configured.length?configured.map(chain=>chain.chain_id):available.some(chain=>chain.chain_id===56)?[56]:available.slice(0,1).map(chain=>chain.chain_id);
 const candidates=settings.assets.filter(asset=>chain_ids.includes(asset.chain_id));
 const enabled=configured.length?candidates.filter(asset=>asset.enabled):[];
 return {chain_ids,asset_ids:(enabled.length?enabled:candidates.filter(asset=>['USDT','USDC'].includes(asset.symbol))).map(asset=>asset.id)};
}
/** Quick setup never carries manual RPC credentials or client-created exchange rates. */
export function cryptoSetupSelection(wallet:CryptoWallet|undefined,settings:CryptoSettings,chainIDs:number[],assetIDs:string[]){
 if(!wallet||!canAllocate(wallet))throw new Error(t('请先启用钱包并完成备份与恢复索引确认'));
 const chain_ids=[...new Set(chainIDs)],asset_ids=[...new Set(assetIDs)];
 if(!chain_ids.length||chain_ids.some(id=>!cryptoOperationsSupported(id)||!walletSupportsChain(wallet,id)||!settings.chains.some(chain=>chain.chain_id===id)))throw new Error(t('请选择钱包支持的网络'));
 const selected=asset_ids.map(id=>settings.assets.find(asset=>asset.id===id));
 if(!asset_ids.length||selected.some(asset=>!asset||!chain_ids.includes(asset.chain_id))||chain_ids.some(id=>!selected.some(asset=>asset?.chain_id===id)))throw new Error(t('请为每个已选网络选择至少一种收款币种'));
 return {wallet_id:wallet.id,chain_ids,asset_ids};
}
export interface CryptoSetupOperation {key:string;id:string;completed:boolean}
/** A successful idempotent response may describe an older configuration. Always read the current state. */
export async function confirmCryptoSetup(api:WalletAPI,selection:ReturnType<typeof cryptoSetupSelection>,operation:CryptoSetupOperation,password:string):Promise<CryptoSettings>{
 if(!operation.completed){
  await api<CryptoSettings>('/setup','POST',{...selection,password,operation_id:operation.id});
  operation.completed=true;
 }
 return api<CryptoSettings>('/setup/status');
}

export function transferStateText(state:string){const labels:Record<string,string>={observed:'已检测，待网络确认',confirmed:'已最终确认',review_required:'待管理员核对',orphaned:'已被链重组撤回'};return t(labels[state]||state)}

/** Persist only a public idempotency key so a page reload cannot turn an uncertain request into a new invoice. */
export function invoiceOperationID(orderID:string,assetID:string,generate:()=>string):string {
 const key='guangyue.crypto.invoice-operation.'+orderID;
 try {const raw=sessionStorage.getItem(key);if(raw){const saved=JSON.parse(raw);if(saved.asset_id===assetID&&typeof saved.id==='string'&&/^[A-Za-z0-9_-]{16,80}$/.test(saved.id))return saved.id}}
 catch {/* Private browsing may disable session storage. In-memory retry remains available. */}
 const id=generate();try{sessionStorage.setItem(key,JSON.stringify({asset_id:assetID,id}))}catch{}return id;
}
export function clearInvoiceOperation(orderID:string){try{sessionStorage.removeItem('guangyue.crypto.invoice-operation.'+orderID)}catch{}}
