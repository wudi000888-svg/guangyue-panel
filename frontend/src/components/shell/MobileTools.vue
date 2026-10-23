<script setup lang="ts">
// Mobile "display & account" sheet. Focus trap, Escape and scroll lock come from useModalFocus.
import { defineAsyncComponent, ref, toRef } from "vue";
import { Bell, Building2, KeyRound, LogOut, Moon, RefreshCw, Sun, X } from "lucide-vue-next";
import { usePanelContext } from "../../composables/panelContext";
import { useModalFocus } from "../../composables/useModalFocus";
import { t } from "../../i18n";
const HeaderBalance = defineAsyncComponent(() => import("../HeaderBalance.vue"));

const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ close: []; refresh: [] }>();
const { state, owner, selectedSite, selectedSiteName, switchSite, theme, toggleTheme, modal, go, signOut } = usePanelContext();
const dialog = ref<HTMLElement | null>(null);
const close = () => emit("close");
useModalFocus(toRef(props, "open"), dialog, close);
</script>
<template>
  <div v-if="open && state" class="mobile-tools-shade" @click.self="close">
    <section id="mobile-tools" ref="dialog" class="mobile-tools-panel" role="dialog" aria-modal="true" :aria-label="t('显示与账户')" tabindex="-1">
      <header><div><strong>{{state.me.username}}</strong><small>{{owner?t('管理员'):t('成员')}} · {{(state.system.edition||'lite').toUpperCase()}}</small></div><button class="icon" :aria-label="t('关闭')" @click="close"><X :size="20"/></button></header>
      <HeaderBalance v-if="!selectedSite" :key="state.me.id" :user-id="state.me.id" @click="close"/>
      <button v-if="selectedSite" @click="close();switchSite('')"><Building2 :size="18"/><span>{{selectedSiteName}} · {{t('返回本站')}}</span></button>
      <button @click="toggleTheme"><Sun v-if="theme==='dark'" :size="18"/><Moon v-else :size="18"/><span>{{theme==='dark'?t('切换浅色模式'):t('切换深色模式')}}</span></button>
      <button @click="close();modal='password'"><KeyRound :size="18"/><span>{{t('账户与密码')}}</span></button>
      <button @click="close();go('messages')"><Bell :size="18"/><span>{{t('站内信')}}</span><span v-if="state.unread_messages" class="badge">{{state.unread_messages}}</span></button>
      <button @click="close();emit('refresh')"><RefreshCw :size="18"/><span>{{t('刷新')}}</span></button>
      <button class="danger-button" @click="close();signOut()"><LogOut :size="18"/><span>{{t('退出登录')}}</span></button>
    </section>
  </div>
</template>
