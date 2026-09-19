<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue';
import { Plus, RefreshCw, Server, Trash2, X, Users, RadioTower, Activity, Power, Settings2 } from 'lucide-vue-next';
import { useApi, isCancelled } from '../lib/api';
import { serialPoll } from '../lib/requests';
import { usePanelContext } from '../composables/panelContext';
import TablePageLayout from '../components/TablePageLayout.vue';
import SubsiteNodePool from '../components/SubsiteNodePool.vue';
import ManagedSitePolicy from '../components/ManagedSitePolicy.vue';
import { siteState, type ManagedSite } from '../lib/sites';
import { t } from '../i18n';
const api=useApi('/business-sites');
const {state,selectedSite,switchSite,go,bytes}=usePanelContext();
const settling=ref<ManagedSite[]>([]);
const sites=ref<ManagedSite[]>([]),error=ref(''),loaded=ref(false),refreshing=ref(false),busy=ref(false),adding=ref(false);
const pooling=ref<ManagedSite|null>(null);
const removing=ref<ManagedSite|null>(null),editing=ref<ManagedSite|null>(null),controlling=ref<ManagedSite|null>(null);
const form=reactive({token:'',url:'',name:''});
const jobs=reactive<Record<string,boolean>>({});
const needsAddress=computed(()=>form.token.trim().startsWith('gyp_'));
const online=computed(()=>sites.value.filter(s=>s.last_seen>Date.now()/1000-90&&!s.error).length);
const direct=computed(()=>sites.value.filter(s=>s.connection));
const changing=computed(()=>busy.value||adding.value||!!removing.value||!!editing.value||!!controlling.value||!!pooling.value);
let disposed=false,directoryRevision=0;
function replace(site:ManagedSite){const i=sites.value.findIndex(s=>s.id===site.id);if(i>=0)sites.value[i]=site;}
async function probe(site:ManagedSite){
 if(jobs[site.id])return;jobs[site.id]=true;const revision=directoryRevision;
 try{const updated=await api<ManagedSite>('/'+site.id+'/probe','POST',{});if(revision===directoryRevision)replace(updated);}
 catch(e){if(!isCancelled(e)&&!disposed&&revision===directoryRevision){const current=sites.value.find(s=>s.id===site.id);if(current)current.error=(e as Error).message;}}
 finally{delete jobs[site.id];}
}
async function load(probeAll=false){
 if(refreshing.value)return;refreshing.value=true;const revision=directoryRevision;
 try{const out=await api<{sites:ManagedSite[];settling?:ManagedSite[]}>();if(revision!==directoryRevision)return;sites.value=out.sites;settling.value=out.settling||[];loaded.value=true;error.value='';
  if(probeAll){const queue=[...direct.value];await Promise.all(Array.from({length:Math.min(4,queue.length)},async()=>{while(queue.length&&!disposed){const site=queue.shift();if(site)await probe(site);}}));}
 }catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{refreshing.value=false;}
}
function close(){if(busy.value)return;adding.value=false;removing.value=null;controlling.value=null;form.token='';error.value='';}
async function connect(){directoryRevision++;busy.value=true;error.value='';try{
 const site=await api<ManagedSite>('/import','POST',form);form.token='';form.url='';form.name='';adding.value=false;
 sites.value=sites.value.filter(s=>s.id!==site.id);sites.value.push(site);await probe(site);
}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function remove(){if(!removing.value)return;directoryRevision++;busy.value=true;error.value='';try{
 const id=removing.value.id;await api('/'+id,'DELETE',{});sites.value=sites.value.filter(s=>s.id!==id);removing.value=null;
}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function control(){if(!controlling.value)return;directoryRevision++;busy.value=true;error.value='';try{
 const s=controlling.value;replace(await api<ManagedSite>('/'+s.id+'/control','POST',{paused:!s.connection?.status?.paused}));controlling.value=null;
}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function manage(site:ManagedSite,page:string){await switchSite(site.id,site.name);if(selectedSite.value===site.id)go(page);}
const poll=serialPoll(async()=>{if(!document.hidden&&!changing.value)await load(true);},()=>15000);
onMounted(()=>{void load(true);poll.start();});
onUnmounted(()=>{disposed=true;poll.stop();form.token='';});
</script>
<template>
 <div class="subsite-page">
  <div class="site-summary"><div><Server :size="20"/><span>{{t('主站')}}</span><strong>{{state?.system.site_id}}</strong></div><div><span>{{t('子站')}}</span><strong>{{loaded?sites.length:'—'}} <small>/ 64</small></strong></div><div><span>{{t('在线站点')}}</span><strong>{{loaded?online:'—'}}</strong></div></div>
  <TablePageLayout :title="t('子站管理与调度')" :description="t('复制子站令牌，在 Pro 中粘贴即可连接；无需创建分组或注册文件。')">
   <template #actions><button :disabled="refreshing||busy" @click="load(true)"><RefreshCw :size="15"/>{{t('刷新')}}</button><button class="primary" :disabled="busy" @click="adding=true;error='' "><Plus :size="16"/>{{t('导入子站令牌')}}</button></template>
   <template #notice><p class="pool-intro">{{t('连接后可直接管理子站用户、节点、权限和流量。子站管理员仍可本地登录、独立运营，并随时撤销令牌。')}}</p><p v-if="error&&!adding&&!removing&&!controlling" class="error" role="alert">{{t(error)}}</p></template>
   <table class="adaptive-table"><thead><tr><th>{{t('子站')}}</th><th>{{t('连接状态')}}</th><th>{{t('用户与节点')}}</th><th>{{t('实时流量')}}</th><th>{{t('调度操作')}}</th></tr></thead><tbody>
    <tr v-for="s in sites" :key="s.id">
     <td :data-label="t('子站')"><strong>{{s.name}}</strong><small>{{s.connection?.url||s.info?.vless_host||s.id}}</small><small v-if="s.connection?.status">{{s.connection.status.edition.toUpperCase()}} · v{{s.connection.status.version}}</small></td>
     <td :data-label="t('连接状态')"><span :class="['badge',(s.error||s.connection?.status?.service_error)?'danger':s.last_seen>Date.now()/1000-90?'success':'neutral']">{{t(jobs[s.id]?'检测中…':siteState(s))}}</span><small v-if="s.error" class="site-error">{{t(s.error)}}</small><small v-if="s.connection?.status?.service_error" class="site-error">{{t(s.connection.status.service_error)}}</small><small>{{s.last_seen?new Date(s.last_seen*1000).toLocaleTimeString():'—'}}</small></td>
     <td :data-label="t('用户与节点')"><template v-if="s.connection?.status?.control">{{s.connection.status.users}} {{t('成员')}} · {{s.connection.status.nodes}} {{t('节点')}}<small>{{t('在线用户')}} {{!s.error&&s.connection.status.sampled_at>Date.now()-15000?(s.connection.status.online_users??'—'):'—'}}</small></template><template v-else-if="!s.connection">{{s.grants.length}} {{t('成员')}} · {{s.nodes.length+2}} {{t('节点')}}</template><template v-else>—</template></td>
     <td :data-label="t('实时流量')"><template v-if="s.connection?.status?.live && !s.error && s.connection.status.sampled_at>Date.now()-15000">↑ {{s.connection.status.live.upload_rate==null?'—':bytes(s.connection.status.live.upload_rate)+'/s'}}<br/>↓ {{s.connection.status.live.download_rate==null?'—':bytes(s.connection.status.live.download_rate)+'/s'}}</template><template v-else>—</template><small v-if="s.connection?.status?.control">{{t('累计流量')}} {{bytes(s.connection.status.upload+s.connection.status.download)}}</small></td>
     <td :data-label="t('调度操作')"><div class="row-actions" v-if="s.connection">
      <button v-if="s.connection.scope==='manage'" :disabled="busy" @click="pooling=s">{{t('节点池')}} · {{s.mount?.nodes.length||0}}</button><button :disabled="busy||s.connection.scope!=='manage'" @click="manage(s,'users')"><Users :size="14"/>{{t('用户')}}</button><button :disabled="busy||s.connection.scope!=='manage'" @click="manage(s,'nodes')"><RadioTower :size="14"/>{{t('节点')}}</button><button :disabled="busy||s.connection.scope!=='manage'" @click="manage(s,'monitor')"><Activity :size="14"/>{{t('监控')}}</button>
      <button v-if="s.connection.status?.control&&s.connection.scope==='manage'" :disabled="busy" @click="controlling=s;error='' "><Power :size="14"/>{{t(s.connection.status.paused?'恢复服务':'暂停服务')}}</button>
     </div><div v-else class="row-actions"><button @click="editing=s"><Settings2 :size="14"/>{{t('权限调度')}}</button></div><button class="remove-site" :disabled="busy" @click="removing=s;error='' "><Trash2 :size="14"/>{{t('移除子站')}}</button></td>
    </tr><tr v-if="!sites.length"><td colspan="5" class="empty">{{t(loaded?'在子站生成令牌，然后点击“导入子站令牌”建立连接。':'加载中…')}}</td></tr>
   </tbody></table>
  </TablePageLayout>
  <section v-if="settling.length" class="settling-sites"><h3>{{t('等待结算的已移除子站')}}</h3><p class="field-help">{{t('授权撤回并确认最终用量后释放预留额度；子站离线或令牌已撤销时继续保留账本。')}}</p><p v-for="s in settling" :key="s.id">{{s.name}} · {{t(s.mount?.error||'等待撤销与流量结算')}}</p></section>
  <SubsiteNodePool v-if="pooling" :site="pooling" @close="pooling=null" @saved="load()"/>
  <ManagedSitePolicy v-if="editing" :site="editing" @close="editing=null" @saved="load()"/>
  <Teleport to="body"><div v-if="adding||removing||controlling" class="message-overlay" @click.self="close">
   <form v-if="adding" class="compose-card" role="dialog" aria-modal="true" :aria-label="t('导入子站令牌')" @submit.prevent="connect">
    <header><h2>{{t('导入子站令牌')}}</h2><button type="button" class="icon" :disabled="busy" :aria-label="t('关闭')" @click="close"><X/></button></header>
    <p>{{t('在子站的“配对令牌”页面生成管理令牌，复制完整内容粘贴到这里。')}}</p>
    <label>{{t('子站令牌')}}<textarea v-model="form.token" rows="4" maxlength="8192" autocomplete="off" spellcheck="false" required/></label>
    <label v-if="needsAddress">{{t('子站 HTTPS 地址')}}<input v-model="form.url" type="url" placeholder="https://child.example.com" required/></label>
    <label>{{t('备注名称（选填）')}}<input v-model="form.name" maxlength="64"/></label>
    <p class="field-help">{{t('新令牌自带地址，旧 gyp_ 令牌补填地址即可。重复导入同一站点会更新连接，不会新增重复记录。')}}</p>
    <p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button type="button" :disabled="busy" @click="close">{{t('取消')}}</button><button class="primary" :disabled="busy">{{t(busy?'正在连接…':'连接子站')}}</button></footer>
   </form>
   <section v-else-if="removing" class="compose-card" role="alertdialog" aria-modal="true" :aria-label="t('移除子站')"><h2>{{t('移除子站')}} · {{removing.name}}</h2><p>{{t('移除后主站停止管理此站点，子站的软件、账号和数据保留。离线子站也可以移除，之后可重新导入令牌。')}}</p><p v-if="!removing.connection||removing.mount" class="field-help">{{t('移除后立即停止分发挂载节点，离线授权等待租约到期，未结算额度继续保留。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('取消')}}</button><button class="danger-button" :disabled="busy" @click="remove">{{t('确认移除')}}</button></footer></section>
   <section v-else-if="controlling" class="compose-card" role="alertdialog" aria-modal="true" :aria-label="t('服务调度')"><h2>{{t(controlling.connection?.status?.paused?'恢复服务':'暂停服务')}} · {{controlling.name}}</h2><p>{{t('暂停会停止子站代理用户的访问；面板登录仍可用。恢复后按原有用户和节点权限提供服务。')}}</p><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button :disabled="busy" @click="close">{{t('取消')}}</button><button class="primary" :disabled="busy" @click="control">{{t('确认')}}</button></footer></section>
  </div></Teleport>
 </div>
</template>
<style scoped>
.subsite-page{display:flex;flex-direction:column;gap:24px}.site-summary{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}.site-summary>div{display:flex;flex-direction:column;gap:10px;border:1px solid var(--border);border-radius:12px;padding:20px;background:var(--surface)}.site-summary span,small{font-size:12px;color:var(--muted)}.site-summary strong{font-size:23px;overflow-wrap:anywhere}.subsite-page td small{display:block;margin-top:7px;overflow-wrap:anywhere}.row-actions{display:flex;gap:6px;flex-wrap:wrap}.remove-site{margin-top:10px;color:var(--danger)}.site-error{max-width:240px}.compose-card textarea{width:100%;resize:vertical;font:inherit;overflow-wrap:anywhere}.compose-card{max-height:90dvh;overflow:auto}@media(max-width:700px){.site-summary{grid-template-columns:1fr}.row-actions{justify-content:flex-end}}
</style>
