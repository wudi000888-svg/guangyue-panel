<script setup lang="ts">
import { useApi, isCancelled } from "./lib/api";
const api = useApi();
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { Clock3, FileSearch, LoaderCircle, RefreshCw, ShieldCheck } from 'lucide-vue-next';
import CountryMark from './CountryMark.vue';
import QualityTags from './QualityTags.vue';
import { locale, t } from './i18n';
import type { IPQuality } from './quality';

type MemberNode = { id:string; name:string; protocol:'vless'|'hy2'; probe_ip:string; country:string; country_code:string; checked_at:number; quality:IPQuality|null };
type MemberReports = { pool:'private'|'public'; active:boolean; readonly:true; nodes:MemberNode[] };
const props=defineProps<{pool:'private'|'public'}>();
const reports=ref<MemberReports|null>(null),loading=ref(false),error=ref(''),search=ref('');
const visibleNodes=computed(()=>{const query=search.value.trim().toLowerCase();return (reports.value?.nodes||[]).filter(node=>!query||[node.name,node.probe_ip,node.country,node.protocol].some(value=>value.toLowerCase().includes(query)))});
const completed=computed(()=>reports.value?.nodes.filter(node=>node.quality?.at).length||0);
const date=(at:number)=>at?new Date(at*1000).toLocaleString(locale.value):t('尚未检测');
let request:AbortController|undefined,timer:ReturnType<typeof setTimeout>|undefined,mounted=false;
async function load(clear=false){
 request?.abort();const controller=new AbortController();request=controller;
 if(clear){reports.value=null;search.value=''}
 loading.value=true;error.value='';const pool=props.pool;
 try{
  const result=await api<MemberReports>('/node-quality?pool='+pool,'GET',undefined,{signal:controller.signal});
  if(!controller.signal.aborted&&pool===props.pool)reports.value=result;
 }catch(reason){if(!controller.signal.aborted&&!isCancelled(reason)){error.value=(reason as Error).message;reports.value=null}}
 finally{if(request===controller){loading.value=false;request=undefined}}
}
function schedule(){timer=setTimeout(async()=>{if(!document.hidden&&!loading.value)await load();if(mounted)schedule()},60000)}
watch(()=>props.pool,()=>load(true),{immediate:true});
onMounted(()=>{mounted=true;schedule()});
onUnmounted(()=>{mounted=false;request?.abort();clearTimeout(timer)});
</script>

<template>
 <section class="member-quality" :aria-label="t('已授权节点质量')" :aria-busy="loading">
  <header class="member-quality-heading"><div><span class="member-quality-eyebrow"><ShieldCheck :size="13"/>{{t('节点质量')}}</span><h2>{{t(pool==='public'?'公共订阅节点质量':'普通订阅节点质量')}}</h2><p>{{t('查看已授权节点的出口、风险标签和检测报告。')}}</p></div><button class="text-button" :disabled="loading" @click="load()"><RefreshCw :size="14" :class="{spin:loading}"/>{{t('刷新报告')}}</button></header>
  <div class="member-quality-summary"><span>{{t('已授权节点')}}<strong>{{reports?.nodes.length??'—'}}</strong></span><span>{{t('已有报告')}}<strong>{{reports?completed:'—'}}</strong></span><span class="member-quality-readonly"><ShieldCheck :size="13"/>{{t('只读报告')}}</span></div>
  <div v-if="error" class="member-quality-empty" role="alert"><FileSearch :size="28"/><h3>{{t('暂时无法读取报告')}}</h3><p>{{t(error)}}</p><button @click="load()">{{t('重新读取')}}</button></div>
  <div v-else-if="!reports" class="member-quality-empty" role="status"><LoaderCircle :size="25" class="spin"/><p>{{t('正在读取节点质量…')}}</p></div>
  <template v-else>
   <div v-if="!reports.active" class="member-quality-empty"><ShieldCheck :size="30"/><h3>{{t('当前账号暂无可用节点')}}</h3><p>{{t('请检查账号有效期和流量额度，或联系管理员。')}}</p></div>
   <div v-else-if="!reports.nodes.length" class="member-quality-empty"><FileSearch :size="30"/><h3>{{t(pool==='public'?'暂无已授权的公共节点':'暂无已授权的普通节点')}}</h3><p>{{t('可用节点更新后，质量报告会在这里显示。')}}</p></div>
   <template v-else>
    <label v-if="reports.nodes.length>3" class="member-quality-search"><span>{{t('筛选节点')}}</span><input v-model="search" type="search" :placeholder="t('搜索名称、IP、国家或协议')"/></label>
    <div class="member-quality-grid"><article v-for="node in visibleNodes" :key="node.id" class="member-quality-card"><header><CountryMark :code="node.country_code" :country="node.country"/><div><h3>{{node.name}}</h3><p>{{node.probe_ip||t('出口待确认')}}</p></div><span class="badge neutral">{{node.protocol==='hy2'?'HY2':'VLESS'}}</span></header><QualityTags :value="node.quality||undefined" :name="node.name" read-only/><footer><Clock3 :size="12"/><span>{{t('报告时间')}} · {{date(node.quality?.at||0)}}</span></footer></article></div>
    <p v-if="!visibleNodes.length" class="member-quality-no-results">{{t('没有匹配的节点')}}</p>
   </template>
  </template>
  <p class="member-quality-note">{{t('检测由管理员和后台维护；刷新报告只读取已保存结果。')}}</p>
 </section>
</template>

<style scoped>
.member-quality{border:1px solid var(--border);border-radius:12px;background:var(--surface);padding:24px;margin-top:24px;min-width:0}.member-quality-heading{display:flex;align-items:center;justify-content:space-between;gap:18px}.member-quality-eyebrow{display:flex;align-items:center;gap:7px;color:var(--accent-text);font-size:11px}.member-quality-heading h2{font-size:17px;margin:9px 0 7px;font-weight:600}.member-quality-heading p{font-size:12px;line-height:1.6;color:var(--muted);margin:0}.member-quality-heading>button{flex-shrink:0;font-size:11px}.member-quality-summary{display:flex;align-items:center;gap:24px;padding:18px 0;margin-bottom:18px;border-bottom:1px solid var(--border);font-size:11px;color:var(--muted)}.member-quality-summary>span{display:flex;gap:10px;align-items:center}.member-quality-summary strong{font-size:16px;color:var(--text);font-weight:600;font-variant-numeric:tabular-nums}.member-quality-readonly{margin-left:auto;color:var(--accent-text)}.member-quality-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.member-quality-card{border:1px solid var(--border);border-radius:9px;padding:18px;min-width:0}.member-quality-card>header{display:flex;gap:10px;align-items:center;margin-bottom:11px}.member-quality-card>header>div{min-width:0;flex:1}.member-quality-card h3{font-size:12px;font-weight:600;margin:0 0 5px;overflow-wrap:anywhere;line-height:1.5}.member-quality-card p{font-size:11px;color:var(--muted);margin:0;font-variant-numeric:tabular-nums}.member-quality-card .badge{font-size:9px;flex-shrink:0}.member-quality-card>footer{display:flex;gap:6px;align-items:center;color:var(--muted);font-size:10px;margin-top:15px;border-top:1px solid var(--border);padding-top:12px}.member-quality-empty{display:flex;align-items:center;flex-direction:column;text-align:center;padding:27px 12px;color:var(--muted)}.member-quality-empty h3{font-size:14px;margin:14px 0 6px;color:var(--text)}.member-quality-empty p{font-size:12px;line-height:1.7;margin:6px 0}.member-quality-empty button{font-size:11px;margin-top:12px}.member-quality-note{margin:19px 0 0;font-size:10px;line-height:1.8;color:var(--muted)}.member-quality-search{display:flex;gap:14px;align-items:center;font-size:11px;color:var(--muted);margin-bottom:18px}.member-quality-search input{flex:1;min-width:0;max-width:400px;padding:9px 12px;font-size:11px}.member-quality-no-results{font-size:12px;color:var(--muted);padding:18px;text-align:center}@media(max-width:720px){.member-quality{padding:18px}.member-quality-heading{align-items:flex-start;gap:10px}.member-quality-heading h2{font-size:16px}.member-quality-heading>button{font-size:10px;gap:5px;padding:5px}.member-quality-heading p{font-size:11px}.member-quality-summary{gap:15px;flex-wrap:wrap}.member-quality-readonly{margin-left:0}.member-quality-grid{grid-template-columns:1fr}.member-quality-card{padding:15px}.member-quality-search{align-items:stretch;flex-direction:column;gap:8px}.member-quality-search input{max-width:none}}
</style>
