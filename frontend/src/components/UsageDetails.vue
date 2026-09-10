<script setup lang="ts">
import {ref,watch} from 'vue';
import {useApi,isCancelled} from '../lib/api';
import {latestRequest} from '../lib/requests';
import {bytes,date} from '../lib/format';
import {rateText} from '../lib/quota';
import {t} from '../i18n';
import type {User} from '../types';
const props=defineProps<{user:User}>(),api=useApi(),request=latestRequest();
type Report={period_id:string;periods:User[];nodes:{site_id:string;node_id:string;rate_milli:number;rate_revision:string;upload:number;download:number}[]};
const open=ref(false),data=ref<Report|null>(null),period=ref(''),error=ref('');
async function load(){const current=request.start();error.value='';try{const result=await api<Report>('/usage?user_id='+props.user.id+(period.value?'&period_id='+encodeURIComponent(period.value):''),'GET',undefined,{signal:current.signal});if(request.isCurrent(current))data.value=result;}catch(e){if(!isCancelled(e)&&request.isCurrent(current))error.value=(e as Error).message;}}
watch(()=>props.user.id,()=>{request.cancel();data.value=null;period.value='';if(open.value)void load();});
watch([open,period],()=>{if(open.value)void load();});
</script>
<template><details class="usage-details" @toggle="open=($event.target as HTMLDetailsElement).open"><summary>{{t('节点用量明细')}}</summary><div v-if="open" class="usage-details-body"><div class="toolbar"><label>{{t('配额周期')}}<select v-model="period"><option value="">{{t('当前周期')}}</option><option v-for="u in data?.periods" :key="u.meter?.period_id" :value="u.meter?.period_id">{{date(u.meter?.start||0)}} — {{date(u.meter?.end||0)}}</option></select></label></div><p class="field-help">{{t('按站点、节点和生效倍率保留实际流量。升级前的汇总不会拆成虚构的节点明细。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><table v-if="data" class="adaptive-table"><thead><tr><th>{{t('站点')}}</th><th>{{t('节点')}}</th><th>{{t('节点倍率')}}</th><th>{{t('实际流量')}}</th></tr></thead><tbody><tr v-for="n in data.nodes" :key="n.site_id+'/'+n.node_id+'/'+n.rate_revision"><td :data-label="t('站点')">{{n.site_id}}</td><td :data-label="t('节点')">{{n.node_id}}</td><td :data-label="t('节点倍率')"><span class="badge neutral">{{rateText(n)}}</span></td><td :data-label="t('实际流量')">{{bytes(n.upload+n.download)}}</td></tr><tr v-if="!data.nodes.length"><td colspan="4" class="empty">{{t('本周期暂无节点用量')}}</td></tr></tbody></table></div></details></template>
<style scoped>.usage-details{border:1px solid var(--border);border-radius:10px;margin:0 0 24px;background:var(--surface)}summary{cursor:pointer;padding:15px 18px;font-size:13px;font-weight:600}.usage-details-body{padding:0 18px 16px}.toolbar{margin:0}.toolbar label{font-size:12px;color:var(--muted);display:flex;gap:10px;align-items:center}.usage-details td{overflow-wrap:anywhere}@media(max-width:700px){.usage-details-body{padding:0 12px 12px}}</style>
