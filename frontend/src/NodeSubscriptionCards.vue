<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { Check, Clock3, Copy, FileSearch, LoaderCircle, QrCode, RefreshCw, ShieldCheck, X } from 'lucide-vue-next';
import QRCode from 'qrcode';
import CountryMark from './CountryMark.vue';
import QualityTags from './QualityTags.vue';
import { locale, t } from './i18n';
import type { IPQuality } from './quality';

export type SubscriptionNode = {
  dns?: {mode: string; doh?: string; ipv6?: string};
  id: string;
  name: string;
  protocol: 'vless' | 'hy2';
  uri: string;
  probe_ip: string;
  country: string;
  country_code: string;
  checked_at: number;
  quality: IPQuality | null;
};
const props = defineProps<{ nodes: SubscriptionNode[]; active: boolean; pool: 'private' | 'public'; loading?: boolean }>();
const emit = defineEmits<{ refresh: [] }>();
const search = ref(''), copied = ref(''), copyError = ref('');
const availableNodes = computed(() => props.active ? props.nodes.filter(node => !!node.id && !!node.uri) : []);
const visibleNodes = computed(() => {
  const query = search.value.trim().toLowerCase();
  return availableNodes.value.filter(node => !query || [node.name, node.probe_ip, node.country, node.country_code, node.protocol, node.protocol === 'hy2' ? 'hysteria2' : 'vless'].some(value => (value || '').toLowerCase().includes(query)));
});
const reportCount = computed(() => availableNodes.value.filter(node => !!node.quality?.at).length);
const protocolName = (node: SubscriptionNode) => node.protocol === 'hy2' ? 'HY2' : 'VLESS';
const nodeSNI = (node: SubscriptionNode) => { try { return new URL(node.uri).searchParams.get('sni') || ''; } catch { return ''; } };
const reportDate = (node: SubscriptionNode) => node.quality?.at ? new Date(node.quality.at * 1000).toLocaleString(locale.value) : t('尚未检测');
let copiedTimer: ReturnType<typeof setTimeout> | undefined, alive = true;
async function copyNodes(nodes: SubscriptionNode[], marker: string) {
  if (!nodes.length) return;
  copyError.value = '';
  try {
    await navigator.clipboard.writeText(nodes.map(node => node.uri).join('\n'));
    if (!alive) return;
    copied.value = marker;
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => { copied.value = ''; }, 2500);
  } catch {
    if (alive) copyError.value = t('无法复制链接，请允许剪贴板访问或使用节点二维码。');
  }
}

const qrDialog = ref<HTMLDialogElement | null>(null), qrNodeID = ref(''), qrImage = ref(''), qrLoading = ref(false), qrError = ref('');
const qrNode = computed(() => availableNodes.value.find(node => node.id === qrNodeID.value));
let qrSequence = 0, qrURI = '', qrTrigger: HTMLElement | null = null;
async function showQR(node: SubscriptionNode, event: MouseEvent) {
  const sequence = ++qrSequence;
  qrNodeID.value = node.id;
  qrURI = node.uri;
  qrImage.value = '';
  qrError.value = '';
  qrLoading.value = true;
  qrTrigger = event.currentTarget as HTMLElement;
  await nextTick();
  if (!alive || sequence !== qrSequence || !qrNode.value) return;
  qrDialog.value?.showModal();
  try {
    const image = await QRCode.toDataURL(node.uri, { width: 320, margin: 3, errorCorrectionLevel: 'M', color: { dark: '#172c29', light: '#ffffff' } });
    if (alive && sequence === qrSequence && qrNode.value?.uri === node.uri) qrImage.value = image;
  } catch {
    if (alive && sequence === qrSequence) qrError.value = t('节点二维码生成失败，请使用复制链接。');
  } finally {
    if (alive && sequence === qrSequence) qrLoading.value = false;
  }
}
function clearQR() {
  qrSequence++;
  qrNodeID.value = '';
  qrURI = '';
  qrImage.value = '';
  qrLoading.value = false;
  qrError.value = '';
  const trigger = qrTrigger;
  qrTrigger = null;
  nextTick(() => { if (alive && trigger?.isConnected) trigger.focus(); });
}
function closeQR() {
  if (qrDialog.value?.open) qrDialog.value.close();
  else clearQR();
}
function clickBackdrop(event: MouseEvent) {
  const dialog = qrDialog.value;
  if (!dialog || event.target !== dialog) return;
  const box = dialog.getBoundingClientRect();
  if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) closeQR();
}
watch([() => props.nodes, () => props.active, () => props.pool], () => {
  if (qrNodeID.value && (!qrNode.value || qrNode.value.uri !== qrURI)) closeQR();
});
onUnmounted(() => { alive = false; qrSequence++; clearTimeout(copiedTimer); qrDialog.value?.close(); });
</script>

<template>
  <section class="subscription-nodes" :aria-label="t('订阅节点与质量')" :aria-busy="loading">
    <header class="subscription-nodes-heading">
      <div><span class="subscription-nodes-eyebrow"><ShieldCheck :size="13"/>{{ pool === 'public' ? t('公共订阅') : t('普通订阅') }}</span><h2>{{ t('节点与质量') }}</h2><p>{{ t('在同一张节点卡片中复制链接、查看二维码和质量报告。') }}</p></div>
      <div class="subscription-nodes-tools">
        <button v-if="visibleNodes.length > 1" :disabled="loading" @click="copyNodes(visibleNodes, 'all')"><Check v-if="copied === 'all'" :size="14"/><Copy v-else :size="14"/>{{ copied === 'all' ? t('已复制') : t('复制当前节点') }}</button>
        <button class="text-button" :disabled="loading" @click="emit('refresh')"><RefreshCw :size="14" :class="{ spin: loading }"/>{{ t('刷新节点与报告') }}</button>
      </div>
    </header>
    <p v-if="copyError" class="subscription-copy-error" role="alert">{{ copyError }}</p>
    <span class="subscription-nodes-status" role="status" aria-live="polite">{{ copied ? t('节点链接已复制') : '' }}</span>
    <div v-if="!availableNodes.length" class="subscription-nodes-empty" role="status"><FileSearch :size="30"/><h3>{{ t('当前筛选下暂无已授权节点') }}</h3><p>{{ t('请检查成员权限、账号有效期或协议筛选；节点可用后会在这里显示。') }}</p></div>
    <template v-else>
      <div class="subscription-nodes-summary"><span>{{ t('已授权节点') }}<strong>{{ availableNodes.length }}</strong></span><span>{{ t('已有报告') }}<strong>{{ reportCount }}</strong></span><span class="subscription-readonly"><ShieldCheck :size="13"/>{{ t('只读报告') }}</span></div>
      <label v-if="availableNodes.length > 3 || search" class="subscription-node-search"><span>{{ t('筛选节点') }}</span><input v-model="search" type="search" :placeholder="t('搜索名称、IP、国家或协议')"/></label>
      <div class="subscription-node-grid">
        <article v-for="node in visibleNodes" :key="node.id" class="subscription-node-card">
          <header><CountryMark :code="node.country_code" :country="node.country"/><div><h3>{{ node.name }}</h3><p>{{ node.probe_ip || t('出口待确认') }}</p></div><span :class="['tag', node.protocol === 'hy2' ? 'hy2' : 'vless']">{{ protocolName(node) }}</span></header>
          <div v-if="nodeSNI(node)" class="subscription-node-sni"><span>SNI</span><strong>{{nodeSNI(node)}}</strong></div><div v-if="node.dns?.mode === 'secure'" class="subscription-node-sni"><ShieldCheck :size="12"/><span>{{t('出口 DNS 防护')}}</span><strong>{{node.dns.ipv6 === 'block' ? t('IPv6 已阻止') : 'IPv4 / IPv6'}}</strong></div>
          <div class="subscription-node-quality"><QualityTags :value="node.quality?.at ? node.quality : undefined" :name="node.name" read-only/></div>
          <div class="subscription-node-report"><Clock3 :size="12"/><span>{{ t('报告时间') }} · {{ reportDate(node) }}</span></div>
          <footer><button class="primary" :aria-label="t('复制节点链接') + ' · ' + protocolName(node) + ' · ' + node.name" @click="copyNodes([node], node.id)"><Check v-if="copied === node.id" :size="15"/><Copy v-else :size="15"/>{{ copied === node.id ? t('已复制') : t('复制链接') }}</button><button :aria-label="t('查看节点二维码') + ' · ' + protocolName(node) + ' · ' + node.name" @click="showQR(node, $event)"><QrCode :size="16"/>{{ t('节点二维码') }}</button></footer>
        </article>
      </div>
      <p v-if="!visibleNodes.length" class="subscription-nodes-no-results">{{ t('没有匹配的节点') }}</p>
      <p v-if="availableNodes.some(n=>n.dns?.mode === 'secure')" class="subscription-nodes-note">{{t('Mihomo 完整订阅包含经节点解析的 DNS 配置。单个节点链接与二维码不携带客户端 DNS 设置，请在客户端启用 DNS 接管。')}}</p><p class="subscription-nodes-note">{{ t('质量报告由管理员或后台检测生成；刷新仅重新读取节点和已有报告。') }}</p>
    </template>
  </section>
  <Teleport to="body">
    <dialog ref="qrDialog" class="subscription-node-qr" aria-labelledby="subscription-node-qr-title" @cancel.prevent="closeQR" @close="clearQR" @click="clickBackdrop">
      <template v-if="qrNode">
        <header><div><span>{{ protocolName(qrNode) }} · {{ pool === 'public' ? t('公共订阅') : t('普通订阅') }}</span><h2 id="subscription-node-qr-title">{{ t('节点二维码') }}</h2></div><button class="icon" :aria-label="t('关闭节点二维码')" autofocus @click="closeQR"><X :size="20"/></button></header>
        <p class="subscription-node-qr-name">{{ qrNode.name }}</p>
        <div class="subscription-node-qr-image" :aria-busy="qrLoading"><LoaderCircle v-if="qrLoading" :size="28" class="spin"/><img v-else-if="qrImage" :src="qrImage" :alt="t('当前节点链接二维码')" width="320" height="320"/><p v-else-if="qrError" role="alert">{{ qrError }}</p></div>
        <p class="subscription-node-qr-note">{{ t('使用代理客户端扫描，导入当前节点。') }}</p>
        <button class="primary" @click="copyNodes([qrNode], qrNode.id)"><Check v-if="copied === qrNode.id" :size="15"/><Copy v-else :size="15"/>{{ copied === qrNode.id ? t('已复制') : t('复制节点链接') }}</button>
        <p v-if="copyError" class="subscription-copy-error" role="alert">{{ copyError }}</p>
      </template>
    </dialog>
  </Teleport>
</template>

<style scoped>
.subscription-node-sni{display:flex;align-items:baseline;gap:8px;min-width:0;color:var(--muted);font-size:10px;margin-top:13px;line-height:1.7}.subscription-node-sni>span{flex-shrink:0}.subscription-node-sni strong{color:var(--secondary);font-weight:400;overflow-wrap:anywhere}
.subscription-nodes{margin-top:28px;border:1px solid var(--border);border-radius:12px;padding:24px;background:var(--surface);min-width:0}.subscription-nodes-heading{display:flex;align-items:center;justify-content:space-between;gap:18px}.subscription-nodes-eyebrow{display:flex;align-items:center;gap:7px;color:var(--accent-text);font-size:11px}.subscription-nodes-heading h2{font-size:18px;margin:9px 0 7px;font-weight:600}.subscription-nodes-heading p{font-size:12px;color:var(--muted);margin:0;line-height:1.7}.subscription-nodes-tools{display:flex;gap:9px;flex-shrink:0}.subscription-nodes-tools button{font-size:11px;min-height:33px}.subscription-nodes-summary{display:flex;align-items:center;gap:23px;padding:19px 0;border-bottom:1px solid var(--border);margin-bottom:19px;font-size:11px;color:var(--muted)}.subscription-nodes-summary>span{display:flex;gap:10px;align-items:center}.subscription-nodes-summary strong{color:var(--text);font-size:16px;font-weight:600}.subscription-readonly{margin-left:auto;color:var(--accent-text)}.subscription-node-search{display:flex;align-items:center;gap:14px;margin-bottom:18px;font-size:11px;color:var(--muted)}.subscription-node-search input{flex:1;max-width:420px;font-size:11px;min-width:0}.subscription-node-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:15px}.subscription-node-card{display:flex;flex-direction:column;border:1px solid var(--border);border-radius:10px;padding:19px;min-width:0;background:var(--surface)}.subscription-node-card>header{display:flex;align-items:center;gap:10px}.subscription-node-card>header>div{flex:1;min-width:0}.subscription-node-card h3{font-size:13px;line-height:1.55;margin:0 0 5px;overflow-wrap:anywhere;font-weight:600}.subscription-node-card>header p{font-size:11px;font-variant-numeric:tabular-nums;color:var(--muted);margin:0;overflow-wrap:anywhere}.subscription-node-card>header .tag{flex-shrink:0;font-size:9px}.subscription-node-quality{margin:12px 0 10px;flex:1}.subscription-node-report{display:flex;align-items:center;gap:6px;font-size:10px;color:var(--muted);margin:4px 0 15px;line-height:1.6}.subscription-node-report>svg{flex-shrink:0}.subscription-node-card>footer{display:flex;gap:9px;padding-top:15px;border-top:1px solid var(--border)}.subscription-node-card>footer button{flex:1;min-width:0;font-size:11px;min-height:35px;padding:8px}.subscription-nodes-note{margin:19px 0 0;font-size:10px;line-height:1.8;color:var(--muted)}.subscription-nodes-empty{display:flex;flex-direction:column;align-items:center;text-align:center;padding:36px 12px 17px;color:var(--muted)}.subscription-nodes-empty h3{font-size:14px;color:var(--text);margin:15px 0 7px}.subscription-nodes-empty p{font-size:12px;line-height:1.7;margin:0}.subscription-nodes-no-results{padding:24px;text-align:center;font-size:12px;color:var(--muted)}.subscription-copy-error{font-size:12px;line-height:1.7;color:#cf6577}.subscription-nodes-status{position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap}
.subscription-node-qr{width:min(410px,calc(100vw - 32px));max-height:calc(100dvh - 32px);overflow:auto;border:1px solid var(--border);border-radius:15px;padding:24px;background:var(--surface);color:var(--text);box-shadow:0 24px 90px #0005}.subscription-node-qr[open]{display:flex;flex-direction:column;align-items:stretch;gap:15px}.subscription-node-qr::backdrop{background:#020812ba;backdrop-filter:blur(5px)}.subscription-node-qr>header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.subscription-node-qr>header span{font-size:10px;color:var(--accent-text)}.subscription-node-qr h2{font-size:21px;margin:8px 0 0}.subscription-node-qr-name{margin:0;font-size:12px;line-height:1.7;overflow-wrap:anywhere;color:var(--secondary)}.subscription-node-qr-image{display:grid;place-items:center;min-height:200px;background:#fff;border-radius:10px;padding:4px;color:#172c29}.subscription-node-qr-image img{display:block;width:100%;max-width:320px;height:auto;border-radius:7px}.subscription-node-qr-image p{font-size:12px;line-height:1.7;padding:15px}.subscription-node-qr-note{font-size:11px;color:var(--muted);text-align:center;margin:0;line-height:1.7}.subscription-node-qr>.primary{justify-content:center;width:100%;font-size:12px}
@media(max-width:1000px){.subscription-nodes-heading{align-items:flex-start;flex-direction:column}.subscription-nodes-tools{flex-wrap:wrap}}@media(max-width:720px){.subscription-nodes{padding:18px}.subscription-node-grid{grid-template-columns:1fr}.subscription-nodes-heading h2{font-size:17px}.subscription-nodes-summary{gap:16px;flex-wrap:wrap}.subscription-readonly{margin-left:0}.subscription-node-search{align-items:stretch;flex-direction:column;gap:8px}.subscription-node-search input{max-width:none}.subscription-node-card{padding:16px}.subscription-node-qr{padding:19px}.subscription-nodes-tools{gap:7px}.subscription-nodes-tools button{font-size:10px}}
</style>
