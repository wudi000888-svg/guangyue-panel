<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, provide, ref, watch } from "vue";
import { RouterView } from "vue-router";
import { usePanel } from "./composables/usePanel";
import { panelKey } from "./composables/panelContext";
import PanelDialogs from "./components/PanelDialogs.vue";
import PanelUpdater from "./components/PanelUpdater.vue";
import HeaderBalance from "./components/HeaderBalance.vue";
import { Bell, ArrowUpRight, ArrowLeft, ChevronRight, Eye, EyeOff, KeyRound, LoaderCircle, LogOut, Menu, Moon, Sun, PanelLeftClose, PanelLeftOpen, Building2, RefreshCw, Search, ShieldCheck, SlidersHorizontal, X } from "lucide-vue-next";
import { t } from "./i18n";
import { groupNavigation, searchNavigation } from "./lib/navigation";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import RegistrationCaptcha from "./components/RegistrationCaptcha.vue";

const panel = usePanel();
provide(panelKey, panel);
const { selectedSite, selectedSiteName, switchSite, api, state, ready, busy, error, page, mobileNav, site, login, registration, registrationReset, modal, theme, sideCollapsed, toggleTheme, confirmation, owner, pendingHY, pageTitle, pageDescriptions, nav, refresh, task, signIn, signOut, registerAccount, go } = panel;
const isDesktop = ref(matchMedia('(min-width: 901px)').matches);
const registerMode = ref(false), showPassword = ref(false), showConfirmPassword = ref(false), refreshing = ref(false);
const mobileTools = ref(false), drawer = ref<HTMLElement | null>(null), toolsDialog = ref<HTMLElement | null>(null);
const commandOpen = ref(false), commandQuery = ref(''), commandIndex = ref(0);
const commandDialog = ref<HTMLElement | null>(null), commandInput = ref<HTMLInputElement | null>(null), pageContent = ref<HTMLElement | null>(null);
const shellGroups = computed(() => groupNavigation(nav.value, owner.value).map(group => ({ ...group, label: t(group.label) })));
const shellGroup = computed(() => shellGroups.value.find(group => group.items.some(item => item.id === page.value))?.label || '');
const searchEntries = computed(() => shellGroups.value.flatMap(group => group.items.map(item => ({ ...item, group: group.label, description: item.id === 'shop' && owner.value ? t('管理套餐上架与销售规则') : t(pageDescriptions[item.id] || '') }))));
const commandResults = computed(() => searchNavigation(searchEntries.value, commandQuery.value));
const quickNav = computed(() => (owner.value ? ['overview', 'nodes', 'users', 'fleet', 'subscription'] : ['subscription', 'shop', 'wallet', 'orders', 'tickets']).flatMap(id => nav.value.filter(item => item.id === id)));
const mobileLayer = computed(() => mobileNav.value || mobileTools.value);
const overlayOpen = computed(() => mobileLayer.value || commandOpen.value);
const roleLabel = computed(() => t(state.value?.system.role === 'controller' || !state.value?.system.role && state.value?.system.edition === 'pro' ? '主站' : state.value?.system.role === 'business' ? '子站' : '独立站'));
const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ K' : 'Ctrl K';
let oldOverflow = '', oldFocus: HTMLElement | null = null, locked = false;
const closeMobile = () => { mobileNav.value = false; mobileTools.value = false; };
const closeOverlays = () => { closeMobile(); commandOpen.value = false; };
function openSearch() {
  if (!state.value || modal.value || confirmation.value || document.getElementById('app')?.inert) return;
  commandQuery.value = '';
  commandOpen.value = true;
  closeMobile();
}
function navigate(id: string) {
  go(id);
  closeOverlays();
  void nextTick(() => pageContent.value?.focus({ preventScroll: true }));
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
onMounted(() => { desktop = matchMedia('(min-width: 901px)'); desktop.addEventListener('change', onDesktop); document.addEventListener('keydown', shellKeys); });
onBeforeUnmount(() => { closeOverlays(); desktop?.removeEventListener('change', onDesktop); document.removeEventListener('keydown', shellKeys); });
</script>
<template>

  <div v-if="!ready" class="loading-screen" role="status" aria-live="polite">
    <div class="shell-loading"><img src="/guangyue-mark.svg" alt="" width="56" height="56"/><strong>{{site.panel_name}}</strong><span><LoaderCircle class="spin" :size="16"/>{{t('正在准备你的工作台')}}</span></div>
  </div>
  <main v-else-if="!state" class="login-screen">
    <div class="login-language"><LanguageSwitcher/></div>
    <section class="login-scene" :aria-label="t('月映珠江')">
      <div class="login-scene-copy"><span class="courtyard-kicker">GUANGYUE · 岭南</span><h2>{{t('一庭月色，连接四方。')}}</h2><p>{{t('从容管理每一条连接。')}}</p></div>
      <img class="login-art" src="/guangyue-courtyard.svg" alt="" width="1000" height="760"/>
      <div class="login-scene-foot"><span>GUANGZHOU · 23.13° N</span><span>{{t('月映珠江')}}</span></div>
    </section>
    <div class="login-entry">
    <div class="login-brand">
      <img class="courtyard-mark" src="/guangyue-mark.svg" alt="" width="42" height="42"/><span
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
        <ShieldCheck :size="15" />{{ t("企业成员授权访问") }}</div>
      <button v-if="site.registration_enabled" type="button" class="link-button" @click="registerMode=true">{{t('注册账号')}}</button>
    </form>
    <form v-else class="login-form" @submit.prevent="registerAccount">
      <div class="eyebrow">GUANGYUE PANEL</div><h1>{{t('创建账号')}}</h1>
      <p class="login-subtitle">{{t('注册后默认获得演示套餐，可在订阅管理查看当前套餐。')}}</p>
      <label>{{t('账号')}}<input v-model="registration.username" autocomplete="username" required maxlength="32" :placeholder="t('用户名')"/></label>
      <label for="register-password">{{t('密码')}}</label>
      <div class="password-field"><input id="register-password" v-model="registration.password" :type="showPassword?'text':'password'" autocomplete="new-password" required minlength="8" maxlength="72" :placeholder="t('至少 8 位密码')"/><button type="button" class="icon" :aria-label="showPassword?t('隐藏密码'):t('显示密码')" :aria-pressed="showPassword" @click="showPassword=!showPassword"><EyeOff v-if="showPassword" :size="18"/><Eye v-else :size="18"/></button></div>
      <label for="register-confirm-password">{{t('确认密码')}}</label>
      <div class="password-field"><input id="register-confirm-password" v-model="registration.confirm_password" :type="showConfirmPassword?'text':'password'" autocomplete="new-password" required minlength="8" maxlength="72"/><button type="button" class="icon" :aria-label="showConfirmPassword?t('隐藏密码'):t('显示密码')" :aria-pressed="showConfirmPassword" @click="showConfirmPassword=!showConfirmPassword"><EyeOff v-if="showConfirmPassword" :size="18"/><Eye v-else :size="18"/></button></div>
      <RegistrationCaptcha v-if="site.registration_captcha" :key="registrationReset" v-model="registration.captcha_token"/>
      <p v-if="error" class="error" role="alert">{{t(error)}}</p>
      <button class="primary full" :disabled="busy||(site.registration_captcha&&!registration.captcha_token)"><LoaderCircle v-if="busy" class="spin" :size="18"/><span>{{t('立即注册')}}</span><ArrowUpRight :size="18"/></button>
      <button type="button" class="link-button" @click="registerMode=false">{{t('返回登录')}}</button>
    </form>
    <footer>{{site.panel_name}} · {{site.organization}}</footer>
    </div>
  </main>
  <div
    v-else
    :class="['app-shell', { collapsed: sideCollapsed }]"
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
        ><span class="brand-icon"><img src="/guangyue-mark.svg" alt="" width="36" height="36"/></span
        ><span class="brand-name"
          >{{site.panel_name}}<small
            >{{ t("企业控制台") }}<template v-if="!owner || selectedSite"> · v{{ state.system.version.split("-")[0] }}</template></small
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
      <nav :aria-label="t('主导航')" class="grouped-nav">
        <div v-for="group in shellGroups" :key="group.id" :class="['nav-group','nav-group-'+group.id]">
          <div class="nav-caption">{{group.label}}</div>
          <button v-for="item in group.items" :key="item.id" :class="{selected:page===item.id}" :title="item.label" :aria-label="item.label" :aria-current="page===item.id?'page':undefined" @click="navigate(item.id)">
            <component :is="item.icon" :size="18"/><span>{{item.label}}</span>
            <span v-if="item.id==='messages' && state.unread_messages" class="nav-count unread-count">{{state.unread_messages>99?'99+':state.unread_messages}}</span>
          </button>
        </div>
      </nav>
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
          class="sidebar-setting"
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
            <span class="breadcrumb-group">{{shellGroup}} <ChevronRight :size="13"/></span>{{ pageTitle }}
          </p>

        </div>
        <HeaderBalance v-if="!selectedSite" :key="state.me.id" :user-id="state.me.id"/>
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
              }}<small>{{ owner ? t("管理员") : t("企业成员") }}</small></span
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
      <main id="main-content" ref="pageContent" class="content" tabindex="-1" :aria-label="pageTitle">
        <div v-if="selectedSite" class="remote-context" role="status"><Building2 :size="17"/><div><strong>{{selectedSiteName}}</strong><span>{{t('当前操作将应用于此子站')}}</span></div><button @click="switchSite('')"><ArrowLeft :size="15"/>{{t('返回本站')}}</button></div>
        <RouterView />
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
        <header><div><strong>{{state.me.username}}</strong><small>{{owner?t('管理员'):t('企业成员')}} · {{(state.system.edition||'lite').toUpperCase()}}</small></div><button class="icon" :aria-label="t('关闭')" @click="closeMobile"><X :size="20"/></button></header>
        <button v-if="selectedSite" @click="closeMobile();switchSite('')"><Building2 :size="18"/><span>{{selectedSiteName}} · {{t('返回本站')}}</span></button>
        <button @click="toggleTheme"><Sun v-if="theme==='dark'" :size="18"/><Moon v-else :size="18"/><span>{{theme==='dark'?t('切换浅色模式'):t('切换深色模式')}}</span></button>
        <button @click="closeMobile();modal='password'"><KeyRound :size="18"/><span>{{t('账户与密码')}}</span></button>
        <button @click="closeMobile();go('messages')"><Bell :size="18"/><span>{{t('站内信')}}</span><span v-if="state.unread_messages" class="badge">{{state.unread_messages}}</span></button>
        <button @click="closeMobile();refreshPanel()"><RefreshCw :size="18"/><span>{{t('刷新')}}</span></button>
        <button class="danger-button" @click="closeMobile();signOut()"><LogOut :size="18"/><span>{{t('退出登录')}}</span></button>
      </section>
    </div>
  </Teleport>
  <PanelDialogs />
</template>

<style>

.private-file-import{border:1px dashed var(--border);background:var(--surface);border-radius:10px;padding:19px;margin-bottom:18px}.private-file-heading{display:flex;gap:12px;align-items:flex-start}.private-file-heading>svg{color:var(--accent-text);flex-shrink:0;margin-top:2px}.private-file-heading strong{font-size:13px;font-weight:600}.private-file-heading p{font-size:11px;line-height:1.75;color:var(--muted);margin:6px 0 0}.private-file-summary{display:flex;gap:9px;align-items:center;margin:16px 0 3px;padding:11px 12px;background:var(--bg);border:1px solid var(--border);border-radius:7px}.private-file-summary>svg{color:var(--accent-text);flex-shrink:0}.private-file-summary>div{flex:1;min-width:0}.private-file-summary strong{display:block;font-size:12px;overflow-wrap:anywhere;font-weight:500}.private-file-summary span{display:block;font-size:10px;color:var(--muted);margin-top:5px}.private-file-picker{margin-top:15px!important;border:1px solid var(--border);border-radius:7px;background:var(--bg);color:var(--text)!important;min-height:35px}.private-file-picker.disabled{opacity:.6;cursor:wait}.private-file-import>.field-caption{margin-bottom:0}.private-plain-protocol{display:grid;grid-template-columns:minmax(125px,155px) 1fr;gap:17px;align-items:start;padding:15px;border:1px solid var(--border);border-radius:9px;margin:16px 0}.private-plain-protocol>label{margin:0;font-size:11px}.private-plain-protocol select{font-size:12px;margin-top:8px}.private-plain-protocol>div{padding-top:2px;min-width:0}.private-plain-protocol strong{font-size:11px;font-weight:500;color:var(--text);overflow-wrap:anywhere}.private-plain-protocol p,.private-plain-protocol span{font-size:10px;line-height:1.8;color:var(--muted);margin:6px 0 0}.private-plain-protocol span{display:block;color:var(--accent-text)}.private-import-issues{margin:14px 0;padding:13px 15px;border:1px solid var(--border);border-radius:8px;background:var(--bg);font-size:11px}.private-import-issues strong{color:var(--text)}.private-import-issues p{font-size:11px;line-height:1.7;color:var(--muted);margin:7px 0}.private-import-issues details{max-height:165px;overflow:auto}.private-import-issues summary{cursor:pointer;color:var(--accent-text)}@media(max-width:550px){.private-plain-protocol{grid-template-columns:1fr;gap:12px}.private-file-import{padding:15px}.private-plain-protocol select{width:100%}.private-file-picker{width:100%;box-sizing:border-box}}
.default-direct-badge{display:inline-flex;align-items:center;gap:5px;font-size:10px;margin-top:7px}.default-direct-notice{display:flex;align-items:flex-start;gap:11px;border:1px solid var(--border);border-radius:9px;padding:15px;background:var(--accent-soft);margin:16px 0}.default-direct-notice>svg{color:var(--accent-text);flex-shrink:0}.default-direct-notice>div{min-width:0}.default-direct-notice strong{font-size:12px;font-weight:600}.default-direct-notice p{font-size:11px;line-height:1.7;margin:7px 0;color:var(--muted)}.default-direct-notice button{font-size:11px;padding:4px 0;min-height:28px;white-space:normal;text-align:left}
.node-sni-setting{padding:15px;border:1px solid var(--border);border-radius:9px;background:var(--surface-raised);margin:16px 0}.node-sni-setting label{margin:0!important}.node-sni-setting p{font-size:11px;line-height:1.7;color:var(--muted);margin:9px 0 0;overflow-wrap:anywhere}.node-sni-setting strong{font-weight:500;color:var(--secondary)}.node-sni-label{display:block;font-size:10px;line-height:1.6;color:var(--accent-text);margin-top:7px;max-width:220px;overflow-wrap:anywhere;white-space:normal}
.resource-membership-state{display:flex;flex-direction:column;align-items:center;gap:14px;padding:42px 24px;background:var(--surface);border:1px dashed var(--border);border-radius:10px;color:var(--muted);text-align:center}.resource-membership-state h2{font-size:15px;color:var(--text);margin:0}.resource-membership-state p{font-size:12px;line-height:1.8;margin:0;max-width:570px;overflow-wrap:anywhere}.resource-membership-state button{font-size:11px}.resource-membership-notice{display:flex;align-items:center;justify-content:space-between;gap:12px;border:1px solid var(--border);border-left:3px solid #d8a454;border-radius:7px;background:var(--surface);padding:12px 15px;margin-bottom:18px;font-size:11px;color:var(--muted)}
.resource-page-description{font-size:12px;line-height:1.7;color:var(--muted);margin:9px 0 0}.resource-section-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;margin:0 0 20px}.resource-section-heading h2{font-size:15px;margin:0}.resource-section-heading p{font-size:12px;color:var(--muted);margin:7px 0 0;line-height:1.7}.resource-section-heading>button{flex-shrink:0}.resource-source-filter{display:flex;align-items:center;gap:10px;margin:0 0 16px;padding:12px 15px;border:1px solid var(--border);background:var(--surface);border-radius:8px;font-size:12px}.resource-source-filter>button{margin-left:auto}.resource-origin{color:var(--accent-text)!important;font-size:10px!important;line-height:1.6}.private-pool-page .pool-table td:nth-child(2){min-width:245px;max-width:310px}.private-pool-page .user-stat-grid{margin-bottom:26px}.import-file-button{display:inline-flex!important;align-items:center;justify-content:center;gap:6px;font-size:11px!important;cursor:pointer;margin:0!important;padding:9px 12px;position:relative;color:var(--muted)!important}.import-file-button:focus-within{outline:2px solid var(--accent-text);outline-offset:-2px}.import-file-button input{position:absolute;inset:0;opacity:0;cursor:pointer;width:100%;padding:0!important;min-height:0!important}.import-tabs{flex-wrap:wrap}@media(max-width:650px){.private-pool-page>.page-heading{flex-direction:column;align-items:stretch}.private-pool-page .heading-actions{justify-content:flex-start;flex-wrap:wrap}.resource-section-heading{align-items:flex-start;gap:10px}.resource-section-heading p{font-size:11px}.resource-section-heading>button{font-size:10px}.resource-source-filter{flex-wrap:wrap}.private-pool-page .resource-tabs>button{flex:1}}

</style>

<style>
.edition-badge{font-size:10px;letter-spacing:.12em;font-weight:700;color:var(--accent-text);border:1px solid var(--border);border-radius:6px;padding:5px 7px}.site-switch{font-size:11px;color:var(--accent-text)}
</style>
