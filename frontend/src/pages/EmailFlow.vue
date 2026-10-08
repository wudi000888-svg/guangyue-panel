<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { Mail, ShieldCheck, LoaderCircle } from 'lucide-vue-next';
import { useApi, invalidateSession } from '../lib/api';
import { saveEmailProof } from '../lib/email';
import { usePanelContext } from '../composables/panelContext';
import { t } from '../i18n';
import LanguageSwitcher from '../LanguageSwitcher.vue';
import '../styles/integrations.css';
const route = useRoute(), router = useRouter(), api = useApi();
const { clearSession } = usePanelContext();
const token = computed(() => typeof route.query.token === 'string' ? route.query.token : '');
const reset = computed(() => route.params.action === 'reset');
const email = ref(''), password = ref(''), confirmPassword = ref(''), busy = ref(false), error = ref(''), message = ref(''), verified = ref(false), registration = ref(false);
watch(() => route.params.action, () => { password.value = ''; confirmPassword.value = ''; error.value = ''; message.value = ''; verified.value = false; registration.value = false; });
watch(token, value => { if (value) { error.value = ''; message.value = ''; verified.value = false; } });
async function submit() {
  if (busy.value) return;
  busy.value = true; error.value = ''; message.value = '';
  try {
    if (!reset.value) {
      const v = await api<{purpose: string; email: string; registration_token?: string}>('/email/verify', 'POST', { token: token.value });
      registration.value = v.purpose === 'registration';
      if (registration.value && v.registration_token) saveEmailProof(v.email, v.registration_token);
      verified.value = true; message.value = t('邮箱验证成功');
      await router.replace({ path: '/email/verify' });
    } else if (token.value) {
      await api('/email/reset/confirm', 'POST', { token: token.value, password: password.value, confirm_password: confirmPassword.value });
      password.value = ''; confirmPassword.value = ''; invalidateSession(); clearSession();
      verified.value = true; message.value = t('密码已更新，请使用新密码登录');
      await router.replace({ path: '/email/reset' });
    } else {
      const v = await api<{message: string}>('/email/reset/request', 'POST', { email: email.value });
      message.value = t(v.message);
    }
  } catch (e) { error.value = t((e as Error).message); }
  finally { busy.value = false; }
}
</script>
<template>
  <main class="email-flow-screen"><div class="login-language"><LanguageSwitcher/></div><section class="email-flow-card">
    <img src="/design/moon-seal.svg" width="48" height="48" alt=""/><h1><Mail v-if="!verified" :size="24"/><ShieldCheck v-else :size="24"/>{{reset?t('找回密码'):t('验证邮箱')}}</h1>
    <p v-if="error" class="error" role="alert">{{error}}</p><p v-if="message" class="integration-notice" role="status">{{message}}</p>
    <form v-if="!verified" @submit.prevent="submit">
      <template v-if="reset&&token"><p>{{t('设置新密码后，所有已登录会话将退出。')}}</p><label>{{t('新密码')}}<input v-model="password" type="password" autocomplete="new-password" minlength="8" maxlength="72" required/></label><label>{{t('确认密码')}}<input v-model="confirmPassword" type="password" autocomplete="new-password" minlength="8" maxlength="72" required/></label></template>
      <template v-else-if="reset"><p>{{t('输入已验证的邮箱，我们将发送密码重置链接。')}}</p><label>{{t('邮箱地址')}}<input v-model.trim="email" type="email" autocomplete="email" maxlength="254" required/></label></template>
      <p v-else>{{t('请点击下方按钮确认验证邮箱，链接不会自动生效。')}}</p>
      <button class="primary full" :disabled="busy||(!reset&&!token)"><LoaderCircle v-if="busy" class="spin" :size="16"/>{{reset?(token?t('重置密码'):t('发送重置邮件')):t('确认验证邮箱')}}</button>
    </form>
    <RouterLink v-if="verified&&registration" class="primary email-return" :to="{path:'/overview',query:{register:'1'}}">{{t('继续注册')}}</RouterLink>
    <RouterLink v-else class="link-button email-return" to="/overview">{{t('返回面板')}}</RouterLink>
  </section></main>
</template>
