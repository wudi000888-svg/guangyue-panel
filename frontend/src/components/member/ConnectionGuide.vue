<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { ArrowUpRight, Check, Copy, Download, LoaderCircle, Monitor, QrCode, Smartphone } from 'lucide-vue-next';
import QRCode from 'qrcode';
import clients from '../../data/clients.json';
import { downloadBlob, getRemoteSite, isCancelled } from '../../lib/api';
import { download } from '../../lib/download';
import { t } from '../../i18n';
const props = defineProps<{ subscriptionUrl: string; active: boolean; initialPlatform?: string; initialClient?: string }>();
const emit = defineEmits<{ copy: [value: string] }>();
const platforms = ['Windows', 'macOS', 'Linux', 'Android', 'iOS / iPadOS'];
const platform = ref(platforms.includes(props.initialPlatform || '') ? props.initialPlatform! : '');
const clientID = ref(props.initialClient || ''), packageURL = ref(''), qr = ref(''), error = ref(''), downloading = ref(false), qrOpen = ref(false);
const choices = computed(() => clients.filter(client => client.downloads.some(item => item.platform === platform.value)));
const client = computed(() => choices.value.find(item => item.id === clientID.value));
const packages = computed(() => client.value?.downloads.filter(item => item.platform === platform.value) || []);
const link = computed(() => {
  if (!props.subscriptionUrl || !client.value) return '';
  try {
    const value = new URL(props.subscriptionUrl, location.origin);
    value.searchParams.set('format', client.value.format === 'Mihomo' ? 'mihomo' : 'base64');
    if (!client.value.protocols.includes('HY2')) value.searchParams.set('protocol', 'vless');
    else value.searchParams.delete('protocol');
    return value.toString();
  } catch { return ''; }
});
watch(choices, list => { if (!platform.value) return; if (!list.some(item => item.id === clientID.value)) clientID.value = list[0]?.id || ''; }, { immediate: true });
watch(packages, list => { packageURL.value = list.length === 1 ? list[0]!.url : ''; }, { immediate: true });
let qrSequence = 0;
const downloadScope = new AbortController();
watch([link, qrOpen], async ([value, open]) => {
  const sequence = ++qrSequence; qr.value = ''; error.value = '';
  if (!value || !open) return;
  try { const image = await QRCode.toDataURL(value, { width: 240, margin: 3, color: { dark: '#173D36', light: '#ffffff' } }); if (sequence === qrSequence) qr.value = image; }
  catch { if (sequence === qrSequence) error.value = t('订阅二维码生成失败，请使用复制地址。'); }
});
async function saveConfig() {
  if (!link.value || downloading.value) return;
  downloading.value = true; error.value = '';
  const url = link.value, filename = client.value?.format === 'Mihomo' ? 'guangyue.yaml' : 'guangyue.txt';
  try { if (getRemoteSite()) window.open(url, '_blank', 'noopener,noreferrer'); else download(await downloadBlob(url, { signal: downloadScope.signal }), filename); }
  catch (reason) { if (!isCancelled(reason)) error.value = reason instanceof Error ? t(reason.message) : t('请求失败'); }
  finally { downloading.value = false; }
}
onBeforeUnmount(() => { qrSequence++; downloadScope.abort(); });
</script>
<template>
  <div class="connection-guide">
    <section class="guide-step"><header><span>1</span><div><h3>{{t('选择设备与客户端')}}</h3><p>{{t('为当前设备选择一个客户端，订阅格式会自动匹配。')}}</p></div></header><div class="guide-platforms" role="group" :aria-label="t('选择操作系统')"><button v-for="p in platforms" :key="p" :aria-pressed="platform===p" @click="platform=p"><component :is="p==='Android'||p==='iOS / iPadOS'?Smartphone:Monitor" :size="17"/>{{p}}</button></div><div v-if="platform" class="guide-clients" role="group" :aria-label="t('选择客户端')"><button v-for="item in choices" :key="item.id" :aria-pressed="clientID===item.id" @click="clientID=item.id"><span><strong>{{item.name}}</strong><small>{{item.paid?t('付费'):t('免费')}}</small></span><Check v-if="clientID===item.id" :size="17"/></button></div></section>
    <template v-if="client">
      <section class="guide-step"><header><span>2</span><div><h3>{{t('下载并安装')}}</h3><p>{{t('已安装客户端可直接进行下一步。')}}</p></div></header><p class="guide-note">{{t(client.note)}}</p><div class="guide-download"><label>{{t('选择安装包')}}<select v-model="packageURL"><option v-if="packages.length>1" value="" disabled>{{t('选择设备架构')}}</option><option v-for="item in packages" :key="item.url" :value="item.url">{{item.platform}} · {{item.arch}}</option></select></label><a v-if="packageURL" class="guide-button" :href="packageURL" target="_blank" rel="noopener noreferrer"><Download :size="17"/>{{t('下载客户端')}}<ArrowUpRight :size="15"/></a><a :href="client.source" target="_blank" rel="noopener noreferrer">{{t('官方发布页')}}<ArrowUpRight :size="15"/></a></div><details class="guide-architecture"><summary>{{t('不确定选择哪个安装包？')}}</summary><p>{{t('Apple Silicon 选择 ARM64，Intel / AMD 电脑通常选择 x64；Android 请在设备信息中核对架构。直链为已核实版本，其他格式或新版请打开官方发布页。')}}</p></details></section>
      <section class="guide-step"><header><span>3</span><div><h3>{{t('导入订阅并连接')}}</h3><p>{{t('复制地址后，在客户端的订阅管理中导入，再选择节点连接。')}}</p></div></header><p v-if="!active" class="guide-note">{{t('当前服务不可用，可先安装客户端，恢复套餐后再导入订阅。')}}</p><div class="member-actions"><button class="primary" :disabled="!active||!link" @click="emit('copy',link)"><Copy :size="17"/>{{t('复制订阅地址')}}</button><button :disabled="!active||!link" :aria-expanded="qrOpen" @click="qrOpen=!qrOpen"><QrCode :size="17"/>{{t('扫描二维码')}}</button><button :disabled="!active||!link||downloading" @click="saveConfig"><LoaderCircle v-if="downloading" :size="17" class="spin"/><Download v-else :size="17"/>{{t('下载订阅')}}</button></div><div v-if="qrOpen&&active" class="guide-qr" role="status"><img v-if="qr" :src="qr" width="220" height="220" :alt="t('当前订阅地址二维码')"/><LoaderCircle v-else-if="!error" :size="22" class="spin"/><p>{{client.name}} · {{t('用客户端扫描导入')}}</p></div><p v-if="error" class="error" role="alert">{{error}}</p></section>
    </template>
  </div>
</template>
<style scoped>
.connection-guide{display:grid;gap:0}.guide-step{padding:24px 0;border-top:1px solid var(--border)}.guide-step:first-child{padding-top:0;border-top:0}.guide-step:last-child{padding-bottom:0}.guide-step>header{display:flex;gap:13px;align-items:flex-start;margin-bottom:18px}.guide-step>header>span{display:grid;place-items:center;background:var(--accent-soft);color:var(--accent-text);width:28px;height:28px;border-radius:8px;font-size:13px;font-weight:650;flex-shrink:0}.guide-step h3{font-size:16px;margin:2px 0 6px;line-height:1.5}.guide-step header p,.guide-note,.guide-architecture p{font-size:13px;color:var(--secondary);line-height:1.8;margin:0}.guide-platforms{display:flex;gap:8px;flex-wrap:wrap}.guide-platforms button{min-height:44px;font-size:13px;gap:8px}.guide-platforms button[aria-pressed=true],.guide-clients button[aria-pressed=true]{border-color:var(--accent);color:var(--accent-text);background:var(--accent-soft)}.guide-clients{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px;margin-top:14px}.guide-clients button{display:flex;align-items:center;justify-content:space-between;text-align:left;min-height:65px;gap:10px;padding:12px 14px;background:var(--surface)}.guide-clients strong{display:block;font-size:14px}.guide-clients small{display:block;font-size:12px;color:var(--secondary);margin-top:5px}.guide-download{display:flex;gap:14px;align-items:flex-end;flex-wrap:wrap;margin-top:17px}.guide-download label{display:flex;flex-direction:column;gap:7px;min-width:200px;font-size:13px}.guide-download select{min-height:44px}.guide-download a{display:inline-flex;align-items:center;justify-content:center;gap:7px;min-height:44px;font-size:13px;color:var(--accent-text);text-decoration:none}.guide-download .guide-button{border:1px solid var(--border);border-radius:8px;padding:0 16px;background:var(--surface)}.guide-architecture{margin-top:15px;font-size:13px;color:var(--secondary)}.guide-architecture summary{cursor:pointer;min-height:44px;align-content:center}.guide-architecture p{padding:0 0 8px}.guide-note{margin-bottom:15px}.guide-qr{display:flex;align-items:center;flex-direction:column;width:fit-content;max-width:100%;padding:16px;background:var(--surface-raised);border:1px solid var(--border);border-radius:12px;margin-top:18px;min-width:220px;box-sizing:border-box}.guide-qr img{background:#fff;max-width:100%;height:auto;border-radius:6px}.guide-qr p{font-size:13px;color:var(--secondary);text-align:center;margin:12px 0 0}.guide-step .member-actions{display:flex;gap:10px;flex-wrap:wrap}.guide-step .member-actions button{min-height:44px}@media(max-width:600px){.guide-platforms button{flex:1 0 30%}.guide-clients{grid-template-columns:1fr 1fr}.guide-clients strong{font-size:13px}.guide-download label{width:100%;min-width:0}.guide-download select{width:100%;font-size:16px}.guide-qr{margin-left:auto;margin-right:auto}.guide-step .member-actions .primary{width:100%}.guide-step .member-actions>button{flex:1}.guide-step{padding:22px 0}}
</style>
