<script setup lang="ts">
import {computed,onMounted,ref} from 'vue';
import {useModalFocus} from '../composables/useModalFocus';
import {X,RefreshCw,Search,CheckCircle2,TriangleAlert} from 'lucide-vue-next';
import {useApi,isCancelled} from '../lib/api';
import {usePanelContext} from '../composables/panelContext';
import NodeGroupPicker from './NodeGroupPicker.vue';
import type {ManagedSite,MountedNode} from '../lib/sites';
import type {Node,NodeGroup,GroupMember} from '../types';
import {applyMountGroups,defaultMountGroups,mountGroupSelection} from '../lib/mounts';
import {t} from '../i18n';
const props=defineProps<{site:ManagedSite}>(),emit=defineEmits<{close:[];saved:[site:ManagedSite]}>();
const api=useApi('/business-sites'),mainAPI=useApi(),{nodeGroups,groupMembers,bytes}=usePanelContext();
const current=ref<ManagedSite|null>(null),mounts=ref<MountedNode[]>([]),monthlyBudgetGB=ref(0),cooldownHours=ref(24);
const busy=ref(false),error=ref(''),savedNotice=ref(false),saveStage=ref<'idle'|'loading'|'saving'|'syncing'|'refreshing'|'saved'>('idle'),search=ref(''),targetGroups=ref<string[]>([]),baseline=ref('');
const dialog=ref<HTMLElement|null>(null),groupRefreshFailed=ref(false),savedCount=ref(0);
const close=()=>{if(!busy.value)emit('close');};
useModalFocus(ref(true),dialog,close);
const feedback=computed(()=>{
 if(saveStage.value==='loading')return t('正在读取子站节点…');
 if(saveStage.value==='saving')return t('正在保存挂载并同步子站，请稍候…');
 if(saveStage.value==='syncing')return t('正在同步子站授权…');
 if(saveStage.value==='refreshing')return t('已保存，正在刷新节点组成员…');
 if(error.value)return error.value;
 if(groupRefreshFailed.value)return t('挂载已保存，节点组列表刷新失败，请重试刷新。');
 if(savedNotice.value)return current.value?.mount?.error?t('挂载已保存，子站授权仍待同步。'):t('挂载已保存，节点组成员已更新。');
 return dirty.value?t('有未保存的修改'):t('按套餐节点组自动授权');
});
const catalog=computed(()=>current.value?.mount?.catalog);
const signature=()=>JSON.stringify([mounts.value,targetGroups.value,monthlyBudgetGB.value,cooldownHours.value]);
const dirty=computed(()=>!!current.value&&baseline.value!==signature());
const visible=computed(()=>catalog.value?.nodes.filter(n=>[n.name,n.id,n.protocol,n.probe_ip,host(n)].join(' ').toLowerCase().includes(search.value.toLowerCase()))||[]);
const missing=computed(()=>mounts.value.filter(m=>!catalog.value?.nodes.some(n=>n.id===m.node_id)));
const mount=(id:string)=>mounts.value.find(n=>n.node_id===id);
const groupNames=(ids:string[])=>ids.map(id=>nodeGroups.value.find(g=>g.id===id)?.name||id).join('、')||t('未分配节点组');
function host(n:Node){return n.protocol==='hy2'?catalog.value?.info.hy2_host:catalog.value?.info.vless_host;}
function fill(s:ManagedSite){current.value=s;monthlyBudgetGB.value=(s.monthly_budget||0)/1073741824;cooldownHours.value=(s.cooldown_seconds||86400)/3600;mounts.value=(s.mount?.nodes||[]).map(n=>({...n,group_ids:[...n.group_ids]}));targetGroups.value=mountGroupSelection(mounts.value,defaultMountGroups(nodeGroups.value));baseline.value=signature();}
async function refreshGroupDirectory(){const g=await mainAPI<{groups:NodeGroup[];members:Record<string,GroupMember[]>}>('/node-groups');nodeGroups.value=g.groups;groupMembers.value=g.members;}
async function load(){if(busy.value)return;busy.value=true;saveStage.value='loading';error.value='';try{const [s]=await Promise.all([api<ManagedSite>('/'+props.site.id+'/node-pool'),refreshGroupDirectory()]);fill(s);}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;saveStage.value='idle';}}
function selectNode(n:Node,enabled:boolean){savedNotice.value=false;if(!enabled){mounts.value=mounts.value.filter(m=>m.node_id!==n.id);return;}if(!mount(n.id))mounts.value.push({node_id:n.id,name:'',enabled:true,group_ids:[...targetGroups.value]});}
function selectVisible(){for(const n of visible.value)if(n.enabled)selectNode(n,true);}
function applyTargetGroups(groups=targetGroups.value){if(!mounts.value.length)return;savedNotice.value=false;mounts.value=applyMountGroups(mounts.value,groups);}
async function refreshSavedGroups(){
 saveStage.value='refreshing';
 try{await refreshGroupDirectory();groupRefreshFailed.value=false;}
 catch(e){if(!isCancelled(e))groupRefreshFailed.value=true;}
 finally{saveStage.value='saved';}
}
async function acceptSaved(site:ManagedSite){
 fill(site);savedCount.value=site.mount?.nodes.length||0;savedNotice.value=true;
 // The policy is already durable. Publish it before the secondary read, which
 // may fail independently and must never be presented as a failed save.
 emit('saved',site);
 await refreshSavedGroups();
}
async function retryGroups(){if(busy.value)return;busy.value=true;try{await refreshSavedGroups();}finally{busy.value=false;}}
async function save(){
 if(busy.value||!current.value?.mount)return;
 busy.value=true;error.value='';savedNotice.value=false;groupRefreshFailed.value=false;saveStage.value='saving';
 try{
  if(!Number.isFinite(monthlyBudgetGB.value)||monthlyBudgetGB.value<0||monthlyBudgetGB.value>1073741824||!Number.isFinite(cooldownHours.value)||cooldownHours.value<1||cooldownHours.value>8760)throw new Error(t('子站预算冷却时长无效'));
  const saved=await api<ManagedSite>('/'+props.site.id+'/node-pool','PUT',{revision:current.value.revision,catalog_revision:current.value.mount.catalog.revision,monthly_budget:Math.round(monthlyBudgetGB.value*1073741824),cooldown_seconds:Math.round(cooldownHours.value*3600),nodes:mounts.value,assignment:'groups'});
  await acceptSaved(saved);
 }catch(e){saveStage.value='idle';if(!isCancelled(e))error.value=e instanceof TypeError?t('连接中断，保存结果尚未确认。请刷新目录核对后再操作。'):(e as Error).message;}
 finally{busy.value=false;}
}
async function sync(){
 if(busy.value||dirty.value)return;busy.value=true;error.value='';saveStage.value='syncing';
 try{await acceptSaved(await api<ManagedSite>('/'+props.site.id+'/node-pool/sync','POST',{}));}
 catch(e){saveStage.value='idle';if(!isCancelled(e))error.value=(e as Error).message;}
 finally{busy.value=false;}
}
onMounted(load);
</script>
<template>
<Teleport to="body"><div class="message-overlay" @click.self="!busy&&emit('close')"><section ref="dialog" tabindex="-1" class="compose-card node-pool" role="dialog" aria-modal="true" :aria-label="t('子站节点池')">
<header><div><h2>{{site.name}} · {{t('挂载节点')}}</h2><p>{{t('保存挂载后，套餐包含对应子站节点组的用户自动获得节点，无需单独分配。客户端直接连接子站。')}}</p></div><button class="icon" :disabled="busy" :aria-label="t('关闭')" @click="emit('close')"><X/></button></header>
<template v-if="catalog&&current?.mount">
<div class="pool-state"><span>{{mounts.length}} {{t('个已选节点')}} · {{current.grants.length}} {{t('位已授权用户')}}</span><small>{{t('上次同步')}} {{current.mount.last_sync?new Date(current.mount.last_sync*1000).toLocaleString():'—'}}</small><button :disabled="busy||dirty" @click="load"><RefreshCw :size="14"/>{{t('刷新目录')}}</button><button :disabled="busy||dirty" @click="sync">{{t('立即同步')}}</button></div>
<p v-if="current.mount.error" class="error" role="status">{{t(current.mount.error)}} · {{t('未确认前不分发新授权，未结算额度继续保留。')}}</p><p v-if="catalog.paused" class="error">{{t('子站服务或节点共享已暂停')}}</p>
<fieldset class="pool-fields" :disabled="busy"><details class="target-groups" open><summary>{{t('挂载节点分组')}}：{{groupNames(targetGroups)}}</summary><NodeGroupPicker v-model="targetGroups" :groups="nodeGroups" :exclude-ids="['legacy-private']" @update:model-value="applyTargetGroups"/><p class="field-help">{{t('子站节点不能加入默认本地节点组；选择后会应用到当前已挂载节点和之后新勾选的节点，也可以在单个节点中再调整。')}}</p></details>
<label class="monthly-budget">{{t('子站每月资源预算（GiB）')}}<input v-model.number="monthlyBudgetGB" type="number" min="0" step="0.01" required/><small>{{t('0 表示不限；达到上限后移除所有成员订阅，冷却结束后自动恢复。')}}</small></label><label class="monthly-budget">{{t('预算冷却时长（小时）')}}<input v-model.number="cooldownHours" type="number" min="1" max="8760" step="1" required/><small>{{t('共享预算达到上限后，子站节点暂停此时长；节点组归属不会被修改。')}}</small></label><p v-if="current.monthly_budget" class="field-help">{{t('本月已使用')}} {{bytes(current.monthly_usage||0)}} / {{bytes(current.monthly_budget)}}<template v-if="current.budget_cooldown_until"> · {{t('冷却中，预计')}} {{new Date(current.budget_cooldown_until*1000).toLocaleString()}} {{t('恢复')}}</template></p>
<div class="toolbar"><div class="search"><Search :size="16"/><input v-model="search" :placeholder="t('搜索节点、协议或出口 IP')" :aria-label="t('搜索子站节点')"/></div><button :disabled="!targetGroups.length" @click="selectVisible">{{t('选择当前列表')}}</button></div>
<div class="source-nodes"><article v-for="n in visible" :key="n.id" :class="{selected:!!mount(n.id)}"><div class="node-main"><label class="node-choice"><input type="checkbox" :checked="!!mount(n.id)" :disabled="!mount(n.id)&&(!n.enabled||!targetGroups.length)" @change="selectNode(n,($event.target as HTMLInputElement).checked)"/><span :class="['tag',n.protocol]">{{n.protocol.toUpperCase()}}</span><strong>{{mount(n.id)?.name||n.name}}</strong></label><span class="badge" :class="!n.enabled||mount(n.id)?.enabled===false?'neutral':mount(n.id)?'success':'neutral'">{{t(!n.enabled||mount(n.id)?.enabled===false?'已停用':mount(n.id)?'已选择挂载':'未挂载')}}</span></div>
<div class="node-details"><span>{{t('所属站点')}}：{{site.name}}</span><span>{{t('接入地址')}}：{{host(n)}}:443</span><span>{{t('出口 IP')}}：{{n.probe_ip||t('待检测')}}</span><span>{{t('流量倍率')}}：{{(n.rate_milli??1000)/1000}}×</span></div>
<p class="node-groups">{{t('主站节点组')}}：{{mount(n.id)?groupNames(mount(n.id)!.group_ids):'—'}}</p>
<details v-if="mount(n.id)" class="node-options"><summary>{{t('调整分组、别名或启停')}}</summary><NodeGroupPicker v-model="mount(n.id)!.group_ids" :groups="nodeGroups" :exclude-ids="['legacy-private']"/><label>{{t('展示别名')}}<input v-model="mount(n.id)!.name" maxlength="64"/></label><label class="check"><input type="checkbox" v-model="mount(n.id)!.enabled"/>{{t('启用挂载')}}</label><small>{{n.id}}</small></details>
</article><p v-if="!visible.length" class="field-help">{{t('没有匹配的共享节点')}}</p></div>
<div v-for="m in missing" :key="m.node_id" class="missing-node"><span>{{m.name||m.node_id}} · {{t('来源节点已删除')}}</span><button @click="mounts=mounts.filter(n=>n!==m)">{{t('卸载')}}</button></div>
<p class="field-help">{{t('同名节点按协议分别授权。勾选 VLESS 不会自动勾选 HY2。')}}</p>
<p class="field-help">{{t('用户权限由已生效套餐的节点组决定，子站额度自动同步。请在套餐管理中配置包含的节点组。')}}</p></fieldset>
<p v-if="dirty" class="field-help">{{t('有未保存的修改，请保存后再刷新或同步。')}}</p></template>
<footer class="mount-save-bar">
 <div class="mount-feedback" :class="{warning:!!error||groupRefreshFailed||!!current?.mount?.error}" role="status" aria-live="polite" aria-atomic="true">
  <RefreshCw v-if="busy" :size="18" class="spin"/><TriangleAlert v-else-if="error||groupRefreshFailed||current?.mount?.error" :size="18"/><CheckCircle2 v-else-if="savedNotice&&!dirty" :size="18"/>
  <div><strong>{{feedback}}</strong><small v-if="savedNotice&&!dirty">{{savedCount}} {{t('个节点')}} · {{groupNames(targetGroups)}}</small></div>
 </div>
 <div class="mount-save-actions"><button v-if="groupRefreshFailed" :disabled="busy" @click="retryGroups">{{t('重试刷新列表')}}</button><button :disabled="busy" @click="close">{{t('关闭')}}</button><button class="primary" :disabled="busy||!current" @click="save"><RefreshCw v-if="busy" :size="15" class="spin"/>{{busy?t('处理中…'):savedNotice&&!dirty?t('再次保存'):t('保存挂载')}}</button></div>
</footer>
</section></div></Teleport>
</template>
<style scoped>
.node-pool{width:min(1020px,94vw);max-width:1020px;max-height:92dvh;overflow:auto}.node-pool header{align-items:flex-start}.node-pool header p{font-size:13px;color:var(--muted);line-height:1.7}.pool-state{display:flex;align-items:center;gap:10px;flex-wrap:wrap;padding:14px 0;font-size:13px}.pool-state small{color:var(--muted)}.pool-fields{border:0;padding:0;margin:0;min-width:0}.target-groups{padding:14px;border:1px solid var(--border);border-radius:9px;font-size:13px;margin:12px 0}.target-groups summary,.node-options summary{cursor:pointer;line-height:1.6}.target-groups[open] summary,.node-options[open] summary{margin-bottom:14px}.source-nodes{display:grid;gap:12px;margin:16px 0}.source-nodes article{padding:16px;border:1px solid var(--border);border-radius:10px;background:var(--surface-hover)}.source-nodes article.selected{border-color:var(--accent)}.node-main{display:flex;align-items:center;justify-content:space-between;gap:12px}.node-choice{display:flex;flex-direction:row;align-items:center;gap:10px;font-size:14px;margin:0;min-width:0}.node-choice strong{overflow-wrap:anywhere}.node-choice input,.check input{width:16px;height:16px;margin:0;flex-shrink:0}.node-details{display:grid;grid-template-columns:1fr 1fr;gap:8px;margin:14px 0 8px;font-size:12px;color:var(--muted);overflow-wrap:anywhere}.node-groups{font-size:12px;line-height:1.6}.node-options{font-size:12px}.node-options>label{margin-top:14px}.check{display:flex;flex-direction:row;align-items:center;gap:10px}.missing-node{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px;border:1px solid var(--border);border-radius:9px;font-size:13px}.save-progress,.save-notice{display:flex;align-items:center;gap:8px;padding:10px 12px;border-radius:10px;font-size:13px}.save-progress{color:var(--accent-text);background:var(--accent-soft)}.save-notice{color:var(--accent-text);background:var(--accent-soft)}.node-pool footer{position:sticky;bottom:-28px;background:var(--surface);padding:16px 0;margin-top:16px;display:flex;gap:8px;justify-content:flex-end;flex-wrap:wrap}@media(max-width:640px){.node-details{grid-template-columns:1fr}.node-main{align-items:flex-start}.node-choice{flex-wrap:wrap}.node-pool footer button{flex:1}.node-pool footer .primary{flex-basis:100%}}

.node-pool footer.mount-save-bar{position:sticky;bottom:-28px;border-top:1px solid var(--border);display:flex;justify-content:space-between;gap:16px;z-index:2;padding:16px 0;background:var(--surface)}
.mount-feedback{flex:1;display:flex;align-items:center;gap:9px;min-width:0;color:var(--accent-text)}.mount-feedback svg{flex-shrink:0}.mount-feedback strong{font-size:13px;font-weight:600;line-height:1.6;overflow-wrap:anywhere}.mount-feedback small{display:block;line-height:1.6;color:var(--secondary);font-size:12px}.mount-feedback.warning{color:var(--red)}.mount-save-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
@media(max-width:640px){.node-pool footer.mount-save-bar{bottom:-20px;gap:10px}.mount-feedback,.mount-save-actions{flex-basis:100%}.node-pool footer .mount-save-actions .primary{flex-basis:auto}}
</style>
