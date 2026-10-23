<script setup lang="ts">
import { defineAsyncComponent, onBeforeUnmount, provide, ref } from "vue";
import { RouterView, useRoute } from "vue-router";
import { LoaderCircle } from "lucide-vue-next";
import { usePanel } from "./composables/usePanel";
import { panelKey } from "./composables/panelContext";
import { t } from "./i18n";
import { onAccessDenied, isSiteAccessStatus, type AccessDenial, type SiteAccessStatus, errorMessage } from "./lib/api";
import AuthScreen from "./components/shell/AuthScreen.vue";
import AppShell from "./components/shell/AppShell.vue";
// Dialogs and the access-blocked screen are only needed after the shell has
// loaded (or when access is refused). Keeping them async prevents the sizeable
// admin action forms from blocking the login and first paint.
const PanelDialogs = defineAsyncComponent(() => import("./components/PanelDialogs.vue"));
const AccessBlocked = defineAsyncComponent(() => import("./components/AccessBlocked.vue"));

const panel = usePanel();
provide(panelKey, panel);
const { api, state, ready, site, mobileNav } = panel;
const route = useRoute();
// Lives here so the selected sign-in/registration form survives sign-out.
const registerMode = ref(false);
const shell = ref<InstanceType<typeof AppShell> | null>(null);
const accessDenial = ref<AccessDenial | null>(null), accessRetrying = ref(false), accessError = ref('');
const removeAccessListener = onAccessDenied(status => {
  accessDenial.value = status;
  accessError.value = '';
  shell.value?.closeOverlays();
  mobileNav.value = false;
  panel.clearSession();
});
async function retryAccess() {
  if (accessRetrying.value) return;
  accessRetrying.value = true;
  accessError.value = '';
  try {
    const status = await api<SiteAccessStatus>('/access-status');
    if (!isSiteAccessStatus(status)) throw new Error(t('服务器响应无效，请稍后重试'));
    if (status.allowed) window.location.reload();
    else accessDenial.value = { ...status, access_denied: true } as AccessDenial;
  } catch (reason) { accessError.value = errorMessage(reason); }
  finally { accessRetrying.value = false; }
}
onBeforeUnmount(removeAccessListener);
</script>
<template>
  <AccessBlocked v-if="accessDenial" :status="accessDenial" :busy="accessRetrying" :error="accessError" @retry="retryAccess"/>
  <RouterView v-else-if="route.meta.publicEmail"/>
  <div v-else-if="!ready" class="loading-screen" role="status" aria-live="polite">
    <div class="shell-loading"><img src="/design/moon-seal.svg" alt="" width="56" height="56"/><strong>{{site.panel_name}}</strong><span><LoaderCircle class="spin" :size="16"/>{{t('正在准备你的工作台')}}</span></div>
  </div>
  <AuthScreen v-else-if="!state" v-model:register-mode="registerMode"/>
  <AppShell v-else ref="shell"/>
  <!-- Editing dialogs are only part of the authenticated shell. Avoid
       fetching their sizeable chunk on the public login/access screens. -->
  <PanelDialogs v-if="!accessDenial && state" />
</template>
