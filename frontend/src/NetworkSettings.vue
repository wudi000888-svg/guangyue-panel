<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { Gauge, RefreshCw, Check, LoaderCircle } from 'lucide-vue-next';
import { useApi, isCancelled } from './lib/api';
import { t } from './i18n';
const api = useApi();
type NetworkState = {available:boolean;retry_after?:number;error?:string;revision:string;managed:boolean;desired:{bbr:boolean;hy2:boolean};bbr_supported:boolean;actual:{bbr:boolean;hy2:boolean;hy2_running:boolean;congestion_control:string;default_qdisc:string;rmem_max:string;wmem_max:string};operation?:{stage:string;error?:string}};
const state=ref<NetworkState|null>(null),bbr=ref(false),hy2=ref(false),busy=ref(false),error=ref(''),saved=ref(false);
let timer:ReturnType<typeof setTimeout>|undefined,stopped=false;
const running=computed(()=>busy.value||['queued','applying'].includes(state.value?.operation?.stage||''));
const changed=computed(()=>state.value?.available&&(bbr.value!==state.value.desired.bbr||hy2.value!==state.value.desired.hy2||!state.value.managed));
function receive(value:NetworkState){state.value=value;if(value.available&&!['queued','applying'].includes(value.operation?.stage||'')){bbr.value=value.desired.bbr;hy2.value=value.desired.hy2;}}
function schedule(){clearTimeout(timer);if(!stopped)timer=setTimeout(()=>load(true),2000);}
async function load(poll=false){try{const value=await api<NetworkState>('/network-settings');if(stopped)return;receive(value);error.value=value.error||value.operation?.error||'';if(value.retry_after){clearTimeout(timer);timer=setTimeout(()=>load(),value.retry_after*1000);return;}if(['queued','applying'].includes(value.operation?.stage||''))schedule();else if(poll)saved.value=value.operation?.stage==='succeeded';}catch(e){if(!stopped&&!isCancelled(e)){error.value=(e as Error).message;if(poll)schedule();}}}
async function save(){if(running.value||!state.value?.available)return;busy.value=true;saved.value=false;error.value='';try{receive(await api<NetworkState>('/network-settings','POST',{bbr:bbr.value,hy2:hy2.value,revision:state.value.revision}));schedule();}catch(e){if(!isCancelled(e)){error.value=(e as Error).message;schedule();}}finally{busy.value=false;}}
onMounted(()=>load());onBeforeUnmount(()=>{stopped=true;clearTimeout(timer);});
</script>
<template>
 <section class="settings-card network-settings" aria-labelledby="network-title">
  <header><Gauge :size="20"/><div><h2 id="network-title">{{t('网络优化')}}</h2><p>{{t('本站服务器 · 新安装默认启用 BBR 与 HY2 优化')}}</p></div><button class="icon" :disabled="running" :aria-label="t('刷新网络状态')" @click="load()"><RefreshCw :size="16"/></button></header>
  <p v-if="error" class="error" role="alert">{{t(error)}}</p>
  <form v-if="state?.available" @submit.prevent="save">
   <div class="network-options">
    <section class="network-option"><div class="network-option-heading"><h3>TCP BBR</h3><button type="button" class="network-toggle" role="switch" :aria-checked="bbr" :aria-label="t('启用 BBR 优化')" :disabled="running" @click="bbr=!bbr;saved=false"><i/></button></div><p>{{t('启用系统 BBR 与 fq 默认队列，作用于 VLESS 等 TCP 连接。')}}</p><dl><div><dt>{{t('实际拥塞算法')}}</dt><dd>{{state.actual.congestion_control||t('未知')}}</dd></div><div><dt>{{t('默认队列')}}</dt><dd>{{state.actual.default_qdisc||t('未知')}}</dd></div></dl><small v-if="!state.bbr_supported">{{t('启用时尝试加载 BBR 内核模块，不支持时会报告失败。')}}</small></section>
    <section class="network-option"><div class="network-option-heading"><h3>Hysteria2 / QUIC</h3><button type="button" class="network-toggle" role="switch" :aria-checked="hy2" :aria-label="t('启用 HY2 优化')" :disabled="running" @click="hy2=!hy2;saved=false"><i/></button></div><p>{{t('使用标准 BBR 自适应速率，忽略客户端带宽限制，将 UDP 缓冲上限提高至至少 8 MiB。')}}</p><dl><div><dt>{{t('实际 HY2 策略')}}</dt><dd>{{t(!state.actual.hy2_running?'待生效':state.actual.hy2?'优化 BBR':'核心默认策略')}}</dd></div><div><dt>{{t('接收 / 发送上限')}}</dt><dd>{{(Number(state.actual.rmem_max)/1048576).toFixed(1)}} / {{(Number(state.actual.wmem_max)/1048576).toFixed(1)}} MiB</dd></div></dl></section>
   </div>
   <p class="network-note">{{t('关闭时恢复面板接管前的系统参数；此前已开启 BBR 的机器可能仍使用 BBR。HY2 关闭后恢复核心默认策略。')}}</p>
   <p class="network-note">{{t('修改 HY2 策略会短暂重启面板与 HY2，客户端将重新连接。BBR 切换对新建 TCP 连接生效，现有网卡队列不会被强制替换。')}}</p>
   <footer><span role="status"><LoaderCircle v-if="running" class="spin" :size="15"/><Check v-else-if="saved" :size="15"/>{{t(running?'正在应用并核对生效状态…':saved?'网络优化已生效':'网络优化独立于界面模式和日志模式')}}</span><button class="primary" :disabled="running||!changed">{{t('应用网络优化')}}</button></footer>
  </form>
  <p v-else-if="!error" class="field-help">{{t(state?'此部署尚未启用维护服务':'正在读取网络状态…')}}</p>
 </section>
</template>
<style scoped>
.network-settings{margin-bottom:24px}.network-settings header>div{flex:1}.network-options{display:grid;grid-template-columns:1fr 1fr;gap:16px}.network-option{padding:18px;border:1px solid var(--border);background:var(--surface-raised);border-radius:12px;min-width:0}.network-option-heading{display:flex;align-items:center;justify-content:space-between;gap:12px}.network-option h3{margin:0;font-size:14px}.network-option p,.network-note{font-size:12px;line-height:1.7;color:var(--muted)}.network-option dl{margin:16px 0 0;font-size:12px}.network-option dl>div{display:flex;justify-content:space-between;gap:12px;padding:6px 0}.network-option dt{color:var(--muted)}.network-option dd{margin:0;text-align:right;font-variant-numeric:tabular-nums}.network-option small{display:block;margin-top:8px;line-height:1.6;color:var(--muted)}.network-toggle{width:42px;min-width:42px;height:24px;min-height:24px;border:0;border-radius:20px;background:var(--border);padding:3px;display:flex;justify-content:flex-start}.network-toggle i{display:block;width:18px;height:18px;border-radius:50%;background:white;box-shadow:0 1px 3px #0003;transition:transform .15s}.network-toggle[aria-checked=true]{background:#0eab92}.network-toggle[aria-checked=true] i{transform:translateX(18px)}.network-settings footer{border-top:1px solid var(--border);padding-top:16px;display:flex;align-items:center;justify-content:space-between;gap:16px}.network-settings footer>span{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--muted)}.network-settings footer>button{white-space:nowrap}@media(max-width:650px){.network-options{grid-template-columns:1fr}.network-settings footer{align-items:stretch;flex-direction:column}.network-option-heading{min-height:44px}.network-toggle{position:relative}.network-toggle:after{content:'';position:absolute;inset:-10px 0}}
</style>
