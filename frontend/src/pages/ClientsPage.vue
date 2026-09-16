<script setup lang="ts">
import { computed, ref } from 'vue';
import { Download, ExternalLink, Monitor, Smartphone, ArrowRight } from 'lucide-vue-next';
import clients from '../data/clients.json';
import { t } from '../i18n';
const platforms = ['Windows', 'macOS', 'Linux', 'Android', 'iOS / iPadOS'];
const platform = ref('');
const visible = computed(() => clients.filter(c => !platform.value || c.downloads.some(d => d.platform === platform.value)));
const downloads = (client: typeof clients[number]) => client.downloads.filter(d => !platform.value || d.platform === platform.value);
</script>
<template>
  <section class="clients-page">
    <header class="page-heading"><div><h1>{{t('客户端中心')}}</h1><p class="section-subtitle">{{t('按系统与架构下载客户端，再导入对应格式的面板订阅')}}</p></div><RouterLink class="client-link" to="/subscription">{{t('前往订阅')}}<ArrowRight :size="16"/></RouterLink></header>
    <div class="platforms" role="group" :aria-label="t('选择操作系统')">
      <button :class="{primary: !platform}" :aria-pressed="!platform" @click="platform=''">{{t('全部平台')}}</button>
      <button v-for="p in platforms" :key="p" :class="{primary: platform===p}" :aria-pressed="platform===p" @click="platform=p"><component :is="p==='Android'||p==='iOS / iPadOS'?Smartphone:Monitor" :size="16"/>{{p}}</button>
    </div>
    <p class="client-help">{{t('Apple Silicon 选择 ARM64，Intel / AMD 电脑通常选择 x64；Android 请在设备信息中核对架构。直链为已核实版本，其他格式或新版请打开官方发布页。')}}</p>
    <div class="client-grid">
      <article v-for="client in visible" :key="client.id" class="client-card">
        <header><div class="client-mark">{{client.name.slice(0,2)}}</div><div><h2>{{client.name}}</h2><small>{{client.version?'v'+client.version:t('商店分发')}}</small></div><span class="badge neutral">{{client.paid?t('付费'):t('免费')}}</span></header>
        <div class="client-tags"><span v-for="protocol in client.protocols" :key="protocol" class="badge success">{{protocol}}</span><span class="badge neutral">{{client.format}}</span></div>
        <p>{{t(client.note)}}</p>
        <div class="downloads"><a v-for="link in downloads(client)" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer"><Download :size="15"/><span>{{link.platform}} <strong>{{link.arch}}</strong></span><ExternalLink :size="13"/></a></div>
        <footer><a :href="client.source" target="_blank" rel="noopener noreferrer">{{t('官方发布页')}}<ExternalLink :size="13"/></a><small>{{t('链接核实于')}} 2026-09-16</small></footer>
      </article>
    </div>
  </section>
</template>
<style scoped>
.clients-page{display:grid;gap:20px}.page-heading{justify-content:space-between;gap:20px;margin:0}.page-heading>div{display:block}.page-heading h1{font-size:19px}.platforms{display:flex;gap:8px;flex-wrap:wrap}.client-help{color:var(--muted);font-size:12px;line-height:1.8;max-width:1000px}.client-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,340px),1fr));gap:18px}.client-card{border:1px solid var(--border);border-radius:12px;padding:22px;background:var(--surface);display:flex;flex-direction:column;gap:18px;min-width:0}.client-card header{display:flex;align-items:center;gap:12px}.client-card header>.badge{margin-left:auto}.client-card header small{display:block;margin-top:6px}.client-mark{width:42px;height:42px;border-radius:12px;background:var(--surface-soft);color:var(--green);display:grid;place-items:center;font-weight:700}.client-card p{font-size:12px;color:var(--muted);line-height:1.8}.client-tags{display:flex;gap:6px;flex-wrap:wrap}.downloads{display:grid;gap:8px}.downloads a{display:flex;align-items:center;gap:10px;border:1px solid var(--border);border-radius:7px;padding:11px 12px;font-size:12px}.downloads a:hover{border-color:var(--green);color:var(--green)}.downloads a>span{flex:1}.downloads strong{margin-left:5px}.client-card footer{margin-top:auto;border-top:1px solid var(--border);padding-top:15px;display:flex;gap:10px;justify-content:space-between;align-items:center;flex-wrap:wrap}.client-card footer a,.client-link{display:inline-flex;align-items:center;gap:6px;font-size:12px;color:var(--green)}@media(max-width:650px){.page-heading{align-items:flex-start;flex-direction:column}.client-card{padding:18px}.platforms button{padding:8px 11px}}
</style>
