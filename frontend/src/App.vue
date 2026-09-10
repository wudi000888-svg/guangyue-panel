<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, provide, ref, watch } from "vue";
import { RouterView } from "vue-router";
import { usePanel } from "./composables/usePanel";
import { panelKey } from "./composables/panelContext";
import PanelDialogs from "./components/PanelDialogs.vue";
import PanelUpdater from "./components/PanelUpdater.vue";
const panel=usePanel();
provide(panelKey,panel);
const { selectedSite, selectedSiteName, switchSite, api, state, ready, busy, error, page, mobileNav, site, login, modal, theme, sideCollapsed, viewMode, simpleMode, toggleTheme, confirmation, owner, pendingHY, titles, pageDescriptions, nav, navGroups, currentGroup, date, refresh, task, signIn, signOut, go } = panel;
import { Bell, ArrowUpRight, ChevronRight, KeyRound, LoaderCircle, LogOut, Menu, Moon, Sun, PanelLeftClose, PanelLeftOpen, Building2, RadioTower, RefreshCw, ShieldCheck, SlidersHorizontal, X } from "lucide-vue-next";
import { t } from "./i18n";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import ViewModeSwitcher from "./ViewModeSwitcher.vue";
const isDesktop = ref(matchMedia('(min-width: 901px)').matches);
const mobileTools = ref(false), drawer = ref<HTMLElement|null>(null), toolsDialog = ref<HTMLElement|null>(null);
const quickNav = computed(() => ['overview','ips','nodes','users','subscription'].flatMap(id => nav.value.filter(item => item.id === id)));
const mobileLayer = computed(() => mobileNav.value || mobileTools.value);
let oldOverflow = '', oldFocus: HTMLElement|null = null, locked = false;
const closeMobile = () => { mobileNav.value = false; mobileTools.value = false; };
watch(mobileLayer, active => {
  if (active && !locked) { oldFocus = document.activeElement as HTMLElement; oldOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; locked = true; }
  if (!active && locked) { document.body.style.overflow = oldOverflow; locked = false; }
}, {flush:'sync'});
watch(mobileLayer, active => { if (!active) oldFocus?.focus(); }, {flush:'post'});
watch([mobileNav, mobileTools], async () => {
  await nextTick();
  const target = mobileTools.value ? toolsDialog.value : mobileNav.value ? drawer.value : null;
  target?.focus();
});
watch([page, state, modal, confirmation], () => { if (!state.value || modal.value || confirmation.value) closeMobile(); });
watch(page, closeMobile);
function mobileKeys(e: KeyboardEvent) {
  if (!mobileLayer.value || document.getElementById('app')?.inert) return;
  if (e.key === 'Escape') { e.preventDefault(); closeMobile(); return; }
  if (e.key !== 'Tab') return;
  const box = mobileTools.value ? toolsDialog.value : drawer.value;
  const items = Array.from(box?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],select:not(:disabled),[tabindex="0"]') || []).filter(el => el.getClientRects().length);
  const first = items[0], last = items.at(-1), active = document.activeElement;
  if (!first) { e.preventDefault(); box?.focus(); }
  else if (e.shiftKey && (active === first || active === box)) { e.preventDefault(); last?.focus(); }
  else if (!e.shiftKey && (active === last || !box?.contains(active))) { e.preventDefault(); first.focus(); }
}
let desktop: MediaQueryList;
function onDesktop() { isDesktop.value = desktop.matches; if (desktop.matches) closeMobile(); }
onMounted(() => { desktop = matchMedia('(min-width: 901px)'); desktop.addEventListener('change', onDesktop); document.addEventListener('keydown', mobileKeys); });
onBeforeUnmount(() => { closeMobile(); desktop?.removeEventListener('change', onDesktop); document.removeEventListener('keydown', mobileKeys); });
</script>
<template>

  <div v-if="!ready" class="loading-screen">
    <LoaderCircle class="spin" :size="28" />
  </div>
  <main v-else-if="!state" class="login-screen">
    <div class="login-language"><LanguageSwitcher/></div>
    <div class="login-brand">
      <Building2 :size="32" /><span
        >{{site.panel_name}}<small>{{site.organization}}</small></span
      >
    </div>
    <form class="login-form" @submit.prevent="signIn">
      <div class="eyebrow">GUANGYUE PANEL</div>
      <h1>{{ t("登录企业控制台") }}</h1>
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
      <label
        >{{ t("密码") }}<input
          v-model="login.password"
          type="password"
          autocomplete="current-password"
          required
          :placeholder="t('密码')"
          maxlength="72"
      /></label>
      <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
      <button class="primary full" :disabled="busy">
        <LoaderCircle v-if="busy" class="spin" :size="18" /><span>{{ t("登录") }}</span
        ><ArrowUpRight :size="18" />
      </button>
      <div class="login-security">
        <ShieldCheck :size="15" />{{ t("企业成员授权访问") }}</div>
    </form>
    <footer>{{site.panel_name}} · {{site.organization}}</footer>
  </main>
  <div
    v-else
    :class="['app-shell', { collapsed: sideCollapsed, 'simple-mode': simpleMode }]"
    :inert="!!modal || !!confirmation"
  >
    <div v-if="mobileNav" class="nav-shade" @click="mobileNav = false"></div>
    <aside id="mobile-navigation" ref="drawer" tabindex="-1" :class="['sidebar', { open: mobileNav }]" :role="mobileNav ? 'dialog' : undefined" :aria-modal="mobileNav ? true : undefined" :aria-label="t('主导航')" :inert="mobileTools">
      <button class="icon drawer-close" :aria-label="t('关闭导航')" @click="mobileNav=false"><X :size="20"/></button>
      <a
        class="brand"
        href="#"
        :aria-label="site.panel_name + ' · ' + t('仪表盘')"
        @click.prevent="go('overview')"
        ><span class="brand-icon"><RadioTower :size="25" /></span
        ><span class="brand-name"
          >{{site.panel_name}}<small
            >{{ t("企业控制台") }}<template v-if="!owner || selectedSite"> · v{{ state.system.version.split("-")[0] }}</template></small
          ></span
        ></a
      >
      <PanelUpdater v-if="owner && !selectedSite" :version="state.system.version.split('-')[0]" @open="closeMobile"/>
      <div class="workspace">
        <span class="workspace-symbol"><Building2 :size="16" /></span>
        <div>
          {{site.organization}}<small>{{
            state.system.panel_host.replace(/^https?:\/\//, "")
          }}</small>
        </div>
        <ShieldCheck :size="17" />
      </div>
      <nav :aria-label="t('主导航')" class="grouped-nav">
        <div v-for="group in navGroups" :key="group.id" :class="['nav-group','nav-group-'+group.id]">
          <div class="nav-caption">{{group.label}}</div>
          <button v-for="item in group.items" :key="item.id" :class="{selected:page===item.id}" :title="item.label" :aria-label="item.label" :aria-current="page===item.id?'page':undefined" @click="go(item.id)">
            <component :is="item.icon" :size="18"/><span>{{item.label}}</span>
            <span v-if="item.id==='messages' && state.unread_messages" class="nav-count unread-count">{{state.unread_messages>99?'99+':state.unread_messages}}</span>
          </button>
        </div>
      </nav>
      <div class="sidebar-bottom">
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
          }}<span>443</span>
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
          <h1>
            <span class="breadcrumb-group">{{currentGroup}} <ChevronRight :size="13"/></span>{{ t(titles[page]) }}
          </h1>
          <p>{{ t(pageDescriptions[page]) }}</p>
        </div>
        <div class="top-actions">
 <button v-if="selectedSite" class="site-switch desktop-action" @click="switchSite('')">{{selectedSiteName}} · {{t('返回本站')}}</button>
 <span v-else class="edition-badge desktop-action">{{state.system.edition==='pro'?'PRO':'LITE'}}</span>
          <ViewModeSwitcher v-if="isDesktop" class="desktop-action" v-model="viewMode"/>
          <LanguageSwitcher/>
          <button v-if="!simpleMode" class="icon inbox-bell" :title="t('站内信')" :aria-label="t('站内信')" @click="go('messages')"><Bell :size="18"/><span v-if="state.unread_messages" class="bell-count">{{state.unread_messages>99?'99+':state.unread_messages}}</span></button>
          <span class="live-label"
            ><span class="dot" />{{ date(state.system.applied_at, true) }}</span
          ><button class="icon desktop-action" :title="t('刷新')" @click="refresh()">
            <RefreshCw :size="17" /></button
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
      <div class="content">
        <RouterView />
        <footer class="page-footer">
          <span>{{site.panel_name}} · {{site.organization}}</span
          ><span>v{{ state.system.version.split("-")[0] }}</span>
        </footer>
      </div>
      <nav class="mobile-quick-nav" :aria-label="t('常用导航')">
        <button v-for="item in quickNav" :key="item.id" :aria-current="page===item.id?'page':undefined" :class="{selected:page===item.id}" @click="go(item.id)"><component :is="item.icon" :size="20"/><span>{{item.label}}</span></button>
      </nav>
    </div>
  </div>
  <Teleport to="body">
    <div v-if="!isDesktop && mobileTools && state" class="mobile-tools-shade" @click.self="closeMobile">
      <section id="mobile-tools" ref="toolsDialog" class="mobile-tools-panel" role="dialog" aria-modal="true" :aria-label="t('显示与账户')" tabindex="-1">
        <header><div><strong>{{state.me.username}}</strong><small>{{owner?t('管理员'):t('企业成员')}} · {{(state.system.edition||'lite').toUpperCase()}}</small></div><button class="icon" :aria-label="t('关闭')" @click="closeMobile"><X :size="20"/></button></header>
        <div class="mobile-preference"><span>{{t('界面模式')}}</span><ViewModeSwitcher v-model="viewMode"/></div>
        <button v-if="selectedSite" @click="closeMobile();switchSite('')"><Building2 :size="18"/><span>{{selectedSiteName}} · {{t('返回本站')}}</span></button>
        <button @click="toggleTheme"><Sun v-if="theme==='dark'" :size="18"/><Moon v-else :size="18"/><span>{{theme==='dark'?t('切换浅色模式'):t('切换深色模式')}}</span></button>
        <button @click="closeMobile();modal='password'"><KeyRound :size="18"/><span>{{t('账户与密码')}}</span></button>
        <button v-if="!simpleMode" @click="closeMobile();go('messages')"><Bell :size="18"/><span>{{t('站内信')}}</span><span v-if="state.unread_messages" class="badge">{{state.unread_messages}}</span></button>
        <button @click="closeMobile();refresh()"><RefreshCw :size="18"/><span>{{t('刷新')}}</span></button>
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
