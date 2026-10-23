<script setup lang="ts">
// Authenticated layout: sidebar, top bar, page outlet, command palette and mobile sheets.
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { RouterView } from "vue-router";
import { Bell, ArrowLeft, ChevronRight, KeyRound, LoaderCircle, LogOut, Menu, Moon, Sun, PanelLeftClose, PanelLeftOpen, Building2, RefreshCw, Search, ShieldCheck, SlidersHorizontal, X, GripVertical, Grid2X2, FolderInput } from "lucide-vue-next";
import { usePanelContext } from "../../composables/panelContext";
import { useModalFocus } from "../../composables/useModalFocus";
import { errorMessage } from "../../lib/api";
import { groupNavigation, sidebarNavigation, MEMBER_NAVIGATION_LABELS } from "../../lib/navigation";
import { allowedRoute } from "../../lib/access";
import { router } from "../../router";
import { t } from "../../i18n";
import LanguageSwitcher from "../../LanguageSwitcher.vue";
import CommandPalette from "./CommandPalette.vue";
import MobileTools from "./MobileTools.vue";
const PanelUpdater = defineAsyncComponent(() => import("../PanelUpdater.vue"));
const HeaderBalance = defineAsyncComponent(() => import("../HeaderBalance.vue"));

const panel = usePanelContext();
const { sidebarCustomization } = panel;
const { selectedSite, selectedSiteName, switchSite, api, state, error, page, mobileNav, site, modal, theme, sideCollapsed, toggleTheme, confirmation, owner, pendingHY, pageTitle, pageDescriptions, nav, publicFeaturesEnabled, refresh, task, signOut, go } = panel;
const isDesktop = ref(matchMedia('(min-width: 901px)').matches);
const refreshing = ref(false);
const mobileTools = ref(false), drawer = ref<HTMLElement | null>(null);
const commandOpen = ref(false), pageContent = ref<HTMLElement | null>(null);
const permittedNav = computed(() => nav.value.filter(item => allowedRoute(router.resolve('/' + item.id).meta, {
  role: state.value?.me.role || null, edition: state.value?.system.edition || 'lite', publicFeatures: publicFeaturesEnabled.value,
})).map(item => ({ ...item, label: !owner.value && MEMBER_NAVIGATION_LABELS[item.id] ? t(MEMBER_NAVIGATION_LABELS[item.id]!) : item.label })));
const visibleNav = computed(() => sidebarNavigation(permittedNav.value, sidebarCustomization.selected(owner.value ? 'admin' : 'member'), owner.value));
const allShellGroups = computed(() => groupNavigation(permittedNav.value, owner.value).map(group => ({ ...group, label: t(group.label) })));
const shellTitle = computed(() => permittedNav.value.find(item => item.id === page.value)?.label || pageTitle.value);
const shellGroups = computed(() => groupNavigation(visibleNav.value, owner.value).map(group => ({ ...group, label: t(group.label) })));
const shellGroup = computed(() => allShellGroups.value.find(group => group.items.some(item => item.id === page.value))?.label || '');
const searchEntries = computed(() => allShellGroups.value.flatMap(group => group.items.map(item => ({ ...item, group: group.label, description: item.id === 'shop' && owner.value ? t('管理套餐上架与销售规则') : t(pageDescriptions[item.id] || '') }))));
const quickNav = computed(() => [...visibleNav.value.filter(item => item.id !== 'settings').slice(0, 4), ...visibleNav.value.filter(item => item.id === 'settings')]);
const dragPreviewStyle = computed(() => {
  const position = sidebarCustomization.pointerPosition.value;
  return position ? { left: Math.max(8, Math.min(position.x + 14, window.innerWidth - 250)) + 'px', top: Math.max(8, Math.min(position.y + 14, window.innerHeight - 65)) + 'px' } : {};
});
const mobileLayer = computed(() => mobileNav.value || mobileTools.value);
const roleLabel = computed(() => t(state.value?.system.role === 'controller' || !state.value?.system.role && state.value?.system.edition === 'pro' ? '主站' : state.value?.system.role === 'business' ? '子站' : '独立站'));
const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ K' : 'Ctrl K';
const closeMobile = () => { mobileNav.value = false; mobileTools.value = false; };
const closeOverlays = () => { closeMobile(); commandOpen.value = false; };
// The off-canvas drawer is a modal dialog on small screens only.
useModalFocus(computed(() => mobileNav.value && !isDesktop.value), drawer, () => { mobileNav.value = false; });
function openSearch() {
  if (!state.value || modal.value || confirmation.value || document.getElementById('app')?.inert) return;
  const activeDialog = document.querySelector('[role="dialog"][aria-modal="true"], [role="alertdialog"][aria-modal="true"]');
  if (activeDialog && !mobileNav.value && !mobileTools.value) return;
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
  catch (reason) { error.value = errorMessage(reason); }
  finally { refreshing.value = false; }
}
watch([page, state, modal, confirmation], () => { if (!state.value || modal.value || confirmation.value) closeOverlays(); });
watch(page, closeOverlays);
function shellKeys(e: KeyboardEvent) {
  if (modal.value || confirmation.value || document.getElementById('app')?.inert) return;
  if (e.key === 'Escape' && sidebarCustomization.dragging.value) { e.preventDefault(); sidebarCustomization.endDrag(); return; }
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k' && state.value) { e.preventDefault(); if (commandOpen.value) commandOpen.value = false; else openSearch(); }
}
let desktop: MediaQueryList;
function onDesktop() { isDesktop.value = desktop.matches; if (desktop.matches) closeMobile(); }
onMounted(() => { desktop = matchMedia('(min-width: 901px)'); desktop.addEventListener('change', onDesktop); document.addEventListener('keydown', shellKeys); });
onBeforeUnmount(() => { closeOverlays(); desktop?.removeEventListener('change', onDesktop); document.removeEventListener('keydown', shellKeys); });
defineExpose({ closeOverlays });
</script>
<template>
  <div
    v-if="state"
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
    <CommandPalette :open="commandOpen" :entries="searchEntries" @close="commandOpen=false" @navigate="navigate"/>
    <MobileTools :open="!isDesktop && mobileTools" @close="closeMobile" @refresh="refreshPanel()"/>
  </Teleport>
</template>
