<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { Mail, Send, ShieldCheck } from 'lucide-vue-next';
import { usePanelContext } from '../composables/panelContext';
import { isCancelled } from '../lib/api';
import { t } from '../i18n';
type EmailAccount = { email: string; verified: boolean; verified_at: number; pending_email: string; available: boolean };
const { api } = usePanelContext();
const account = ref<EmailAccount | null>(null), email = ref(''), password = ref(''), busy = ref(false), error = ref(''), notice = ref('');
async function load() { try { account.value = await api<EmailAccount>('/account/email'); } catch (e) { if (!isCancelled(e)) error.value = t((e as Error).message); } }
async function change(remove = false) {
  if (busy.value) return; busy.value = true; error.value = ''; notice.value = '';
  try { await api('/account/email', remove ? 'DELETE' : 'POST', { ...(remove ? {} : { email: email.value }), current_password: password.value }); notice.value = t(remove ? '邮箱已解除绑定' : '验证邮件已进入发送队列，请在邮件中确认绑定。'); email.value = ''; await load(); }
  catch (e) { if (!isCancelled(e)) error.value = t((e as Error).message); }
  finally { busy.value = false; password.value = ''; }
}
onMounted(() => { void load(); window.addEventListener('focus', load); });
onUnmounted(() => { window.removeEventListener('focus', load); password.value = ''; });
</script>
<template>
  <section class="email-account"><h3><Mail :size="18"/>{{t('账户邮箱')}}</h3><p v-if="error" class="error" role="alert">{{error}}</p><p v-if="notice" class="integration-notice" role="status">{{notice}}</p><template v-if="account"><p v-if="account.verified"><ShieldCheck :size="15"/> {{account.email}} · {{t('已验证')}}</p><p v-else>{{t('尚未绑定已验证邮箱')}}</p><p v-if="account.pending_email">{{t('等待验证')}}：{{account.pending_email}}</p><p v-if="!account.available">{{t('站点暂未开启邮件服务，请联系管理员。')}}</p><form v-if="account.available||account.verified" @submit.prevent="change()"><label v-if="account.available">{{account.verified?t('新邮箱'):t('邮箱地址')}}<input v-model.trim="email" type="email" required maxlength="254" autocomplete="email" placeholder="you@example.com"/></label><label>{{t('当前密码')}}<input v-model="password" type="password" autocomplete="current-password" required/></label><div class="integration-actions"><button v-if="account.available" class="primary" :disabled="busy"><Send :size="16"/>{{t('发送绑定验证邮件')}}</button><button v-if="account.verified" type="button" :disabled="busy||!password" @click="change(true)">{{t('解除邮箱绑定')}}</button></div></form><p>{{t('邮箱用于找回密码和接收订单通知，须点击验证邮件后生效。')}}</p></template></section>
</template>
