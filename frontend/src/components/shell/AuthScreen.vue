<script setup lang="ts">
// Sign-in, self-registration and the registration e-mail verification step.
import { defineAsyncComponent, onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { ArrowUpRight, Eye, EyeOff, LoaderCircle, ShieldCheck } from "lucide-vue-next";
import { usePanelContext } from "../../composables/panelContext";
import { errorMessage, isCancelled } from "../../lib/api";
import { readEmailProof, clearEmailProof } from "../../lib/email";
import { t } from "../../i18n";
import LanguageSwitcher from "../LanguageSwitcher.vue";
const RegistrationCaptcha = defineAsyncComponent(() => import("../RegistrationCaptcha.vue"));

// Kept by the parent so the chosen form survives sign-out like before.
const registerMode = defineModel<boolean>("registerMode", { default: false });
const { api, busy, error, site, login, registration, registrationReset, signIn, registerAccount } = usePanelContext();
const route = useRoute();
const showPassword = ref(false), showConfirmPassword = ref(false);
const emailStatus = ref({ available: false, registration_verification: false }), sendingEmail = ref(false), emailNotice = ref("");
function restoreEmailProof() {
  const proof = readEmailProof();
  if (proof) { registration.email = proof.email; registration.email_token = proof.token; }
  if (route.query.register === "1") registerMode.value = true;
}
watch(() => route.query.register, restoreEmailProof);
watch(() => registration.email, email => {
  const proof = readEmailProof();
  if (proof?.email !== email) { registration.email_token = ""; clearEmailProof(); }
});
async function sendRegistrationEmail() {
  if (sendingEmail.value) return;
  sendingEmail.value = true; emailNotice.value = ""; error.value = "";
  try {
    const v = await api<{ message: string }>("/email/registration", "POST", { email: registration.email });
    emailNotice.value = t(v.message);
  } catch (e) { error.value = errorMessage(e); }
  finally { sendingEmail.value = false; }
}
async function emailAvailability() {
  // A concurrent expired session can cancel this public request. Retry in the
  // new session generation so account recovery remains available after logout.
  for (let attempt = 0; attempt < 3; attempt++) {
    try { emailStatus.value = await api<typeof emailStatus.value>("/email/status"); return; }
    catch (reason) { if (!isCancelled(reason) || route.meta.publicEmail) return; }
  }
}
watch(registerMode, () => { showPassword.value = false; showConfirmPassword.value = false; error.value = ""; });
onMounted(() => { restoreEmailProof(); void emailAvailability(); });
</script>
<template>
  <main class="login-screen">
    <div class="login-language"><LanguageSwitcher/></div>
    <section class="login-scene" :aria-label="t('月映珠江')">
      <div class="login-scene-copy"><span class="courtyard-kicker">GUANGYUE · MOONCOURT</span><h2>{{t('一庭月色，连接四方。')}}</h2><p>{{t('从容管理每一条连接。')}}</p></div>
      <img class="login-art" src="/design/courtyard-night.svg" alt="" width="1000" height="960" fetchpriority="high" decoding="async"/>
      <div class="login-scene-foot"><span>GUANGZHOU · 23.13° N</span><span>{{t('月映珠江')}}</span></div>
    </section>
    <div class="login-entry">
    <div class="login-brand">
      <img class="courtyard-mark" src="/design/moon-seal.svg" alt="" width="42" height="42"/><span
        >{{site.panel_name}}<small>{{site.organization}}</small></span
      >
    </div>
    <form v-if="!registerMode" class="login-form" @submit.prevent="signIn">
      <div class="eyebrow">GUANGYUE PANEL</div>
      <h1>{{ t("欢迎回来") }}</h1><p class="login-subtitle">{{t('登录后管理你的服务与连接。')}}</p>
      <p v-if="site.login_notice" class="login-notice">{{site.login_notice}}</p>
      <label
        >{{ t("账号") }}<input
          v-model="login.username"
          autocomplete="username"
          required
          autofocus
          :placeholder="t('用户名')"
          maxlength="32"
      /></label>
      <label for="login-password">{{t('密码')}}</label>
      <div class="password-field"><input id="login-password" v-model="login.password" :type="showPassword?'text':'password'" autocomplete="current-password" required :placeholder="t('密码')" maxlength="72"/><button type="button" class="icon" :aria-label="showPassword?t('隐藏密码'):t('显示密码')" :aria-pressed="showPassword" @click="showPassword=!showPassword"><EyeOff v-if="showPassword" :size="18"/><Eye v-else :size="18"/></button></div>
      <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
      <button class="primary full" :disabled="busy">
        <LoaderCircle v-if="busy" class="spin" :size="18" /><span>{{ t("登录") }}</span
        ><ArrowUpRight :size="18" />
      </button>
      <div class="login-security">
        <ShieldCheck :size="15" />{{ t("账号安全登录") }}</div>
      <button v-if="site.registration_enabled" type="button" class="link-button" @click="registerMode=true">{{t('注册账号')}}</button>
      <RouterLink v-if="emailStatus.available" class="link-button" to="/email/reset">{{t('忘记密码')}}</RouterLink>
    </form>
    <form v-else class="login-form" @submit.prevent="registerAccount">
      <div class="eyebrow">GUANGYUE PANEL</div><h1>{{t('创建账号')}}</h1>
      <p class="login-subtitle">{{t('注册后获得演示套餐，可在“我的服务”查看套餐和用量。')}}</p>
      <label>{{t('账号')}}<input v-model="registration.username" autocomplete="username" required maxlength="32" :placeholder="t('用户名')"/></label>
      <template v-if="emailStatus.available"><label>{{t('邮箱地址')}}<input v-model.trim="registration.email" type="email" autocomplete="email" maxlength="254" :required="emailStatus.registration_verification" :readonly="!!registration.email_token"/></label><p v-if="registration.email_token" class="integration-notice" role="status">{{t('邮箱已验证，继续填写注册信息。')}} <button type="button" class="link-button" @click="registration.email_token='';registration.email='';clearEmailProof()">{{t('更换邮箱')}}</button></p><div v-else class="registration-mail"><button type="button" :disabled="sendingEmail||!registration.email" @click="sendRegistrationEmail">{{sendingEmail?t('发送中…'):t('发送验证邮件')}}</button><small>{{t('打开邮件链接验证后，再继续注册。')}}</small></div><p v-if="emailNotice" role="status">{{emailNotice}}</p></template>
      <label for="register-password">{{t('密码')}}</label>
      <div class="password-field"><input id="register-password" v-model="registration.password" :type="showPassword?'text':'password'" autocomplete="new-password" required minlength="8" maxlength="72" :placeholder="t('至少 8 位密码')"/><button type="button" class="icon" :aria-label="showPassword?t('隐藏密码'):t('显示密码')" :aria-pressed="showPassword" @click="showPassword=!showPassword"><EyeOff v-if="showPassword" :size="18"/><Eye v-else :size="18"/></button></div>
      <label for="register-confirm-password">{{t('确认密码')}}</label>
      <div class="password-field"><input id="register-confirm-password" v-model="registration.confirm_password" :type="showConfirmPassword?'text':'password'" autocomplete="new-password" required minlength="8" maxlength="72"/><button type="button" class="icon" :aria-label="showConfirmPassword?t('隐藏密码'):t('显示密码')" :aria-pressed="showConfirmPassword" @click="showConfirmPassword=!showConfirmPassword"><EyeOff v-if="showConfirmPassword" :size="18"/><Eye v-else :size="18"/></button></div>
      <RegistrationCaptcha v-if="site.registration_captcha" :key="registrationReset" v-model="registration.captcha_token"/>
      <p v-if="error" class="error" role="alert">{{t(error)}}</p>
      <button class="primary full" :disabled="busy||(site.registration_captcha&&!registration.captcha_token)||((emailStatus.registration_verification||!!registration.email)&&!registration.email_token)"><LoaderCircle v-if="busy" class="spin" :size="18"/><span>{{t('立即注册')}}</span><ArrowUpRight :size="18"/></button>
      <button type="button" class="link-button" @click="registerMode=false">{{t('返回登录')}}</button>
    </form>
    <footer>{{site.panel_name}} · {{site.organization}}</footer>
    </div>
  </main>
</template>
