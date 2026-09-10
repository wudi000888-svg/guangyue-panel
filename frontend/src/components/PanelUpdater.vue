<script setup lang="ts">
import {computed,nextTick,onMounted,onBeforeUnmount,ref,watch} from 'vue';
import {ArrowUpCircle,History,RefreshCw,X,LoaderCircle,CheckCircle2,ExternalLink} from 'lucide-vue-next';
import {t} from '../i18n';
import {activeStages,newerVersion,updateOutcome,type UpdateState,type VersionOperation} from '../lib/updater';
const emit=defineEmits<{open:[]}>();
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
const badge=ref<HTMLElement|null>(null),position=ref({left:'70px',top:'64px'});
function place(){const box=badge.value?.getBoundingClientRect();const width=Math.min(window.innerWidth<=900?320:288,window.innerWidth-24);position.value={left:Math.max(12,Math.min(box?.left||70,window.innerWidth-width-12))+'px',top:Math.max(12,Math.min((box?.bottom||56)+8,window.innerHeight-220))+'px'};}
const dialog=ref<HTMLElement|null>(null);let previousFocus:HTMLElement|null=null;let previousOverflow='';let previousInert=false;let isolated=false;
function releaseDialog(){if(!isolated)return;const app=document.getElementById('app');if(app)app.inert=previousInert;document.body.style.overflow=previousOverflow;isolated=false;void nextTick(()=>{if(previousFocus?.getClientRects().length&&getComputedStyle(previousFocus).visibility!=='hidden')previousFocus.focus();else document.querySelector<HTMLElement>('.mobile-menu')?.focus();});}
watch(open,value=>{if(value){place();emit('open');}},{flush:'sync'});
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
onMounted(async()=>{try{const saved=JSON.parse(sessionStorage.getItem(storeKey)||'null');if(saved?.request_id&&saved?.version){remember(saved);open.value=true;}}catch{/* Ignore invalid browser state. */}await load();schedule();document.addEventListener('keydown',escape);window.addEventListener('resize',place);});
onBeforeUnmount(()=>{stopped=true;clearTimeout(timer);document.removeEventListener('keydown',escape);window.removeEventListener('resize',place);releaseDialog();});
</script>
<template>
 <div class="version-control"><button ref="badge" class="version-badge" :class="{update:releases.length}" @click="toggle" :title="t('版本与更新')" :aria-label="t('版本与更新')" :aria-expanded="open"><span>v{{current}}</span><i/></button></div>
 <Teleport to="body"><div v-if="open" class="version-shade" @click.self="close"><section ref="dialog" tabindex="-1" class="version-popover" :style="position" role="dialog" aria-modal="true" :aria-label="t('版本与更新')">
  <header><span>{{t('当前版本')}}</span><div><button class="icon" :disabled="busy||running" @click="load(true)" :aria-label="t('检查更新')"><RefreshCw :size="15" :class="{spin:busy}"/></button><button class="icon version-close" :disabled="running" @click="close" :aria-label="t('关闭')"><X :size="15"/></button></div></header>
  <div class="version-current"><strong>v{{current}}</strong><span>{{t('最新版本')}}: {{info?.checked_at?'v'+(info.releases?.[0]?.version||current):t('尚未检查')}}</span></div>
  <div class="version-body">
   <p v-if="error" class="error" role="alert">{{t(error)}}</p>
   <div v-if="running" class="update-progress" aria-live="polite"><CheckCircle2 v-if="countdown!==null" :size="24"/><LoaderCircle v-else class="spin" :size="24"/><strong>{{countdown!==null?t('版本切换成功'):t(stages[operation?.stage||'queued']||'等待执行')}}</strong><span>v{{pending?.expected_version}} → v{{pending?.version}}</span><p v-if="countdown!==null">{{countdown}} {{t('秒后自动刷新')}}</p><p v-else>{{t('正在后台处理，服务重启期间会自动重连。')}}</p><p v-if="waiting" class="error">{{t('等待时间较长，请检查服务器运行状态。页面会继续重试。')}}</p></div>
   <div v-else-if="confirm" class="version-confirm"><h3>{{t(confirm==='update'?'确认更新版本':'确认回退版本')}} · v{{confirm==='update'?selected:rollbackVersion}}</h3><p>{{t('操作前自动创建备份，服务会短暂重启。健康检查通过后，页面将在 8 秒后自动刷新。')}}</p><p v-if="confirm==='rollback'">{{t('保留当前账号、配置和流量数据；不兼容当前数据库的旧版本不能回退。')}}</p><footer><button @click="confirm=null">{{t('取消')}}</button><button class="primary" @click="apply" :disabled="busy">{{t(confirm==='update'?'确认更新':'确认回退')}}</button></footer></div>
   <template v-else>
    <p v-if="!available" class="field-help">{{t(info?.error||'正在读取更新状态')}}</p>
    <template v-else>
     <div v-if="releases.length" class="version-announcement"><ArrowUpCircle :size="24"/><div><strong>{{t('有新版本可用！')}}</strong><span>v{{releases[0]?.version}}</span></div></div>
     <div v-else-if="info?.checked_at&&!error" class="version-latest"><CheckCircle2 :size="18"/>{{t('已是最新版本')}}</div>
     <button v-if="releases.length" class="primary full version-install" @click="selected=releases[0]!.version;confirm='update'" :disabled="busy"><ArrowUpCircle :size="16"/>{{t('立即更新')}}</button>
     <button v-else class="full version-install" @click="load(true)" :disabled="busy"><RefreshCw :size="15" :class="{spin:busy}"/>{{t('检查更新')}}</button>
     <details class="version-advanced"><summary><History :size="14"/>{{t('选择版本与回退')}}</summary>
      <p class="field-help">{{t('安装基线')}} · v{{info?.installed_version}}</p>
      <div v-if="releases.length" class="version-release"><label>{{t('选择更新版本')}}<select v-model="selected" :aria-label="t('选择更新版本')"><option v-for="r in releases" :key="r.version" :value="r.version">v{{r.version}}</option></select></label><a v-if="targetRelease" :href="targetRelease.url" target="_blank" rel="noopener noreferrer">{{t('查看发布说明')}}<ExternalLink :size="12"/></a><button @click="confirm='update'" :disabled="busy">{{t('更新到所选版本')}}</button></div>
      <div class="version-rollback"><p class="field-help">{{t('仅列出安装基线之后、具有本机备份的已安装版本。')}}</p><label v-if="info?.rollback_versions?.length">{{t('选择回退版本')}}<select v-model="rollbackVersion" :aria-label="t('选择回退版本')"><option value="">{{t('请选择版本')}}</option><option v-for="r in info.rollback_versions" :key="r.version" :value="r.version" :disabled="!r.compatible">v{{r.version}}{{r.compatible?'':' · '+t('数据库不兼容')}}</option></select></label><p v-else class="field-help">{{t('暂无可回退版本')}}</p><button v-if="info?.rollback_versions?.length" @click="confirm='rollback'" :disabled="busy||!rollbackVersion||!info.rollback_versions.some(r=>r.version===rollbackVersion&&r.compatible)">{{t('回退到所选版本')}}</button></div>
      <a class="version-help" href="https://github.com/wudi000888-svg/guangyue-panel/blob/main/docs/UPDATES.md" target="_blank" rel="noopener noreferrer">{{t('更新与回退说明')}}<ExternalLink :size="12"/></a>
     </details>
    </template>
   </template>
  </div>
  <a class="version-guide" :href="info?.releases?.[0]?.url||'https://github.com/wudi000888-svg/guangyue-panel/releases'" target="_blank" rel="noopener noreferrer">{{t('查看更新日志')}}<ExternalLink :size="12"/></a>
 </section></div></Teleport>
</template>
<style>
.version-control{margin:6px 12px 12px 59px}.version-badge{padding:2px 7px;gap:5px;font-size:11px;min-height:24px;color:var(--muted);border:0;background:var(--surface-raised);border-radius:6px;font-variant-numeric:tabular-nums}.sidebar .version-badge{color:var(--muted);background:var(--surface-raised)}.version-badge.update,.sidebar .version-badge.update{color:#b77916;background:#fef3c7}.version-badge i{width:6px;height:6px;background:currentColor;border-radius:50%;opacity:.45}.version-badge.update i{background:#f59e0b;opacity:1;box-shadow:0 0 0 3px #f59e0b1c}.version-shade{position:fixed;inset:0;background:transparent;z-index:1600}.version-popover{position:absolute;width:288px;max-width:calc(100vw - 24px);max-height:calc(100dvh - 90px);overflow:auto;overscroll-behavior:contain;border:1px solid var(--border);background:var(--surface);color:var(--text);border-radius:12px;box-shadow:0 8px 32px #0f172a26,0 2px 6px #0f172a12;outline:none}.version-popover>header{display:flex;justify-content:space-between;align-items:center;padding:10px 16px;border-bottom:1px solid var(--border);font-size:12px;font-weight:600}.version-popover header>div{display:flex;gap:2px}.version-popover header .icon{border:0;background:transparent;width:28px;height:28px;min-height:28px;padding:0;color:var(--muted)}.version-current{display:flex;flex-direction:column;gap:5px;padding:18px 12px 16px;text-align:center}.version-current strong{font-size:27px;line-height:1.25;letter-spacing:-.7px;font-variant-numeric:tabular-nums}.version-current span{font-size:11px;color:var(--muted)}.version-body{padding:0 16px 12px}.version-announcement{display:flex;gap:12px;align-items:center;border:1px solid #f5c95f70;border-radius:8px;background:#f59e0b09;padding:12px;color:#ca800b}.version-announcement>svg{background:#f59e0b15;border-radius:50%;padding:5px;box-sizing:content-box}.version-announcement strong,.version-announcement span{display:block;font-size:12px}.version-announcement span{font-size:11px;margin-top:3px}.version-popover .version-install{width:100%;margin-top:8px;min-height:36px;border-radius:7px;font-size:12px;justify-content:center}.version-popover .primary{background:#10b5a0;border-color:#10b5a0;color:#fff}.version-popover .primary:hover{background:#0e9e8c}.version-latest{display:flex;align-items:center;justify-content:center;gap:8px;padding:8px;color:var(--accent-text);font-size:12px}.version-advanced{margin-top:14px}.version-advanced summary{display:flex;align-items:center;justify-content:center;gap:6px;font-size:11px;color:var(--muted);cursor:pointer;min-height:28px}.version-advanced[open] summary{justify-content:flex-start}.version-popover label{display:grid;gap:7px;font-size:12px}.version-popover select{width:100%;font-size:12px}.version-popover .field-help{font-size:11px;line-height:1.7}.version-release{display:grid;gap:9px}.version-release a,.version-help{display:flex;align-items:center;gap:5px;color:var(--accent-text);font-size:11px}.version-rollback{border-top:1px solid var(--border);margin-top:14px;padding-top:4px}.version-rollback>button{width:100%;font-size:12px;margin:10px 0}.version-help{margin-top:12px}.version-guide{display:flex;align-items:center;justify-content:center;gap:5px;padding:10px;border-top:1px solid var(--border);font-size:11px;color:var(--muted);text-decoration:none}.version-guide:hover{color:var(--accent-text)}.update-progress{display:flex;align-items:center;text-align:center;flex-direction:column;gap:8px;padding:10px 0;font-size:12px}.update-progress>svg{color:var(--accent-text)}.update-progress p,.version-confirm p{font-size:12px;line-height:1.7}.version-confirm h3{font-size:13px}.version-confirm footer{display:flex;justify-content:flex-end;gap:8px;margin-top:14px}.version-confirm button{font-size:12px}.version-popover .error{overflow-wrap:anywhere;font-size:12px}.app-shell.collapsed .version-control{margin:8px 0;text-align:center}.app-shell.collapsed .version-badge span{display:none}@media(max-width:900px){.version-shade{background:#02061740}.version-popover{width:320px;max-height:calc(100dvh - 110px)}.version-popover header .icon{height:40px;min-height:40px;width:36px}.version-popover .version-install,.version-confirm button,.version-advanced summary{min-height:44px}.version-popover select{font-size:16px}.version-control{margin-left:59px}}
</style>
