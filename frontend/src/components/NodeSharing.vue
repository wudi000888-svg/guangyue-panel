<script setup lang="ts">
import {onMounted,ref} from 'vue';
import {X} from 'lucide-vue-next';
import {useModalFocus} from '../composables/useModalFocus';
import {useApi,isCancelled} from '../lib/api';
import type {Node} from '../types';
import {t} from '../i18n';
const props=defineProps<{token:{id:string;name:string}}>(),emit=defineEmits<{close:[]}>();
const api=useApi('/fleet/tokens/'+props.token.id+'/sharing'),busy=ref(false),error=ref(''),pending=ref(false),nodes=ref<Node[]>([]);
const dialog=ref<HTMLElement|null>(null);
function close(){if(!busy.value)emit('close');}
useModalFocus(ref(true),dialog,close);
const sharing=ref({enabled:true,all:true,node_ids:[] as string[],revision:''});
async function request(save=false){if(busy.value)return;busy.value=true;error.value='';try{const out=await api<{sharing:typeof sharing.value;nodes:Node[];pending:boolean}>('',save?'PUT':'GET',save?sharing.value:undefined);sharing.value=out.sharing;nodes.value=out.nodes;pending.value=out.pending;}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
onMounted(()=>request());
</script>
<template><Teleport to="body"><div class="message-overlay" @click.self="close"><section ref="dialog" tabindex="-1" class="compose-card" role="dialog" aria-modal="true" :aria-label="t('节点共享权限')"><header><h2>{{t('节点共享权限')}} · {{token.name}}</h2><button class="icon" :disabled="busy" :aria-label="t('关闭')" @click="close"><X/></button></header><p>{{t('仅影响此令牌的挂载用户，本站账号和节点继续独立运行。')}}</p><label class="check"><input type="checkbox" :disabled="busy" v-model="sharing.enabled"/>{{t('允许主站挂载')}}</label><label class="check"><input type="checkbox" :disabled="busy" v-model="sharing.all"/>{{t('允许共享全部节点')}}</label><div v-if="!sharing.all" class="nodes"><label v-for="n in nodes" :key="n.id" class="check"><input type="checkbox" :disabled="busy" v-model="sharing.node_ids" :value="n.id"/>{{n.name}} · {{n.protocol.toUpperCase()}}</label></div><p v-if="pending" class="error">{{t('共享策略已保存，核心撤销待确认，请检查运维状态。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('关闭')}}</button><button class="primary" :disabled="busy||!sharing.revision" @click="request(true)">{{t('保存并应用')}}</button></footer></section></div></Teleport></template>
<style scoped>.check{display:flex;flex-direction:row;align-items:center;gap:10px;margin:14px 0}.check input{width:16px;height:16px}.nodes{max-height:45vh;overflow:auto}.compose-card{max-height:90vh;overflow:auto}.check{min-height:44px;font-size:13px;line-height:1.7}.compose-card>p{font-size:13px;line-height:1.8}.compose-card button{min-height:44px}</style>
