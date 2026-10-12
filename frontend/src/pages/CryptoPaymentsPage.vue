<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { ArrowRight, CircleCheck, Coins, RefreshCw, ShieldAlert } from 'lucide-vue-next';
import PageHeading from '../components/PageHeading.vue';
import CryptoWalletSettings from '../components/CryptoWalletSettings.vue';
import { useApi, isCancelled } from '../lib/api';
import { cryptoWorkspaceReadiness, cryptoWorkspaceSections, type CryptoWorkspaceSectionID } from '../lib/cryptoModule';
import type { CryptoSettings } from '../lib/cryptoPayments';
import { t } from '../i18n';
import '../styles/admin-ui.css';
import '../styles/integrations.css';

const api = useApi('/commerce/crypto/admin');
const section = ref<CryptoWorkspaceSectionID>('payments');
const settings = ref<CryptoSettings>();
const loading = ref(true);
const error = ref('');

const readiness = computed(() => settings.value
  ? cryptoWorkspaceReadiness(settings.value)
  : { tone: 'muted' as const, label: '正在读取收款状态', detail: '正在读取最近一次预检结果。' });

async function load() {
  loading.value = true;
  error.value = '';
  try {
    settings.value = await api<CryptoSettings>('/setup/status');
  } catch (reason) {
    if (!isCancelled(reason)) error.value = t((reason as Error).message);
  } finally {
    loading.value = false;
  }
}

onMounted(() => { void load(); });
</script>

<template>
  <section class="commerce-page admin-page crypto-workspace" :aria-busy="loading">
    <PageHeading :title="t('加密货币')" :description="t('独立管理链上收款、HD 钱包、余额与归集任务。')" eyebrow="CRYPTO PAYMENTS">
      <template #actions>
        <RouterLink class="crypto-workspace-link" to="/orders">{{ t('查看订单') }}<ArrowRight :size="16" /></RouterLink>
      </template>
    </PageHeading>

    <section class="crypto-workspace-status" :class="'tone-' + readiness.tone" aria-live="polite">
      <div class="crypto-workspace-status-icon"><CircleCheck v-if="readiness.tone === 'ready'" :size="21" /><ShieldAlert v-else :size="21" /></div>
      <div><strong>{{ readiness.label }}</strong><p>{{ t(readiness.detail) }}</p></div>
      <button type="button" :disabled="loading" @click="load"><RefreshCw :size="16" :class="{ spin: loading }" />{{ t('刷新状态') }}</button>
    </section>
    <p v-if="error" class="error crypto-workspace-error" role="alert">{{ error }} <button type="button" @click="load">{{ t('重试') }}</button></p>
    <p class="crypto-workspace-risk"><ShieldAlert :size="17" /><span>{{ t('热钱包密钥由面板服务器保管，仅建议存放小额资金并定期归集；外部 xpub 只读，不能在面板签名。') }}</span></p>

    <nav class="crypto-workspace-tabs" role="tablist" :aria-label="t('加密货币模块')">
      <button v-for="item in cryptoWorkspaceSections" :key="item.id" type="button" role="tab" :aria-selected="section === item.id" :class="{ active: section === item.id }" @click="section = item.id">
        <Coins :size="17" /><span><strong>{{ t(item.label) }}</strong><small>{{ t(item.description) }}</small></span>
      </button>
    </nav>

    <CryptoWalletSettings :active="true" :section="section" />
  </section>
</template>

<style scoped>
.crypto-workspace{gap:20px;min-width:0}.crypto-workspace-link{display:inline-flex;align-items:center;gap:7px;color:var(--accent-text);font-size:13px}.crypto-workspace-status{display:flex;align-items:center;gap:13px;padding:16px 18px;border:1px solid var(--border);border-radius:13px;background:var(--surface);min-width:0}.crypto-workspace-status-icon{display:grid;place-items:center;width:38px;height:38px;flex:0 0 38px;border-radius:11px;background:var(--accent-soft);color:var(--accent-text)}.crypto-workspace-status strong{font-size:14px}.crypto-workspace-status p{margin:5px 0 0;color:var(--secondary);font-size:12px;line-height:1.7}.crypto-workspace-status button{display:inline-flex;align-items:center;gap:7px;margin-left:auto;min-height:42px;flex-shrink:0}.crypto-workspace-status.tone-warning .crypto-workspace-status-icon{background:color-mix(in srgb,#c99a45 14%,var(--surface));color:var(--warning)}.crypto-workspace-status.tone-muted .crypto-workspace-status-icon{background:var(--surface-raised);color:var(--muted)}.crypto-workspace-error{margin:0}.crypto-workspace-error button{margin-left:8px}.crypto-workspace-risk{display:flex;align-items:flex-start;gap:9px;margin:0;padding:13px 15px;border:1px solid color-mix(in srgb,#c99a45 28%,var(--border));border-radius:10px;background:color-mix(in srgb,#c99a45 8%,var(--surface));color:var(--secondary);font-size:12px;line-height:1.8}.crypto-workspace-risk svg{flex-shrink:0;color:var(--warning);margin-top:2px}.crypto-workspace-tabs{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:9px}.crypto-workspace-tabs button{display:flex;align-items:flex-start;gap:10px;text-align:left;min-width:0;min-height:76px;padding:13px 14px;border:1px solid var(--border);border-radius:11px;background:var(--surface);color:var(--secondary)}.crypto-workspace-tabs button.active{border-color:var(--accent);background:var(--accent-soft);color:var(--accent-text)}.crypto-workspace-tabs button>span{display:grid;gap:5px;min-width:0}.crypto-workspace-tabs strong{font-size:13px}.crypto-workspace-tabs small{font-size:11px;line-height:1.5;color:var(--muted);overflow-wrap:anywhere}.crypto-workspace-tabs button.active small{color:var(--accent-text)}@media(max-width:650px){.crypto-workspace-status{align-items:flex-start;flex-wrap:wrap}.crypto-workspace-status button{margin-left:51px}.crypto-workspace-tabs{grid-template-columns:1fr}.crypto-workspace-tabs button{min-height:62px}}
</style>
