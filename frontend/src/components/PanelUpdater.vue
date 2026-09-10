<script setup lang="ts">
import {computed,nextTick,onMounted,onBeforeUnmount,ref,watch} from 'vue';
import {ArrowUpCircle,History,RefreshCw,X,LoaderCircle,CheckCircle2,ExternalLink} from 'lucide-vue-next';
import {t} from '../i18n';
import {activeStages,newerVersion,updateOutcome,type UpdateState,type VersionOperation} from '../lib/updater';
const props=defineProps<{version:string}>();
const open=ref(false),busy=ref(false),error=ref(''),info=ref<UpdateState|null>(null),selected=ref(''),confirm=ref<'update'|'rollback'|null>(null),rollbackVersion=ref(''),pending=ref<VersionOperation|null>(null),countdown=ref<number|null>(null),waiting=ref(false);
const storeKey='guangyue-version-operation';let timer:ReturnType<typeof setTimeout>|undefined;let stopped=false;let countdownUntil=0;
const stages:Record<string,string>={queued:'等待执行',downloading:'下载安装包',verifying:'校验安装包',backing_up:'创建回滚备份',installing:'切换版本',restarting:'重启并检查健康',recovering:'恢复原版本',succeeded:'版本切换成功',failed:'版本操作失败'};
const releases=computed(()=>info.value?.releases?.filter(r=>newerVersion(r.version,info.value?.current_version||props.version))||[]);
const targetRelease=computed(()=>releases.value.find(r=>r.version===selected.value));
const current=computed(()=>info.value?.current_version||props.version);
const running=computed(()=>!!pending.value);
const operation=computed(()=>info.value?.operation?.request_id===pending.value?.request_id?info.value?.operation:pending.value);
const available=computed(()=>!!info.value?.available);
const dialog=ref<HTMLElement|null>(null);let previousFocus:HTMLElement|null=null;let previousOverflow='';let previousInert=false;let isolated=false;
function releaseDialog(){if(!isolated)return;const app=document.getElementById('app');if(app)app.inert=previousInert;document.body.style.overflow=previousOverflow;previousFocus?.focus();isolated=false;}
watch([open,confirm,running],async()=>{
 if(!open.value){releaseDialog();return;}
 if(!isolated){previousFocus=document.activeElement as HTMLElement;previousOverflow=document.body.style.overflow;const app=document.getElementById('app');previousInert=app?.inert||false;if(app)app.inert=true;document.body.style.overflow='hidden';isolated=true;}
 await nextTick();if(open.value&&!dialog.value?.contains(document.activeElement))dialog.value?.focus();
});
async function request(path:string,method='GET',body?:unknown){
 const response=await fetch('/api/updates'+path,{method,credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json','X-Requested-With':'guangyue'},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(30000)});
 let data:any={};try{data=await response.json();}catch{/* The proxy may return a restart page. */}
 if(!response.ok){const e=new Error(data.error||t('暂时无法连接更新服务')) as Error&{status?:number};e.status=response.status;throw e;}
 return data;
}
function remember(op:VersionOperation|null){pending.value=op;try{if(op)sessionStorage.setItem(storeKey,JSON.stringify(op));else sessionStorage.removeItem(storeKey);}catch{/* The operation also persists on the server. */}}
function receive(data:UpdateState){info.value=data;if(!releases.value.some(r=>r.version===selected.value))selected.value=releases.value[0]?.version||'';if(data.operation&&activeStages.has(data.operation.stage)&&!pending.value){remember(data.operation);open.value=true;schedule();}}
async function load(check=false){busy.value=true;error.value='';try{const data=await request(check?'/check':'',check?'POST':'GET',check?{}:undefined);receive(data);error.value=data.warning||'';}catch(e){error.value=(e as Error).message;}finally{busy.value=false;}}
async function toggle(){open.value=!open.value;if(open.value&&!running.value)await load();}
function schedule(){clearTimeout(timer);if(!stopped&&pending.value)timer=setTimeout(poll,countdown.value===null?2000:1000);}
async function poll(){
 if(stopped||!pending.value)return;
 const selectedPending=pending.value;let state:UpdateState|undefined,healthVersion:string|undefined;
 try{state=await request('');receive(state!);}catch{/* Keep the progress panel through the service restart. */}
 try{const r=await fetch('/api/health',{cache:'no-store',signal:AbortSignal.timeout(5000)});if(r.ok)healthVersion=(await r.json()).version;}catch{/* Poll again after the core checks finish. */}
 if(stopped||pending.value?.request_id!==selectedPending.request_id)return;
 const outcome=updateOutcome(selectedPending,state?.operation,healthVersion);
 if(outcome==='failed'){error.value=state?.operation?.error||t('版本操作失败');remember(null);countdown.value=null;return;}
 if(outcome==='success'){
  if(!countdownUntil)countdownUntil=Date.now()+8000;
  countdown.value=Math.max(0,Math.ceil((countdownUntil-Date.now())/1000));
  if(countdown.value===0){remember(null);window.location.reload();return;}
 }else{countdownUntil=0;countdown.value=null;waiting.value=Date.now()-(selectedPending.started_at||0)*1000>600000;}
 if(state?.available&&state.operation?.request_id!==selectedPending.request_id&&!activeStages.has(state.operation?.stage||'')&&Date.now()-(selectedPending.started_at||0)*1000>45000){error.value=t('未收到执行确认，请重新检测版本');remember(null);return;}
 schedule();
}
async function apply(){
 if(!confirm.value||busy.value||running.value)return;
 const action=confirm.value,version=action==='update'?selected.value:rollbackVersion.value;
 const op:VersionOperation={action,version,expected_version:current.value,request_id:crypto.randomUUID(),stage:'queued',started_at:Math.floor(Date.now()/1000)};
 remember(op);confirm.value=null;busy.value=true;error.value='';countdownUntil=0;waiting.value=false;
 try{const response=await request('/apply','POST',{action:op.action,version:op.version,expected_version:op.expected_version,request_id:op.request_id});info.value={...info.value!,operation:response};}
 catch(e){const status=(e as Error&{status?:number}).status;if(status&&status<500){error.value=(e as Error).message;remember(null);}else{error.value=t('连接暂时中断，正在确认更新状态');}}
 finally{busy.value=false;schedule();}
}
function close(){if(!running.value)open.value=false;confirm.value=null;}
function escape(e:KeyboardEvent){
 if(!open.value)return;
 if(e.key==='Escape'){e.preventDefault();close();}
 if(e.key==='Tab'){
  const items=Array.from(dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],select:not(:disabled),summary,[tabindex="0"]')||[]).filter(el=>el.getClientRects().length);
  const first=items[0],last=items.at(-1),active=document.activeElement;
  if(!first){e.preventDefault();dialog.value?.focus();}
  else if(e.shiftKey&&(active===first||active===dialog.value)){e.preventDefault();last?.focus();}
  else if(!e.shiftKey&&(active===last||active===dialog.value)){e.preventDefault();first.focus();}
 }
}
onMounted(async()=>{try{const saved=JSON.parse(sessionStorage.getItem(storeKey)||'null');if(saved?.request_id&&saved?.version){remember(saved);open.value=true;}}catch{/* Ignore invalid browser state. */}await load();schedule();document.addEventListener('keydown',escape);});
onBeforeUnmount(()=>{stopped=true;clearTimeout(timer);document.removeEventListener('keydown',escape);releaseDialog();});
</script>
<template>
 <div class="version-control"><button class="version-badge" :class="{update:releases.length}" @click="toggle" :title="t('版本与更新')" :aria-label="t('版本与更新')" :aria-expanded="open"><ArrowUpCircle :size="13"/><span>v{{current}}</span><i v-if="releases.length"/></button></div>
 <Teleport to="body"><div v-if="open" class="version-shade" @click.self="close"><section ref="dialog" tabindex="-1" class="version-popover" role="dialog" aria-modal="true" :aria-label="t('版本与更新')">
  <header><div><span class="eyebrow">GUANGYUE PANEL</span><h2>{{t('版本与更新')}}</h2></div><button class="icon" :disabled="running" @click="close" :aria-label="t('关闭')"><X :size="18"/></button></header>
  <div class="version-current"><strong>v{{current}}</strong><span>{{t('当前运行版本')}}</span><small v-if="info?.installed_version">{{t('安装基线')}} · v{{info.installed_version}}</small></div>
  <p v-if="error" class="error" role="alert">{{t(error)}}</p>
  <template v-if="running"><div class="update-progress" aria-live="polite"><CheckCircle2 v-if="countdown!==null" :size="24"/><LoaderCircle v-else class="spin" :size="24"/><strong>{{countdown!==null?t('版本切换成功'):t(stages[operation?.stage||'queued']||'等待执行')}}</strong><span>v{{pending?.expected_version}} → v{{pending?.version}}</span><p v-if="countdown!==null">{{countdown}} {{t('秒后自动刷新')}}</p><p v-else>{{t('正在后台处理，服务重启期间会自动重连。')}}</p><p v-if="waiting" class="error">{{t('等待时间较长，请检查服务器运行状态。页面会继续重试。')}}</p></div></template>
  <template v-else-if="confirm"><div class="version-confirm"><h3>{{t(confirm==='update'?'确认更新版本':'确认回退版本')}} · v{{confirm==='update'?selected:rollbackVersion}}</h3><p>{{t('操作前自动创建备份，服务会短暂重启。健康检查通过后，页面将在 8 秒后自动刷新。')}}</p><p v-if="confirm==='rollback'">{{t('保留当前账号、配置和流量数据；不兼容当前数据库的旧版本不能回退。')}}</p><footer><button @click="confirm=null">{{t('取消')}}</button><button class="primary" @click="apply" :disabled="busy">{{t(confirm==='update'?'确认更新':'确认回退')}}</button></footer></div></template>
  <template v-else>
   <p v-if="!available" class="field-help">{{t(info?.error||'正在读取更新状态')}}</p>
   <template v-else><div class="version-check"><span>{{info?.checked_at?new Date(info.checked_at*1000).toLocaleString():t('尚未检查版本')}}</span><button @click="load(true)" :disabled="busy"><RefreshCw :size="14" :class="{spin:busy}"/>{{t('检查更新')}}</button></div>
    <div v-if="releases.length" class="version-release"><label>{{t('选择更新版本')}}<select v-model="selected" :aria-label="t('选择更新版本')"><option v-for="r in releases" :key="r.version" :value="r.version">v{{r.version}}</option></select></label><a v-if="targetRelease" :href="targetRelease.url" target="_blank" rel="noopener noreferrer">{{t('查看发布说明')}}<ExternalLink :size="13"/></a><pre v-if="targetRelease?.notes">{{targetRelease.notes}}</pre><button class="primary full" @click="confirm='update'" :disabled="busy"><ArrowUpCircle :size="16"/>{{t('更新到所选版本')}}</button></div>
    <p v-else class="field-help">{{t(info?.checked_at?'当前没有可用的新版本':'点击检查更新，读取正式发布版本。')}}</p>
    <details class="version-rollback"><summary><History :size="15"/>{{t('版本回退')}}</summary><p class="field-help">{{t('仅列出安装基线之后、具有本机备份的已安装版本。')}}</p><label v-if="info?.rollback_versions?.length">{{t('选择回退版本')}}<select v-model="rollbackVersion" :aria-label="t('选择回退版本')"><option value="">{{t('请选择版本')}}</option><option v-for="r in info.rollback_versions" :key="r.version" :value="r.version" :disabled="!r.compatible">v{{r.version}}{{r.compatible?'':' · '+t('数据库不兼容')}}</option></select></label><p v-else class="field-help">{{t('暂无可回退版本')}}</p><button v-if="info?.rollback_versions?.length" @click="confirm='rollback'" :disabled="busy||!rollbackVersion||!info.rollback_versions.some(r=>r.version===rollbackVersion&&r.compatible)">{{t('回退到所选版本')}}</button></details>
   </template>
   <a class="version-guide" href="https://github.com/wudi000888-svg/guangyue-panel/blob/main/docs/UPDATES.md" target="_blank" rel="noopener noreferrer">{{t('更新与回退说明')}}<ExternalLink :size="13"/></a>
  </template>
 </section></div></Teleport>
</template>
<style>
.version-control{margin:8px 12px 14px 59px}.version-badge{padding:3px 7px;gap:5px;font-size:11px;min-height:24px;color:var(--muted,#94a3b8);border:1px solid var(--border,#26374c);background:transparent;border-radius:6px}.version-badge.update{color:#14b8a6}.version-badge i{width:5px;height:5px;background:currentColor;border-radius:50%}.version-shade{position:fixed;inset:0;background:#02061780;z-index:1600;display:flex;align-items:flex-start;padding:72px 24px 24px;overflow:auto}.version-popover{width:440px;max-width:100%;padding:22px;border:1px solid var(--border,#28384d);background:var(--surface,#122033);color:var(--text,#e2e8f0);border-radius:16px;box-shadow:0 24px 80px #0005}.version-popover header{display:flex;justify-content:space-between;align-items:center}.version-popover h2{font-size:19px;margin:5px 0 0}.version-popover .eyebrow{font-size:9px;color:#14b8a6;letter-spacing:1.6px}.version-current{display:flex;flex-direction:column;gap:5px;margin:25px 0;text-align:center}.version-current strong{font-size:32px;letter-spacing:-1px}.version-current span{font-size:12px}.version-current small,.version-check>span{font-size:11px;color:var(--muted,#94a3b8)}.version-check{display:flex;justify-content:space-between;align-items:center;gap:8px}.version-release{display:grid;gap:12px;margin-top:18px}.version-popover label{display:grid;gap:8px;font-size:12px}.version-popover select{width:100%}.version-release a,.version-guide{display:flex;gap:6px;align-items:center;font-size:12px;color:#14b8a6}.version-release pre{font-family:inherit;font-size:12px;line-height:1.6;white-space:pre-wrap;overflow-wrap:anywhere;max-height:190px;overflow:auto;padding:12px;margin:0;background:#64748b0f;border-radius:9px}.version-rollback{border-top:1px solid var(--border,#28384d);margin-top:20px;padding-top:16px}.version-rollback summary{display:flex;gap:7px;align-items:center;cursor:pointer;font-size:13px}.version-rollback>button{margin-top:12px}.version-guide{margin-top:22px}.update-progress{display:flex;align-items:center;text-align:center;flex-direction:column;gap:10px;padding:15px 0}.update-progress>svg{color:#14b8a6}.update-progress p,.version-confirm p{font-size:13px;line-height:1.65}.version-confirm h3{font-size:15px}.version-confirm footer{display:flex;justify-content:flex-end;gap:10px;margin-top:20px}.version-popover .error{overflow-wrap:anywhere}.app-shell.collapsed .version-control{margin:8px 0;text-align:center}.app-shell.collapsed .version-badge span{display:none}@media(max-width:700px){.version-shade{padding:60px 12px 16px}.version-popover{width:100%;padding:18px}.version-control{margin-left:59px}}
</style>
