<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue';
import {X,ArrowRight,Package,ShieldCheck} from 'lucide-vue-next';
import type {Plan,User} from '../types';
import {useApi,isCancelled} from '../lib/api';
import {bytes,date} from '../lib/format';
import {quotaUsed} from '../lib/quota';
import {t} from '../i18n';
import {useModalFocus} from '../composables/useModalFocus';
const props=defineProps<{users:User[]}>(),emit=defineEmits<{close:[];updated:[]}>(),api=useApi();
const plans=ref<Plan[]>([]),action=ref('assign'),planID=ref(''),days=ref(30),busy=ref(false),error=ref(''),dialog=ref<HTMLElement|null>(null),operationID=crypto.randomUUID();
type Effect={id:number;username:string;before:User;after:User;exhausted:boolean};
const preview=ref<{effects:Effect[];expected:string}|null>(null);
useModalFocus(computed(()=>true),dialog,()=>{if(!busy.value)emit('close');});
watch([action,planID,days],()=>preview.value=null);
const body=()=>({ids:props.users.map(u=>u.id),action:action.value,plan_id:planID.value,days:days.value,operation_id:operationID});
async function submit(){busy.value=true;error.value='';try{if(!preview.value){preview.value=await api('/entitlements/batch','POST',{...body(),preview:true});}else{await api('/entitlements/batch','POST',{...body(),expected:preview.value.expected});emit('updated');emit('close');}}catch(e){if(!isCancelled(e)){error.value=(e as Error).message;preview.value=null;}}finally{busy.value=false;}}
const groupsChanged=(e:Effect)=>{const old=e.before.entitlement?.group_ids??['legacy-private','legacy-public'],next=e.after.entitlement?.group_ids??['legacy-private','legacy-public'];return {added:next.filter(g=>!old.includes(g)).length,removed:old.filter(g=>!next.includes(g)).length};};
onMounted(async()=>{try{plans.value=(await api<{plans:Plan[]}>('/plans')).plans.filter(p=>!p.archived);}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}});
</script>
<template>
<Teleport to="body"><div class="modal-shade" @click.self="!busy&&emit('close')"><form class="modal entitlement-dialog" ref="dialog" tabindex="-1" role="dialog" aria-modal="true" :aria-label="t('套餐与权益')" @submit.prevent="submit"><div class="modal-head"><h2><Package :size="19"/>{{t('套餐与权益')}}</h2><button type="button" class="icon" :disabled="busy" :aria-label="t('关闭')" @click="emit('close')"><X :size="20"/></button></div><p class="field-help">{{t('已选择')}} {{users.length}} {{t('位成员')}} · {{users.map(u=>u.username).join('、')}}</p>
 <template v-if="!preview"><label>{{t('操作类型')}}<select v-model="action" :aria-label="t('操作类型')"><option value="assign">{{t('分配或更换套餐')}}</option><option value="renew">{{t('延长有效期')}}</option><option value="reset">{{t('开始新的配额周期')}}</option><option value="independent">{{t('切换为独立配置')}}</option><option value="disable">{{t('停用账号')}}</option></select></label><label v-if="action==='assign'">{{t('选择套餐')}}<select v-model="planID" required><option value="" disabled>{{t('请选择套餐')}}</option><option v-for="p in plans" :key="p.id" :value="p.id">{{p.name}} · v{{p.version}} · {{p.quota?bytes(p.quota):t('不限')}}</option></select></label><label v-if="action==='renew'">{{t('延长天数')}}<input v-model.number="days" type="number" min="1" max="36500" required/></label><div class="entitlement-note"><ShieldCheck :size="18"/><p>{{action==='reset'?t('先撤回旧周期授权，再开始新周期。离线业务站确认前保持等待，实际累计流量不会清零。'):action==='renew'?t('从当前到期时间或现在起延长，不重置已用额度。'):action==='independent'?t('保留额度、协议和到期时间，恢复默认普通与公共节点组权限。'):t('更换套餐保留当前已用额度。权限变化会同步到代理核心和业务站。')}}</p></div></template>
 <template v-else><h3>{{t('确认权益变更')}}</h3><div class="effects"><article v-for="e in preview.effects" :key="e.id"><strong>{{e.username}}</strong><div class="effect-plans"><span>{{e.before.entitlement?.name||t('独立配置')}}</span><ArrowRight :size="15"/><span>{{e.after.entitlement?.name||t('独立配置')}}</span></div><dl><div><dt>{{t('配额用量')}}</dt><dd>{{bytes(quotaUsed(e.after))}} / {{e.after.quota?bytes(e.after.quota):t('不限')}}</dd></div><div><dt>{{t('到期时间')}}</dt><dd>{{date(e.after.expires)}}</dd></div><div><dt>{{t('节点组变化')}}</dt><dd>+{{groupsChanged(e).added}} / −{{groupsChanged(e).removed}}</dd></div></dl><span v-if="e.exhausted" class="badge danger">{{t('应用后额度用尽')}}</span></article></div><p class="field-help">{{t('业务站在下一次同步后生效。VLESS 撤权会重启核心并中断本站已有 VLESS 连接。')}}</p></template>
 <p v-if="error" class="error" role="alert">{{t(error)}}</p><div class="modal-footer"><button type="button" :disabled="busy" @click="preview?preview=null:emit('close')">{{preview?t('返回修改'):t('取消')}}</button><button class="primary" :disabled="busy||(!preview&&action==='assign'&&!planID)">{{busy?t('处理中…'):preview?t('确认应用'):t('预览影响')}}</button></div></form></div></Teleport>
</template>
<style scoped>
.entitlement-dialog{max-width:640px;max-height:92dvh;overflow:auto}.modal-head h2{display:flex;align-items:center;gap:10px}.entitlement-dialog>label{margin:18px 0}.entitlement-note{display:flex;align-items:flex-start;gap:12px;border:1px solid var(--border);border-radius:10px;background:var(--surface-hover);padding:15px;margin:18px 0}.entitlement-note svg{flex-shrink:0;color:var(--accent-text);margin-top:3px}.entitlement-note p{font-size:12px;line-height:1.8;margin:0;color:var(--muted)}.effects{display:flex;flex-direction:column;gap:10px;max-height:48dvh;overflow:auto}.effects article{padding:16px;border:1px solid var(--border);border-radius:10px;font-size:13px}.effect-plans{display:flex;align-items:center;gap:9px;margin:10px 0;color:var(--muted)}dl{margin:10px 0 0;display:flex;flex-direction:column;gap:9px}dl>div{display:flex;justify-content:space-between;gap:12px}dt{color:var(--muted)}dd{margin:0}.modal-footer{position:sticky;bottom:-24px;background:var(--surface);padding:16px 0}
</style>
