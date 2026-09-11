<script setup lang="ts">
import {onMounted,ref,watch} from 'vue';
import {usePanelContext} from '../composables/panelContext';
import {useCommerce,money,cents,stamp,stateText,type Wallet,type MoneyTransaction} from '../lib/commerce';
import {t} from '../i18n';
import '../commerce.css';
const {owner,state}=usePanelContext(),{read,run,busy,error,notice}=useCommerce();
const selected=ref(state.value?.me.id||0),wallet=ref<Wallet|null>(null),items=ref<MoneyTransaction[]>([]),code=ref(''),amount=ref(''),reason=ref(''),kind=ref('credit');
let sequence=0;async function load(){const current=++sequence,user=selected.value;const [w,v]=await Promise.all([read<Wallet>('/wallet?user_id='+user),read<{items:MoneyTransaction[]}>('/transactions?user_id='+user)]);if(current===sequence){wallet.value=w;items.value=v?.items||[]}}
async function more(){if(!items.value.length)return;const user=selected.value;const v=await read<{items:MoneyTransaction[]}>('/transactions?user_id='+selected.value+'&before='+encodeURIComponent(items.value.at(-1)!.id));if(v&&selected.value===user)items.value.push(...v.items)}
async function redeem(){const v=await run('/redeem',{code:code.value});if(v){code.value='';selected.value=state.value?.me.id||selected.value;notice.value=t('兑换成功，余额已到账');await load()}}
async function adjust(){let value:string;try{value=cents(amount.value)}catch(e){error.value=(e as Error).message;return}const v=await run('/adjust',{user_id:selected.value,amount:value,kind:kind.value,reason:reason.value});if(v){amount.value='';reason.value='';notice.value=t('余额调整已完成');await load()}}
watch(selected,load);onMounted(load);
</script>
<template><section class="commerce-page">
 <p v-if="error" class="error commerce-alert" role="alert">{{error}}</p><p v-if="notice" class="commerce-alert" role="status">{{notice}}</p>
 <div class="commerce-actions"><label v-if="owner">{{t('查看成员')}}<select v-model.number="selected" :aria-label="t('查看成员')"><option v-for="u in state?.users" :key="u.id" :value="u.id">{{u.username}}</option></select></label><button @click="load">{{t('刷新')}}</button></div>
 <div class="commerce-grid"><article class="commerce-card"><p class="commerce-muted">{{t('可用余额')}}</p><div class="commerce-amount">{{money(wallet?.available)}}</div></article><article class="commerce-card"><p class="commerce-muted">{{t('冻结余额')}}</p><div class="commerce-amount">{{money(wallet?.held)}}</div><p class="commerce-muted">{{t('开通中的订单会暂时冻结对应金额')}}</p></article></div>
 <div class="commerce-grid"><form class="commerce-card" @submit.prevent="redeem"><h2>{{t('兑换码充值')}}</h2><p class="commerce-muted">{{t('兑换成功后计入当前登录账号余额')}}</p><label>{{t('输入兑换码')}}<input v-model.trim="code" maxlength="80" autocomplete="off" autocapitalize="characters" spellcheck="false" required placeholder="GY-XXXXXXXX-XXXXXXXX-XXXXXXXXXX"/></label><button class="primary" :disabled="busy||!code">{{busy?t('处理中…'):t('确认兑换')}}</button></form>
 <form v-if="owner" class="commerce-card" @submit.prevent="adjust"><h2>{{t('人工调整余额')}}</h2><label>{{t('操作类型')}}<select v-model="kind" :aria-label="t('操作类型')"><option value="credit">{{t('人工入账')}}</option><option value="gift">{{t('赠送')}}</option><option value="debit">{{t('人工扣减')}}</option></select></label><label>{{t('金额 / 元')}}<input v-model="amount" inputmode="decimal" required/></label><label>{{t('调整原因')}}<input v-model.trim="reason" maxlength="500" required/></label><p class="commerce-muted">{{t('当前管理员会话可直接调整余额，操作会永久记入资金流水')}}</p><button class="primary" :disabled="busy">{{t('确认调整')}}</button></form></div>
 <article class="commerce-card"><h2>{{t('资金流水')}}</h2><div v-if="!items.length" class="commerce-empty">{{t('暂无资金流水')}}</div><div v-for="row in items" :key="row.id" class="commerce-card"><div class="commerce-line"><strong>{{stateText(row.kind)}}</strong><strong>{{money(row.amount)}}</strong></div><p class="commerce-muted">{{stamp(row.created)}} · {{row.reason}}</p><p class="commerce-id">{{row.id}}</p><p class="commerce-id">{{row.reference}}</p><p class="commerce-muted">{{t('可用余额')}} {{money(row.available)}} · {{t('冻结余额')}} {{money(row.held)}}</p></div><button v-if="items.length>=50" @click="more">{{t('加载更多')}}</button></article>
</section></template>
