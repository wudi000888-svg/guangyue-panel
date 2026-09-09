<script setup lang="ts">
import { useApi, isCancelled } from "./lib/api";
const api = useApi();
import { latestRequest } from "./lib/requests";
const loadRequest = latestRequest();
onUnmounted(()=>loadRequest.cancel());
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { Activity, Check, LoaderCircle, Save, ShieldCheck } from 'lucide-vue-next';
import { t } from './i18n';
type RuntimeMode = 'normal' | 'no_logs';
type RuntimeSettings = {mode:RuntimeMode;revision:string;applied:boolean;changed_at:number;audit_enabled:boolean;traffic_history_enabled:boolean;subscription_access_enabled:boolean;application_logs_enabled:boolean;core_logs_enabled:boolean};
const emit=defineEmits<{updated:[]}>();
const current=ref<RuntimeSettings|null>(null),selected=ref<RuntimeMode>('normal'),busy=ref(false),error=ref(''),saved=ref(false);
const changed=computed(()=>!!current.value && (selected.value!==current.value.mode || !current.value.applied));
async function load(){const request=loadRequest.start();error.value='';try{const v=await api<RuntimeSettings>('/runtime-settings','GET',undefined,{signal:request.signal});if(!loadRequest.isCurrent(request))return false;current.value=v;selected.value=v.mode;emit('updated');return true;}catch(e){if(loadRequest.isCurrent(request)&&!isCancelled(e)){current.value=null;error.value=(e as Error).message;}return false;}}
async function save(){if(busy.value||!current.value)return;busy.value=true;loadRequest.cancel();error.value='';saved.value=false;try{const v=await api<RuntimeSettings>('/runtime-settings','PUT',{mode:selected.value,revision:current.value.revision});current.value=v;selected.value=v.mode;saved.value=!!v.applied;emit('updated');if(!v.applied)error.value='运行模式尚未完整生效，请重新加载后重试';}catch(e){if(!isCancelled(e)){const message=(e as Error).message;const known=await load();error.value=known?message:message+' · '+t('当前生效状态未知，请重新加载');}}finally{busy.value=false;}}
watch(selected,()=>{saved.value=false});
onMounted(load);
</script>
<template>
 <section class="settings-card runtime-settings" aria-labelledby="runtime-title">
  <header><ShieldCheck :size="20"/><div><h2 id="runtime-title">{{t('日志与运行模式')}}</h2><p>{{t('全站生效，与顶部简易／专业界面模式独立')}}</p></div><span v-if="current" class="runtime-badge" :class="{pending:!current.applied}"><Check v-if="current.applied" :size="12"/>{{t(current.applied?'已生效':'待生效')}}</span></header>
  <p v-if="error" class="error" role="alert">{{t(error)}} <button type="button" :disabled="busy" @click="load">{{t('重新加载')}}</button></p>
  <form v-if="current" @submit.prevent="save">
   <div class="runtime-options" role="group" :aria-label="t('运行模式')">
    <label class="runtime-option" :class="{chosen:selected==='normal'}"><input v-model="selected" type="radio" value="normal" name="runtime-mode" :disabled="busy"/><Activity :size="18"/><span><strong>{{t('常规模式')}}</strong><small>{{t('记录操作审计、小时流量趋势和订阅访问时间，保留服务诊断日志。')}}</small></span></label>
    <label class="runtime-option" :class="{chosen:selected==='no_logs'}"><input v-model="selected" type="radio" value="no_logs" name="runtime-mode" :disabled="busy"/><ShieldCheck :size="18"/><span><strong>{{t('无日志模式')}}</strong><small>{{t('停止新增上述记录，并关闭本应用控制程序与协议核心的运行日志输出。')}}</small></span></label>
   </div>
   <div class="runtime-details"><p><strong>{{t('始终保留')}}</strong>{{t('用户与节点配置、配额累计、必要的鉴权状态、最新健康与质量结果。')}}</p><p>{{t('切换不会清空已有历史记录，也不会重启节点。Nginx 不记录本站访问与请求错误日志。')}}</p><p>{{t('此设置仅管理本应用；系统服务启停记录和云服务商记录不在控制范围内。')}}</p></div>
   <footer class="runtime-save"><span v-if="saved" role="status"><Check :size="14"/>{{t('运行模式已保存并生效')}}</span><span v-else>{{t('当前运行模式')}} · {{t(current.mode==='no_logs'?'无日志模式':'常规模式')}}</span><button class="primary" :disabled="busy||!changed"><LoaderCircle v-if="busy" :size="15" class="spin"/><Save v-else :size="15"/>{{t(busy?'保存中…':'应用运行模式')}}</button></footer>
  </form>
  <p v-else-if="!error" role="status">{{t('正在读取运行模式…')}}</p>
 </section>
</template>
<style scoped>
.runtime-settings{margin:0 0 24px}.runtime-settings header{align-items:flex-start}.runtime-settings header>div{flex:1}.runtime-badge{display:inline-flex;align-items:center;gap:5px;padding:5px 9px;white-space:nowrap;font-size:11px;border-radius:20px;color:var(--accent-text);background:var(--accent-soft)}.runtime-badge.pending{color:#c3923a;background:#c3923a14}.runtime-options{display:grid;grid-template-columns:1fr 1fr;gap:14px}.runtime-options .runtime-option{margin:0;display:flex;flex-direction:row;align-items:flex-start;gap:12px;border:1px solid var(--border);border-radius:9px;padding:18px;cursor:pointer;background:var(--surface-raised)}.runtime-option.chosen{border-color:var(--accent);background:var(--accent-soft)}.runtime-option input{width:15px;min-height:15px;height:15px;flex-shrink:0;margin:2px 0 0;accent-color:var(--accent)}.runtime-option>svg{flex-shrink:0;color:var(--accent-text)}.runtime-option strong{display:block;font-size:13px}.runtime-option small{display:block;line-height:1.7}.runtime-details{padding:14px 0}.runtime-details strong{color:var(--secondary);margin-right:9px}.runtime-save{border-top:1px solid var(--border);padding-top:18px;display:flex;justify-content:space-between;align-items:center;gap:12px}.runtime-save>span{display:flex;align-items:center;gap:6px;font-size:11px;color:var(--muted)}.runtime-save button{white-space:nowrap}@media(max-width:650px){.runtime-options{grid-template-columns:1fr}.runtime-save{align-items:flex-start;flex-direction:column}.runtime-save button{width:100%}.runtime-settings{padding:18px}.runtime-badge{font-size:10px}}
</style>
