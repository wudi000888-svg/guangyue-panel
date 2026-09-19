<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue';
import { KeyRound, Trash2, Copy, X, Power } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import NodeSharing from '../components/NodeSharing.vue';
import TablePageLayout from '../components/TablePageLayout.vue';
import { t } from '../i18n';
import type { SiteStatus } from '../lib/sites';
type Token={id:string;name:string;scope:string;expires:number;last_used:number};
const api=useApi('/fleet'),local=useApi();
const status=ref<SiteStatus|null>(null),controlling=ref(false);
const tokens=ref<Token[]>([]),siteID=ref(''),error=ref(''),busy=ref(false),creating=ref(false),newToken=ref('');
const removal=ref<Token|null>(null),sharingToken=ref<Token|null>(null);
const form=reactive({name:'Pro 主站',scope:'manage',days:90,forever:true});
async function load(){try{const [out,service]=await Promise.all([api<{site_id:string;tokens:Token[]}>(),local<SiteStatus>("/site-status")]);tokens.value=out.tokens;siteID.value=out.site_id;status.value=service;}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}}
function close(){if(busy.value)return;creating.value=false;newToken.value='';removal.value=null;controlling.value=false;error.value='';}
async function create(){busy.value=true;error.value='';try{const out=await api<{connection_token:string}>('/tokens','POST',form);newToken.value=out.connection_token;await load();}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function remove(){if(!removal.value)return;busy.value=true;error.value='';try{await api('/tokens/'+removal.value.id,'DELETE',{});removal.value=null;await load();}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function control(){if(!status.value)return;busy.value=true;error.value='';try{status.value=await local<SiteStatus>('/site-control','PUT',{paused:!status.value.paused});controlling.value=false;}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function copy(){try{await navigator.clipboard.writeText(newToken.value);}catch{error.value=t('剪贴板不可用，请选择内容复制');}}
onMounted(load);onUnmounted(()=>{newToken.value='';});
</script>
<template>
 <TablePageLayout :title="t('配对令牌')" :description="t('子站生成令牌，Pro 粘贴导入即可管理和调度。')">
  <template #actions><button v-if="status" :disabled="busy" @click="controlling=true;error='' "><Power :size="16"/>{{t(status.paused?'恢复服务':'暂停服务')}}</button><button class="primary" @click="creating=true;newToken='';error='' "><KeyRound :size="16"/>{{t('生成子站令牌')}}</button></template>
  <template #notice><p>{{t('本站标识')}} <code>{{siteID}}</code></p><p class="pool-intro">{{t('令牌包含本站地址和管理权限，支持永久有效。撤销后，主站的后续访问立即失效，本地登录和服务不受影响。')}}</p><p v-if="error&&!creating&&!removal&&!controlling" class="error" role="alert">{{t(error)}}</p></template>
  <table class="adaptive-table"><thead><tr><th>{{t('名称')}}</th><th>{{t('权限')}}</th><th>{{t('有效期')}}</th><th>{{t('最近使用')}}</th><th>{{t('操作')}}</th></tr></thead><tbody><tr v-for="token in tokens" :key="token.id"><td :data-label="t('名称')">{{token.name}}</td><td :data-label="t('权限')">{{t(token.scope==='manage'?'站点管理':'只读查看')}}</td><td :data-label="t('有效期')">{{token.expires?new Date(token.expires*1000).toLocaleDateString():t('永久有效')}}</td><td :data-label="t('最近使用')">{{token.last_used?new Date(token.last_used*1000).toLocaleString():'—'}}</td><td :data-label="t('操作')"><button v-if="token.scope==='manage'" @click="sharingToken=token">{{t('节点共享')}}</button><button @click="removal=token;error='' "><Trash2 :size="14"/>{{t('撤销')}}</button></td></tr><tr v-if="!tokens.length"><td colspan="5" class="empty">{{t('尚未创建接入令牌')}}</td></tr></tbody></table>
 </TablePageLayout>
 <NodeSharing v-if="sharingToken" :token="sharingToken" @close="sharingToken=null"/>
 <Teleport to="body"><div v-if="creating||removal||controlling" class="message-overlay" @click.self="close">
  <form v-if="creating" class="compose-card" role="dialog" aria-modal="true" :aria-label="t('生成子站令牌')" @submit.prevent="create"><header><h2>{{t('生成子站令牌')}}</h2><button type="button" class="icon" :disabled="busy" :aria-label="t('关闭')" @click="close"><X/></button></header>
   <template v-if="!newToken"><label>{{t('名称')}}<input v-model="form.name" maxlength="64" required/></label><label class="inline-check"><input v-model="form.forever" type="checkbox"/>{{t('永久有效')}}</label><label v-if="!form.forever">{{t('有效天数')}}<input v-model.number="form.days" type="number" min="1" max="365" required/></label></template>
   <template v-else><p>{{t('令牌只显示一次，请复制到 Pro 的“导入子站令牌”窗口。')}}</p><textarea :value="newToken" readonly rows="5" spellcheck="false" :aria-label="t('子站令牌')"/><button type="button" class="primary" @click="copy"><Copy :size="16"/>{{t('复制令牌')}}</button></template>
   <p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button type="button" :disabled="busy" @click="close">{{t(newToken?'完成':'取消')}}</button><button v-if="!newToken" class="primary" :disabled="busy">{{t('生成令牌')}}</button></footer>
  </form>
  <section v-else-if="removal" class="compose-card" role="alertdialog" aria-modal="true" :aria-label="t('撤销接入令牌')"><h2>{{t('撤销接入令牌')}} · {{removal.name}}</h2><p>{{t('撤销后，使用此令牌的主站将无法继续访问本站。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('取消')}}</button><button class="danger-button" :disabled="busy" @click="remove">{{t('确认')}}</button></footer></section>
  <section v-else-if="controlling" class="compose-card" role="alertdialog" aria-modal="true" :aria-label="t('服务调度')"><h2>{{t(status?.paused?'恢复服务':'暂停服务')}}</h2><p>{{t('暂停会停止子站代理用户的访问；面板登录仍可用。恢复后按原有用户和节点权限提供服务。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('取消')}}</button><button class="primary" :disabled="busy" @click="control">{{t('确认')}}</button></footer></section>
 </div></Teleport>
</template>
<style scoped>.compose-card textarea{width:100%;resize:vertical;font:inherit;overflow-wrap:anywhere}.inline-check{display:flex;flex-direction:row;gap:8px}.inline-check input{width:16px;height:16px}</style>
