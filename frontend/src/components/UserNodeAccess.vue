<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue';
import {X} from 'lucide-vue-next';
import type {User,NodeGroup,GroupMember} from '../types';
import {useApi,isCancelled} from '../lib/api';
import {usePanelContext} from '../composables/panelContext';
import {useModalFocus} from '../composables/useModalFocus';
import {userNodeGroups} from '../lib/nodeGroups';
import NodeGroupPicker from './NodeGroupPicker.vue';
import {t} from '../i18n';
const props=defineProps<{users?:User[];initialGroups?:string[]}>(),emit=defineEmits<{close:[];updated:[]}>();
const {state}=usePanelContext(),api=useApi();
const choices=computed(()=>(props.users||state.value?.users||[]).filter(u=>!u.archived&&!u.mount_access));
const ids=ref<number[]>(props.users?.map(u=>u.id)||[]),groups=ref<NodeGroup[]>([]),members=ref<Record<string,GroupMember[]>>({}),groupIDs=ref<string[]>(props.initialGroups?[...props.initialGroups]:props.users?.length===1?[...userNodeGroups(props.users[0])]:[]);
const mode=ref(props.users?.length===1&&!props.initialGroups?'replace':'add'),busy=ref(false),error=ref(''),loaded=ref(false),dialog=ref<HTMLElement|null>(null),search=ref('');
const review=ref<{expected:string;effects:{id:number;username:string;before:User;after:User}[]}|null>(null);
const selected=computed(()=>choices.value.filter(u=>ids.value.includes(u.id))),visible=computed(()=>choices.value.filter(u=>u.username.toLowerCase().includes(search.value.toLowerCase())));
let operationID=crypto.randomUUID();
const valid=computed(()=>loaded.value&&ids.value.length>0&&ids.value.length<=100&&(mode.value!=='add'||groupIDs.value.length>0));
useModalFocus(computed(()=>true),dialog,()=>{if(!busy.value)emit('close');});
watch([ids,groupIDs,mode],()=>{review.value=null;operationID=crypto.randomUUID();},{deep:true});
const names=(u:User)=>userNodeGroups(u).map(id=>groups.value.find(g=>g.id===id)?.name||id).join('、')||t('无节点权限');
async function submit(){if(!valid.value)return;busy.value=true;error.value='';const body={ids:ids.value,action:'groups',group_mode:mode.value,group_ids:mode.value==='inherit'?[]:groupIDs.value,operation_id:operationID};try{if(!review.value){review.value=await api('/entitlements/batch','POST',{...body,preview:true});}else{await api('/entitlements/batch','POST',{...body,expected:review.value.expected});emit('updated');emit('close');}}catch(e){if(!isCancelled(e)){error.value=(e as Error).message;review.value=null;}}finally{busy.value=false;}}
onMounted(async()=>{try{const result=await api<{groups:NodeGroup[];members:Record<string,GroupMember[]>}>('/node-groups');groups.value=result.groups;members.value=result.members;loaded.value=true;}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}});
</script>
<template>
<Teleport to="body"><div class="modal-shade" @click.self="!busy&&emit('close')"><form ref="dialog" class="modal user-node-access" tabindex="-1" role="dialog" aria-modal="true" :aria-label="t('分配节点')" @submit.prevent="submit"><header class="modal-head"><h2>{{t('分配节点')}}</h2><button type="button" class="icon" :disabled="busy" :aria-label="t('关闭')" @click="emit('close')"><X/></button></header>
<p class="field-help">{{t('选择用户和节点组即可使用，无需创建套餐。额度、有效期和账号信息保持不变。')}}</p>
<template v-if="!review"><fieldset :disabled="busy" class="access-fields"><template v-if="!users"><label>{{t('选择用户')}}<input v-model="search" :placeholder="t('搜索成员账号')"/></label><div class="user-choices"><label v-for="u in visible" :key="u.id"><input type="checkbox" v-model="ids" :value="u.id"/>{{u.username}}<small v-if="!u.enabled">{{t('已停用')}}</small></label></div></template><p v-else>{{selected.map(u=>u.username).join('、')}}</p>
<NodeGroupPicker v-if="mode!=='inherit'" v-model="groupIDs" :groups="groups" :members="members"/>
<details><summary>{{t('更多权限设置')}}</summary><label>{{t('分配方式')}}<select v-model="mode"><option value="add">{{t('追加所选节点组')}}</option><option value="replace">{{t('仅使用所选节点组')}}</option><option value="inherit">{{t('恢复套餐或默认节点权限')}}</option></select></label></details>
<p class="field-help">{{t(mode==='add'?'保留原有节点权限，再加入勾选的组。':mode==='replace'?'未勾选的组将被移除；不勾选任何组会撤销全部节点权限。':'恢复当前套餐的节点组；独立用户恢复默认普通和公共节点组。')}}</p></fieldset></template>
<template v-else><h3>{{t('确认节点权限')}}</h3><div class="access-review"><article v-for="e in review.effects" :key="e.id"><strong>{{e.username}}</strong><p>{{t('当前')}}：{{names(e.before)}}</p><p>{{t('调整后')}}：{{names(e.after)}}</p></article></div><p class="field-help">{{t('组内节点共享权限。主站立即更新订阅，子站授权在下次同步后生效。')}}</p></template>
<p v-if="error" class="error" role="alert">{{t(error)}}</p><footer class="modal-footer"><button type="button" :disabled="busy" @click="review?review=null:emit('close')">{{t(review?'返回修改':'取消')}}</button><button class="primary" :disabled="busy||!valid">{{t(busy?'处理中…':review?'确认分配':'下一步')}}</button></footer>
</form></div></Teleport>
</template>
<style scoped>
.user-node-access{max-width:720px;max-height:92dvh;overflow:auto}.access-fields{border:0;padding:0;margin:0;min-width:0;display:grid;gap:16px}.user-choices{max-height:200px;overflow:auto;display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;padding:14px;border:1px solid var(--border);border-radius:10px}.user-choices label{display:flex;flex-direction:row;gap:8px;align-items:center;overflow-wrap:anywhere;font-size:13px}.user-choices input{width:16px;height:16px;margin:0}.user-choices small{color:var(--muted)}summary{cursor:pointer;font-size:13px;margin-bottom:12px}.access-review{display:grid;gap:12px;max-height:48dvh;overflow:auto}.access-review article{padding:14px;border:1px solid var(--border);border-radius:9px;font-size:13px}.access-review p{color:var(--muted);line-height:1.6}.modal-footer{position:sticky;bottom:-24px;background:var(--surface);padding:16px 0}@media(max-width:500px){.user-choices{grid-template-columns:1fr}}
</style>
