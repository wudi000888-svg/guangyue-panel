<script setup lang="ts">
import {computed,onMounted,onUnmounted,reactive,ref} from 'vue';
import {Plus,RefreshCw,X} from 'lucide-vue-next';
import {useApi,isCancelled} from '../lib/api';
import {serialPoll} from '../lib/requests';
import {usePanelContext} from '../composables/panelContext';
import {type ManagedSite} from '../lib/sites';
import SubsiteNodePool from './SubsiteNodePool.vue';
import GroupNodeList from './GroupNodeList.vue';
import type {GroupMember} from '../types';
import {t} from '../i18n';
const api=useApi('/business-sites'),{go}=usePanelContext();
const sites=ref<ManagedSite[]>([]),selected=ref<ManagedSite|null>(null),error=ref(''),loaded=ref(false),busy=ref(false),adding=ref(false);
const form=reactive({token:'',url:'',name:''});
const pools=computed(()=>sites.value.filter(s=>s.connection?.scope==='manage'));
let disposed=false;
async function load(){try{const out=await api<{sites:ManagedSite[]}>();if(disposed)return;sites.value=out.sites;loaded.value=true;error.value='';}catch(e){if(!isCancelled(e)&&!disposed)error.value=(e as Error).message;}}
async function connect(){busy.value=true;error.value='';try{const site=await api<ManagedSite>('/import','POST',form);form.token='';form.url='';form.name='';adding.value=false;await load();selected.value=site;}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
function close(){if(busy.value)return;adding.value=false;form.token='';error.value='';}
function members(s:ManagedSite):GroupMember[]{return (s.mount?.nodes||[]).map(m=>{const n=s.mount?.catalog.nodes.find(n=>n.id===m.node_id);return {site_id:s.id,site_name:s.name,source:'mounted',node_id:m.node_id,name:m.name||n?.name||m.node_id,protocol:n?.protocol||'—',entry_host:n?.protocol==='hy2'?s.mount?.catalog.info.hy2_host:s.mount?.catalog.info.vless_host,exit_ip:n?.probe_ip,status:!m.enabled||!n?.enabled||!s.enabled||s.mount?.catalog.paused?'disabled':s.mount?.error||!s.mount?.lease_until||s.mount.lease_until<Date.now()/1000?'pending':'ready'};});}
const poll=serialPoll(async()=>{if(!document.hidden&&!selected.value&&!adding.value)await load();},()=>15000);
onMounted(()=>{void load();poll.start();});onUnmounted(()=>{disposed=true;poll.stop();form.token='';});
</script>
<template>
<section class="subsite-pools">
 <div class="resource-section-heading"><div><h2>{{t('子站节点池')}}</h2><p>{{t('挂载后客户端直接连接子站，流量不经过主站。子站节点与子站 IP 池是两种独立的出口方式。')}}</p></div><div class="pool-actions"><button @click="load"><RefreshCw :size="15"/>{{t('刷新')}}</button><button class="primary" @click="adding=true"><Plus :size="15"/>{{t('导入子站令牌')}}</button></div></div>
 <ol class="pool-steps"><li>{{t('导入子站令牌')}}</li><li>{{t('勾选需要的节点')}}</li><li>{{t('保存并分配用户')}}</li></ol>
 <p v-if="error&&!adding" class="error" role="alert">{{t(error)}}</p>
 <div class="pool-links"><button @click="go('users')">{{t('用户管理')}}</button><button @click="go('plans?tab=groups')">{{t('查看节点分组')}}</button><button @click="go('subscription')">{{t('订阅管理')}}</button><button @click="go('fleet')">{{t('子站管理与调度')}}</button></div>
 <div v-if="pools.length" class="pool-cards"><article v-for="s in pools" :key="s.id"><header><h3>{{s.name}}</h3><span class="badge neutral">{{s.mount?.nodes.filter(n=>n.enabled).length||0}} {{t('已启用挂载')}}</span></header><p class="muted">{{s.connection?.url}}</p><p>{{t('分配方式')}} · {{t(!s.mount||s.mount.assignment==='groups'?'按主站节点组自动分配':'手动选择用户与预算')}}</p><p v-if="s.mount?.error||s.error" class="error">{{t(s.mount?.error||s.error)}}</p><p v-else-if="s.mount?.last_sync" class="field-help">{{t('上次同步')}} {{new Date(s.mount.last_sync*1000).toLocaleString()}}</p><GroupNodeList v-if="s.mount?.nodes.length" :members="members(s)"/><button class="primary" @click="selected=s">{{t('选择与挂载节点')}}</button></article></div>
 <div v-else class="pool-empty"><h3>{{t(loaded?'尚未连接可挂载的子站':'加载中…')}}</h3><p>{{t('在子站生成管理令牌，导入后即可选择共享节点。默认子站节点组已准备好。')}}</p><button v-if="loaded" @click="adding=true">{{t('导入子站令牌')}}</button></div>
 <SubsiteNodePool v-if="selected" :site="selected" @close="selected=null" @saved="load"/>
 <Teleport to="body"><div v-if="adding" class="message-overlay" @click.self="close"><form class="compose-card" role="dialog" aria-modal="true" :aria-label="t('导入子站令牌')" @submit.prevent="connect"><header><h2>{{t('导入子站令牌')}}</h2><button type="button" class="icon" :disabled="busy" :aria-label="t('关闭')" @click="close"><X/></button></header><p>{{t('在子站的“配对令牌”页面生成管理令牌，复制完整内容粘贴到这里。')}}</p><label>{{t('子站令牌')}}<textarea v-model="form.token" rows="4" maxlength="8192" autocomplete="off" spellcheck="false" required/></label><label v-if="form.token.trim().startsWith('gyp_')">{{t('子站 HTTPS 地址')}}<input v-model="form.url" type="url" placeholder="https://child.example.com" required/></label><label>{{t('备注名称（选填）')}}<input v-model="form.name" maxlength="64"/></label><p v-if="error" class="error" role="alert">{{t(error)}}</p><footer><button type="button" :disabled="busy" @click="close">{{t('取消')}}</button><button class="primary" :disabled="busy">{{t(busy?'正在连接…':'连接并选择节点')}}</button></footer></form></div></Teleport>
</section>
</template>
<style scoped>
.subsite-pools{display:grid;gap:20px}.pool-actions,.pool-links{display:flex;gap:8px;flex-wrap:wrap}.pool-steps{display:flex;gap:36px;flex-wrap:wrap;padding:18px 18px 18px 36px;margin:0;background:var(--surface);border:1px solid var(--border);border-radius:12px;font-size:13px}.pool-cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(300px,100%),1fr));gap:16px}.pool-cards article,.pool-empty{padding:22px;background:var(--surface);border:1px solid var(--border);border-radius:12px;overflow-wrap:anywhere}.pool-cards header{display:flex;gap:12px;align-items:center;justify-content:space-between}.pool-cards h3{margin:0;font-size:16px}.pool-cards p,.pool-empty p{font-size:13px;line-height:1.7}.pool-empty{text-align:center;padding:42px 20px}.mounted-summary{max-height:140px;overflow:auto;padding-left:18px;font-size:13px;line-height:1.9}.compose-card textarea{width:100%;font:inherit;resize:vertical}.compose-card{max-height:90dvh;overflow:auto}
</style>
