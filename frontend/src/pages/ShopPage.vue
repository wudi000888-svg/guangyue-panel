<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { ArrowRight, Check, CircleCheck, CircleHelp, LoaderCircle, Package, RefreshCw, ShoppingBag } from 'lucide-vue-next';
import { usePanelContext } from '../composables/panelContext';
import { useApi } from '../lib/api';
import { useCommerce, cents, money, type Offer } from '../lib/commerce';
import { bytes } from '../lib/format';
import type { Plan } from '../types';
import { t } from '../i18n';
import '../commerce.css';
import '../styles/member-ui.css';
const router=useRouter(),{owner,state}=usePanelContext(),{read,run,api,busy,error,notice}=useCommerce(),panelApi=useApi();
const offers=ref<Offer[]>([]),plans=ref<Plan[]>([]),selected=ref(''),price=ref(''),enabled=ref(true),purchaseLimit=ref(0),editing=ref<Offer|null>(null),category=ref(''),loading=ref(true),ordering=ref('');
const visible=computed(()=>offers.value.filter(v=>(owner.value||v.enabled)&&(!category.value||v.plan.category===category.value)));
const categories=computed(()=>[...new Set(offers.value.filter(v=>owner.value||v.enabled).map(v=>v.plan.category).filter(Boolean))]);
const currentPlan=(o:Offer)=>state.value?.me.entitlement?.plan_id===o.plan.id&&state.value.me.entitlement.version===o.plan.version&&(!state.value.me.expires||state.value.me.expires>Date.now()/1000);
const canRenew=(o:Offer)=>currentPlan(o)&&!!state.value?.me.expires&&o.plan.valid_days>0;
const renewalBlocked=(o:Offer)=>currentPlan(o)&&!canRenew(o);
function selectPlan(){if(editing.value)return;const p=plans.value.find(v=>v.id===selected.value);if(p)price.value=(Number(p.price||'0')/100).toFixed(2)}
let sequence=0;
async function load(){const current=++sequence;loading.value=true;error.value='';try{const result=await read<{items:Offer[]}>('/offers');if(result&&current===sequence)offers.value=result.items;if(owner.value){try{const result=await panelApi<{plans:Plan[]}>('/plans');if(current===sequence)plans.value=result.plans.filter(v=>!v.archived)}catch(e){if(current===sequence)error.value=(e as Error).message}}}finally{if(current===sequence)loading.value=false}}
function edit(o:Offer){editing.value=o;selected.value=o.plan.id;price.value=(Number(o.price)/100).toFixed(2);enabled.value=o.enabled;purchaseLimit.value=o.purchase_limit||0;document.getElementById('offer-editor')?.scrollIntoView({block:'start',behavior:window.matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth'})}
function reset(){editing.value=null;selected.value='';price.value='';purchaseLimit.value=0;enabled.value=true}
async function save(){const p=plans.value.find(v=>v.id===selected.value);if(!p)return;let amount:string;try{amount=/^0(?:\.0{1,2})?$/.test(price.value.trim())?'0':cents(price.value)}catch(e){error.value=(e as Error).message;return}if(!Number.isInteger(purchaseLimit.value)||purchaseLimit.value<0||purchaseLimit.value>100000){error.value=t('限购次数应为 0–100000，0 表示不限购');return}busy.value=true;error.value='';notice.value='';try{await api('/offers','POST',{id:editing.value?.id||'',version:editing.value?.version||0,plan_id:p.id,plan_version:p.version,price:amount,enabled:enabled.value,purchase_limit:purchaseLimit.value});reset();notice.value=t('套餐上架配置已保存');await load()}catch(e){error.value=t((e as Error).message)}finally{busy.value=false}}
async function order(o:Offer){ordering.value=o.id;try{const v=await run('/orders',{offer_id:o.id,offer_version:o.version});if(v)await router.push('/orders')}finally{ordering.value=''}}
onMounted(load);
</script>
<template>
  <section class="commerce-page member-page shop-page" :aria-busy="loading">
    <header class="member-heading"><div><span class="member-eyebrow"><ShoppingBag :size="14"/>PLANS & ACCESS</span><h1>{{owner?t('套餐上架'):t('购买套餐')}}</h1><p>{{owner?t('上架状态决定用户是否可以购买；套餐节点权限来自套餐管理。'):t('选择适合你的套餐，连接每一段日常。')}}</p></div><div class="member-actions"><RouterLink :to="owner?'/plans':'/orders'">{{owner?t('套餐管理'):t('我的订单')}}<ArrowRight :size="16"/></RouterLink></div></header>
    <p v-if="error" class="error member-feedback" role="alert">{{error}}<button :disabled="loading" @click="load">{{t('重试')}}</button></p><p v-if="notice" class="member-feedback" role="status"><CircleCheck :size="17"/>{{notice}}</p>
    <div class="member-toolbar"><div class="member-tabs" role="group" :aria-label="t('套餐分类')"><button :aria-pressed="!category" @click="category=''">{{t('全部分类')}}</button><button v-for="v in categories" :key="v" :aria-pressed="category===v" @click="category=v">{{v}}</button></div><button :disabled="loading||busy" @click="load"><RefreshCw :size="16" :class="{spin:loading}"/>{{t('刷新')}}</button></div>
    <div class="member-hint"><CircleHelp :size="18"/><p>{{t('使用站内余额购买。创建订单后预览并确认，才会冻结余额。有效期内续费不重置当前周期用量。')}}</p></div>
    <div v-if="loading&&!offers.length" class="offer-grid" role="status" :aria-label="t('正在加载套餐…')"><div v-for="n in 3" :key="n" class="member-skeleton"/></div>
    <div v-else-if="visible.length" class="offer-grid">
      <article v-for="o in visible" :key="o.id" class="member-panel offer-card" :class="{'is-current':currentPlan(o)}">
        <div class="offer-topline"><span class="member-label">{{o.plan.category||t('套餐')}}</span><span v-if="currentPlan(o)" class="member-status active"><Check :size="13"/>{{t('当前套餐')}}</span><span v-else-if="owner" :class="['member-status',o.enabled?'active':'']">{{o.enabled?t('已上架'):t('已下架')}}</span></div>
        <h2>{{o.plan.name}}</h2><p class="offer-description">{{o.plan.description}}</p>
        <div class="offer-price">{{money(o.price)}}<span>{{o.plan.valid_days?o.plan.valid_days+' '+t('天'):t('永久有效')}}</span></div>
        <dl class="member-facts"><div><dt>{{t('周期额度')}}</dt><dd>{{o.plan.quota?bytes(o.plan.quota):t('不限')}}</dd></div><div><dt>{{t('配额周期')}}</dt><dd>{{o.plan.cycle==='30d'?t('每 30 天'):o.plan.cycle==='month'?t('每自然月'):t('不自动重置')}}</dd></div><div v-if="owner"><dt>{{t('节点权限组')}}</dt><dd>{{o.plan.group_ids.length}}</dd></div><div v-if="o.purchase_limit||owner"><dt>{{t('限购次数')}}</dt><dd>{{o.purchase_limit||t('不限')}}</dd></div><div v-if="owner"><dt>{{t('套餐版本')}}</dt><dd>v{{o.plan.version}}</dd></div></dl>
        <div class="offer-actions"><button class="primary" :disabled="busy||!o.enabled||renewalBlocked(o)" @click="order(o)"><LoaderCircle v-if="ordering===o.id" :size="16" class="spin"/><template v-if="ordering===o.id">{{t('正在创建订单…')}}</template><template v-else>{{renewalBlocked(o)?t('永久权益无需续费'):canRenew(o)?t('创建续费订单'):t('创建开通订单')}}<ArrowRight v-if="!renewalBlocked(o)" :size="16"/></template></button><button v-if="owner" :disabled="busy" @click="edit(o)">{{t('编辑销售配置')}}</button></div>
      </article>
    </div>
    <div v-else-if="!error" class="member-empty"><Package :size="30"/><h2>{{t('暂无套餐')}}</h2><p>{{owner?t('暂无套餐上架配置，请先选择套餐并上架。'):t('暂无可购买套餐，请联系管理员')}}</p><button v-if="category" @click="category=''">{{t('全部分类')}}</button></div>
    <form v-if="owner" id="offer-editor" class="member-panel offer-editor" @submit.prevent="save"><div class="member-panel-title"><div><h2>{{editing?t('编辑套餐上架'):t('新增套餐上架')}}</h2><p>{{t('售价可以为 0，免费套餐也可上架。')}}</p></div><Package :size="22"/></div><div class="member-form-grid"><label>{{t('套餐')}}<select v-model="selected" :aria-label="t('套餐')" required @change="selectPlan"><option value="" disabled>{{t('选择套餐')}}</option><option v-for="p in plans" :key="p.id" :value="p.id">{{p.name}} · v{{p.version}}</option></select></label><label>{{t('售价 / 元')}}<input v-model="price" inputmode="decimal" required placeholder="0.00"/></label><label>{{t('限购次数')}}<input v-model.number="purchaseLimit" type="number" min="0" max="100000" step="1" required/><small>{{t('0 表示不限购；按每个用户统计已创建、开通中的和已完成订单。')}}</small></label><label class="inline-check"><input v-model="enabled" type="checkbox"/>{{t('上架销售')}}</label></div><div class="member-actions"><button class="primary" :disabled="busy||!selected"><LoaderCircle v-if="busy" :size="16" class="spin"/>{{busy?t('保存中…'):t('保存套餐上架')}}</button><button v-if="editing" type="button" :disabled="busy" @click="reset">{{t('取消')}}</button></div></form>
  </section>
</template>
<style scoped>
.offer-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,290px),1fr));gap:20px}.offer-card{display:flex;flex-direction:column;gap:0;transition:border-color .2s,box-shadow .2s}.offer-card.is-current{border-color:var(--accent);box-shadow:0 0 0 1px var(--accent)}.offer-topline{display:flex;justify-content:space-between;gap:12px;align-items:center;min-height:29px;margin-bottom:18px}.offer-card h2{font-size:22px;line-height:1.4;overflow-wrap:anywhere}.offer-description{margin:0;min-height:44px;font-size:13px;line-height:1.8;color:var(--secondary);overflow-wrap:anywhere}.offer-price{padding:23px 0;display:flex;gap:10px;align-items:baseline;font-size:36px;letter-spacing:-1px;font-weight:650;font-variant-numeric:tabular-nums;border-bottom:1px solid var(--border);margin-bottom:24px;flex-wrap:wrap}.offer-price>span{font-size:13px;font-weight:400;letter-spacing:0;color:var(--secondary)}.offer-actions{display:flex;flex-direction:column;gap:9px;margin-top:auto;padding-top:28px}.offer-actions .primary{justify-content:center;min-height:46px}.offer-editor{scroll-margin-top:24px}.offer-editor .member-form-grid{margin-top:8px}.offer-editor .inline-check{align-self:start;margin-top:29px;min-height:44px}.offer-editor .inline-check input{min-height:0}.shop-page .member-toolbar>.member-tabs{flex:0 1 auto}.shop-page .member-toolbar>button{margin-left:auto}@media(max-width:650px){.offer-grid{grid-template-columns:1fr}.offer-price{font-size:34px}.offer-editor .inline-check{margin-top:0}.offer-card h2{font-size:21px}}
</style>
