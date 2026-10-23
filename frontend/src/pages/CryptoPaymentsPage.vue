<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { ArrowUpRight, ChevronDown, CircleCheck, ChevronLeft, WalletCards, RefreshCw, ShieldAlert } from 'lucide-vue-next';
import CryptoWalletSettings from '../components/CryptoWalletSettings.vue';
import WalletPortfolioGallery from '../components/WalletPortfolioGallery.vue';
import type {CryptoWallet} from '../lib/cryptoWallet';
import type {WalletAction} from '../lib/walletGallery';
import {applyWalletList,applyWalletListChange,type WalletListChange} from '../lib/cryptoWalletList';
import { useApi, isCancelled, errorMessage } from '../lib/api';
import { cryptoWorkspaceReadiness, cryptoWorkspaceSections, type CryptoWorkspaceSectionID } from '../lib/cryptoModule';
import type { CryptoSettings } from '../lib/cryptoPayments';
import { t } from '../i18n';
import '../styles/admin-ui.css';
import '../styles/integrations.css';
const api = useApi('/commerce/crypto/admin');
const section = ref<CryptoWorkspaceSectionID>('treasury');
const selectedWalletId=ref(''),wallets=ref<CryptoWallet[]>([]),detailsOpen=ref(false),requestedAction=ref<WalletAction>('assets'),actionNonce=ref(0);
function viewWallet(walletID:string,action:WalletAction='assets'){selectedWalletId.value=walletID;requestedAction.value=action;actionNonce.value++;detailsOpen.value=true;section.value='treasury'}
function viewAssets(walletID:string){viewWallet(walletID)}
function selectSection(value:CryptoWorkspaceSectionID){section.value=value;if(value==='treasury')detailsOpen.value=false}
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
let loadSequence=0,walletSequence=0;
function acceptWallets(change:WalletListChange){walletSequence++;wallets.value=applyWalletListChange(wallets.value,change);if(selectedWalletId.value&&!wallets.value.some(wallet=>wallet.id===selectedWalletId.value)){selectedWalletId.value='';detailsOpen.value=false}}
onUnmounted(() => { loadSequence++; });
async function load(){
 const sequence=++loadSequence,walletRequest=walletSequence;loading.value=true;error.value='';
 const [status,list]=await Promise.allSettled([api<CryptoSettings>('/setup/status'),api<{items:CryptoWallet[]}>('/wallets')]);
 if(sequence!==loadSequence)return;
 if(status.status==='fulfilled')settings.value=status.value;else if(!isCancelled(status.reason))error.value=errorMessage(status.reason);
 if(list.status==='fulfilled'&&walletRequest===walletSequence){wallets.value=applyWalletList(list.value,wallets.value).wallets;if(selectedWalletId.value&&!wallets.value.some(wallet=>wallet.id===selectedWalletId.value)){selectedWalletId.value='';detailsOpen.value=false}}else if(list.status==='rejected'&&!isCancelled(list.reason))error.value=[error.value,errorMessage(list.reason)].filter(Boolean).join(' ');
 loading.value=false;
}
onMounted(() => { void load(); });
</script>
<template>
 <section class="commerce-page crypto-wallet-workspace">
  <header class="crypto-wallet-header"><div><span class="crypto-wallet-brand"><WalletCards :size="21"/></span><h1>{{t('链上钱包')}}</h1></div><RouterLink to="/orders">{{t('订单')}}<ArrowUpRight :size="14"/></RouterLink></header>
  <nav ref="sectionTabs" class="crypto-wallet-topnav" role="tablist" :aria-label="t('加密货币模块')" @keydown="navigateSection"><button v-for="item in cryptoWorkspaceSections" :id="'crypto-section-'+item.id" :key="item.id" type="button" role="tab" :aria-selected="section===item.id" aria-controls="crypto-section-panel" :tabindex="section===item.id?0:-1" :class="{active:section===item.id}" @click="selectSection(item.id)">{{t(item.label)}}</button></nav>
  <p v-if="error" class="crypto-wallet-error error" role="alert">{{error}}<button type="button" @click="load">{{t('重试')}}</button></p>
  <section id="crypto-section-panel" class="crypto-wallet-content" role="tabpanel" :aria-labelledby="'crypto-section-'+section">
   <template v-if="section==='treasury'"><WalletPortfolioGallery v-if="!detailsOpen&&wallets.length" :wallets="wallets" :active="true" :selected-wallet-id="selectedWalletId" @action="viewWallet" @manage="section='wallets'"/><div v-else-if="!detailsOpen" class="crypto-gallery-empty"><WalletCards :size="34"/><h2>{{loading?t('正在读取钱包…'):t('创建你的第一个收款钱包')}}</h2><p>{{t('新建热钱包，或导入已有钱包的公钥与加密备份。')}}</p><button type="button" class="primary" @click="section='wallets'">{{t('创建或导入钱包')}}</button></div><template v-else><button type="button" class="crypto-return-gallery" @click="detailsOpen=false"><ChevronLeft :size="17"/>{{t('返回全部钱包')}}</button><CryptoWalletSettings v-model:selected-wallet-id="selectedWalletId" :active="true" section="treasury" :requested-action="requestedAction" :action-nonce="actionNonce" :receiving-settings="settings" @manage-wallets="section='wallets'" @settings-changed="load" @wallets-changed="acceptWallets"/></template></template>
   <CryptoWalletSettings v-else v-model:selected-wallet-id="selectedWalletId" :active="true" :section="section" :receiving-settings="settings" @manage-wallets="section='wallets'" @view-assets="viewAssets" @settings-changed="load" @wallets-changed="acceptWallets"/>
  </section>
  <details class="crypto-wallet-disclosure"><summary><CircleCheck v-if="readiness.tone==='ready'" :size="14"/><ShieldAlert v-else :size="14"/><span>{{t(readiness.label)}}</span><span class="crypto-risk-label">{{t('保管与收款说明')}}</span><ChevronDown :size="14"/></summary><div><p>{{t(readiness.detail)}}</p><p>{{t('热钱包密钥由面板服务器保管，仅建议存放小额资金并定期归集；外部 xpub 只读，不能在面板签名。')}}</p><button type="button" :disabled="loading" @click="load"><RefreshCw :size="14" :class="{spin:loading}"/>{{t('刷新收款状态')}}</button></div></details>
 </section>
</template>
<style scoped>
.crypto-wallet-workspace{display:block;width:100%;flex:1;box-sizing:border-box;max-width:1120px;margin:0 auto;min-width:0}.crypto-wallet-header{display:flex;justify-content:space-between;align-items:center;gap:15px;margin-bottom:25px}.crypto-wallet-header>div{display:flex;align-items:center;gap:11px}.crypto-wallet-brand{display:grid;place-items:center;width:36px;height:36px;border-radius:11px;background:var(--accent-soft);color:var(--accent-text)}.crypto-wallet-header h1{font-size:22px;font-weight:600;letter-spacing:-.6px;margin:0}.crypto-wallet-header>a{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--muted);min-height:44px}.crypto-wallet-topnav{display:flex;gap:27px;border-bottom:1px solid var(--border);margin-bottom:25px}.crypto-wallet-topnav button{position:relative;min-height:48px;padding:0 2px 12px;background:none;border:0;border-radius:0;color:var(--muted);font-size:13px;font-weight:500}.crypto-wallet-topnav button.active{color:var(--text)}.crypto-wallet-topnav button.active:after{content:'';position:absolute;left:0;right:0;bottom:-1px;height:3px;border-radius:3px;background:var(--accent)}.crypto-wallet-topnav button:focus-visible{outline:2px solid var(--accent);outline-offset:-2px}.crypto-wallet-content{min-width:0}.crypto-wallet-error{display:flex;align-items:center;gap:12px;font-size:12px;margin:0 0 15px;padding:10px 0;flex-wrap:wrap}.crypto-wallet-error button{min-height:44px;margin-left:auto}.crypto-wallet-disclosure{margin-top:28px;color:var(--muted);border-top:1px solid var(--border);font-size:12px}.crypto-wallet-disclosure summary{display:flex;align-items:center;gap:7px;min-height:48px;cursor:pointer;list-style:none;line-height:1.7}.crypto-wallet-disclosure summary::-webkit-details-marker{display:none}.crypto-wallet-disclosure summary>svg:first-child{color:var(--accent-text);flex-shrink:0}.crypto-risk-label{margin-left:auto}.crypto-wallet-disclosure[open] summary>svg:last-child{transform:rotate(180deg)}.crypto-wallet-disclosure>div{padding:0 0 12px 21px;max-width:800px}.crypto-wallet-disclosure p{line-height:1.9;margin:0 0 9px}.crypto-wallet-disclosure button{display:flex;align-items:center;gap:6px;min-height:44px;font-size:12px;background:none}@media(max-width:650px){.crypto-wallet-header{margin-bottom:15px}.crypto-wallet-header h1{font-size:20px}.crypto-wallet-topnav{justify-content:space-between;gap:15px;margin-bottom:21px}.crypto-wallet-topnav button{font-size:12px;min-height:48px}.crypto-risk-label{display:none}.crypto-wallet-disclosure summary>svg:last-child{margin-left:auto}}
.crypto-return-gallery{display:flex;align-items:center;gap:6px;min-height:44px;border:0;background:none;color:var(--secondary);font-size:12px;margin-bottom:18px;padding:0}.crypto-gallery-empty{display:grid;justify-items:center;gap:12px;text-align:center;padding:55px 20px;border:1px dashed var(--border);border-radius:17px;color:var(--muted)}.crypto-gallery-empty h2{font-size:21px;font-weight:550;color:var(--text);margin:0}.crypto-gallery-empty p{font-size:12px;line-height:1.8;margin:0}.crypto-gallery-empty button{min-height:44px;margin-top:8px}
</style>
