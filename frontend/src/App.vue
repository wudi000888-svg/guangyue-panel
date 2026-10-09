<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onBeforeUnmount, provide, ref, watch } from "vue";
import { RouterView, useRoute } from "vue-router";
import { usePanel } from "./composables/usePanel";
import { panelKey } from "./composables/panelContext";
// Dialogs, updater and registration captcha are only needed after the shell has
// loaded (or when the user opens registration). Keeping them async prevents
// the sizeable admin action forms from blocking the login and first paint.
const PanelDialogs = defineAsyncComponent(() => import("./components/PanelDialogs.vue"));
const PanelUpdater = defineAsyncComponent(() => import("./components/PanelUpdater.vue"));
import HeaderBalance from "./components/HeaderBalance.vue";
import { Bell, ArrowUpRight, ArrowLeft, ChevronRight, Eye, EyeOff, KeyRound, LoaderCircle, LogOut, Menu, Moon, Sun, PanelLeftClose, PanelLeftOpen, Building2, RefreshCw, Search, ShieldCheck, SlidersHorizontal, X, GripVertical, Grid2X2, FolderInput } from "lucide-vue-next";
import { t } from "./i18n";
import { groupNavigation, searchNavigation, sidebarNavigation, MEMBER_NAVIGATION_LABELS } from "./lib/navigation";
import { allowedRoute } from "./lib/access";
import { router } from "./router";
import LanguageSwitcher from "./LanguageSwitcher.vue";
const RegistrationCaptcha = defineAsyncComponent(() => import("./components/RegistrationCaptcha.vue"));
import AccessBlocked from "./components/AccessBlocked.vue";
import { onAccessDenied, isSiteAccessStatus, isCancelled, type AccessDenial, type SiteAccessStatus } from "./lib/api";
import { readEmailProof, clearEmailProof } from './lib/email';

const panel = usePanel();
const emailRoute = useRoute();
const { sidebarCustomization } = panel;
provide(panelKey, panel);
const { selectedSite, selectedSiteName, switchSite, api, state, ready, busy, error, page, mobileNav, site, login, registration, registrationReset, modal, theme, sideCollapsed, toggleTheme, confirmation, owner, pendingHY, pageTitle, pageDescriptions, nav, publicFeaturesEnabled, refresh, task, signIn, signOut, registerAccount, go } = panel;
const isDesktop = ref(matchMedia('(min-width: 901px)').matches);
const registerMode = ref(false), showPassword = ref(false), showConfirmPassword = ref(false), refreshing = ref(false);
const emailStatus = ref({ available: false, registration_verification: false }), sendingEmail = ref(false), emailNotice = ref('');
function restoreEmailProof() {
  const proof = readEmailProof();
  if (proof) { registration.email = proof.email; registration.email_token = proof.token; }
  if (emailRoute.query.register === '1') registerMode.value = true;
}
watch(() => emailRoute.query.register, restoreEmailProof);
watch(() => registration.email, email => {
  const proof = readEmailProof();
  if (proof?.email !== email) { registration.email_token = ''; clearEmailProof(); }
});
async function sendRegistrationEmail() {
  if (sendingEmail.value) return;
  sendingEmail.value = true; emailNotice.value = ''; error.value = '';
  try {
    const v = await api<{message:string}>('/email/registration', 'POST', { email: registration.email });
    emailNotice.value = t(v.message);
  } catch (e) { error.value = t((e as Error).message); }
  finally { sendingEmail.value = false; }
}
async function emailAvailability() {
  // A concurrent expired session can cancel this public request. Retry in the
  // new session generation so account recovery remains available after logout.
  for (let attempt = 0; attempt < 3; attempt++) {
    try { emailStatus.value = await api('/email/status'); return; }
    catch (reason) { if (!isCancelled(reason) || emailRoute.meta.publicEmail) return; }
  }
}
watch([ready, () => !!state.value, () => !!emailRoute.meta.publicEmail], () => {
  if (ready.value && !emailRoute.meta.publicEmail) void emailAvailability();
});
const mobileTools = ref(false), drawer = ref<HTMLElement | null>(null), toolsDialog = ref<HTMLElement | null>(null);
const commandOpen = ref(false), commandQuery = ref(''), commandIndex = ref(0);
const commandDialog = ref<HTMLElement | null>(null), commandInput = ref<HTMLInputElement | null>(null), pageContent = ref<HTMLElement | null>(null);
const accessDenial = ref<AccessDenial | null>(null), accessRetrying = ref(false), accessError = ref('');
const removeAccessListener = onAccessDenied(status => {
  accessDenial.value = status;
  accessError.value = '';
  closeOverlays();
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
  } catch (reason) { accessError.value = reason instanceof Error ? reason.message : t('请求失败'); }
  finally { accessRetrying.value = false; }
}
const permittedNav = computed(() => nav.value.filter(item => allowedRoute(router.resolve('/' + item.id).meta, {
  role: state.value?.me.role || null, edition: state.value?.system.edition || 'lite', publicFeatures: publicFeaturesEnabled.value,
})).map(item => ({ ...item, label: !owner.value && MEMBER_NAVIGATION_LABELS[item.id] ? t(MEMBER_NAVIGATION_LABELS[item.id]!) : item.label })));
const visibleNav = computed(() => sidebarNavigation(permittedNav.value, sidebarCustomization.selected(owner.value ? 'admin' : 'member'), owner.value));
const allShellGroups = computed(() => groupNavigation(permittedNav.value, owner.value).map(group => ({ ...group, label: t(group.label) })));
const shellTitle = computed(() => permittedNav.value.find(item => item.id === page.value)?.label || pageTitle.value);
const shellGroups = computed(() => groupNavigation(visibleNav.value, owner.value).map(group => ({ ...group, label: t(group.label) })));
const shellGroup = computed(() => allShellGroups.value.find(group => group.items.some(item => item.id === page.value))?.label || '');
const searchEntries = computed(() => allShellGroups.value.flatMap(group => group.items.map(item => ({ ...item, group: group.label, description: item.id === 'shop' && owner.value ? t('管理套餐上架与销售规则') : t(pageDescriptions[item.id] || '') }))));
const commandResults = computed(() => searchNavigation(searchEntries.value, commandQuery.value));
const quickNav = computed(() => [...visibleNav.value.filter(item => item.id !== 'settings').slice(0, 4), ...visibleNav.value.filter(item => item.id === 'settings')]);
const dragPreviewStyle = computed(() => {
  const position = sidebarCustomization.pointerPosition.value;
  return position ? { left: Math.max(8, Math.min(position.x + 14, window.innerWidth - 250)) + 'px', top: Math.max(8, Math.min(position.y + 14, window.innerHeight - 65)) + 'px' } : {};
});
const mobileLayer = computed(() => mobileNav.value || mobileTools.value);
const overlayOpen = computed(() => mobileLayer.value || commandOpen.value);
const roleLabel = computed(() => t(state.value?.system.role === 'controller' || !state.value?.system.role && state.value?.system.edition === 'pro' ? '主站' : state.value?.system.role === 'business' ? '子站' : '独立站'));
const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ K' : 'Ctrl K';
let oldOverflow = '', oldFocus: HTMLElement | null = null, locked = false;
const closeMobile = () => { mobileNav.value = false; mobileTools.value = false; };
const closeOverlays = () => { closeMobile(); commandOpen.value = false; };
function openSearch() {
  if (!state.value || modal.value || confirmation.value || document.getElementById('app')?.inert) return;
  const activeDialog = document.querySelector('[role="dialog"][aria-modal="true"], [role="alertdialog"][aria-modal="true"]');
  if (activeDialog && !mobileNav.value && !mobileTools.value) return;
  commandQuery.value = '';
  commandOpen.value = true;
  closeMobile();
}
function navigate(id: string) {
  go(id);
  closeOverlays();
  void nextTick(() => pageContent.value?.focus({ preventScroll: true }));
}
function openDirectory() {
  sidebarCustomization.directoryRequest.value++;
  navigate('settings');
}
async function unpinSidebar(event: MouseEvent, id: string) {
  const focused = document.activeElement;
  const links = Array.from(drawer.value?.querySelectorAll<HTMLButtonElement>('.nav-route') || []);
  const entry = (event.currentTarget as HTMLElement).closest('.nav-entry');
  const position = links.findIndex(link => entry?.contains(link));
  sidebarCustomization.setPinned(id, false);
  await nextTick();
  if (event.detail === 0 && focused && !focused.isConnected) {
    const remaining = Array.from(drawer.value?.querySelectorAll<HTMLButtonElement>('.nav-route') || []);
    remaining[Math.min(Math.max(0, position), remaining.length - 1)]?.focus();
  }
}
async function refreshPanel() {
  if (refreshing.value) return;
  refreshing.value = true;
  try { await refresh(); }
  catch (reason) { error.value = reason instanceof Error ? reason.message : String(reason); }
  finally { refreshing.value = false; }
}
watch(registerMode, () => { showPassword.value = false; showConfirmPassword.value = false; error.value = ''; });
watch(overlayOpen, active => {
  if (active && !locked) { oldFocus = document.activeElement as HTMLElement; oldOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; locked = true; }
  if (!active && locked) { document.body.style.overflow = oldOverflow; locked = false; }
}, { flush: 'sync' });
watch(overlayOpen, active => {
  if (!active && oldFocus?.isConnected && oldFocus.getClientRects().length && !oldFocus.closest('[inert]')) oldFocus.focus();
}, { flush: 'post' });
watch([mobileNav, mobileTools, commandOpen], async () => {
  await nextTick();
  const target = commandOpen.value ? commandInput.value : mobileTools.value ? toolsDialog.value : mobileNav.value ? drawer.value : null;
  target?.focus();
});
watch([page, state, modal, confirmation], () => { if (!state.value || modal.value || confirmation.value) closeOverlays(); });
watch(page, closeOverlays);
watch(commandQuery, () => { commandIndex.value = 0; });
watch(() => commandResults.value.map(item => item.id).join('|'), () => { commandIndex.value = Math.min(commandIndex.value, Math.max(0, commandResults.value.length - 1)); });
watch(commandIndex, async () => { await nextTick(); document.getElementById(`navigation-result-${commandIndex.value}`)?.scrollIntoView({ block: 'nearest' }); });
function shellKeys(e: KeyboardEvent) {
  if (modal.value || confirmation.value || document.getElementById('app')?.inert) return;
  if (e.key === 'Escape' && sidebarCustomization.dragging.value) { e.preventDefault(); sidebarCustomization.endDrag(); return; }
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k' && state.value) { e.preventDefault(); if (commandOpen.value) commandOpen.value = false; else openSearch(); return; }
  if (!overlayOpen.value) return;
  if (e.key === 'Escape') { e.preventDefault(); closeOverlays(); return; }
  if (commandOpen.value && ['ArrowDown', 'ArrowUp', 'Enter'].includes(e.key) && document.activeElement === commandInput.value) {
    e.preventDefault();
    if (e.key === 'Enter') { const item = commandResults.value[commandIndex.value]; if (item) navigate(item.id); }
    else if (commandResults.value.length) commandIndex.value = (commandIndex.value + (e.key === 'ArrowDown' ? 1 : -1) + commandResults.value.length) % commandResults.value.length;
    return;
  }
  if (e.key !== 'Tab') return;
  const box = commandOpen.value ? commandDialog.value : mobileTools.value ? toolsDialog.value : drawer.value;
  const items = Array.from(box?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),[tabindex="0"]') || []).filter(el => el.tabIndex >= 0 && el.getClientRects().length && getComputedStyle(el).visibility !== 'hidden');
  const first = items[0], last = items.at(-1), active = document.activeElement;
  if (!first) { e.preventDefault(); box?.focus(); }
  else if (e.shiftKey && (active === first || active === box || !box?.contains(active))) { e.preventDefault(); last?.focus(); }
  else if (!e.shiftKey && (active === last || !box?.contains(active))) { e.preventDefault(); first.focus(); }
}
let desktop: MediaQueryList;
function onDesktop() { isDesktop.value = desktop.matches; if (desktop.matches) closeMobile(); }
onMounted(() => { desktop = matchMedia('(min-width: 901px)'); desktop.addEventListener('change', onDesktop); document.addEventListener('keydown', shellKeys); restoreEmailProof(); });
onBeforeUnmount(() => { removeAccessListener(); closeOverlays(); desktop?.removeEventListener('change', onDesktop); document.removeEventListener('keydown', shellKeys); });
</script>
<template>

  <AccessBlocked v-if="accessDenial" :status="accessDenial" :busy="accessRetrying" :error="accessError" @retry="retryAccess"/>
  <RouterView v-else-if="emailRoute.meta.publicEmail"/>
  <div v-else-if="!ready" class="loading-screen" role="status" aria-live="polite">
    <div class="shell-loading"><img src="/design/moon-seal.svg" alt="" width="56" height="56"/><strong>{{site.panel_name}}</strong><span><LoaderCircle class="spin" :size="16"/>{{t('正在准备你的工作台')}}</span></div>
  </div>
  <main v-else-if="!state" class="login-screen">
    <div class="login-language"><LanguageSwitcher/></div>
    <section class="login-scene" :aria-label="t('月映珠江')">
      <div class="login-scene-copy"><span class="courtyard-kicker">GUANGYUE · MOONCOURT</span><h2>{{t('一庭月色，连接四方。')}}</h2><p>{{t('从容管理每一条连接。')}}</p></div>
      <img class="login-art" src="/design/courtyard-night.svg" alt="" width="1000" height="960"/>
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
  <div
    v-else
    :class="['app-shell', { collapsed: sideCollapsed && !sidebarCustomization.error.value }]"
    :inert="!!modal || !!confirmation || commandOpen"
  >
    <a class="skip-link" href="#main-content" @click.prevent="pageContent?.focus()">{{t('跳到主要内容')}}</a>
    <div v-if="mobileNav" class="nav-shade" @click="mobileNav = false"></div>
    <aside id="mobile-navigation" ref="drawer" tabindex="-1" :class="['sidebar', { open: mobileNav }]" :role="mobileNav ? 'dialog' : undefined" :aria-modal="mobileNav ? true : undefined" :aria-label="t('主导航')" :inert="mobileTools || (!isDesktop && !mobileNav)">
      <button class="icon drawer-close" :aria-label="t('关闭导航')" @click="mobileNav=false"><X :size="20"/></button>
      <a
        class="brand"
        href="#"
        :aria-label="site.panel_name + ' · ' + t('仪表盘')"
        @click.prevent="navigate(owner?'overview':'subscription')"
        ><span class="brand-icon"><img src="/design/moon-seal.svg" alt="" width="36" height="36"/></span
        ><span class="brand-name"
          >{{site.panel_name}}<small
            >{{ owner ? t('运营工作台') : t('个人服务中心') }}<template v-if="!owner || selectedSite"> · v{{ state.system.version.split("-")[0] }}</template></small
          ></span
        ></a
      >

      <div class="workspace" :class="{ 'remote-workspace': selectedSite }">
        <span class="workspace-symbol"><Building2 :size="18"/></span>
        <div><strong>{{selectedSite?selectedSiteName:site.organization}}</strong><small>{{selectedSite?t('正在管理子站'):roleLabel}} · {{(state.system.edition||'lite').toUpperCase()}}</small></div>
        <button v-if="selectedSite" class="icon" :aria-label="t('返回本站')" :title="t('返回本站')" @click="switchSite('')"><ArrowLeft :size="17"/></button>
        <ShieldCheck v-else :size="16"/>
      </div>
      <button class="navigation-search" :title="t('搜索功能')+' · '+shortcut" :aria-label="t('搜索功能')" aria-haspopup="dialog" @click="openSearch"><Search :size="17"/><span>{{t('搜索功能')}}</span><kbd>{{shortcut}}</kbd></button>
      <nav :aria-label="t('主导航')" data-sidebar-drop-target="sidebar" :class="['grouped-nav', {'sidebar-drop-active':sidebarCustomization.target.value==='sidebar'}]" @dragover="sidebarCustomization.acceptDrag($event,'sidebar')" @drop="sidebarCustomization.drop($event,'sidebar')">
        <p v-if="owner&&sidebarCustomization.dragging.value?.source==='directory'" class="sidebar-drop-hint" role="status">{{t('拖到这里，立即放入侧边栏')}}</p>
        <div v-for="group in shellGroups" :key="group.id" :class="['nav-group','nav-group-'+group.id]">
          <div class="nav-caption">{{group.label}}</div>
          <div v-for="item in group.items" :key="item.id" :class="['nav-entry',{selected:page===item.id,draggable:owner&&item.id!=='settings','nav-entry-dragging':sidebarCustomization.dragging.value?.id===item.id}]" :draggable="owner&&item.id!=='settings'" :data-navigation-id="item.id" @dragstart="sidebarCustomization.startDrag($event,item.id,'sidebar')" @dragend="sidebarCustomization.endDrag()">
            <GripVertical v-if="owner&&item.id!=='settings'" class="nav-drag-grip" :size="13" aria-hidden="true" @pointerdown="sidebarCustomization.startPointer($event,item.id,'sidebar')"/>
            <button class="nav-route" :class="{selected:page===item.id}" :title="item.label" :aria-label="item.label" :aria-current="page===item.id?'page':undefined" @click="navigate(item.id)">
              <component :is="item.icon" :size="18"/><span>{{item.label}}</span>
              <span v-if="item.id==='messages' && state.unread_messages" class="nav-count unread-count">{{state.unread_messages>99?'99+':state.unread_messages}}</span>
            </button>
            <button v-if="owner&&item.id!=='settings'" class="nav-unpin" :title="t('移到更多功能')+' · '+item.label" :aria-label="t('移到更多功能')+' · '+item.label" @click="unpinSidebar($event,item.id)"><FolderInput :size="15"/></button>
          </div>
        </div>
      </nav>
      <button v-if="owner" :aria-label="t('更多功能')" :title="t('更多功能')" data-sidebar-drop-target="directory" :class="['sidebar-setting','sidebar-directory',{'directory-drop-active':sidebarCustomization.target.value==='directory'}]" @click="openDirectory" @dragover="sidebarCustomization.acceptDrag($event,'directory')" @drop="sidebarCustomization.drop($event,'directory')"><Grid2X2 :size="17"/><span>{{sidebarCustomization.dragging.value?.source==='sidebar'?t('拖到这里，移至更多功能'):t('更多功能')}}</span></button>
      <div v-if="owner&&(sidebarCustomization.saving.value||sidebarCustomization.error.value||sidebarCustomization.saved.value)" class="sidebar-save-status" :title="sidebarCustomization.saving.value?t('正在自动保存侧边栏…'):sidebarCustomization.error.value||t('侧边栏已自动保存')" :role="sidebarCustomization.error.value?'alert':'status'" aria-live="polite"><LoaderCircle v-if="sidebarCustomization.saving.value" :size="15" class="spin" aria-hidden="true"/><ShieldCheck v-else-if="!sidebarCustomization.error.value" :size="15" aria-hidden="true"/><span v-if="sidebarCustomization.saving.value">{{t('正在自动保存侧边栏…')}}</span><template v-else-if="sidebarCustomization.error.value"><span>{{sidebarCustomization.error.value}}</span><button @click="sidebarCustomization.retry()">{{t('重试保存')}}</button></template><span v-else>{{t('侧边栏已自动保存')}}</span></div>
      <div class="sidebar-bottom">
        <div class="sidebar-version"><span>{{t('版本与更新')}}</span><PanelUpdater v-if="owner && !selectedSite" :version="state.system.version.split('-')[0]" @open="closeOverlays"/><small v-else>v{{state.system.version.split('-')[0]}}</small></div>
        <button class="sidebar-setting mobile-account" :title="t('账户与密码')" @click="modal='password';mobileNav=false"><KeyRound :size="18"/><span>{{t('账户与密码')}}</span></button>
        <div class="edge-status">
          <span
            :class="['dot', { warning: state.system.status !== 'applied' }]"
          />{{
            state.system.status === "applied"
              ? t("配置已同步")
              : state.system.status === "pending"
                ? t("等待同步")
                : t("同步异常")
          }}<span>{{roleLabel}}</span>
        </div>
        <button
          class="sidebar-setting theme-control"
          :title="theme === 'dark' ? t('切换浅色模式') : t('切换深色模式')"
          @click="toggleTheme"
        >
          <Sun v-if="theme === 'dark'" :size="18" /><Moon
            v-else
            :size="18"
          /><span>{{ theme === "dark" ? t("浅色模式") : t("深色模式") }}</span>
        </button>
        <button
          class="sidebar-setting collapse-control"
          :title="sideCollapsed ? t('展开导航') : t('收起导航')"
          @click="sideCollapsed = !sideCollapsed"
        >
          <PanelLeftOpen v-if="sideCollapsed" :size="18" /><PanelLeftClose
            v-else
            :size="18"
          /><span>{{ sideCollapsed ? t("展开导航") : t("收起导航") }}</span>
        </button>
      </div>
    </aside>
    <div class="main-area" :inert="mobileLayer">
      <header class="topbar">
        <button
          class="icon mobile-menu"
          :title="t('打开导航')" :aria-label="t('打开导航')" :aria-expanded="mobileNav" aria-controls="mobile-navigation"
          @click="mobileNav = !mobileNav"
        >
          <Menu :size="21" />
        </button>
        <div class="page-context">
          <p class="page-breadcrumb">
            <span class="breadcrumb-group">{{shellGroup}} <ChevronRight :size="13"/></span>{{ shellTitle }}
          </p>

        </div>
        <HeaderBalance v-if="!selectedSite && isDesktop" :key="state.me.id" :user-id="state.me.id"/>
        <div class="top-actions">
 <button v-if="selectedSite" class="site-switch desktop-action" @click="switchSite('')">{{selectedSiteName}} · {{t('返回本站')}}</button>
 <span v-else class="edition-badge desktop-action">{{state.system.edition==='pro'?'PRO':'LITE'}} · {{t(state.system.role==='controller'||!state.system.role&&state.system.edition==='pro'?'主站':state.system.role==='business'?'子站':'独立站')}}</span>
          <LanguageSwitcher/>
          <button class="icon inbox-bell" :title="t('站内信')" :aria-label="t('站内信')" @click="go('messages')"><Bell :size="18"/><span v-if="state.unread_messages" class="bell-count">{{state.unread_messages>99?'99+':state.unread_messages}}</span></button>
          <button class="icon desktop-action" :title="t('搜索功能')+' · '+shortcut" :aria-label="t('搜索功能')" @click="openSearch"><Search :size="18"/></button>
          <button class="icon desktop-action" :title="t('刷新')" :aria-label="t('刷新')" :disabled="refreshing" :aria-busy="refreshing" @click="refreshPanel()">
            <RefreshCw :size="17" :class="{spin:refreshing}"/></button
          ><button
            class="icon header-theme"
            :title="theme === 'dark' ? t('切换浅色模式') : t('切换深色模式')"
            @click="toggleTheme"
          >
            <Sun v-if="theme === 'dark'" :size="18" /><Moon
              v-else
              :size="18"
            /></button
          ><button
            class="header-account"
            :title="t('账户与密码')"
            @click="modal = 'password'"
          >
            <span class="avatar small">{{
              state.me.username.slice(0, 1).toUpperCase()
            }}</span
            ><span
              >{{ state.me.username
              }}<small>{{ owner ? t("管理员") : t("成员") }}</small></span
            ></button
          ><button class="icon desktop-action" :title="t('退出登录')" @click="signOut">
            <LogOut :size="17" />
          </button>
          <button class="icon mobile-tools-button" :aria-label="t('显示与账户')" :title="t('显示与账户')" aria-controls="mobile-tools" :aria-expanded="mobileTools" @click="mobileTools=true"><SlidersHorizontal :size="20"/></button>
        </div>
      </header>
      <div
        v-if="error && !modal && !confirmation"
        class="page-error"
        role="alert"
      >
        {{ t(error)
        }}<button class="icon" :title="t('关闭提示')" @click="error = ''">
          <X :size="16" />
        </button>
      </div>
      <div
        v-if="state.system.status === 'error'"
        class="sync-alert"
        role="alert"
      >{{ t("协议同步异常：") }}{{ state.system.error
        }}<button
          v-if="owner"
          @click="
            task(async () => {
              await api('/apply', 'POST', {});
              await refresh();
            })
          "
        >{{ t("重试") }}</button>
      </div>
      <div v-if="pendingHY" class="sync-alert" role="status">{{ t("正在关闭") }}{{ pendingHY }}{{ t("个旧 HY2 连接") }}</div>
      <main id="main-content" ref="pageContent" class="content" tabindex="-1" :aria-label="shellTitle">
        <div v-if="selectedSite" class="remote-context" role="status"><Building2 :size="17"/><div><strong>{{selectedSiteName}}</strong><span>{{t('当前操作将应用于此子站')}}</span></div><button @click="switchSite('')"><ArrowLeft :size="15"/>{{t('返回本站')}}</button></div>
        <RouterView v-slot="{ Component, route }">
          <Transition name="page" mode="out-in">
            <component :is="Component" :key="route.path" />
          </Transition>
        </RouterView>
        <footer class="page-footer">
          <span>{{site.panel_name}} · {{site.organization}}</span
          ><span>v{{ state.system.version.split("-")[0] }}</span>
        </footer>
      </main>
      <nav class="mobile-quick-nav" :aria-label="t('常用导航')">
        <button v-for="item in quickNav" :key="item.id" :aria-current="page===item.id?'page':undefined" :class="{selected:page===item.id}" @click="navigate(item.id)"><component :is="item.icon" :size="20"/><span>{{item.label}}</span></button>
      </nav>
    </div>
  </div>
  <Teleport to="body">
    <div v-if="sidebarCustomization.dragging.value&&sidebarCustomization.pointerPosition.value" class="sidebar-drag-preview" aria-hidden="true" :style="dragPreviewStyle"><GripVertical :size="15"/>{{permittedNav.find(item=>item.id===sidebarCustomization.dragging.value?.id)?.label}}</div>
    <div v-if="owner&&!isDesktop&&sidebarCustomization.dragging.value?.source==='directory'" data-sidebar-drop-target="sidebar" :class="['sidebar-mobile-drop',{'active':sidebarCustomization.target.value==='sidebar'}]" role="status"><Grid2X2 :size="22"/>{{t('拖到这里，立即放入侧边栏')}}</div>
    <div v-if="commandOpen && state" class="navigation-shade" @click.self="commandOpen=false">
      <section ref="commandDialog" class="navigation-palette" role="dialog" aria-modal="true" :aria-label="t('搜索功能')" tabindex="-1">
        <div class="navigation-palette-input"><Search :size="21"/><input ref="commandInput" v-model="commandQuery" :placeholder="t('搜索页面、节点、套餐…')" :aria-label="t('搜索功能')" role="combobox" aria-autocomplete="list" aria-expanded="true" aria-controls="navigation-results" :aria-activedescendant="commandResults.length?'navigation-result-'+commandIndex:undefined" autocomplete="off"/><button class="icon" :aria-label="t('关闭')" @click="commandOpen=false"><X :size="19"/></button></div>
        <div class="navigation-palette-summary" aria-live="polite">{{commandQuery.trim()?t('搜索结果'):t('全部功能')}} <span>{{commandResults.length}}</span><button v-if="commandQuery" class="text-button" @click="commandQuery='';commandInput?.focus()">{{t('清空搜索')}}</button></div>
        <div id="navigation-results" class="navigation-results" role="listbox" :aria-label="t('搜索结果')">
          <button v-for="(item,index) in commandResults" :id="'navigation-result-'+index" :key="item.id" role="option" tabindex="-1" :aria-selected="commandIndex===index" :class="{active:commandIndex===index}" @mouseenter="commandIndex=index" @click="navigate(item.id)"><span class="navigation-result-icon"><component :is="item.icon" :size="19"/></span><span class="navigation-result-copy"><strong>{{item.label}}<small>{{item.group}}</small></strong><span>{{item.description}}</span></span><ChevronRight :size="16"/></button>
        </div>
        <div v-if="!commandResults.length" class="navigation-no-results"><Search :size="28"/><strong>{{t('没有找到相关功能')}}</strong><p>{{t('试试搜索「节点」「套餐」或「设置」。')}}</p><button @click="commandQuery='';commandInput?.focus()">{{t('查看全部功能')}}</button></div>
        <footer><span><kbd>↑</kbd><kbd>↓</kbd>{{t('选择')}}</span><span><kbd>Enter</kbd>{{t('打开')}}</span><span><kbd>Esc</kbd>{{t('关闭')}}</span></footer>
      </section>
    </div>
    <div v-if="!isDesktop && mobileTools && state" class="mobile-tools-shade" @click.self="closeMobile">
      <section id="mobile-tools" ref="toolsDialog" class="mobile-tools-panel" role="dialog" aria-modal="true" :aria-label="t('显示与账户')" tabindex="-1">
        <header><div><strong>{{state.me.username}}</strong><small>{{owner?t('管理员'):t('成员')}} · {{(state.system.edition||'lite').toUpperCase()}}</small></div><button class="icon" :aria-label="t('关闭')" @click="closeMobile"><X :size="20"/></button></header>
        <HeaderBalance v-if="!selectedSite" :key="state.me.id" :user-id="state.me.id" @click="closeMobile"/>
        <button v-if="selectedSite" @click="closeMobile();switchSite('')"><Building2 :size="18"/><span>{{selectedSiteName}} · {{t('返回本站')}}</span></button>
        <button @click="toggleTheme"><Sun v-if="theme==='dark'" :size="18"/><Moon v-else :size="18"/><span>{{theme==='dark'?t('切换浅色模式'):t('切换深色模式')}}</span></button>
        <button @click="closeMobile();modal='password'"><KeyRound :size="18"/><span>{{t('账户与密码')}}</span></button>
        <button @click="closeMobile();go('messages')"><Bell :size="18"/><span>{{t('站内信')}}</span><span v-if="state.unread_messages" class="badge">{{state.unread_messages}}</span></button>
        <button @click="closeMobile();refreshPanel()"><RefreshCw :size="18"/><span>{{t('刷新')}}</span></button>
        <button class="danger-button" @click="closeMobile();signOut()"><LogOut :size="18"/><span>{{t('退出登录')}}</span></button>
      </section>
    </div>
  </Teleport>
  <PanelDialogs v-if="!accessDenial" />
</template>
