<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import { Mail, Send, ShieldCheck, RefreshCw, LoaderCircle } from 'lucide-vue-next';
import { usePanelContext } from '../composables/panelContext';
import { serialPoll } from '../lib/requests';
import { stamp } from '../lib/commerce';
import { isCancelled } from '../lib/api';
import { t } from '../i18n';
type MailConfig = { enabled: boolean; host: string; port: number; tls_mode: 'tls' | 'starttls'; username: string; from_email: string; from_name: string; has_password: boolean; registration_verification: boolean; order_notifications: boolean; version: number };
type Delivery = { id: string; to: string; kind: string; state: string; attempts: number; next_attempt: number; last_error: string; created: number; sent_at: number };
const { api } = usePanelContext();
const form = ref<MailConfig | null>(null), smtpPassword = ref(''), adminPassword = ref(''), recipient = ref('');
const deliveries = ref<Delivery[]>([]), error = ref(''), notice = ref(''), busy = ref(false), loading = ref(true);
let disposed = false;
function fail(e: unknown) { if (!isCancelled(e) && !disposed) error.value = t(e instanceof Error ? e.message : '请求失败'); }
async function history() { try { const v = await api<{ items: Delivery[] }>('/email/deliveries'); if (!disposed) deliveries.value = v.items; } catch (e) { fail(e); } }
const poll = serialPoll(history, () => 5000);
async function load() { loading.value = true; error.value = ''; try { const v = await api<MailConfig>('/email/settings'); if (!disposed) form.value = v; await history(); } catch (e) { fail(e); } finally { if (!disposed) loading.value = false; } }
async function save() {
  if (!form.value || busy.value) return; busy.value = true; error.value = ''; notice.value = '';
  try { const v = await api<MailConfig>('/email/settings', 'PUT', { ...form.value, has_password: undefined, password: smtpPassword.value, admin_password: adminPassword.value }); if (!disposed) { form.value = v; smtpPassword.value = ''; notice.value = t('邮件服务已保存'); } }
  catch (e) { fail(e); } finally { adminPassword.value = ''; busy.value = false; }
}
async function sendTest() {
  if (!recipient.value || busy.value) return; busy.value = true; error.value = ''; notice.value = '';
  try { await api('/email/test', 'POST', { to: recipient.value, admin_password: adminPassword.value }); notice.value = t('测试邮件已进入发送队列，请查看投递状态并检查收件箱。'); await history(); }
  catch (e) { fail(e); } finally { adminPassword.value = ''; busy.value = false; }
}
async function retry(delivery: Delivery) {
  busy.value = true; error.value = ''; notice.value = '';
  try { await api('/email/deliveries/retry', 'POST', { id: delivery.id, admin_password: adminPassword.value }); notice.value = t('邮件已重新排队'); await history(); }
  catch (e) { fail(e); } finally { adminPassword.value = ''; busy.value = false; }
}
watch(() => form.value?.enabled, enabled => { if (!enabled && form.value) form.value.registration_verification = false; });
function changeTLS() { if (form.value) form.value.port = form.value.tls_mode === 'tls' ? 465 : 587; }
const deliveryLabels: Record<string, string> = { pending: '待发送', sending: '发送中', retry: '等待重试', sent: '已发送', failed: '发送失败', expired: '已过期', cancelled: '已取消' };
const kindLabels: Record<string,string> = { registration:'注册验证',bind:'邮箱绑定',reset:'密码找回',test:'测试邮件',commerce:'订单通知' };
onMounted(async () => { await load(); if (!disposed) poll.start(); });
onUnmounted(() => { disposed = true; poll.stop(); smtpPassword.value = ''; adminPassword.value = ''; });
</script>
<template>
  <section class="service-integration">
    <header class="integration-heading"><div><h2><Mail :size="20"/>{{t('邮件服务')}}</h2><p>{{t('连接 SMTP 邮箱，用于邮箱验证、找回密码和订单通知。')}}</p></div><button :disabled="loading||busy" @click="load"><RefreshCw :size="16"/>{{t('重新加载')}}</button></header>
    <p v-if="error" class="error" role="alert">{{error}}</p><p v-if="notice" class="integration-notice" role="status">{{notice}}</p>
    <p v-if="loading&&!form" role="status">{{t('加载中…')}}</p>
    <form v-if="form" class="integration-form" @submit.prevent="save"><fieldset :disabled="busy||loading"><label class="inline-check"><input v-model="form.enabled" type="checkbox"/>{{t('启用邮件发送')}}</label><div class="integration-grid">
      <label>{{t('SMTP 服务器')}}<input v-model.trim="form.host" :required="form.enabled" maxlength="253" placeholder="smtp.example.com"/></label><label>{{t('连接加密')}}<select v-model="form.tls_mode" @change="changeTLS"><option value="tls">{{t('TLS 加密连接（通常 465）')}}</option><option value="starttls">{{t('STARTTLS 升级加密（通常 587）')}}</option></select></label><label>{{t('SMTP 端口')}}<input v-model.number="form.port" type="number" min="1" max="65535" :required="form.enabled"/></label><label>{{t('SMTP 用户名')}}<input v-model.trim="form.username" :required="form.enabled" maxlength="254" autocomplete="off"/></label><label>{{t('SMTP 密码或授权码')}}<input v-model="smtpPassword" type="password" autocomplete="new-password" :required="form.enabled&&!form.has_password" :placeholder="form.has_password?t('留空保持原密钥'):t('请输入邮箱授权码')"/></label><label>{{t('发件人邮箱')}}<input v-model.trim="form.from_email" type="email" :required="form.enabled" maxlength="254" placeholder="noreply@example.com"/></label><label>{{t('发件人名称')}}<input v-model.trim="form.from_name" maxlength="100" :placeholder="t('例如：广月面板')"/></label></div>
      <div class="integration-explanation"><ShieldCheck :size="19"/><p>{{t('只使用验证证书的加密连接。请使用邮箱授权码，并在邮件服务商完成发件域名的 SPF、DKIM 和 DMARC 配置。')}}</p></div>
      <label class="inline-check"><input v-model="form.registration_verification" type="checkbox" :disabled="!form.enabled"/>{{t('注册时必须验证邮箱')}}</label><label class="inline-check"><input v-model="form.order_notifications" type="checkbox" :disabled="!form.enabled"/>{{t('发送订单和退款通知')}}</label><p class="integration-muted">{{t('注册邮箱验证需要先启用完整的邮件配置；订单通知发送到成员已验证的邮箱。')}}</p>
      <label>{{t('管理员当前密码')}}<input v-model="adminPassword" type="password" autocomplete="current-password" required/></label><button class="primary" :disabled="busy||loading"><LoaderCircle v-if="busy" :size="16" class="spin"/>{{t('保存邮件服务')}}</button>
    </fieldset></form>
    <form v-if="form" class="integration-form" @submit.prevent="sendTest"><h3>{{t('发送测试邮件')}}</h3><fieldset :disabled="busy||!form.enabled"><label>{{t('测试收件人')}}<input v-model.trim="recipient" type="email" maxlength="254" required placeholder="you@example.com"/></label><label>{{t('管理员当前密码')}}<input v-model="adminPassword" type="password" autocomplete="current-password" required/></label><button class="primary" :disabled="busy"><Send :size="16"/>{{t('发送测试邮件')}}</button></fieldset></form>
    <details class="integration-review" open><summary>{{t('邮件投递记录')}} <span>{{deliveries.length}}</span></summary><p class="integration-muted">{{t('队列会自动重试临时失败；已发送表示 SMTP 服务商已接受，请同时检查收件箱或垃圾邮件。')}}</p><div class="email-delivery-scroll"><table v-if="deliveries.length" class="email-delivery-table"><thead><tr><th>{{t('收件人')}}</th><th>{{t('类型')}}</th><th>{{t('投递状态')}}</th><th>{{t('时间与错误')}}</th><th>{{t('操作')}}</th></tr></thead><tbody><tr v-for="item in deliveries" :key="item.id"><td>{{item.to}}</td><td>{{t(kindLabels[item.kind]||item.kind)}}</td><td>{{t(deliveryLabels[item.state]||item.state)}}<br/>{{t('尝试次数')}} {{item.attempts}}</td><td>{{stamp(item.sent_at||item.created)}}<p v-if="item.last_error">{{item.last_error}}</p><small v-if="item.next_attempt&&!item.sent_at">{{t('下次尝试')}} {{stamp(item.next_attempt)}}</small></td><td><button v-if="['failed','retry'].includes(item.state)" :disabled="busy||!adminPassword" @click="retry(item)">{{t('重试发送')}}</button></td></tr></tbody></table><p v-else class="integration-empty">{{t('暂无邮件投递记录')}}</p></div><label v-if="deliveries.some(item=>['failed','retry'].includes(item.state))" class="integration-password">{{t('管理员当前密码')}}<input v-model="adminPassword" type="password" autocomplete="current-password" :placeholder="t('验证密码后可重试失败的投递')"/></label></details>
  </section>
</template>
