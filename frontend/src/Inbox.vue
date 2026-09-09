<script setup lang="ts">
import { useApi, isCancelled } from "./lib/api";
import { latestRequest } from "./lib/requests";
const api = useApi("/messages");
const loadRequest = latestRequest();
import { ref, reactive, onMounted, onUnmounted, watch } from 'vue';
import { Mail, Send, CheckCheck, Plus, ArrowLeft, Trash2, X, Inbox as InboxIcon } from 'lucide-vue-next';
import { t, locale } from './i18n';
type Message={id:number;sender:string;title:string;body:string;category:string;created:number;read_at:number;recipients:number;read_count:number};
const props=defineProps<{owner:boolean;users:{id:number;username:string}[];me:number;supportEmail:string;unread:number}>();
const emit=defineEmits<{refresh:[]}>();
const items=ref<Message[]>([]),folder=ref('inbox'),unreadOnly=ref(false),selected=ref<Message|null>(null),next=ref(0),busy=ref(false),loading=ref(false),error=ref(''),notice=ref(''),compose=ref(false),confirmSend=ref(false);
const draft=reactive({recipient:'',title:'',body:'',category:'notice'});
const category=(v:string)=>t(({notice:'通知',maintenance:'维护',account:'账户'} as Record<string,string>)[v]||v);
const date=(v:number)=>new Date(v*1000).toLocaleString(locale.value);
async function load(more=false){
 const request=loadRequest.start();loading.value=true;error.value='';
 try{const v=await api('?folder='+folder.value+(unreadOnly.value?'&unread=1':'')+(more?'&before='+next.value:''),'GET',undefined,{signal:request.signal});if(!loadRequest.isCurrent(request))return;items.value=more?[...items.value,...v.items]:v.items;next.value=v.next_before;}
 catch(e){if(loadRequest.isCurrent(request)&&!isCancelled(e))error.value=(e as Error).message;}
 finally{if(loadRequest.isCurrent(request))loading.value=false;}
}
onUnmounted(()=>loadRequest.cancel());
async function action(fn:()=>Promise<void>){if(busy.value)return;busy.value=true;error.value='';notice.value='';try{await fn();emit('refresh');}catch(e){if(!isCancelled(e))error.value=(e as Error).message;}finally{busy.value=false;}}
async function open(m:Message){selected.value=m;if(folder.value!=='sent'&&!m.read_at)await action(async()=>{await api('/'+m.id+'/read','POST',{});m.read_at=Math.floor(Date.now()/1000);});}
async function markAll(){await action(async()=>{await api('/read-all','POST',{});await load();notice.value='已全部标为已读';});}
async function remove(){if(!selected.value)return;const id=selected.value.id;await action(async()=>{await api('/'+id,'DELETE',{});selected.value=null;await load();notice.value='站内信已移除';});}
function newMessage(){Object.assign(draft,{recipient:String(props.me),title:'',body:'',category:'notice'});compose.value=true;confirmSend.value=false;error.value='';}
async function send(){await action(async()=>{await api('','POST',{title:draft.title,body:draft.body,category:draft.category,all:draft.recipient==='all',recipient_id:draft.recipient==='all'?0:Number(draft.recipient)});compose.value=false;confirmSend.value=false;selected.value=null;folder.value='sent';await load();notice.value='站内信已发送';});}
watch([folder,unreadOnly],()=>{selected.value=null;load();});watch(()=>props.unread,()=>{if(!selected.value&&!compose.value)load();});onMounted(()=>load());
</script>
<template><section class="inbox-page">
 <div class="page-heading"><div><div class="eyebrow">MESSAGE CENTER</div><h1>{{t('站内信')}}</h1><p class="section-subtitle">{{t('集中接收维护通知与账户消息')}}</p></div><button v-if="owner" class="primary" @click="newMessage"><Plus :size="16"/>{{t('发送站内信')}}</button></div>
 <p v-if="error" class="error" role="alert">{{t(error)}}</p><p v-if="notice" class="settings-saved" role="status">{{t(notice)}}</p>
 <div class="inbox-toolbar"><div class="segmented"><button :class="{selected:folder==='inbox'}" @click="folder='inbox'"><InboxIcon :size="15"/>{{t('收件箱')}}<span v-if="unread" class="count">{{unread}}</span></button><button v-if="owner" :class="{selected:folder==='sent'}" @click="folder='sent'"><Send :size="15"/>{{t('已发送')}}</button></div><span class="spacer"/><label v-if="folder==='inbox'" class="inline-check"><input v-model="unreadOnly" type="checkbox"/>{{t('仅看未读')}}</label><button v-if="folder==='inbox'" :disabled="busy||!unread" @click="markAll"><CheckCheck :size="15"/>{{t('全部已读')}}</button></div>
 <article v-if="selected" class="message-detail"><header><button class="text-button" @click="selected=null"><ArrowLeft :size="16"/>{{t('返回列表')}}</button><button v-if="folder==='inbox'" class="text-button" :disabled="busy" @click="remove"><Trash2 :size="15"/>{{t('移出收件箱')}}</button></header><span class="badge neutral">{{category(selected.category)}}</span><h2>{{selected.title}}</h2><p class="message-meta">{{selected.sender}} · {{date(selected.created)}}<span v-if="folder==='sent'"> · {{t('已读')}} {{selected.read_count}} / {{selected.recipients}}</span></p><div class="message-body">{{selected.body}}</div></article>
 <div v-else class="message-list"><button v-for="m in items" :key="m.id" :class="['message-row',{unread:folder==='inbox'&&!m.read_at}]" @click="open(m)"><span class="message-symbol"><Mail :size="18"/><i v-if="folder==='inbox'&&!m.read_at"/></span><div class="message-preview"><div><strong>{{m.title}}</strong><span class="badge neutral">{{category(m.category)}}</span></div><p>{{m.body}}</p><small>{{m.sender}}<span v-if="folder==='sent'"> · {{t('已读')}} {{m.read_count}} / {{m.recipients}}</span></small></div><time>{{date(m.created)}}</time></button><div v-if="!items.length" class="message-empty"><InboxIcon :size="35"/><h3>{{t(loading?'正在加载…':'暂无站内信')}}</h3><p>{{t(unreadOnly?'你已读完全部消息':'维护通知与账户消息会显示在这里')}}</p></div><button v-if="next" class="load-more" :disabled="loading" @click="load(true)">{{t('加载更多')}}</button></div>
 <p class="message-support" v-if="supportEmail">{{t('需要帮助？')}} <a :href="'mailto:'+supportEmail">{{supportEmail}}</a></p>
 <Teleport to="body"><div v-if="compose" class="message-overlay" @click.self="compose=false" @keydown.esc="compose=false"><form class="compose-card" role="dialog" aria-modal="true" :aria-label="t('发送站内信')" @submit.prevent="confirmSend=true"><header><div><span class="eyebrow">NEW MESSAGE</span><h2>{{t('发送站内信')}}</h2></div><button class="icon" type="button" :aria-label="t('关闭')" @click="compose=false"><X :size="20"/></button></header><p v-if="error" class="error" role="alert">{{t(error)}}</p><div class="settings-fields"><label>{{t('收件人')}}<select v-model="draft.recipient" required :aria-label="t('收件人')"><option value="all">{{t('全部现有成员')}}</option><option v-for="u in users" :key="u.id" :value="String(u.id)">{{u.username}}</option></select></label><label>{{t('消息类型')}}<select v-model="draft.category"><option value="notice">{{t('通知')}}</option><option value="maintenance">{{t('维护')}}</option><option value="account">{{t('账户')}}</option></select></label></div><label>{{t('标题')}}<input v-model="draft.title" required maxlength="120" :aria-label="t('标题')"/></label><label>{{t('正文')}}<textarea v-model="draft.body" required rows="7" maxlength="8000" :aria-label="t('正文')"/></label><small>{{draft.body.length}} / 8000 · {{t('仅支持纯文本')}}</small><div v-if="confirmSend" class="send-confirm"><p>{{t('确认发送给')}} <strong>{{draft.recipient==='all'?t('全部现有成员'):users.find(u=>String(u.id)===draft.recipient)?.username}}</strong>？</p><button type="button" class="primary" :disabled="busy" @click="send">{{t(busy?'发送中…':'确认发送')}}</button><button type="button" @click="confirmSend=false">{{t('继续编辑')}}</button></div><footer v-else><button type="button" @click="compose=false">{{t('取消')}}</button><button class="primary"><Send :size="15"/>{{t('预览收件人并发送')}}</button></footer></form></div></Teleport>
</section></template>
