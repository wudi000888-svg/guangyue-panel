<script setup lang="ts">
import { computed, ref } from 'vue';
import { ratePath, type LivePoint } from '../lib/monitor';
import { bytes } from '../lib/format';
import { t, locale } from '../i18n';
const props=defineProps<{ points: LivePoint[] }>();
const focus=ref<number | null>(null);
const ceiling=computed(()=>Math.max(1,...props.points.flatMap(p=>[p.upload_rate??0,p.download_rate??0]))*1.15);
const selected=computed(()=>props.points[focus.value??props.points.length-1]);
const time=(at:number)=>new Date(at).toLocaleTimeString(locale.value,{hour:'2-digit',minute:'2-digit',second:'2-digit'});
const rate=(value:number|null|undefined)=>value==null?t('暂不可用'):bytes(Math.round(value))+'/s';
</script>
<template>
  <div class="live-chart">
    <div class="chart-legend"><span class="upload">● {{t('上传')}}</span><span class="download">● {{t('下载')}}</span><span v-if="selected" class="chart-reading">{{time(selected.at)}} · {{rate(selected.upload_rate)}} ↑ · {{rate(selected.download_rate)}} ↓</span></div>
    <svg viewBox="0 0 900 245" role="img" :aria-label="t('最近五分钟用户上传与下载速率趋势')">
      <title>{{t('最近五分钟用户上传与下载速率趋势')}}</title>
      <g v-for="tick in [0,.5,1]" :key="tick"><line x1="60" x2="880" :y1="205-tick*175" :y2="205-tick*175" class="grid"/><text x="54" :y="209-tick*175" text-anchor="end">{{bytes(Math.round(ceiling*tick))}}/s</text></g>
      <path :d="ratePath(points,'upload_rate',ceiling)" class="upload-path"/><path :d="ratePath(points,'download_rate',ceiling)" class="download-path"/>
      <text v-if="points.length" x="60" y="232">{{time(points[0]!.at)}}</text><text v-if="points.length>1" x="880" y="232" text-anchor="end">{{time(points.at(-1)!.at)}}</text>
      <text v-if="!points.some(p=>p.upload_rate!==null)" x="470" y="112" text-anchor="middle">{{t('等待有效流量采样')}}</text>
    </svg>
    <input v-if="points.length>1" v-model.number="focus" type="range" min="0" :max="points.length-1" :aria-label="t('查看历史采样点')"/>
  </div>
</template>
<style scoped>
.chart-legend{display:flex;gap:16px;align-items:center;flex-wrap:wrap;font-size:12px}.upload{color:var(--green)}.download{color:#a47bec}.chart-reading{margin-left:auto;color:var(--muted);font-variant-numeric:tabular-nums}.live-chart svg{display:block;width:100%;min-height:150px;margin-top:8px;overflow:visible}.grid{stroke:var(--border);stroke-dasharray:4 5}.live-chart text{fill:var(--muted);font-size:10px}.upload-path,.download-path{fill:none;stroke-width:2.5;stroke-linecap:round;stroke-linejoin:round}.upload-path{stroke:var(--green)}.download-path{stroke:#a47bec}.live-chart input{padding:0;width:100%;min-height:24px;accent-color:var(--green)}@media(max-width:650px){.chart-reading{margin-left:0;width:100%;font-size:11px}.live-chart text{font-size:12px}}
</style>
