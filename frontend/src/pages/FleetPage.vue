<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue';
import { KeyRound, Trash2, Copy, X, Power } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import NodeSharing from '../components/NodeSharing.vue';
import {useModalFocus} from '../composables/useModalFocus';
import '../styles/admin-ui.css';
import TablePageLayout from '../components/TablePageLayout.vue';
import { t } from '../i18n';
import type { SiteStatus } from '../lib/sites';
type Token={id:string;name:string;scope:string;expires:number;last_used:number};
const api=useApi('/fleet'),local=useApi();
const status=ref<SiteStatus|null>(null),controlling=ref(false);
const tokens=ref<Token[]>([]),siteID=ref(''),error=ref(''),busy=ref(false),creating=ref(false),newToken=ref(''),copied=ref(false),loaded=ref(false);
const removal=ref<Token|null>(null),sharingToken=ref<Token|null>(null);
const dialog=ref<HTMLElement|null>(null);
useModalFocus(computed(()=>creating.value||!!removal.value||controlling.value),dialog,close);
const form=reactive({name:'Pro 主站',scope:'manage',days:90,forever:true});
async function load(){try{const [out,service]=await Promise.all([api<{site_id:string;tokens:Token[]}>(),local<SiteStatus>("/site-status")]);tokens.value=out.tokens;siteID.value=out.site_id;status.value=service;loaded.value=true;error.value='';}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}}
function close(){if(busy.value)return;creating.value=false;newToken.value='';copied.value=false;removal.value=null;controlling.value=false;error.value='';}
async function create(){if(busy.value)return;busy.value=true;error.value='';try{const out=await api<{connection_token:string}>('/tokens','POST',form);newToken.value=out.connection_token;await load();}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function remove(){if(!removal.value)return;busy.value=true;error.value='';try{await api('/tokens/'+removal.value.id,'DELETE',{});removal.value=null;await load();}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function control(){if(!status.value)return;busy.value=true;error.value='';try{status.value=await local<SiteStatus>('/site-control','PUT',{paused:!status.value.paused});controlling.value=false;}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function copy(){try{await navigator.clipboard.writeText(newToken.value);copied.value=true;}catch{error.value=t('剪贴板不可用，请选择内容复制');}}
onMounted(load);onUnmounted(()=>{newToken.value='';});
</script>
<template>
 <TablePageLayout class="admin-page pairing-page" :title="t('配对令牌')" :description="t('子站生成令牌，Pro 粘贴导入即可管理和调度。')">
  <template #actions><button v-if="status" :disabled="busy" @click="controlling=true;error='' "><Power :size="16"/>{{t(status.paused?'恢复服务':'暂停服务')}}</button><button class="primary" @click="creating=true;newToken='';error='' "><KeyRound :size="16"/>{{t('生成子站令牌')}}</button></template>
  <template #notice><div class="admin-context-strip"><KeyRound :size="20"/><div><strong>{{t('本站标识')}} <code>{{siteID||'—'}}</code></strong><p>{{t('令牌连接不改变本站的独立登录与运营能力。管理范围和节点共享由本站控制。')}}</p></div></div><ol class="admin-journey"><li><span>1</span><div><strong>{{t('生成连接令牌')}}</strong><p>{{t('选择管理或只读权限，设置有效期。')}}</p></div></li><li><span>2</span><div><strong>{{t('在 Pro 主站导入')}}</strong><p>{{t('复制完整令牌，即可连接本站。')}}</p></div></li><li><span>3</span><div><strong>{{t('配置节点共享')}}</strong><p>{{t('可随时调整共享范围或撤销令牌。')}}</p></div></li></ol><p v-if="error&&!creating&&!removal&&!controlling" class="error" role="alert">{{t(error)}}</p></template>
  <table class="adaptive-table"><thead><tr><th>{{t('名称')}}</th><th>{{t('权限')}}</th><th>{{t('有效期')}}</th><th>{{t('最近使用')}}</th><th>{{t('操作')}}</th></tr></thead><tbody><tr v-for="token in tokens" :key="token.id"><td :data-label="t('名称')">{{token.name}}</td><td :data-label="t('权限')">{{t(token.scope==='manage'?'站点管理':'只读查看')}}</td><td :data-label="t('有效期')">{{token.expires?new Date(token.expires*1000).toLocaleDateString():t('永久有效')}}</td><td :data-label="t('最近使用')">{{token.last_used?new Date(token.last_used*1000).toLocaleString():'—'}}</td><td :data-label="t('操作')"><button v-if="token.scope==='manage'" @click="sharingToken=token">{{t('节点共享')}}</button><button @click="removal=token;error='' "><Trash2 :size="14"/>{{t('撤销')}}</button></td></tr><tr v-if="!tokens.length"><td colspan="5" class="empty">{{t(loaded?'尚未创建接入令牌':'加载中…')}}</td></tr></tbody></table>
 </TablePageLayout>
 <NodeSharing v-if="sharingToken" :token="sharingToken" @close="sharingToken=null"/>
 <Teleport to="body"><div v-if="creating||removal||controlling" class="message-overlay" @click.self="close">
  <form v-if="creating" ref="dialog" tabindex="-1" class="compose-card" role="dialog" aria-modal="true" :aria-label="t('生成子站令牌')" @submit.prevent="create"><header><h2>{{t('生成子站令牌')}}</h2><button type="button" class="icon" :disabled="busy" :aria-label="t('关闭')" @click="close"><X/></button></header>
   <template v-if="!newToken"><label>{{t('名称')}}<input v-model="form.name" maxlength="64" required/></label><label>{{t('管理权限')}}<select v-model="form.scope"><option value="manage">{{t('站点管理')}}</option><option value="read">{{t('只读查看')}}</option></select><small>{{t(form.scope==='manage'?'允许主站管理用户、节点、服务与共享资源。':'仅允许查看站点状态，不允许修改配置或挂载节点。')}}</small></label><label class="inline-check"><input v-model="form.forever" type="checkbox"/>{{t('永久有效')}}</label><label v-if="!form.forever">{{t('有效天数')}}<input v-model.number="form.days" type="number" min="1" max="365" required/></label></template>
   <template v-else><p>{{t('令牌只显示一次，请复制到 Pro 的“导入子站令牌”窗口。')}}</p><textarea :value="newToken" readonly rows="5" spellcheck="false" :aria-label="t('子站令牌')"/><button type="button" class="primary" @click="copy"><Copy :size="16"/>{{t(copied?'已复制':'复制令牌')}}</button></template>
   <p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button type="button" :disabled="busy" @click="close">{{t(newToken?'完成':'取消')}}</button><button v-if="!newToken" class="primary" :disabled="busy">{{t('生成令牌')}}</button></footer>
  </form>
  <section v-else-if="removal" ref="dialog" tabindex="-1" class="compose-card" role="alertdialog" aria-modal="true" :aria-label="t('撤销接入令牌')"><h2>{{t('撤销接入令牌')}} · {{removal.name}}</h2><p>{{t('撤销后，使用此令牌的主站将无法继续访问本站。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('取消')}}</button><button class="danger-button" :disabled="busy" @click="remove">{{t('确认')}}</button></footer></section>
  <section v-else-if="controlling" ref="dialog" tabindex="-1" class="compose-card" role="alertdialog" aria-modal="true" :aria-label="t('服务调度')"><h2>{{t(status?.paused?'恢复服务':'暂停服务')}}</h2><p>{{t('暂停会停止子站代理用户的访问；面板登录仍可用。恢复后按原有用户和节点权限提供服务。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('取消')}}</button><button class="primary" :disabled="busy" @click="control">{{t('确认')}}</button></footer></section>
 </div></Teleport>
</template>
<style scoped>.compose-card textarea{width:100%;resize:vertical;font:inherit;overflow-wrap:anywhere}.inline-check{display:flex;flex-direction:row;gap:8px}.inline-check input{width:16px;height:16px}.pairing-page :deep(.table-wrap){border-radius:12px}.pairing-page :deep(td button){min-height:44px}.compose-card{max-height:90dvh;overflow:auto}.compose-card small{font-size:12px;color:var(--secondary);line-height:1.7}.compose-card footer button{min-height:44px}</style>
