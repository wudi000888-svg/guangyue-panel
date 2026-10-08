import { ref } from 'vue';
import { useApi, isCancelled } from './api';
import { t } from '../i18n';
import type { Plan } from '../types';
export interface Wallet {user_id:number;available:string;held:string;currency:string}
// A successful money operation invalidates the header's read of our own wallet.
export const walletRevision = ref(0);
export interface MoneyTransaction {id:string;kind:string;amount:string;available:string;held:string;reason:string;reference:string;created:number}
export interface Offer {id:string;version:number;enabled:boolean;price:string;purchase_limit?:number;plan:Plan}
export interface PaymentMethod {id:string;code:string;name:string;enabled:boolean;version:number;archived?:boolean;base_url?:string;merchant_id?:string;channel?:string;checkout:boolean;has_secret?:boolean;has_webhook_secret?:boolean;mode?:'test'|'live';webhook_url?:string}
export interface PaymentAttempt {id:string;purpose?:'order'|'topup';order_id:string;user_id:number;provider_id:string;state:string;amount:string;currency:string;merchant_ref:string;external_ref?:string;checkout_url?:string;message?:string;refund_status?:string;created:number;updated:number;expires:number}
export interface PaymentReceipt {id:string;provider_id:string;external_ref:string;attempt_id:string;order_id:string;user_id:number;amount:string;currency:string;state:string;reason:string;created:number;updated:number}
export interface PaymentCheckout {attempt:PaymentAttempt;checkout_url:string;type:'redirect'}
export interface PaymentDiagnostic {ok:boolean;message:string;checked_at:number;last_event_at:number;pending:number;refund_required:number}
export interface Order {id:string;user_id:number;state:string;created:number;updated:number;expires:number;offer:Offer;action:string;before_expiry:number;message:string;payment_attempt_id?:string;payment_provider_id?:string}
export interface Redemption {id:string;batch_id:string;suffix:string;amount:string;expires:number;state:string;redeemed_by:number;redeemed_at:number;note:string;has_secret?:boolean}
export interface Ticket {id:string;user_id:number;title:string;category:string;state:string;priority:string;order_id:string;site_id:string;node_id:string;resource:string;created:number;updated:number;closed:number;revision:string}
export interface TicketReply {id:string;ticket_id:string;user_id:number;sender:string;body:string;internal:boolean;attachments:string[];created:number}
/** Generate an idempotency key even in webviews without randomUUID(). */
export function operationID(): string {
 try {
  const c = globalThis.crypto;
  if (typeof c?.randomUUID === 'function') return c.randomUUID();
  const bytes = new Uint8Array(16);
  if (typeof c?.getRandomValues === 'function') c.getRandomValues(bytes);
  else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  return Array.from(bytes, v => v.toString(16).padStart(2, '0')).join('');
 } catch {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
 }
}
export function cents(value:string):string{if(!/^(0|[1-9]\d*)(\.\d{1,2})?$/.test(value))throw new Error(t('金额最多两位小数'));const [whole,fraction='']=value.split('.');const n=BigInt(whole)*100n+BigInt(fraction.padEnd(2,'0'));if(n<=0n||n>100000000000n)throw new Error(t('金额超出限制'));return n.toString()}
export function money(value:string|undefined):string{const n=BigInt(value||'0'),abs=n<0n?-n:n;return (n<0n?'-':'')+'¥'+(abs/100n).toLocaleString()+'.'+(abs%100n).toString().padStart(2,'0')}
export const stamp=(n:number)=>n?new Date(n*1000).toLocaleString():t('不限');
const states:Record<string,string>={pending:'待确认',provisioning:'开通中',completed:'已完成',cancelled:'已取消',expired:'已过期',failed:'开通失败',refund_requested:'待审核退款',refund_pending:'待原路退款',refunding:'退款处理中',refunded:'已退款',unused:'未使用',redeemed:'已兑换',revoked:'已作废',open:'待处理',processing:'处理中',waiting_user:'待用户回复',resolved:'已解决',closed:'已关闭',credit:'人工入账',debit:'人工扣减',gift:'赠送',redemption:'兑换码入账',online_topup:'在线充值',hold:'余额冻结',release:'余额解冻',purchase:'套餐消费',refund:'订单退款'};
export const stateText=(s:string)=>t(states[s]||s);
const paymentStates:Record<string,string>={created:'正在创建支付',awaiting_customer:'待支付',paid:'已到账，正在处理',completed:'已完成',expired:'支付已过期',cancelled:'支付已取消',failed:'支付处理失败',refund_required:'待原路退款',refund_pending:'原路退款处理中',refunded:'已原路退款',verified:'已到账，正在处理',applied:'已入账',review_required:'待人工核对',queued:'等待支付平台处理',pending:'支付平台处理中',processing:'支付平台处理中',succeeded:'退款已完成'};
export const paymentStateText=(state:string)=>t(paymentStates[state]||state);
export const canResumePayment=(payment:PaymentAttempt,now=Date.now()/1000)=>['created','awaiting_customer'].includes(payment.state)&&payment.expires>now;
export function paymentCheckoutURL(raw:string):string {
 try {const url=new URL(raw);if(url.protocol==='https:'&&url.hostname&&!url.username&&!url.password)return url.href;}catch{/* Invalid checkout URLs are never opened. */}
 throw new Error(t('支付页面地址无效，请刷新状态后重试'));
}
/** Call synchronously in the click handler, before awaiting checkout creation. */
export function preparePaymentWindow(){
 let popup:Window|null=null;
 try {popup=window.open('about:blank','_blank');if(popup){popup.opener=null;popup.document.title=t('正在准备支付页面…');popup.document.body.textContent=t('正在准备支付页面…');}}catch{try{popup?.close()}catch{}popup=null}
 const close=()=>{try{if(popup&&!popup.closed)popup.close()}catch{}popup=null};
 return {close,open(raw:string){let url:string;try{url=paymentCheckoutURL(raw)}catch(error){close();throw error}
  try{if(popup&&!popup.closed){popup.location.replace(url);popup=null;return 'new-tab' as const}}catch{close()}
  // Popup blockers and embedded browsers still support a normal navigation.
  window.location.assign(url);return 'same-tab' as const;
 }};
}
export function useCommerce(prefix='/commerce'){
 const api=useApi(prefix),busy=ref(false),error=ref(''),notice=ref(''),keys=new Map<string,string>();
 async function run<T>(path:string,body:Record<string,unknown>):Promise<T|null>{if(busy.value)return null;busy.value=true;error.value='';notice.value='';const safe={...body};delete safe.password;const fingerprint=path+JSON.stringify(safe),id=keys.get(fingerprint)||operationID();keys.set(fingerprint,id);
 try{const v=await api<T>(path,'POST',{...body,operation_id:id});keys.delete(fingerprint);if(prefix==='/commerce'&&['/redeem','/adjust','/orders/action'].includes(path))walletRevision.value++;return v}catch(e){if(!isCancelled(e))error.value=e instanceof Error?t(e.message):t('请求失败');return null}finally{busy.value=false}}
 async function read<T>(path:string):Promise<T|null>{try{return await api<T>(path)}catch(e){if(!isCancelled(e))error.value=e instanceof Error?t(e.message):t('请求失败');return null}}
 return {api,run,read,busy,error,notice};
}
