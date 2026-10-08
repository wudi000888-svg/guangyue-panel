<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { CreditCard, Mail, Settings2 } from 'lucide-vue-next';
import { useCommerce } from '../lib/commerce';
import { t } from '../i18n';
import PaymentSettings from './PaymentSettings.vue';
import MailSettings from './MailSettings.vue';
import '../commerce.css';
import '../styles/integrations.css';
const { read, api, busy, error, notice } = useCommerce();
const section = ref('payments'), password = ref('');
const settings = ref({ sales: false, redemption: true, tickets: true, currency: 'CNY', payment_provider: 'manual', payment_webhook_url: '', mail_provider: 'none', mail_from: '', mail_webhook_url: '' });
const tabs = [{ id: 'payments', label: '在线支付', icon: CreditCard }, { id: 'mail', label: '邮件服务', icon: Mail }, { id: 'rules', label: '账户规则', icon: Settings2 }];
function tabKey(event: KeyboardEvent, index: number) {
  if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
  event.preventDefault();
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
  section.value = tabs[next]!.id;
  document.getElementById('integration-tab-' + section.value)?.focus();
}
onMounted(async () => { const v = await read<typeof settings.value>('/settings'); if (v) settings.value = v; });
async function save() {
  busy.value = true; error.value = ''; notice.value = '';
  try { await api('/settings', 'POST', { settings: settings.value, password: password.value }); notice.value = t('账户服务设置已保存'); }
  catch (e) { error.value = t((e as Error).message); }
  finally { password.value = ''; busy.value = false; }
}
</script>
<template>
  <section class="settings-card commerce-preferences">
    <nav class="integration-service-tabs" role="tablist" :aria-label="t('交易与接入分类')"><button v-for="(tab,index) in tabs" :key="tab.id" :id="'integration-tab-'+tab.id" role="tab" :tabindex="section===tab.id?0:-1" :aria-selected="section===tab.id" :aria-controls="'integration-panel-'+tab.id" @click="section=tab.id" @keydown="tabKey($event,index)"><component :is="tab.icon" :size="17"/>{{t(tab.label)}}</button></nav>
    <div :id="'integration-panel-'+section" role="tabpanel" :aria-labelledby="'integration-tab-'+section"><PaymentSettings v-if="section==='payments'"/><MailSettings v-else-if="section==='mail'"/><form v-else class="service-integration integration-form" @submit.prevent="save"><header class="integration-heading"><div><h2>{{t('账户规则')}}</h2><p>{{t('管理兑换码充值与工单服务。')}}</p></div><RouterLink to="/shop">{{t('套餐上架')}} →</RouterLink></header><p v-if="error" class="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{notice}}</p><fieldset :disabled="busy"><label class="inline-check"><input v-model="settings.redemption" type="checkbox"/>{{t('开放兑换码充值')}}</label><label class="inline-check"><input v-model="settings.tickets" type="checkbox"/>{{t('允许成员提交工单')}}</label><p class="integration-muted">{{t('实际是否可购买由“套餐上架”页面中的上架状态决定。')}}</p><label>{{t('管理员当前密码')}}<input v-model="password" type="password" autocomplete="current-password" required/></label><button class="primary" :disabled="busy">{{t('保存账户服务设置')}}</button></fieldset></form></div>
  </section>
</template>
