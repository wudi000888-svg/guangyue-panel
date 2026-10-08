<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import { CreditCard, Mail, Settings2 } from 'lucide-vue-next';
import { useCommerce } from '../lib/commerce';
import { notifyPaymentModuleChanged, type CommerceSettings as CommerceSettingsValue } from '../lib/paymentModule';
import { t } from '../i18n';
import PaymentSettings from './PaymentSettings.vue';
import MailSettings from './MailSettings.vue';
import '../commerce.css';
import '../styles/integrations.css';
const props=defineProps<{ active?: boolean }>();
const { read, api, busy, error, notice } = useCommerce();
const section = ref('payments'), password = ref(''), modulePassword=ref(''), moduleDraft=ref(false), settingsLoaded=ref(false);
let disposed=false;
const settingsLoading=ref(false);
const settings = ref<CommerceSettingsValue>({ payment_module_enabled:false,sales: false, redemption: true, tickets: true, currency: 'CNY', payment_provider: 'manual', payment_webhook_url: '', mail_provider: 'none', mail_from: '', mail_webhook_url: '' });
const tabs = [{ id: 'payments', label: '在线支付', icon: CreditCard }, { id: 'mail', label: '邮件服务', icon: Mail }, { id: 'rules', label: '账户规则', icon: Settings2 }];
function tabKey(event: KeyboardEvent, index: number) {
  if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
  event.preventDefault();
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
  section.value = tabs[next]!.id;
  document.getElementById('integration-tab-' + section.value)?.focus();
}
async function loadSettings(){settingsLoading.value=true;try{const v=await read<CommerceSettingsValue>('/settings');if(v&&!disposed){settings.value=v;moduleDraft.value=v.payment_module_enabled;settingsLoaded.value=true}}finally{settingsLoading.value=false}}
onMounted(loadSettings);
watch(()=>props.active,active=>{password.value='';modulePassword.value='';if(active)void loadSettings()});
watch(section,()=>{password.value='';modulePassword.value=''});
onUnmounted(()=>{disposed=true;password.value='';modulePassword.value=''});
async function saveModule(){if(busy.value||!settingsLoaded.value)return;busy.value=true;error.value='';notice.value='';try{const value=await api<CommerceSettingsValue>('/settings','POST',{settings:{...settings.value,payment_module_enabled:moduleDraft.value},password:modulePassword.value});if(!disposed){settings.value=value;moduleDraft.value=value.payment_module_enabled;notifyPaymentModuleChanged();notice.value=t('支付模块设置已保存');await loadSettings()}}catch(e){if(!disposed)error.value=t((e as Error).message)}finally{modulePassword.value='';busy.value=false}}
async function save() {
  busy.value = true; error.value = ''; notice.value = '';
  try { const {payment_module_enabled: _module,...accountSettings}=settings.value;await api<CommerceSettingsValue>('/settings', 'POST', { settings: accountSettings, password: password.value });await loadSettings();notice.value = t('账户服务设置已保存'); }
  catch (e) { error.value = t((e as Error).message); }
  finally { password.value = ''; busy.value = false; }
}
</script>
<template>
  <section class="settings-card commerce-preferences">
    <nav class="integration-service-tabs" role="tablist" :aria-label="t('交易与接入分类')"><button v-for="(tab,index) in tabs" :key="tab.id" :id="'integration-tab-'+tab.id" role="tab" :tabindex="section===tab.id?0:-1" :aria-selected="section===tab.id" :aria-controls="'integration-panel-'+tab.id" @click="section=tab.id" @keydown="tabKey($event,index)"><component :is="tab.icon" :size="17"/>{{t(tab.label)}}</button></nav>
    <div :id="'integration-panel-'+section" role="tabpanel" :aria-labelledby="'integration-tab-'+section"><template v-if="section==='payments'"><form class="payment-module-switch" @submit.prevent="saveModule"><div><h2>{{t('支付模块')}}</h2><p>{{t('子站被接管后默认关闭，可由本站管理员启用。')}}</p><p>{{t('关闭后停止新购买、充值和提取；历史订单、已批准任务及到账核对继续保留，邮件服务不受影响。')}}</p></div><p v-if="error" class="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{notice}}</p><fieldset :disabled="busy||settingsLoading||!settingsLoaded"><label class="inline-check"><input v-model="moduleDraft" type="checkbox"/>{{t('启用支付模块')}}</label><label>{{t('管理员当前密码')}}<input v-model="modulePassword" type="password" required autocomplete="current-password"/></label><button class="primary" type="submit" :disabled="busy||!settingsLoaded">{{busy?t('保存中…'):t('保存支付模块设置')}}</button></fieldset><button v-if="!settingsLoaded&&!settingsLoading" type="button" @click="loadSettings">{{t('重新读取服务状态')}}</button></form><PaymentSettings :active="active !== false"/></template><MailSettings v-else-if="section==='mail'"/><form v-else class="service-integration integration-form" @submit.prevent="save"><header class="integration-heading"><div><h2>{{t('账户规则')}}</h2><p>{{t('管理兑换码充值与工单服务。')}}</p></div><RouterLink to="/shop">{{t('套餐上架')}} →</RouterLink></header><p v-if="error" class="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{notice}}</p><fieldset :disabled="busy"><label class="inline-check"><input v-model="settings.redemption" type="checkbox"/>{{t('开放兑换码充值')}}</label><label class="inline-check"><input v-model="settings.tickets" type="checkbox"/>{{t('允许成员提交工单')}}</label><p class="integration-muted">{{t('支付模块开启后，实际是否可购买由“套餐上架”中的上架状态决定。')}}</p><label>{{t('管理员当前密码')}}<input v-model="password" type="password" autocomplete="current-password" required/></label><button class="primary" :disabled="busy">{{t('保存账户服务设置')}}</button></fieldset></form></div>
  </section>
</template>

<style scoped>
.payment-module-switch{display:grid;gap:15px;padding:20px;margin:8px 0 24px;border:1px solid var(--border);border-radius:12px;background:var(--surface-raised)}.payment-module-switch h2{font-size:17px;margin:0 0 8px}.payment-module-switch p{font-size:12px;color:var(--secondary);line-height:1.8;margin:4px 0}.payment-module-switch fieldset{display:flex;gap:16px;align-items:end;flex-wrap:wrap;border:0;padding:0;margin:0;min-width:0}.payment-module-switch label{margin:0;flex:1;min-width:160px;font-size:12px}.payment-module-switch .inline-check{display:flex;flex-direction:row;gap:8px;align-items:center;min-height:44px}.payment-module-switch .inline-check input{min-height:16px;width:16px}.payment-module-switch input[type=password]{width:100%;min-height:44px}.payment-module-switch button{min-height:44px}@media(max-width:600px){.payment-module-switch{padding:16px}.payment-module-switch fieldset{display:grid}.payment-module-switch label{min-width:0}}
</style>
