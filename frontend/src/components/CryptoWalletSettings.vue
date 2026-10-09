<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { ArchiveRestore, Check, ChevronDown, Copy, Download, Eye, LoaderCircle, Plus, RefreshCw, ShieldAlert, WalletCards, X } from 'lucide-vue-next';
import { useApi, isCancelled, ApiError, onSessionExpired, onAccessDenied } from '../lib/api';
import { operationID, stamp } from '../lib/commerce';
import { canAllocate, CRYPTO_WALLET_CHAINS, walletChains, validateWalletChains, mergeCryptoWallets, parseCryptoBackup, saveGeneratedWallet, validateBackupPassword, CRYPTO_BACKUP_FILE_LIMIT, MAX_WALLET_NEXT_INDEX, type CryptoAddress, type CryptoAddressPage, type CryptoBackup, type CryptoWallet } from '../lib/cryptoWallet';
import { t } from '../i18n';
import CryptoPaymentSettings from './CryptoPaymentSettings.vue';
import CryptoTreasury from './CryptoTreasury.vue';
import { applyWalletList, shouldResumeWalletList } from '../lib/cryptoWalletList';

type Action = '' | 'hot' | 'xpub' | 'restore' | 'backup' | 'confirm' | 'reveal' | 'status' | 'allocate' | 'recover';
const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true });
const api = useApi('/commerce/crypto/admin');
const wallets = ref<CryptoWallet[]>([]), loading = ref(false), loaded = ref(false), busy = ref(false);
const error = ref(''), listError = ref(''), notice = ref(''), formError = ref(''), phase = ref('');
const dialog = ref<HTMLDialogElement | null>(null), action = ref<Action>(''), targetID = ref('');
const targetSnapshot = ref<CryptoWallet>();
const target = computed(() => targetSnapshot.value);
const name = ref(''), xpub = ref(''), path = ref("m/44'/60'/0'"), password = ref(''), backupPassword = ref(''), backupConfirmation = ref('');
const label = ref(''), riskAck = ref(false), recoveryAck = ref(false), recoveryIndex = ref(''), backupFile = ref<CryptoBackup | null>(null), backupFileName = ref('');
const mnemonic = ref(''), supportedChainIDs = ref<number[]>([56]), fundingNetworks = ref<Record<string,number>>({});
function fundingNetwork(wallet:CryptoWallet){const chains=walletChains(wallet);return chains.find(chain=>chain.chain_id===fundingNetworks.value[wallet.id])||chains[0]}
const selectedID = ref(''), addresses = ref<CryptoAddress[]>([]), addressLoading = ref(false), addressError = ref(''), nextAfter = ref<number | null>(null);
let disposed = false, listSequence = 0, addressSequence = 0, formGeneration = 0;
let secretTimer: ReturnType<typeof setTimeout> | undefined, request: AbortController | undefined, trigger: HTMLElement | null = null;
let reads = new AbortController(), resumeList = false;
// Only this explicit public metadata is retained across a request retry, never passwords or keys.
let formOperation: { publicKey: string; id: string; revision: number; enabled: boolean } | undefined;
let backupSelection = 0;
const titles: Record<Exclude<Action, ''>, string> = { hot: '新建热钱包', xpub: '导入外部 xpub', restore: '从备份恢复', backup: '下载加密备份', confirm: '确认已备份', reveal: '查看助记词', status: '修改钱包状态', allocate: '生成独立地址', recover: '核对恢复索引' };
const title = computed(() => action.value ? t(titles[action.value]) : '');

function clearSecrets() { password.value = ''; backupPassword.value = ''; backupConfirmation.value = ''; }
function clearMnemonic() { clearTimeout(secretTimer); mnemonic.value = ''; }
function pauseReads() {
  resumeList = resumeList || loading.value || !loaded.value;
  listSequence++; addressSequence++; loading.value = false; addressLoading.value = false;
  reads.abort(); reads = new AbortController();
}
function hideSecrets() {
  if (document.hidden) { close(); pauseReads(); }
  else if (shouldResumeWalletList(document.hidden, props.active, disposed, resumeList)) void load();
}
function syncWallet(wallet: CryptoWallet) {
  wallets.value = mergeCryptoWallets(wallets.value, [wallet]);
}
async function load() {
  if (!props.active || disposed) return;
  if (document.hidden) { resumeList = true; return; }
  resumeList = false;
  const sequence = ++listSequence; loading.value = true;
  try { const result = await api<{ items: CryptoWallet[] }>('/wallets', 'GET', undefined, { signal: reads.signal }); if (disposed || sequence !== listSequence || !props.active) return; const state = applyWalletList(result, wallets.value); wallets.value = state.wallets; loaded.value = state.loaded; listError.value = state.listError; }
  catch (reason) { if (!disposed && sequence === listSequence && props.active && !isCancelled(reason)) listError.value = t((reason as Error).message); }
  finally { if (sequence === listSequence) loading.value = false; }
}
async function loadAddresses(wallet: CryptoWallet, append = false) {
  if (!props.active || disposed) return;
  if (!append && selectedID.value === wallet.id) { selectedID.value = ''; addressSequence++; return; }
  if (!append) { selectedID.value = wallet.id; addresses.value = []; nextAfter.value = null; }
  const sequence = ++addressSequence; addressLoading.value = true; addressError.value = '';
  try {
    const page = await api<CryptoAddressPage>(`/wallets/${encodeURIComponent(wallet.id)}/addresses${append && nextAfter.value !== null ? '?after=' + nextAfter.value : ''}`, 'GET', undefined, { signal: reads.signal });
    if (disposed || sequence !== addressSequence || selectedID.value !== wallet.id) return;
    syncWallet(page.wallet); addresses.value = [...new Map([...addresses.value, ...page.items].map(address => [address.id, address])).values()]; nextAfter.value = page.next_after;
  } catch (reason) { if (!disposed && sequence === addressSequence && !isCancelled(reason)) addressError.value = t((reason as Error).message); }
  finally { if (sequence === addressSequence) addressLoading.value = false; }
}
function resetForm() {
  clearSecrets(); clearMnemonic(); name.value = ''; xpub.value = ''; path.value = "m/44'/60'/0'";
  label.value = ''; riskAck.value = false; recoveryAck.value = false; recoveryIndex.value = ''; backupFile.value = null; backupFileName.value = ''; formError.value = ''; phase.value = '';
  formOperation = undefined; supportedChainIDs.value = [56];
}
async function open(next: Exclude<Action, ''>, wallet?: CryptoWallet) {
  if (busy.value) return;
  formGeneration++; resetForm(); targetID.value = wallet?.id || ''; targetSnapshot.value = wallet ? { ...wallet } : undefined; action.value = next;
  recoveryIndex.value = String(wallet?.next_index ?? 0); trigger = document.activeElement as HTMLElement;
  await nextTick(); dialog.value?.showModal();
  dialog.value?.querySelector<HTMLInputElement>('input:not([type=checkbox]):not([type=file])')?.focus();
}
function close() {
  formGeneration++; request?.abort(); request = undefined; resetForm(); action.value = ''; targetID.value = ''; targetSnapshot.value = undefined;
  if (dialog.value?.open) dialog.value.close();
  trigger?.focus(); trigger = null;
}
async function readBackup(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0], generation = formGeneration;
  backupFile.value = null; backupFileName.value = ''; formError.value = '';
  if (!file) return;
  try {
    if (file.size > CRYPTO_BACKUP_FILE_LIMIT) throw new Error('钱包备份文件无效或过大');
    const parsed = parseCryptoBackup(await file.text());
    if (disposed || generation !== formGeneration) return;
    backupFile.value = parsed; backupFileName.value = file.name; backupSelection++;
  } catch (reason) { if (generation === formGeneration) formError.value = t((reason as Error).message); }
}
function download(backup: CryptoBackup, wallet: CryptoWallet) {
  const blob = new Blob([JSON.stringify(backup)], { type: 'application/json' }), url = URL.createObjectURL(blob);
  const link = document.createElement('a'); link.href = url; link.download = `guangyue-wallet-${wallet.id.replace(/[^a-zA-Z0-9_-]/g, '')}.json`;
  document.body.appendChild(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
}
async function submit() {
  if (busy.value || !action.value) return;
  const kind = action.value, wallet = target.value, generation = formGeneration;
  busy.value = true; formError.value = ''; error.value = ''; notice.value = ''; clearMnemonic();
  request = new AbortController(); const controller = request;
  const post = <T,>(route: string, body: Record<string, unknown>) => api<T>(route, 'POST', body, { signal: controller.signal });
  const current = () => !disposed && generation === formGeneration && !controller.signal.aborted;
  const publicKey = JSON.stringify([kind, targetID.value, name.value.trim(), xpub.value.trim(), path.value.trim(), label.value.trim(), riskAck.value, recoveryIndex.value, recoveryAck.value, backupSelection, supportedChainIDs.value]);
  if (!formOperation || formOperation.publicKey !== publicKey || kind === 'hot') {
    const latest = wallets.value.find(item => item.id === targetID.value);
    formOperation = { publicKey, id: operationID(), revision: latest?.revision ?? 0, enabled: latest?.enabled ?? false };
  }
  const operation = formOperation, id = operation.id; let created: CryptoWallet | undefined, creationSubmitted = false;
  try {
    if (kind === 'hot') {
      phase.value = t('正在加载本地钱包组件并创建钱包…');
      created = await saveGeneratedWallet(<T,>(route: string, method?: string, body?: unknown) => { creationSubmitted = true; return api<T>(route, method, body, { signal: controller.signal }); }, { name: name.value.trim(), password: password.value, operation_id: id, risk_ack: riskAck.value, supported_chain_ids: supportedChainIDs.value });
    } else if (kind === 'xpub') {
      if (!xpub.value.startsWith('xpub')) throw new Error('这里只接受外部钱包的 xpub，请勿输入助记词或私钥');
      created = await post<CryptoWallet>('/wallets/xpub', { name: name.value.trim(), xpub: xpub.value.trim(), path: path.value.trim(), supported_chain_ids: validateWalletChains(supportedChainIDs.value), password: password.value, operation_id: id });
    } else if (kind === 'restore') {
      if (!backupFile.value) throw new Error('请选择有效的加密钱包备份文件');
      created = await post<CryptoWallet>('/wallets/restore', { password: password.value, backup_password: backupPassword.value, backup: backupFile.value, operation_id: id });
    } else if (!wallet) { throw new Error('钱包记录已变化，请刷新后重试'); }
    else if (kind === 'backup') {
      validateBackupPassword(backupPassword.value, backupConfirmation.value, password.value);
      const backup = await post<CryptoBackup>(`/wallets/${wallet.id}/backup`, { password: password.value, backup_password: backupPassword.value });
      if (!current()) return;
      download(backup, wallet); close();
      notice.value = wallet.mode === 'hot' && !wallet.backup_confirmed ? t('加密备份已下载，请确认文件已安全保存，再点击“确认已备份”。') : t('加密备份已下载，请安全保存文件及口令。');
    } else if (kind === 'reveal') {
      const result = await post<{ mnemonic: string }>(`/wallets/${wallet.id}/reveal`, { password: password.value });
      if (!current()) { result.mnemonic = ''; return; }
      mnemonic.value = result.mnemonic; result.mnemonic = ''; secretTimer = setTimeout(clearMnemonic, 60000);
    } else if (kind === 'allocate') {
      const result = await post<{ wallet: CryptoWallet; address: CryptoAddress }>(`/wallets/${wallet.id}/addresses`, { password: password.value, operation_id: id, revision: operation.revision, label: label.value.trim() });
      if (!current()) return;
      addressSequence++; syncWallet(result.wallet); selectedID.value = wallet.id; addresses.value = [result.address]; nextAfter.value = null;
      close(); notice.value = t('独立地址已生成并保存');
      // Re-read immediately so existing addresses remain visible alongside the new record.
      selectedID.value = ''; await loadAddresses(result.wallet);
      if (selectedID.value === result.wallet.id && !addresses.value.some(address => address.id === result.address.id)) addresses.value = [result.address, ...addresses.value];
    } else if (kind === 'status') {
      created = await post<CryptoWallet>(`/wallets/${wallet.id}/status`, { password: password.value, operation_id: id, revision: operation.revision, enabled: !operation.enabled });
    } else if (kind === 'confirm') {
      created = await post<CryptoWallet>(`/wallets/${wallet.id}/backup/confirm`, { password: password.value, operation_id: id, revision: operation.revision });
    } else if (kind === 'recover') {
      const index = Number(recoveryIndex.value);
      if (!/^(0|[1-9]\d*)$/.test(recoveryIndex.value) || !Number.isSafeInteger(index) || index < wallet.next_index || index > MAX_WALLET_NEXT_INDEX || !recoveryAck.value) throw new Error('请核对最后分配索引，下一索引不能小于备份记录');
      created = await post<CryptoWallet>(`/wallets/${wallet.id}/recovery`, { password: password.value, operation_id: id, revision: operation.revision, next_index: index, recovery_ack: true });
    }
    if (created && current()) {
      syncWallet(created); close();
      notice.value = kind === 'hot' ? t('热钱包已创建，Gas 地址可提前充值；请先下载并确认备份，再启用收款。') : kind === 'restore' ? t('钱包已恢复，请核对恢复索引和启用状态。') : kind === 'recover' ? t('恢复索引已确认，启用钱包后可继续生成地址。') : t('钱包设置已保存');
      await nextTick();
      if(kind==='hot'||kind==='xpub')document.getElementById('crypto-wallet-'+created.id)?.scrollIntoView({block:'start',behavior:window.matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth'});
      await load();
    }
  } catch (reason) {
    if (current() && !isCancelled(reason)) {
      if (kind === 'hot' && creationSubmitted && !(reason instanceof ApiError)) { close(); error.value = t('创建结果尚未确认，已刷新钱包列表，请先核对是否已创建成功。'); }
      else formError.value = t((reason as Error).message);
    }
    if (reason instanceof ApiError && reason.status < 500) formOperation = undefined;
    // A lost response may follow a successful commit. Never retry with another seed under the old key.
    if (!disposed) await load();
  } finally {
    clearSecrets(); phase.value = ''; busy.value = false;
    if (request === controller) request = undefined;
  }
}
async function copyAddress(value: string) {
  try { await navigator.clipboard.writeText(value); notice.value = t('地址已复制'); }
  catch { error.value = t('复制失败，请手动选择地址'); }
}
const clearSession = () => { close(); listSequence++; addressSequence++; reads.abort(); wallets.value = []; addresses.value = []; listError.value = ''; loaded.value = false; resumeList = false; };
const unsubscribeSession = onSessionExpired(clearSession), unsubscribeAccess = onAccessDenied(clearSession);
watch(() => props.active, active => { if (!active) { close(); pauseReads(); } else void load(); });
onMounted(() => { void load(); document.addEventListener('visibilitychange', hideSecrets); });
onUnmounted(() => { disposed = true; listSequence++; addressSequence++; formGeneration++; request?.abort(); reads.abort(); resetForm(); dialog.value?.close(); document.removeEventListener('visibilitychange', hideSecrets); unsubscribeSession(); unsubscribeAccess(); });
</script>

<template>
  <section class="crypto-wallets" :aria-busy="loading || busy">
    <header class="crypto-heading"><div><h3><WalletCards :size="20" />{{ t('加密钱包与地址') }}</h3><p>{{ t('管理热钱包或导入外部 xpub，按需生成独立 EVM 地址。') }}</p></div><span class="crypto-badge">{{ t('链上收款与资产管理') }}</span></header>
    <p class="crypto-scope">{{ t('用户选择加密支付后自动获得专属地址；到账确认、套餐开通与资产提取均可在面板内跟踪。') }}</p>
    <div class="crypto-risk"><ShieldAlert :size="20" /><p>{{ t('密钥保存在面板服务器，仅建议存放小额资金并定期转走') }}<span>{{ t('外部 xpub 模式仅保存公钥，资金由原钱包控制。') }}</span></p></div>
    <p v-if="listError" class="error" role="alert">{{ listError }}</p><p v-if="error" class="error" role="alert">{{ error }}</p><p v-if="notice" class="integration-notice" role="status">{{ notice }}</p>
    <div class="crypto-toolbar"><button type="button" class="primary" :disabled="busy" @click="open('hot')"><Plus :size="16" />{{ t('新建热钱包') }}</button><button type="button" :disabled="busy" @click="open('xpub')">{{ t('导入外部 xpub') }}</button><button type="button" :disabled="busy" @click="open('restore')"><ArchiveRestore :size="16" />{{ t('从备份恢复') }}</button><button type="button" class="crypto-refresh" :disabled="busy || loading" @click="error = ''; listError = ''; load()"><RefreshCw :size="16" :class="{ spin: loading }" />{{ t('刷新') }}</button></div>
    <CryptoPaymentSettings :wallets="wallets" :active="active"/><CryptoTreasury v-if="wallets.length" :wallets="wallets" :active="active"/><p v-if="!loaded && loading" role="status">{{ t('正在读取钱包…') }}</p><p v-else-if="loaded && !wallets.length" class="crypto-empty">{{ t('还没有钱包。新建热钱包即可开始，或导入已有钱包的收款公钥。') }}</p>
    <article v-for="wallet in wallets" :key="wallet.id" :id="'crypto-wallet-'+wallet.id" class="crypto-wallet-card">
      <header><div><strong>{{ wallet.name }}</strong><span>{{ wallet.mode === 'hot' ? t('面板热钱包') : t('外部 xpub · 只读') }}</span></div><div class="crypto-badges"><span v-if="wallet.next_index >= MAX_WALLET_NEXT_INDEX" class="crypto-badge warning">{{ t('地址索引已用尽') }}</span><span v-if="wallet.recovery_required" class="crypto-badge warning">{{ t('待核对恢复索引') }}</span><span v-else-if="wallet.mode === 'hot' && !wallet.backup_confirmed" class="crypto-badge warning">{{ t('待备份确认') }}</span><span :class="['crypto-badge', { enabled: wallet.enabled }]">{{ wallet.enabled ? t('已启用') : t('已暂停生成地址') }}</span></div></header>
      <dl class="crypto-facts"><div><dt>{{ t('派生路径') }}</dt><dd>{{ wallet.path }}</dd></div><div><dt>{{ t('下一地址索引') }}</dt><dd>{{ wallet.next_index }}</dd></div><div><dt>{{ t('创建时间') }}</dt><dd>{{ stamp(wallet.created) }}</dd></div></dl>
      <div class="crypto-network-badges"><span class="crypto-badge" v-for="chain in walletChains(wallet)" :key="chain.chain_id">{{chain.name}}</span></div>
      <section v-if="wallet.mode==='hot'&&wallet.funding_address" class="crypto-funding"><div class="crypto-funding-heading"><h4>{{t('Gas 手续费地址')}}</h4><label v-if="walletChains(wallet).length>1">{{t('充值网络')}}<select :value="fundingNetwork(wallet)?.chain_id" @change="fundingNetworks[wallet.id]=Number(($event.target as HTMLSelectElement).value)"><option v-for="chain in walletChains(wallet)" :key="chain.chain_id" :value="chain.chain_id">{{chain.name}} · {{chain.native_symbol}}</option></select></label><span v-else class="crypto-badge">{{fundingNetwork(wallet)?.name}}</span></div><p v-if="fundingNetwork(wallet)" class="crypto-note">{{t('仅充值本网络原生币作为手续费')}} · {{fundingNetwork(wallet)?.native_symbol}}</p><div class="crypto-funding-address"><input :value="wallet.funding_address" :aria-label="t('Gas 手续费地址')+' · '+wallet.name" readonly/><button type="button" :aria-label="t('复制 Gas 地址')+' · '+wallet.name" @click="copyAddress(wallet.funding_address)"><Copy :size="16"/></button></div><p class="crypto-note">{{t('这是固定的手续费资金地址，创建钱包后即可充值。每条网络的余额独立，不作为用户订单付款地址。')}}</p><details class="crypto-detail"><summary>{{t('查看地址路径')}}</summary><code>{{wallet.funding_path}}</code></details></section>
      <details class="crypto-detail"><summary>{{ t('钱包公钥与校验地址') }}</summary><p>{{ t('校验地址用于核对钱包，不对应套餐订单。请勿把它当作订单付款地址。') }}</p><label>{{ t('首个地址') }}<input :value="wallet.first_address" readonly /></label><label>{{ t('扩展公钥') }}<textarea :value="wallet.xpub" readonly rows="3" /></label><p>{{ wallet.engine }}<span v-if="wallet.engine_version"> · {{ wallet.engine_version }}</span></p></details>
      <p v-if="wallet.recovery_required" class="crypto-note">{{ t('恢复后先核对旧面板最后分配的地址索引。不能让两个面板同时从同一钱包生成地址。') }}</p>
      <p v-else-if="wallet.mode === 'hot' && !wallet.backup_confirmed" class="crypto-note">{{ t('请下载加密备份并确认已保存，随后才能生成地址。') }}</p>
      <div class="crypto-actions"><button type="button" :disabled="busy || !canAllocate(wallet)" @click="open('allocate', wallet)"><Plus :size="15" />{{ t('生成独立地址') }}</button><button type="button" :disabled="busy" :aria-expanded="selectedID === wallet.id" @click="loadAddresses(wallet)">{{ t('地址记录') }}<ChevronDown :size="15" /></button><button type="button" :disabled="busy" @click="open('backup', wallet)"><Download :size="15" />{{ t('下载加密备份') }}</button><button v-if="wallet.mode === 'hot' && !wallet.backup_confirmed" type="button" :disabled="busy" @click="open('confirm', wallet)"><Check :size="15" />{{ t('确认已备份') }}</button><button v-if="wallet.recovery_required" type="button" :disabled="busy" @click="open('recover', wallet)">{{ t('核对恢复索引') }}</button><button type="button" :disabled="busy || (!wallet.enabled && wallet.recovery_required)" @click="open('status', wallet)">{{ wallet.enabled ? t('暂停生成地址') : t('启用钱包') }}</button></div>
      <details v-if="wallet.mode === 'hot'" class="crypto-detail crypto-withdraw"><summary>{{ t('外部钱包恢复与密钥') }}</summary><p>{{ t('可通过加密备份恢复本面板钱包，或在兼容的外部钱包导入助记词并使用相同派生路径。不是所有钱包都会自动显示全部已分配地址，请逐一核对索引。') }}</p><p>{{ t('转出资产前请核对已选择的网络和代币合约；不同网络的资产与 Gas 余额独立。') }}</p><button type="button" :disabled="busy" @click="open('reveal', wallet)"><Eye :size="15" />{{ t('查看助记词') }}</button></details>
      <section v-if="selectedID === wallet.id" class="crypto-addresses" :aria-busy="addressLoading"><p v-if="addressError" class="error" role="alert">{{ addressError }}</p><p v-if="addressLoading && !addresses.length" role="status">{{ t('正在读取地址…') }}</p><p v-else-if="!addresses.length && !addressError">{{ t('还没有已分配的独立地址') }}</p><article v-for="address in addresses" :key="address.id"><div><strong>#{{ address.index }}<span v-if="address.label"> · {{ address.label }}</span></strong><code>{{ address.address }}</code><small>{{ address.path }} · {{ stamp(address.created) }}</small></div><button type="button" :aria-label="t('复制地址') + ' ' + address.index" @click="copyAddress(address.address)"><Copy :size="16" /></button></article><button v-if="nextAfter !== null" type="button" :disabled="addressLoading" @click="loadAddresses(wallet, true)">{{ t('加载更多') }}</button></section>
    </article>

    <dialog ref="dialog" class="crypto-dialog" aria-labelledby="crypto-dialog-title" @cancel.prevent="close" @close="action && close()">
      <form v-if="action" @submit.prevent="submit">
        <header><div><span>{{ t('加密钱包') }}</span><h3 id="crypto-dialog-title">{{ title }}</h3><p v-if="target">{{ target.name }}</p></div><button type="button" class="icon" :aria-label="t('关闭')" @click="close"><X :size="20" /></button></header>
        <p v-if="formError" class="error" role="alert">{{ formError }}</p>
        <fieldset :disabled="busy">
          <template v-if="action === 'hot'"><p class="crypto-note">{{ t('官方 Trust Wallet Core 在浏览器中创建钱包，随后通过安全连接保存到服务器加密库。无需连接浏览器钱包。') }}</p><label>{{ t('钱包名称') }}<input v-model.trim="name" required maxlength="80" :placeholder="t('例如：小额收款钱包')" /></label><label class="inline-check"><input v-model="riskAck" type="checkbox" required /><span>{{ t('我了解密钥保存在面板服务器，仅存放小额资金，并定期转走资产。') }}</span></label></template>
          <template v-else-if="action === 'xpub'"><p class="crypto-note">{{ t('导入外部钱包导出的收款公钥。普通 OKX 连接不会提供 xpub，请勿输入助记词或私钥。') }}</p><label>{{ t('钱包名称') }}<input v-model.trim="name" required maxlength="80" /></label><label>{{ t('扩展公钥') }}<textarea v-model.trim="xpub" required rows="4" maxlength="160" placeholder="xpub…" autocapitalize="off" autocomplete="off" spellcheck="false" /></label><label>{{ t('收款分支路径') }}<input v-model.trim="path" required maxlength="100" spellcheck="false" /></label><p class="crypto-note">{{ t('支持账户路径 m/44\'/60\'/0\'，或其收款分支 m/44\'/60\'/0\'/0；路径必须与导出的 xpub 一致。') }}</p></template>
          <template v-else-if="action === 'restore'"><p class="crypto-note">{{ t('恢复加密备份中的钱包与已用索引，不会从零开始覆盖旧地址。恢复后仍需核对备份之后是否分配过新地址。') }}</p><label>{{ t('加密备份文件') }}<input type="file" accept=".json,application/json" required @change="readBackup" /></label><p v-if="backupFileName" class="crypto-note">{{ backupFileName }}</p><label>{{ t('备份口令') }}<input v-model="backupPassword" type="password" required autocomplete="off" /></label></template>
          <template v-else-if="action === 'backup'"><p class="crypto-note">{{ target?.mode === 'watch_only' ? t('备份包含外部公钥、派生配置和地址索引，不包含原钱包的私钥。请将文件与备份口令分开保管。') : t('备份包含钱包密钥、派生配置和地址索引。请将文件与备份口令分开保管；遗失口令后无法解密此文件。') }}</p><label>{{ t('备份口令') }}<input v-model="backupPassword" type="password" required minlength="12" autocomplete="new-password" /></label><label>{{ t('再次输入备份口令') }}<input v-model="backupConfirmation" type="password" required minlength="12" autocomplete="new-password" /></label><p class="crypto-note">{{ t('至少 12 个字符，且不能与管理员密码相同。') }}</p></template>
          <template v-else-if="action === 'confirm'"><p class="crypto-note">{{ t('请先确认下载的备份文件可找到，且备份口令已另行保存。确认后允许为此钱包生成地址。') }}</p><label class="inline-check"><input type="checkbox" required /><span>{{ t('我已安全保存加密备份及其口令') }}</span></label></template>
          <template v-else-if="action === 'reveal'"><p class="crypto-note">{{ t('助记词可控制此钱包的全部地址。仅在私密环境查看，不要发送给客服或输入不可信网站。显示后 60 秒或离开当前页面会自动隐藏。') }}</p><div v-if="mnemonic" class="crypto-mnemonic" data-private><p>{{ mnemonic }}</p><button type="button" @click="clearMnemonic">{{ t('立即隐藏') }}</button></div></template>
          <template v-else-if="action === 'allocate'"><p class="crypto-note">{{ t('生成并永久登记下一个独立地址，不自动建立订单或开通套餐。暂停钱包不会删除已有地址。') }}</p><label>{{ t('地址备注') }}<input v-model.trim="label" maxlength="120" :placeholder="t('可选，便于识别用途')" /></label></template>
          <template v-else-if="action === 'status'"><p class="crypto-note">{{ target?.enabled ? t('暂停后不再生成新地址，已有地址、密钥和资金记录继续保留。') : t('启用后允许生成新的独立地址，不代表链上自动支付已启用。') }}</p></template>
          <template v-else-if="action === 'recover'"><p class="crypto-note">{{ t('核对旧面板最后分配的地址索引，填写该索引加一。备份文件无法知道备份之后的新分配记录；请先停止旧面板分配。') }}</p><label>{{ t('确认下一地址索引') }}<input v-model="recoveryIndex" type="number" :min="target?.next_index || 0" :max="MAX_WALLET_NEXT_INDEX" step="1" required /></label><label class="inline-check"><input v-model="recoveryAck" type="checkbox" required /><span>{{ t('已核对全部分配记录，旧面板不再从此钱包分配地址') }}</span></label></template>
          <div v-if="action==='hot'||action==='xpub'" class="crypto-wallet-networks"><strong>{{t('钱包支持的网络')}}</strong><div><label v-for="chain in CRYPTO_WALLET_CHAINS" :key="chain.chain_id" class="inline-check"><input v-model="supportedChainIDs" type="checkbox" :value="chain.chain_id"/><span>{{chain.name}} · {{chain.native_symbol}}</span></label></div><p class="crypto-note">{{t('默认使用 BNB Smart Chain。只会为所选网络显示收款与手续费操作。')}}</p></div>
          <label v-if="!mnemonic">{{ t('管理员当前密码') }}<input v-model="password" type="password" required autocomplete="current-password" /></label>
          <p v-if="busy" class="crypto-working" role="status"><LoaderCircle :size="17" class="spin" />{{ phase || t('正在处理，请稍候…') }}</p>
          <footer><button v-if="!mnemonic" type="submit" class="primary" :disabled="busy || (action === 'restore' && !backupFile) || (['hot','xpub'].includes(action)&&!supportedChainIDs.length)">{{ busy ? t('处理中…') : title }}</button><button type="button" @click="close">{{ t('关闭') }}</button></footer>
        </fieldset>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.crypto-wallets{display:grid;gap:16px;margin-top:24px;padding-top:24px;border-top:1px solid var(--border);min-width:0}.crypto-heading,.crypto-wallet-card>header{display:flex;justify-content:space-between;gap:16px;align-items:flex-start}.crypto-heading h3{display:flex;gap:9px;align-items:center;margin:0;font-size:18px}.crypto-heading p,.crypto-scope{font-size:13px;line-height:1.75;color:var(--secondary);margin:8px 0 0}.crypto-scope{margin:0}.crypto-badge{display:inline-flex;padding:4px 9px;border:1px solid var(--border);border-radius:7px;font-size:11px;white-space:nowrap;color:var(--secondary);background:var(--surface-raised)}.crypto-badge.enabled{color:var(--accent-text);background:var(--accent-soft)}.crypto-badge.warning{color:var(--text);background:color-mix(in srgb,#c99a45 14%,var(--surface))}.crypto-badges{display:flex;flex-wrap:wrap;gap:6px;justify-content:flex-end}.crypto-risk{display:flex;gap:11px;align-items:flex-start;padding:14px 16px;background:color-mix(in srgb,#c99a45 9%,var(--surface));border:1px solid color-mix(in srgb,#c99a45 30%,var(--border));border-radius:10px}.crypto-risk>svg{flex-shrink:0;margin-top:2px}.crypto-risk p{margin:0;font-size:13px;line-height:1.7}.crypto-risk span{display:block;font-size:12px;color:var(--secondary);margin-top:4px}.crypto-toolbar,.crypto-actions{display:flex;gap:8px;flex-wrap:wrap}.crypto-toolbar button,.crypto-actions button{font-size:12px;min-height:40px}.crypto-refresh{margin-left:auto}.crypto-empty{padding:22px;border:1px dashed var(--border);border-radius:10px;text-align:center;font-size:13px;color:var(--secondary);line-height:1.8}.crypto-wallet-card{padding:19px;border:1px solid var(--border);border-radius:13px;display:grid;gap:15px;min-width:0}.crypto-wallet-card>header>div:first-child{display:grid;gap:6px;min-width:0}.crypto-wallet-card strong{font-size:14px;overflow-wrap:anywhere}.crypto-wallet-card>header span:not(.crypto-badge){font-size:12px;color:var(--secondary)}.crypto-facts{display:grid;grid-template-columns:1fr auto 1fr;gap:16px;margin:0;font-size:12px}.crypto-facts dt{color:var(--secondary);margin-bottom:5px}.crypto-facts dd{margin:0;overflow-wrap:anywhere}.crypto-detail{font-size:12px;min-width:0}.crypto-detail summary{cursor:pointer;color:var(--accent-text);min-height:26px;line-height:1.8}.crypto-detail p,.crypto-note{font-size:12px;line-height:1.8;color:var(--secondary);margin:0}.crypto-detail p{margin:8px 0}.crypto-detail label{margin:10px 0}.crypto-detail textarea,.crypto-detail input{font-size:12px;resize:vertical;overflow-wrap:anywhere}.crypto-withdraw{padding-top:10px;border-top:1px solid var(--border)}.crypto-withdraw button{font-size:12px}.crypto-addresses{display:grid;gap:9px;border-top:1px solid var(--border);padding-top:12px;font-size:12px}.crypto-addresses>article{display:flex;gap:10px;align-items:center;padding:10px 0;border-bottom:1px solid var(--border)}.crypto-addresses>article>div{display:grid;gap:6px;flex:1;min-width:0}.crypto-addresses code{font-size:12px;overflow-wrap:anywhere;white-space:normal}.crypto-addresses small{color:var(--secondary);overflow-wrap:anywhere}.crypto-addresses>article button{flex-shrink:0;min-height:40px}.crypto-dialog{width:min(570px,calc(100vw - 28px));max-height:calc(100dvh - 32px);overflow:auto;padding:25px;border:1px solid var(--border);border-radius:16px;background:var(--surface);color:var(--text);box-shadow:0 24px 80px #0005}.crypto-dialog::backdrop{background:#05110dde;backdrop-filter:blur(4px)}.crypto-dialog form{display:grid;gap:18px}.crypto-dialog header{display:flex;justify-content:space-between;gap:15px;align-items:flex-start}.crypto-dialog h3{font-size:20px;margin:5px 0}.crypto-dialog header span,.crypto-dialog header p{font-size:12px;color:var(--secondary);margin:0}.crypto-dialog fieldset{display:grid;gap:17px;border:0;padding:0;margin:0;min-width:0}.crypto-dialog label{margin:0;font-size:13px}.crypto-dialog label input,.crypto-dialog label textarea{width:100%;max-width:100%;min-height:44px;margin-top:7px}.crypto-dialog label textarea{resize:vertical}.crypto-dialog .inline-check{display:flex;flex-direction:row;align-items:flex-start;gap:10px;line-height:1.8}.crypto-dialog .inline-check input{width:17px;min-height:17px;margin:3px 0 0;flex-shrink:0}.crypto-dialog footer{display:flex;gap:9px;flex-wrap:wrap;padding-top:6px}.crypto-dialog button{min-height:44px}.crypto-dialog .error{font-size:13px;line-height:1.8}.crypto-working{display:flex;align-items:center;gap:8px;font-size:13px;margin:0;color:var(--accent-text)}.crypto-mnemonic{padding:17px;border:1px solid var(--border);border-radius:10px;background:var(--surface-raised);font-family:monospace;font-size:14px;line-height:1.9;overflow-wrap:anywhere}.crypto-mnemonic p{margin:0 0 12px;user-select:text}.crypto-mnemonic button{font-family:inherit;font-size:12px}@media(max-width:650px){.crypto-heading,.crypto-wallet-card>header{flex-direction:column;gap:10px}.crypto-badges{justify-content:flex-start}.crypto-facts{grid-template-columns:1fr 1fr;gap:13px}.crypto-facts>div:last-child{grid-column:1/-1}.crypto-toolbar button,.crypto-actions button{flex:1;min-height:44px}.crypto-refresh{margin:0}.crypto-wallet-card{padding:15px}.crypto-dialog{padding:19px}.crypto-dialog footer button{flex:1}.crypto-toolbar{display:grid;grid-template-columns:1fr 1fr}}
.crypto-network-badges{display:flex;gap:6px;flex-wrap:wrap}.crypto-wallet-card{scroll-margin-top:24px}.crypto-wallet-networks{display:grid;gap:10px}.crypto-wallet-networks>strong{font-size:13px}.crypto-wallet-networks>div{display:grid;gap:10px}.crypto-wallet-networks .inline-check{padding:11px 13px;border:1px solid var(--border);border-radius:9px;background:var(--surface-raised)}.crypto-funding{display:grid;gap:10px;padding:16px;background:var(--surface-raised);border:1px solid var(--border);border-radius:10px;min-width:0}.crypto-funding-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.crypto-funding h4{margin:0;font-size:13px}.crypto-funding-heading label{display:flex;align-items:center;gap:8px;font-size:12px;margin:0}.crypto-funding-heading select{min-height:40px;width:auto}.crypto-funding-address{display:flex;gap:8px;min-width:0}.crypto-funding-address input{flex:1;min-width:0;font-family:monospace;font-size:12px;min-height:44px}.crypto-funding-address button{min-height:44px;flex-shrink:0}.crypto-funding code{overflow-wrap:anywhere;font-size:12px}
</style>
