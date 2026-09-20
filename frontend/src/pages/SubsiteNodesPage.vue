<script setup lang="ts">
import { defineAsyncComponent } from 'vue';
import { ArrowRight, Globe2 } from 'lucide-vue-next';
import { usePanelContext } from '../composables/panelContext';
import { t } from '../i18n';
const { go } = usePanelContext();
const SubsitePoolOverview = defineAsyncComponent(() => import('../components/SubsitePoolOverview.vue'));
void SubsitePoolOverview;
</script>

<template>
  <section class="subsite-nodes-page">
    <header class="page-heading">
      <div>
        <div class="eyebrow">SUBSITE NODES</div>
        <h1>{{ t('子站节点') }}</h1>
        <p class="section-subtitle">{{ t('挂载后客户端直接连接子站，流量不经过主站；子站 IP 池用于主站中转，两者互不混用。') }}</p>
      </div>
      <button class="text-button" @click="go('ips/subsite-egress')"><ArrowRight :size="15" />{{ t('查看子站 IP 池') }}</button>
    </header>
    <div class="subsite-node-note"><Globe2 :size="18" /><span>{{ t('子站节点只能加入子站节点组，不会进入本地节点组。') }}</span></div>
    <SubsitePoolOverview />
  </section>
</template>

<style scoped>
.subsite-nodes-page{display:grid;gap:20px}.subsite-node-note{display:flex;align-items:center;gap:10px;padding:13px 16px;border:1px solid var(--border);border-radius:10px;background:var(--surface);font-size:13px;color:var(--muted)}
</style>
