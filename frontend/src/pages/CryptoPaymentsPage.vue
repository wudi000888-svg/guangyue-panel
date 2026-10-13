<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { ArrowUpRight, ChevronDown, CircleCheck, WalletCards, RefreshCw, ShieldAlert } from 'lucide-vue-next';
import CryptoWalletSettings from '../components/CryptoWalletSettings.vue';
import { useApi, isCancelled } from '../lib/api';
import { cryptoWorkspaceReadiness, cryptoWorkspaceSections, type CryptoWorkspaceSectionID } from '../lib/cryptoModule';
import type { CryptoSettings } from '../lib/cryptoPayments';
import { t } from '../i18n';
import '../styles/admin-ui.css';
import '../styles/integrations.css';
const api = useApi('/commerce/crypto/admin');
const section = ref<CryptoWorkspaceSectionID>('treasury');
const selectedWalletId = ref('');
function viewAssets(walletID: string) { selectedWalletId.value = walletID; section.value = 'treasury'; }
const settings = ref<CryptoSettings>();
const loading = ref(true), error = ref(''), sectionTabs = ref<HTMLElement>();
function navigateSection(event: KeyboardEvent) {
 const buttons = Array.from(sectionTabs.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]') || []);
 const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
 if (index < 0 || !['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
 event.preventDefault();
 const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length;
 buttons[next]?.click(); buttons[next]?.focus();
}
const readiness = computed(() => settings.value ? cryptoWorkspaceReadiness(settings.value) : { tone: 'muted' as const, label: '正在读取收款状态', detail: '正在读取最近一次预检结果。' });
let loadSequence = 0;
onUnmounted(() => { loadSequence++; });
async function load() {
 const sequence = ++loadSequence;
 loading.value = true; error.value = '';
 try { const value = await api<CryptoSettings>('/setup/status'); if (sequence === loadSequence) settings.value = value; }
 catch (reason) { if (sequence === loadSequence && !isCancelled(reason)) error.value = t((reason as Error).message); }
 finally { if (sequence === loadSequence) loading.value = false; }
}
onMounted(() => { void load(); });
</script>
<template>
 <section class="commerce-page crypto-wallet-workspace">
  <header class="crypto-wallet-header"><div><span class="crypto-wallet-brand"><WalletCards :size="21"/></span><h1>{{t('链上钱包')}}</h1></div><RouterLink to="/orders">{{t('订单')}}<ArrowUpRight :size="14"/></RouterLink></header>
  <nav ref="sectionTabs" class="crypto-wallet-topnav" role="tablist" :aria-label="t('加密货币模块')" @keydown="navigateSection"><button v-for="item in cryptoWorkspaceSections" :id="'crypto-section-'+item.id" :key="item.id" type="button" role="tab" :aria-selected="section===item.id" aria-controls="crypto-section-panel" :tabindex="section===item.id?0:-1" :class="{active:section===item.id}" @click="section=item.id">{{t(item.label)}}</button></nav>
  <p v-if="error" class="crypto-wallet-error error" role="alert">{{error}}<button type="button" @click="load">{{t('重试')}}</button></p>
  <section id="crypto-section-panel" class="crypto-wallet-content" role="tabpanel" :aria-labelledby="'crypto-section-'+section"><CryptoWalletSettings v-model:selected-wallet-id="selectedWalletId" :active="true" :section="section" @manage-wallets="section='wallets'" @view-assets="viewAssets" @settings-changed="load"/></section>
  <details class="crypto-wallet-disclosure"><summary><CircleCheck v-if="readiness.tone==='ready'" :size="14"/><ShieldAlert v-else :size="14"/><span>{{t(readiness.label)}}</span><span class="crypto-risk-label">{{t('保管与收款说明')}}</span><ChevronDown :size="14"/></summary><div><p>{{t(readiness.detail)}}</p><p>{{t('热钱包密钥由面板服务器保管，仅建议存放小额资金并定期归集；外部 xpub 只读，不能在面板签名。')}}</p><button type="button" :disabled="loading" @click="load"><RefreshCw :size="14" :class="{spin:loading}"/>{{t('刷新收款状态')}}</button></div></details>
 </section>
</template>
<style scoped>
.crypto-wallet-workspace{display:block;max-width:1120px;margin:0 auto;min-width:0}.crypto-wallet-header{display:flex;justify-content:space-between;align-items:center;gap:15px;margin-bottom:25px}.crypto-wallet-header>div{display:flex;align-items:center;gap:11px}.crypto-wallet-brand{display:grid;place-items:center;width:36px;height:36px;border-radius:11px;background:var(--accent-soft);color:var(--accent-text)}.crypto-wallet-header h1{font-size:22px;font-weight:600;letter-spacing:-.6px;margin:0}.crypto-wallet-header>a{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--muted);min-height:44px}.crypto-wallet-topnav{display:flex;gap:27px;border-bottom:1px solid var(--border);margin-bottom:25px}.crypto-wallet-topnav button{position:relative;min-height:48px;padding:0 2px 12px;background:none;border:0;border-radius:0;color:var(--muted);font-size:13px;font-weight:500}.crypto-wallet-topnav button.active{color:var(--text)}.crypto-wallet-topnav button.active:after{content:'';position:absolute;left:0;right:0;bottom:-1px;height:3px;border-radius:3px;background:var(--accent)}.crypto-wallet-topnav button:focus-visible{outline:2px solid var(--accent);outline-offset:-2px}.crypto-wallet-content{min-width:0}.crypto-wallet-error{display:flex;align-items:center;gap:12px;font-size:12px;margin:0 0 15px;padding:10px 0;flex-wrap:wrap}.crypto-wallet-error button{min-height:44px;margin-left:auto}.crypto-wallet-disclosure{margin-top:28px;color:var(--muted);border-top:1px solid var(--border);font-size:11px}.crypto-wallet-disclosure summary{display:flex;align-items:center;gap:7px;min-height:48px;cursor:pointer;list-style:none;line-height:1.7}.crypto-wallet-disclosure summary::-webkit-details-marker{display:none}.crypto-wallet-disclosure summary>svg:first-child{color:var(--accent-text);flex-shrink:0}.crypto-risk-label{margin-left:auto}.crypto-wallet-disclosure[open] summary>svg:last-child{transform:rotate(180deg)}.crypto-wallet-disclosure>div{padding:0 0 12px 21px;max-width:800px}.crypto-wallet-disclosure p{line-height:1.9;margin:0 0 9px}.crypto-wallet-disclosure button{display:flex;align-items:center;gap:6px;min-height:44px;font-size:11px;background:none}@media(max-width:650px){.crypto-wallet-header{margin-bottom:15px}.crypto-wallet-header h1{font-size:20px}.crypto-wallet-topnav{justify-content:space-between;gap:15px;margin-bottom:21px}.crypto-wallet-topnav button{font-size:12px;min-height:48px}.crypto-risk-label{display:none}.crypto-wallet-disclosure summary>svg:last-child{margin-left:auto}}
</style>
