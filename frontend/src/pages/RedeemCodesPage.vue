<script setup lang="ts">
import {onMounted,ref} from 'vue';
import {useCommerce,cents,money,stamp,stateText,type Redemption} from '../lib/commerce';
import {download} from '../lib/download';
import {t} from '../i18n';
import '../commerce.css';

type CodeItem={id:string;code?:string;available:boolean;reason?:string};
const {read,run,busy,error,notice}=useCommerce();
const items=ref<Redemption[]>([]), selected=ref<string[]>([]), amount=ref(''), count=ref(10),
  expires=ref(new Date(Date.now()+30*86400000-new Date().getTimezoneOffset()*60000).toISOString().slice(0,16)),
  forever=ref(false), note=ref(''), password=ref(''), generated=ref<{id:string;code:string}[]>([]),
  plaintext=ref<Record<string,string>>({});
async function load(){items.value=(await read<{items:Redemption[]}>('/codes'))?.items||[];selected.value=[]}
async function more(){if(!items.value.length)return;const v=await read<{items:Redemption[]}>('/codes?before='+encodeURIComponent(items.value.at(-1)!.id));if(v)items.value.push(...v.items)}
async function generate(){let value:string;try{value=cents(amount.value)}catch(e){error.value=(e as Error).message;return}const end=forever.value?0:Math.floor(new Date(expires.value).getTime()/1000);if(!forever.value&&(!end||end<=Date.now()/1000)){error.value=t('有效期必须在未来');return}const v=await run<{codes:{id:string;code:string}[]}>('/codes',{amount:value,count:count.value,expires:end,note:note.value,password:password.value});password.value='';if(v){generated.value=v.codes;notice.value=t('请及时保存兑换码，列表支持重新查看明文');await load()}}
async function revoke(){const ids=selected.value.filter(id=>items.value.find(v=>v.id===id)?.state==='unused');if(!ids.length)return;const v=await run<{revoked:number}>('/codes/revoke',{ids});if(v){notice.value=t('未兑换的选中兑换码已作废');await load()}}
async function reveal(){if(!selected.value.length)return;const v=await run<{items:CodeItem[]}>('/codes/reveal',{ids:selected.value});if(v){for(const item of v.items)if(item.available&&item.code)plaintext.value[item.id]=item.code;const unavailable=v.items.filter(item=>!item.available).length;notice.value=unavailable?t('部分历史兑换码未保存明文，无法恢复'):t('已显示选中兑换码明文')}}
function exportCodes(){const values=generated.value.length?generated.value.map(v=>v.code):Object.values(plaintext.value);if(values.length)download(new Blob([values.join('\n')],{type:'text/plain;charset=utf-8'}),'guangyue-redemption-codes.txt')}
function hideGenerated(){generated.value=[]}
onMounted(load);
</script>
<template><section class="commerce-page"><p v-if="error" class="error commerce-alert" role="alert">{{error}}</p><p v-if="notice" class="commerce-alert" role="status">{{notice}}</p>
 <form class="commerce-card" @submit.prevent="generate"><h2>{{t('生成兑换码')}}</h2><div class="commerce-grid"><label>{{t('每个兑换码金额 / 元')}}<input v-model="amount" inputmode="decimal" required/></label><label>{{t('生成数量')}}<input v-model.number="count" type="number" min="1" max="100" required/></label><label>{{t('兑换有效期')}}<input v-model="expires" type="datetime-local" :disabled="forever" :required="!forever"/></label></div><label class="inline-check"><input v-model="forever" type="checkbox"/>{{t('永久有效')}}</label><label>{{t('批次备注')}}<input v-model.trim="note" maxlength="500"/></label><label>{{t('管理员当前密码')}}<input v-model="password" type="password" autocomplete="current-password" required/></label><button class="primary" :disabled="busy">{{t('生成兑换码')}}</button></form>
 <article v-if="generated.length" class="commerce-card"><div class="commerce-line"><h2>{{t('本次生成的兑换码')}}</h2><div class="commerce-actions"><button @click="exportCodes">{{t('下载兑换码')}}</button><button @click="hideGenerated">{{t('隐藏明文')}}</button></div></div><p class="commerce-muted">{{t('兑换码等同于对应余额，请仅提供给预期接收人')}}</p><p v-for="v in generated" :key="v.id" class="commerce-code">{{v.code}}</p></article>
 <article v-if="Object.keys(plaintext).length" class="commerce-card"><div class="commerce-line"><h2>{{t('已查看的兑换码明文')}}</h2><div class="commerce-actions"><button @click="exportCodes">{{t('下载兑换码')}}</button><button @click="plaintext={}">{{t('隐藏明文')}}</button></div></div><p v-for="(code,id) in plaintext" :key="id" class="commerce-code">{{code}}</p></article>
 <article class="commerce-card"><div class="commerce-line"><h2>{{t('兑换码管理')}}</h2><button @click="load">{{t('刷新')}}</button></div><form v-if="selected.length" class="commerce-preview" @submit.prevent="revoke"><p>{{t('作废仅影响未兑换的码，不会倒扣已入账余额')}}</p><div class="commerce-actions"><button type="button" :disabled="busy" @click="reveal">{{t('查看明文')}} ({{selected.length}})</button><button :disabled="busy">{{t('确认作废')}} ({{selected.filter(id=>items.find(v=>v.id===id)?.state==='unused').length}})</button></div></form><div v-if="!items.length" class="commerce-empty">{{t('暂无兑换码')}}</div><div class="commerce-list"><article v-for="row in items" :key="row.id" class="commerce-card"><div class="commerce-line"><label class="commerce-check-row"><input v-model="selected" type="checkbox" :value="row.id"/><strong>{{plaintext[row.id]||('•••• '+row.suffix)}}</strong></label><span :class="['commerce-badge',row.state]">{{stateText(row.state)}}</span><strong>{{money(row.amount)}}</strong></div><p class="commerce-muted">{{t('有效期')}} {{stamp(row.expires)}} · {{row.note}}</p><p v-if="row.redeemed_by" class="commerce-muted">{{t('兑换用户')}} #{{row.redeemed_by}} · {{stamp(row.redeemed_at)}}</p><p class="commerce-id">{{row.batch_id}}<span v-if="!row.has_secret" class="commerce-muted"> · {{t('历史兑换码无法恢复明文')}}</span></p></article></div><button v-if="items.length>=50" @click="more">{{t('加载更多')}}</button></article>
</section></template>
