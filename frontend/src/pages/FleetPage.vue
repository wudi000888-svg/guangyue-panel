<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue';
import { Plus, KeyRound, RefreshCw, ExternalLink, Trash2, Copy, X, Server } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import { usePanelContext } from '../composables/panelContext';
import TablePageLayout from '../components/TablePageLayout.vue';

import { t } from '../i18n';
type Peer={scope:string;id:string;name:string;url:string;site_id:string;created:number};
type Token={id:string;name:string;scope:string;created:number;expires:number;last_used:number};
const api=useApi('/fleet'), siteAPI=useApi();
const {switchSite}=usePanelContext();
const peers=ref<Peer[]>([]),tokens=ref<Token[]>([]),siteID=ref(''),error=ref(''),busy=ref(false),dialog=ref(''),newToken=ref('');
const status=reactive<Record<string,{loading?:boolean;ok?:boolean;nodes?:number;users?:number;error?:string}>>({});
const peerForm=reactive({name:'',url:'',token:''}),tokenForm=reactive({name:'',scope:'manage',days:90});
const removal=ref<{kind:'peers'|'tokens';id:string;name:string}|null>(null);
async function load(){try{const v=await api<{site_id:string;peers:Peer[];tokens:Token[]}>();peers.value=v.peers;tokens.value=v.tokens;siteID.value=v.site_id;}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}}
function close(){if(busy.value)return;dialog.value='';newToken.value='';peerForm.token='';removal.value=null;}
async function save(){busy.value=true;error.value='';try{
  if(dialog.value==='peer'){await api('/peers','POST',peerForm);peerForm.token='';dialog.value='';}
  else{const v=await api<{token:string}>('/tokens','POST',tokenForm);newToken.value=v.token;}
  await load();
}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function remove(){if(!removal.value)return;busy.value=true;error.value='';try{await api('/'+removal.value.kind+'/'+removal.value.id,'DELETE',{});removal.value=null;await load();}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function probe(peer:Peer){if(status[peer.id]?.loading)return;status[peer.id]={loading:true};try{
  const v=await siteAPI<{system:{status:string}}>('/operations','GET',undefined,{headers:{'X-Guangyue-Site':peer.id}});
  status[peer.id]={ok:v.system.status!=="error"};
}catch(e){if(!isCancelled(e))status[peer.id]={ok:false,error:(e as Error).message};}}
async function copyToken(){try{await navigator.clipboard.writeText(newToken.value);}catch{error.value=t('剪贴板不可用，请选择内容复制');}}
onMounted(load);onUnmounted(()=>{newToken.value='';peerForm.token='';});
</script>
<template>
  <div class="fleet-page">
    <RouterLink to="/fleet">← {{t('返回业务站管理')}}</RouterLink>
    <TablePageLayout :title="t('独立面板接入')" :description="t('集中接入和管理不同 VPS 上的独立站点')">
      <template #actions><button @click="dialog='token';newToken='' "><KeyRound :size="16"/>{{t('创建接入令牌')}}</button><button class="primary" @click="dialog='peer'"><Plus :size="16"/>{{t('接入站点')}}</button></template>
      <template #notice><p class="pool-intro"><Server :size="20"/><span>{{t('本站标识')}} <code>{{siteID}}</code> · {{t('各站点独立保存数据、密钥和代理核心配置')}}</span></p><p v-if="error&&!dialog&&!removal" class="error" role="alert">{{t(error)}}</p></template>
      <table class="adaptive-table"><thead><tr><th>{{t('站点名称')}}</th><th>{{t('面板地址')}}</th><th>{{t('站点标识')}}</th><th>{{t('连接状态')}}</th><th>{{t('操作')}}</th></tr></thead><tbody>
        <tr v-for="peer in peers" :key="peer.id"><td :data-label="t('站点名称')"><strong>{{peer.name}}</strong></td><td :data-label="t('面板地址')"><a :href="peer.url" target="_blank" rel="noopener noreferrer">{{peer.url}}</a></td><td :data-label="t('站点标识')"><code>{{peer.site_id}}</code></td><td :data-label="t('连接状态')"><span :class="['badge',status[peer.id]?.ok?'success':status[peer.id]?.error?'danger':'neutral']">{{t(status[peer.id]?.loading?'检测中…':status[peer.id]?.ok?'已连接':status[peer.id]?.error?'连接失败':'尚未检测')}}</span><p v-if="status[peer.id]?.error" class="field-help">{{t(status[peer.id].error||'')}}</p></td><td :data-label="t('操作')"><div class="row-actions"><button :disabled="status[peer.id]?.loading" @click="probe(peer)"><RefreshCw :size="14"/>{{t('检测')}}</button><button :disabled="peer.scope==='read'" :title="peer.scope==='read'?t('只读令牌仅支持状态监测'):''" @click="switchSite(peer.id,peer.name)"><ExternalLink :size="14"/>{{t('管理站点')}}</button><button class="icon" :aria-label="t('移除站点')" @click="removal={kind:'peers',id:peer.id,name:peer.name}"><Trash2 :size="15"/></button></div></td></tr>
        <tr v-if="!peers.length"><td colspan="5" class="empty">{{t('尚未接入其他站点')}}</td></tr>
      </tbody></table>
    </TablePageLayout>
    <TablePageLayout :title="t('本站接入令牌')" :description="t('在其他 Pro 面板接入本站时使用，可随时撤销')">
      <table class="adaptive-table"><thead><tr><th>{{t('名称')}}</th><th>{{t('权限')}}</th><th>{{t('有效期')}}</th><th>{{t('最近使用')}}</th><th>{{t('操作')}}</th></tr></thead><tbody><tr v-for="token in tokens" :key="token.id"><td :data-label="t('名称')">{{token.name}}</td><td :data-label="t('权限')">{{t(token.scope==='manage'?'站点管理':'只读查看')}}</td><td :data-label="t('有效期')">{{new Date(token.expires*1000).toLocaleDateString()}}</td><td :data-label="t('最近使用')">{{token.last_used?new Date(token.last_used*1000).toLocaleString():'—'}}</td><td :data-label="t('操作')"><button @click="removal={kind:'tokens',id:token.id,name:token.name}">{{t('撤销')}}</button></td></tr><tr v-if="!tokens.length"><td colspan="5" class="empty">{{t('尚未创建接入令牌')}}</td></tr></tbody></table>
    </TablePageLayout>
    <Teleport to="body"><div v-if="dialog||removal" class="message-overlay" @click.self="close">
      <form v-if="dialog" class="compose-card" role="dialog" aria-modal="true" :aria-label="t(dialog==='peer'?'接入站点':'创建接入令牌')" @submit.prevent="save">
        <header><h2>{{t(dialog==='peer'?'接入站点':'创建接入令牌')}}</h2><button type="button" class="icon" :aria-label="t('关闭')" @click="close"><X :size="20"/></button></header>
        <p v-if="error" class="error" role="alert">{{t(error)}}</p>
        <template v-if="dialog==='peer'"><label>{{t('站点名称')}}<input v-model="peerForm.name" maxlength="64" required/></label><label>{{t('面板 HTTPS 地址')}}<input v-model="peerForm.url" type="url" placeholder="https://panel.example.com" required/></label><label>{{t('目标站点接入令牌')}}<input v-model="peerForm.token" type="password" autocomplete="off" required/></label><p class="field-help">{{t('先在目标 Pro 面板创建接入令牌，再填入此处。接入时自动核验站点身份。')}}</p></template>
        <template v-else-if="!newToken"><label>{{t('名称')}}<input v-model="tokenForm.name" maxlength="64" required/></label><label>{{t('权限')}}<select v-model="tokenForm.scope"><option value="read">{{t('只读查看')}}</option><option value="manage">{{t('站点管理')}}</option></select></label><label>{{t('有效天数')}}<input v-model.number="tokenForm.days" type="number" min="1" max="365" required/></label><p class="field-help">{{t('接入令牌绑定当前管理员。禁用管理员或撤销令牌后，远程访问立即失效。')}}</p></template>
        <template v-else><p>{{t('令牌只显示一次，请复制到需要接入本站的 Pro 面板。')}}</p><input :value="newToken" readonly type="password" :aria-label="t('接入令牌')"/><button type="button" @click="copyToken"><Copy :size="16"/>{{t('复制令牌')}}</button></template>
        <footer><button type="button" :disabled="busy" @click="close">{{t(newToken?'完成':'取消')}}</button><button v-if="!newToken" class="primary" :disabled="busy">{{t(busy?'验证中…':'保存')}}</button></footer>
      </form>
      <section v-else-if="removal" class="compose-card" role="alertdialog" aria-modal="true"><h2>{{t(removal.kind==='peers'?'移除站点':'撤销接入令牌')}} · {{removal.name}}</h2><p>{{t('此操作停止对应的远程管理授权，不删除目标站点的数据或节点。')}}</p><p v-if="error" class="error">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('取消')}}</button><button class="danger-button" :disabled="busy" @click="remove">{{t('确认')}}</button></footer></section>
    </div></Teleport>
  </div>
</template>
<style scoped>
.fleet-page{display:flex;flex-direction:column;gap:32px}.row-actions{display:flex;align-items:center;gap:8px}.fleet-page small{font-size:11px;color:var(--muted)}.fleet-page code{font-size:11px}
</style>
