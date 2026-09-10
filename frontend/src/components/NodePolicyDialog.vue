<script setup lang="ts">
import {computed,ref} from 'vue';
import {X} from 'lucide-vue-next';
import type {Node,NodeGroup} from '../types';
import {useApi,isCancelled} from '../lib/api';
import {useModalFocus} from '../composables/useModalFocus';
import {t} from '../i18n';
import NodeGroupPicker from './NodeGroupPicker.vue';
const props=defineProps<{nodes:Node[];groups:NodeGroup[]}>(),emit=defineEmits<{close:[];updated:[]}>(),api=useApi();
const rate=ref((props.nodes[0]?.rate_milli??1000)/1000),groupIDs=ref<string[]>([...(props.nodes[0]?.group_ids||[])]),changeRate=ref(true),changeGroups=ref(false),sameExit=ref(false),busy=ref(false),error=ref(''),review=ref<{expected:Record<string,string>;nodes_affected:number}|null>(null),dialog=ref<HTMLElement|null>(null);
useModalFocus(computed(()=>true),dialog,()=>{if(!busy.value)emit('close');});
async function save(){busy.value=true;error.value='';const input={ids:props.nodes.map(n=>n.id),...(changeRate.value?{rate_milli:Math.round(rate.value*1000)}:{}),...(changeGroups.value?{group_ids:groupIDs.value}:{}),same_exit:sameExit.value};try{if(!review.value){review.value=await api('/nodes/policy','POST',{...input,preview:true});}else{await api('/nodes/policy','POST',{...input,expected:review.value.expected});emit('updated');emit('close');}}catch(e){if(!isCancelled(e)){error.value=(e as Error).message;review.value=null;}}finally{busy.value=false;}}
</script>
<template><Teleport to="body"><div class="modal-shade" @click.self="!busy&&emit('close')"><form class="modal node-policy-dialog" ref="dialog" tabindex="-1" role="dialog" aria-modal="true" :aria-label="t('调整倍率与分组')" @submit.prevent="save"><div class="modal-head"><h2>{{t('调整倍率与分组')}}</h2><button type="button" class="icon" :disabled="busy" :aria-label="t('关闭')" @click="emit('close')"><X :size="20"/></button></div><p class="field-help">{{t('已选择')}} {{nodes.length}} {{t('个节点')}} · {{nodes.map(n=>n.name).join('、')}}</p>
 <template v-if="!review"><label class="inline-check"><input v-model="changeRate" type="checkbox"/>{{t('修改倍率')}}</label><label v-if="changeRate">{{t('节点倍率')}}<input v-model.number="rate" type="number" min="0" max="100" step="0.01" required/></label><label class="inline-check"><input v-model="changeGroups" type="checkbox"/>{{t('替换节点组')}}</label><NodeGroupPicker v-if="changeGroups" v-model="groupIDs" :groups="groups" scope="private"/><label class="inline-check"><input v-model="sameExit" type="checkbox"/>{{t('同时应用到相同出口的其他节点')}}</label></template>
 <template v-else><div class="policy-review"><p>{{review.nodes_affected}} {{t('个节点')}}</p><p v-if="changeRate">{{t('新倍率')}} <strong>{{rate}}×</strong></p><p v-if="changeGroups">{{t('新的节点组')}} · {{groupIDs.map(id=>groups.find(g=>g.id===id)?.name||id).join('、')||t('尚未授权')}}</p><p v-if="sameExit">{{t('本次也会修改同出口的 VLESS 和 HY2 节点。')}}</p></div><p class="field-help">{{t('历史用量不重算。取消分组会撤销对应用户权限，VLESS 撤权会重启本站核心。')}}</p></template>
 <p v-if="error" class="error" role="alert">{{t(error)}}</p><div class="modal-footer"><button type="button" :disabled="busy" @click="review?review=null:emit('close')">{{review?t('返回修改'):t('取消')}}</button><button class="primary" :disabled="busy||(!changeRate&&!changeGroups)">{{busy?t('处理中…'):review?t('确认应用'):t('预览影响')}}</button></div></form></div></Teleport></template>
<style scoped>
.node-policy-dialog{max-width:580px;max-height:90dvh;overflow:auto}.node-policy-dialog>label,.node-policy-dialog>.group-picker{margin:18px 0}.inline-check{display:flex;align-items:center;flex-direction:row;gap:9px;font-size:13px}.inline-check input{width:16px;height:16px;margin:0}.policy-review{background:var(--surface-hover);padding:12px 16px;border:1px solid var(--border);border-radius:10px;font-size:13px;line-height:1.8}.modal-footer{position:sticky;bottom:-24px;background:var(--surface);padding:16px 0}
</style>
