<script setup lang="ts">
import {computed,onMounted,ref} from 'vue';
import {X,RefreshCw} from 'lucide-vue-next';
import {useApi,isCancelled} from '../lib/api';
import {usePanelContext} from '../composables/panelContext';
import NodeGroupPicker from './NodeGroupPicker.vue';
import type {ManagedSite,MountedNode} from '../lib/sites';
import {t} from '../i18n';
import {defaultMountGroups,mountGrantPayload} from '../lib/mounts';
const props=defineProps<{site:ManagedSite}>(),emit=defineEmits<{close:[];saved:[]}>();
const api=useApi('/business-sites'),{state,nodeGroups,loadEntitlements,bytes}=usePanelContext();
const current=ref<ManagedSite|null>(null),mounts=ref<MountedNode[]>([]),grants=ref<{user_id:number;budget:number;quota:number;original_budget:number}[]>([]);
const assignment=ref<'groups'|'manual'>('groups');
const savedNotice=ref(false);
const busy=ref(false),error=ref(''),sourceGroup=ref(''),selected=ref<string[]>([]),targetGroups=ref<string[]>([]);
const catalog=computed(()=>current.value?.mount?.catalog);
const visible=computed(()=>catalog.value?.nodes.filter(n=>!sourceGroup.value||n.group_ids?.includes(sourceGroup.value))||[]);
function fill(s:ManagedSite){current.value=s;assignment.value=s.mount?.assignment==='groups'?'groups':'manual';mounts.value=(s.mount?.nodes||[]).map(n=>({...n,group_ids:[...n.group_ids]}));grants.value=s.grants.map(g=>{const u=s.usage?.[g.user_id];const used=u?(u.metered?(u.quota_upload||0)+(u.quota_download||0):u.upload+u.download):0;const budget=g.quota?Math.max(0,g.quota-used):0;return {user_id:g.user_id,budget,quota:g.quota,original_budget:budget};});}
async function load(){busy.value=true;error.value='';try{const [s]=await Promise.all([api<ManagedSite>('/'+props.site.id+'/node-pool'),loadEntitlements()]);fill(s);targetGroups.value=defaultMountGroups(nodeGroups.value);}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
function addSelected(){if(!targetGroups.value.length)return;for(const id of selected.value){if(!mounts.value.some(n=>n.node_id===id))mounts.value.push({node_id:id,name:'',enabled:true,group_ids:[...targetGroups.value]});}selected.value=[];}
function toggleUser(id:number,enabled:boolean){grants.value=grants.value.filter(g=>g.user_id!==id);if(enabled)grants.value.push({user_id:id,budget:0,quota:0,original_budget:0});}
async function save(){if(!current.value?.mount)return;busy.value=true;error.value='';savedNotice.value=false;try{fill(await api<ManagedSite>('/'+props.site.id+'/node-pool','PUT',{revision:current.value.revision,catalog_revision:current.value.mount.catalog.revision,nodes:mounts.value,assignment:assignment.value,grants:assignment.value==='groups'?[]:mountGrantPayload(grants.value)}));savedNotice.value=true;emit('saved');}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function sync(){busy.value=true;error.value='';try{fill(await api<ManagedSite>('/'+props.site.id+'/node-pool/sync','POST',{}));emit('saved');}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
onMounted(load);
</script>
<template>
<Teleport to="body"><div class="message-overlay" @click.self="!busy&&emit('close')"><section class="compose-card node-pool" role="dialog" aria-modal="true" :aria-label="t('子站节点池')">
<header><div><h2>{{t('子站节点池')}} · {{site.name}}</h2><p>{{t('客户端直接连接子站原有节点。仅挂载选中的节点，不改变子站本地账号和出口。')}}</p></div><button class="icon" :disabled="busy" :aria-label="t('关闭')" @click="emit('close')"><X/></button></header>
<p v-if="error" class="error" role="alert">{{t(error)}}</p>
<p v-if="savedNotice" class="field-help" role="status">{{t('挂载配置已保存，授权状态见下方同步信息。')}}</p>
<template v-if="catalog&&current?.mount">
<div class="pool-state"><span>{{t('上次同步')}} {{current.mount.last_sync?new Date(current.mount.last_sync*1000).toLocaleString():'—'}}</span><button :disabled="busy" @click="load"><RefreshCw :size="14"/>{{t('刷新目录')}}</button><button :disabled="busy" @click="sync">{{t('同步授权')}}</button></div>
<p v-if="current.mount.error" class="error" role="status">{{t(current.mount.error)}} · {{t('未确认前不分发新授权，未结算额度继续保留。')}}</p>
<p v-if="catalog.paused" class="error">{{t('子站服务或节点共享已暂停')}}</p>
<p class="pool-guide">{{t('选择子站节点 → 加入主站节点组 → 给用户或套餐分配该组')}}</p><h3>{{t('选择来源节点')}}</h3><label>{{t('子站节点组')}}<select v-model="sourceGroup"><option value="">{{t('全部')}}</option><option v-for="g in catalog.groups" :value="g.id" :key="g.id">{{g.name}}</option></select></label>
<div class="source-nodes"><label><input type="checkbox" :checked="visible.length>0&&visible.every(n=>selected.includes(n.id))" @change="selected=($event.target as HTMLInputElement).checked?visible.map(n=>n.id):[]"/>{{t('选择当前列表')}}</label><label v-for="n in visible" :key="n.id"><input type="checkbox" v-model="selected" :value="n.id" :disabled="!n.enabled||mounts.some(m=>m.node_id===n.id)"/><span>{{n.name}} · {{n.protocol.toUpperCase()}} · {{(n.rate_milli??1000)/1000}}×<small>{{n.protocol==='hy2'?catalog.info.hy2_host:catalog.info.vless_host}}</small></span></label></div>
<p class="field-help">{{t('按组选择只挂载当前节点，子站日后新增节点不会自动加入。')}}</p><NodeGroupPicker v-model="targetGroups" :groups="nodeGroups"/><button :disabled="busy||!selected.length||!targetGroups.length" @click="addSelected">{{t('加入挂载列表')}}</button>
<h3>{{t('已挂载节点')}}</h3><p v-if="!mounts.length" class="field-help">{{t('尚未挂载节点')}}</p>
<div v-for="m in mounts" :key="m.node_id" class="mount-row"><strong>{{catalog.nodes.find(n=>n.id===m.node_id)?.name||t('来源节点已删除')}}</strong><label>{{t('展示别名')}}<input v-model="m.name" maxlength="64"/></label><label class="check"><input type="checkbox" v-model="m.enabled"/>{{t('启用挂载')}}</label><NodeGroupPicker v-model="m.group_ids" :groups="nodeGroups"/><button :disabled="busy" @click="mounts=mounts.filter(n=>n!==m)">{{t('卸载')}}</button></div>
<p class="field-help">{{t('默认子站节点组只是快捷选择，不会自动授权给所有用户。')}}</p>
<details class="assignment-settings" :open="assignment==='manual'"><summary>{{t('高级分配设置')}}</summary><label>{{t('用户分配方式')}}<select v-model="assignment"><option value="groups">{{t('按主站节点组自动分配（推荐）')}}</option><option value="manual">{{t('手动选择用户与预算')}}</option></select></label><p v-if="assignment==='groups'" class="field-help">{{t('用户和套餐权限以主站为准。系统自动预留并同步可用额度，流量直接经过子站节点。')}}</p><template v-else><h3>{{t('成员与额度')}}</h3><p class="field-help">{{t('用户还须拥有对应主站节点组权限。有限配额用户必须分配子站预算，本地只能使用未预留额度。')}}</p>
<div v-for="u in state?.users.filter(u=>!u.archived&&!u.mount_access)" :key="u.id" class="grant-row"><label class="check"><input type="checkbox" :checked="grants.some(g=>g.user_id===u.id)" @change="toggleUser(u.id,($event.target as HTMLInputElement).checked)"/>{{u.username}} · {{u.quota?bytes(u.quota):t('不限')}}</label><label v-if="grants.find(g=>g.user_id===u.id)">{{t('剩余子站预算（GiB）')}}<input type="number" min="0" step="0.01" :value="grants.find(g=>g.user_id===u.id)!.budget/1073741824" @input="grants.find(g=>g.user_id===u.id)!.budget=Math.round(Number(($event.target as HTMLInputElement).value)*1073741824)"/></label></div>
<p class="field-help">{{t('0 仅适用于不限量用户。卸载或暂停只撤回主站授权；离线时等待租约到期和流量结算。')}}</p>
</template></details><p v-if="assignment==='groups'" class="field-help">{{t('已启用按组自动分配。保存后，在用户管理或套餐管理中选择相同节点组即可。')}}</p>
</template><footer><button :disabled="busy" @click="emit('close')">{{t('关闭')}}</button><button class="primary" :disabled="busy||!current" @click="save">{{t(busy?'处理中…':'保存并同步')}}</button></footer>
</section></div></Teleport>
</template>
<style scoped>
.pool-guide{padding:14px;background:var(--surface);border:1px solid var(--border);border-radius:10px}.assignment-settings{margin-top:24px}.assignment-settings summary{cursor:pointer;margin-bottom:14px}.node-pool{width:min(940px,94vw);max-width:940px;max-height:92dvh;overflow:auto}.pool-state{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.source-nodes{display:grid;gap:10px;max-height:260px;overflow:auto;margin:12px 0;padding:14px;border:1px solid var(--border);border-radius:10px}.source-nodes label,.check{display:flex;flex-direction:row;align-items:center;gap:10px}.source-nodes input,.check input{width:16px;height:16px}.source-nodes small{display:block;color:var(--muted);overflow-wrap:anywhere}.mount-row,.grant-row{padding:16px;border:1px solid var(--border);border-radius:10px;margin:10px 0;display:grid;gap:12px}.mount-row{grid-template-columns:1fr 1fr}.mount-row>button{align-self:end;justify-self:end}.mount-row>strong{grid-column:1/-1}h3{margin-top:24px}.node-pool header{align-items:flex-start}.node-pool header p{font-size:13px;color:var(--muted)}@media(max-width:640px){.mount-row{grid-template-columns:1fr}}
</style>
