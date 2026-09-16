<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { Activity, RefreshCw, Pause, Play } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import { latestRequest, serialPoll } from '../lib/requests';
import { bytes } from '../lib/format';
import { t, locale } from '../i18n';
import { usePanelContext } from '../composables/panelContext';
import type { LiveSnapshot, LiveValue } from '../lib/monitor';
import LiveTrafficChart from '../components/LiveTrafficChart.vue';
const {state}=usePanelContext();
const api=useApi('/monitor'),requests=latestRequest();
const snapshot=ref<LiveSnapshot|null>(null),error=ref(''),busy=ref(false),paused=ref(false),search=ref(''),user=ref(0),page=ref(1),now=ref(Date.now()),received=ref(0);
const fresh=computed(()=>!!snapshot.value&&!snapshot.value.stale&&!error.value&&now.value-received.value<15000);
const rate=(n:number|null|undefined)=>!fresh.value||n==null?'—':bytes(Math.round(n))+'/s';
const count=(n:number|null|undefined)=>!fresh.value||n==null?'—':n;
const totalRate=(v:LiveValue)=>(v.upload_rate??0)+(v.download_rate??0);
const maxRate=computed(()=>Math.max(1,...(snapshot.value?.users??[]).map(totalRate)));
const selectedName=computed(()=>state.value?.users.find(u=>u.id===user.value)?.username||t('全部用户'));
const sampled=computed(()=>snapshot.value?.sampled_at?new Date(snapshot.value.sampled_at).toLocaleTimeString(locale.value):'—');
async function load(){
  const request=requests.start();busy.value=true;
  try{
    const query=new URLSearchParams({q:search.value,page:String(page.value),user_id:String(user.value)});
    const data=await api<LiveSnapshot>('?'+query,'GET',undefined,{signal:request.signal});
    if(requests.isCurrent(request)){snapshot.value=data;received.value=Date.now();now.value=Date.now();error.value='';}
  }catch(e){if(requests.isCurrent(request)&&!isCancelled(e))error.value=(e as Error).message;}
  finally{if(requests.isCurrent(request))busy.value=false;}
}
const poll=serialPoll(async()=>{if(!document.hidden&&!paused.value)await load();},()=>5000);
let clock:ReturnType<typeof setInterval>,debounce:ReturnType<typeof setTimeout>|undefined;
watch(search,()=>{page.value=1;clearTimeout(debounce);debounce=setTimeout(()=>void load(),300);});
watch([user,page],()=>{snapshot.value=null;void load();});
onMounted(()=>{void load();poll.start();clock=setInterval(()=>now.value=Date.now(),1000);});
onUnmounted(()=>{poll.stop();requests.cancel();clearInterval(clock);clearTimeout(debounce);});
</script>
<template>
  <section class="monitor-page">
    <header class="page-heading"><div><h1>{{t('实时监控')}}</h1><p class="section-subtitle">{{t('查看每位用户的活跃连接与实时流量')}}</p></div><div class="monitor-actions"><button @click="paused=!paused"><component :is="paused?Play:Pause" :size="15"/>{{paused?t('继续刷新'):t('暂停刷新')}}</button><button :disabled="busy" @click="load"><RefreshCw :size="15"/>{{t('刷新')}}</button></div></header>
    <div class="monitor-status"><span :class="['badge',fresh?'success':'neutral']"><Activity :size="13"/>{{paused?t('已暂停'):fresh?t('实时采样'):t('等待采样')}}</span><span>{{t('最近采样')}} {{sampled}} · {{t('每 5 秒刷新')}}</span><span>{{t('当前站点')}} · {{snapshot?.site_id||state?.system.site_id}}</span></div>
    <p v-if="error" class="error" role="alert">{{t(error)}}</p>
    <p v-if="snapshot&&(!fresh||!snapshot.connections_available.every(Boolean)||!snapshot.traffic_available.every(Boolean))" class="monitor-note" role="status">{{t('部分数据暂不可用。请检查核心服务；旧版 Xray 需随面板升级后才能统计会话，恢复后等待两次采样。')}}</p>
    <div class="live-cards">
      <article><span>{{t('VLESS 活跃会话')}}</span><strong>{{count(snapshot?.total.vless)}}</strong><small>{{t('当前站点全部用户')}}</small></article>
      <article><span>{{t('HY2 连接')}}</span><strong>{{count(snapshot?.total.hy2)}}</strong><small>QUIC</small></article>
      <article><span>{{t('实时上传')}}</span><strong>{{rate(snapshot?.total.upload_rate)}}</strong><small>{{t('实际流量，不含计费倍率')}}</small></article>
      <article><span>{{t('实时下载')}}</span><strong>{{rate(snapshot?.total.download_rate)}}</strong><small>{{t('实际流量，不含计费倍率')}}</small></article>
    </div>
    <article class="trend-card"><header><div><h2>{{t('流量趋势')}} · {{selectedName}}</h2><p>{{t('最近五分钟，仅保存在内存；重启后重新采样。')}}</p></div><select v-model.number="user" :aria-label="t('选择用户')"><option :value="0">{{t('全部用户')}}</option><option v-for="u in state?.users" :key="u.id" :value="u.id">{{u.username}}</option></select></header><LiveTrafficChart :points="snapshot?.history||[]"/></article>
    <section class="users-live"><header><h2>{{t('用户连接与速率')}}</h2><input v-model="search" :aria-label="t('搜索用户名')" :placeholder="t('搜索用户名')"/></header>
      <div class="table-wrap"><table class="adaptive-table"><thead><tr><th>{{t('用户')}}</th><th>{{t('VLESS 活跃会话')}}</th><th>{{t('HY2 连接')}}</th><th>{{t('实时上传')}}</th><th>{{t('实时下载')}}</th><th>{{t('流量趋势')}}</th></tr></thead><tbody>
        <tr v-for="u in snapshot?.users||[]" :key="u.id" :class="{'selected-user':u.id===user}"><td :data-label="t('用户')"><strong>{{u.username}}</strong><div class="rate-bar" :aria-hidden="true"><i :style="{width:(fresh?totalRate(u)/maxRate*100:0)+'%'}"/></div></td><td :data-label="t('VLESS 活跃会话')">{{count(u.vless)}}</td><td :data-label="t('HY2 连接')">{{count(u.hy2)}}</td><td :data-label="t('实时上传')">{{rate(u.upload_rate)}}</td><td :data-label="t('实时下载')">{{rate(u.download_rate)}}</td><td :data-label="t('流量趋势')"><button class="text-button" @click="user=u.id">{{t('查看趋势')}}</button></td></tr>
        <tr v-if="!snapshot?.users.length"><td colspan="6" class="empty">{{busy?t('加载中'):t('暂无匹配用户')}}</td></tr>
      </tbody></table></div>
      <footer><span>{{t('按当前速率排序')}} · {{snapshot?.user_count||0}} {{t('位用户')}}</span><div><button :disabled="page<=1||busy" @click="page--">{{t('上一页')}}</button><span>{{page}}</span><button :disabled="page*100>=(snapshot?.user_count||0)||busy" @click="page++">{{t('下一页')}}</button></div></footer>
    </section>
    <p class="monitor-note">{{t('VLESS 统计活跃代理会话，HY2 统计已认证 QUIC 连接；多路复用时不等于设备数或 TCP 流数。此页只统计当前站点核心，主控不汇总业务站连接；独立站点请切换站点查看。')}}</p>
  </section>
</template>
<style scoped>
.monitor-page{display:grid;gap:20px}.page-heading{margin:0;justify-content:space-between;gap:20px}.page-heading>div:first-child{display:block}.page-heading h1{font-size:19px}.monitor-actions,.monitor-status{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.monitor-status{color:var(--muted);font-size:12px;gap:14px}.live-cards{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.live-cards article{padding:22px;background:var(--surface);border:1px solid var(--border);border-radius:10px;display:grid;gap:12px;min-width:0}.live-cards article>span{font-size:12px;color:var(--muted)}.live-cards strong{font-size:25px;font-variant-numeric:tabular-nums}.trend-card{border:1px solid var(--border);border-radius:12px;padding:22px;background:var(--surface)}.trend-card header{display:flex;justify-content:space-between;align-items:center;gap:16px;margin-bottom:20px}.trend-card header p{margin-top:8px;font-size:12px;color:var(--muted);line-height:1.7}.trend-card select{max-width:220px}.users-live{display:grid;gap:16px}.users-live>header{display:flex;justify-content:space-between;gap:16px;align-items:center}.users-live input{max-width:260px}.table-wrap{border:1px solid var(--border);border-radius:10px;overflow-x:auto;background:var(--surface)}.rate-bar{height:3px;margin-top:8px;border-radius:4px;max-width:150px;background:var(--surface-soft)}.rate-bar i{display:block;height:3px;background:var(--green);border-radius:4px}.users-live footer{display:flex;align-items:center;justify-content:space-between;gap:12px;color:var(--muted);font-size:12px}.users-live footer>div{display:flex;gap:12px;align-items:center}.monitor-note{font-size:12px;line-height:1.9;color:var(--muted)}.selected-user{box-shadow:inset 3px 0 var(--green)}@media(max-width:1000px){.live-cards{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:650px){.page-heading{align-items:flex-start;flex-direction:column}.live-cards{gap:10px}.live-cards article{padding:16px}.live-cards strong{font-size:20px}.trend-card{padding:16px}.trend-card header{align-items:flex-start;flex-direction:column}.trend-card select{max-width:100%}.users-live footer{align-items:flex-start;flex-direction:column}.users-live>header{flex-direction:column;align-items:flex-start}.users-live input{max-width:100%}}
</style>
