import { onScopeDispose, ref, watch } from 'vue';
import { isCancelled, useApi } from './api';
import { t } from '../i18n';

export interface CommerceSettings {
 payment_module_enabled:boolean;sales:boolean;redemption:boolean;tickets:boolean;currency:string;
 payment_provider:string;payment_webhook_url:string;mail_provider:string;mail_from:string;mail_webhook_url:string;
}
export const paymentModuleRevision=ref(0);
export function notifyPaymentModuleChanged(){paymentModuleRevision.value++;}
/** Resolve the entry site's payment gate independently of the currently selected child site. */
export function usePaymentModule(active:()=>boolean=()=>true){
 const api=useApi('/commerce'),enabled=ref(false),loaded=ref(false),loading=ref(false),error=ref('');
 let generation=0,disposed=false;
 async function refresh(){
  const current=++generation;enabled.value=false;loaded.value=false;error.value='';
  if(!active()||disposed){loading.value=false;return;}
  loading.value=true;
  try{const settings=await api<CommerceSettings>('/settings');if(disposed||current!==generation||!active())return;enabled.value=settings.payment_module_enabled===true;loaded.value=true;}
  catch(reason){if(!disposed&&current===generation&&!isCancelled(reason))error.value=t('暂时无法确认支付服务状态，请刷新后重试。');}
  finally{if(current===generation)loading.value=false;}
 }
 watch([paymentModuleRevision,active],()=>{void refresh()},{immediate:true,flush:'sync'});
 onScopeDispose(()=>{disposed=true;generation++;});
 return {enabled,loaded,loading,error,refresh};
}
