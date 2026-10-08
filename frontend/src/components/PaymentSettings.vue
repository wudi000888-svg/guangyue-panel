<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { CreditCard, Plus, ShieldCheck, RefreshCw, Copy, LoaderCircle, X } from 'lucide-vue-next';
import { useCommerce, money, stamp, paymentStateText, type PaymentMethod, type PaymentAttempt, type PaymentReceipt, type PaymentDiagnostic } from '../lib/commerce';
import { isCancelled } from '../lib/api';
import { t } from '../i18n';
import CryptoWalletSettings from './CryptoWalletSettings.vue';
defineProps<{ active?: boolean }>();
const { read, api, run, busy, error, notice } = useCommerce();
const methods = ref<PaymentMethod[]>([]), payments = ref<PaymentAttempt[]>([]), receipts = ref<PaymentReceipt[]>([]), loading = ref(false), loaded = ref(false);
const editing = ref<PaymentMethod | null>(null), showForm = ref(false), adminPassword = ref(''), formElement = ref<HTMLFormElement|null>(null);
const form = ref({ code: 'epay', name: '', enabled: false, base_url: '', merchant_id: '', channel: 'alipay', secret: '', webhook_secret: '' });
const diagnostics = ref<Record<string,PaymentDiagnostic>>({}), refundTarget = ref<PaymentReceipt|null>(null), refundReason = ref(''), refundReference = ref(''), manualConfirmed = ref(false), refundElement = ref<HTMLFormElement|null>(null);
const unresolved = computed(() => receipts.value.filter(v => ['review_required','refund_required','refund_pending'].includes(v.state)));
const liveMethods = computed(() => methods.value.filter(v => !v.archived));
const archivedMethods = computed(() => methods.value.filter(v => v.archived));
const provider = (id:string) => methods.value.find(v=>v.id===id);
const manualRefund = computed(()=>!!refundTarget.value&&(refundTarget.value.state==='review_required'||provider(refundTarget.value.provider_id)?.code!=='stripe'));
let sequence=0,disposed=false;
async function load() {
  const current=++sequence;loading.value=true;
  try {const [m,p,r]=await Promise.all([read<{items:PaymentMethod[]}>('/payment-methods'),read<{items:PaymentAttempt[]}>('/payments'),read<{items:PaymentReceipt[]}>('/payment-receipts')]);
    if(disposed||current!==sequence)return;if(m)methods.value=m.items;if(p)payments.value=p.items;if(r){receipts.value=r.items;if(refundTarget.value){const next=r.items.find(v=>v.id===refundTarget.value?.id);if(!next||!['review_required','refund_required'].includes(next.state))closeRefund();else refundTarget.value=next}}loaded.value=true;
  }finally{if(current===sequence)loading.value=false}
}
function clearSecrets(){adminPassword.value='';form.value.secret='';form.value.webhook_secret=''}
function closeForm(){showForm.value=false;editing.value=null;clearSecrets()}
async function edit(method?:PaymentMethod) {
  closeRefund();editing.value=method||null;
  form.value={code:method?.code||'epay',name:method?.name||'',enabled:method?.enabled||false,base_url:method?.base_url||'',merchant_id:method?.merchant_id||'',channel:method?.channel||'alipay',secret:'',webhook_secret:''};
  adminPassword.value='';error.value='';notice.value='';showForm.value=true;await nextTick();formElement.value?.scrollIntoView({block:'center',behavior:window.matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth'});formElement.value?.querySelector('input')?.focus({preventScroll:true});
}
async function save() {
  if(busy.value)return;busy.value=true;error.value='';notice.value='';
  try{await api('/payment-methods','POST',{...form.value,id:editing.value?.id||'',version:editing.value?.version||0,password:adminPassword.value});closeForm();notice.value=t('支付方式已保存');await load()}
  catch(e){if(!isCancelled(e))error.value=t((e as Error).message)}finally{clearSecrets();busy.value=false}
}
async function setEnabled(method:PaymentMethod) {
  if(busy.value)return;busy.value=true;error.value='';notice.value='';
  try{await api('/payment-methods','POST',{id:method.id,version:method.version,code:method.code,name:method.name,enabled:!method.enabled,base_url:method.base_url||'',merchant_id:method.merchant_id||'',channel:method.channel||'',secret:'',webhook_secret:'',password:adminPassword.value});notice.value=t('支付方式已更新');await load()}
  catch(e){if(!isCancelled(e))error.value=t((e as Error).message)}finally{adminPassword.value='';busy.value=false}
}
async function remove(method:PaymentMethod) {
  if(busy.value||!window.confirm(t('移除此支付方式？已有付款和回调记录会保留，历史支付仍可处理。')))return;
  busy.value=true;error.value='';notice.value='';
  try{const result=await api<{archived:boolean}>('/payment-methods/delete','POST',{id:method.id,version:method.version,password:adminPassword.value});notice.value=result.archived?t('支付方式已归档，历史记录保留'):t('支付方式已删除');await load()}
  catch(e){if(!isCancelled(e))error.value=t((e as Error).message)}finally{adminPassword.value='';busy.value=false}
}
async function check(method:PaymentMethod) {
  if(busy.value)return;busy.value=true;error.value='';notice.value='';
  try{const result=await api<PaymentDiagnostic>('/payment-methods/check','POST',{id:method.id,password:adminPassword.value});diagnostics.value={...diagnostics.value,[method.id]:result};if(!result.ok)error.value=t(result.message);else notice.value=t(result.message)}
  catch(e){if(!isCancelled(e))error.value=t((e as Error).message)}finally{adminPassword.value='';busy.value=false}
}
function closeRefund(){refundTarget.value=null;refundReason.value='';refundReference.value='';manualConfirmed.value=false;adminPassword.value=''}
async function reviewRefund(receipt:PaymentReceipt){if(busy.value)return;closeForm();closeRefund();refundTarget.value=receipt;error.value='';notice.value='';await nextTick();refundElement.value?.scrollIntoView({block:'center',behavior:window.matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth'});refundElement.value?.querySelector('input')?.focus({preventScroll:true})}
async function refund() {
  const receipt=refundTarget.value;if(!receipt||busy.value||manualRefund.value&&(!manualConfirmed.value||!refundReference.value))return;
  try{const result=await run('/payment-refunds',{receipt_id:receipt.id,action:manualRefund.value?'confirm_manual':'request',external_ref:manualRefund.value?refundReference.value:'',reason:refundReason.value,password:adminPassword.value});if(result){const manual=manualRefund.value;closeRefund();notice.value=manual?t('已记录管理员核实的实际退款'):t('原路退款已进入处理队列，请等待支付平台确认');await load()}}
  finally{adminPassword.value=''}
}
async function copyURL(value:string){try{await navigator.clipboard.writeText(value);notice.value=t('回调地址已复制')}catch{error.value=t('复制失败，请手动复制地址')}}
onMounted(load);onUnmounted(()=>{disposed=true;sequence++;clearSecrets();closeRefund()});
</script>
<template>
  <section class="service-integration" :aria-busy="loading||busy">
    <header class="integration-heading"><div><h2><CreditCard :size="20"/>{{t('在线支付')}}</h2><p>{{t('配置收款方式，用户可支付套餐或充值钱包。')}}</p></div><button type="button" :disabled="busy" @click="edit()"><Plus :size="16"/>{{t('新增支付方式')}}</button></header>
    <p v-if="error" class="error" role="alert">{{error}}</p><p v-if="notice" class="integration-notice" role="status">{{notice}}</p>
    <div class="integration-explanation"><ShieldCheck :size="19"/><p>{{t('仅以支付平台确认的到账结果入账。套餐付款与余额充值分开记录，重复回调不会重复入账。')}}</p></div>
    <p v-if="!loaded" role="status">{{t('加载中…')}}</p><p v-else-if="!liveMethods.length" class="integration-empty">{{t('尚未配置可用的在线支付方式')}}</p>
    <article v-for="method in liveMethods" :key="method.id" class="provider-card">
      <div class="provider-heading"><div><strong>{{method.name}}</strong><span class="service-badge">{{method.code==='stripe'?'Stripe':method.code==='epay'?t('易支付'):t('通用 Webhook')}}</span></div><span :class="['service-badge',{'enabled':method.enabled}]">{{method.enabled?t('已启用'):t('已停用')}}</span></div>
      <p class="integration-muted">{{method.code==='stripe'?(method.mode==='test'?t('测试环境'):t('正式环境')):method.base_url||t('仅接收签名回调')}}<span v-if="method.channel"> · {{method.channel}}</span></p>
      <label v-if="method.webhook_url" class="callback-field">{{t('服务商回调地址')}}<div><input :value="method.webhook_url" readonly :aria-label="t('服务商回调地址')"/><button type="button" :aria-label="t('复制回调地址')" @click="copyURL(method.webhook_url)"><Copy :size="16"/></button></div></label>
      <p v-if="method.code==='stripe'&&!method.has_webhook_secret" class="integration-muted">{{t('请先在 Stripe 创建此回调地址，再编辑填入签名密钥并启用。')}}</p>
      <p v-if="!method.checkout" class="integration-muted">{{t('此方式仅供已有签名回调使用，不会作为用户收银台选项。')}}</p>
      <div class="integration-actions"><button :disabled="busy" @click="edit(method)">{{t('编辑')}}</button><button :disabled="busy||!adminPassword" @click="check(method)">{{t('接入检查')}}</button><button :disabled="busy||!adminPassword||(method.code==='stripe'&&!method.has_webhook_secret)" @click="setEnabled(method)">{{method.enabled?t('停用'):t('启用')}}</button><button :disabled="busy||!adminPassword" @click="remove(method)">{{t('移除支付方式')}}</button></div>
      <div v-if="diagnostics[method.id]" class="payment-diagnostic"><p>{{t(diagnostics[method.id]!.message)}}</p><span>{{t('最近有效回调')}} {{diagnostics[method.id]!.last_event_at?stamp(diagnostics[method.id]!.last_event_at):t('暂无')}}</span><span>{{t('处理中')}} {{diagnostics[method.id]!.pending}} · {{t('待核对或退款')}} {{diagnostics[method.id]!.refund_required}}</span></div>
    </article>
    <details v-if="archivedMethods.length"><summary>{{t('已归档支付方式')}} · {{archivedMethods.length}}</summary><p class="integration-muted">{{t('历史付款与回调继续保留，不再接受新的支付。')}}</p><p v-for="method in archivedMethods" :key="method.id">{{method.name}} · {{method.code}}</p></details>
    <form v-if="showForm" ref="formElement" class="integration-form" @submit.prevent="save">
      <div class="provider-heading"><h3>{{editing?t('编辑支付方式'):t('新增支付方式')}}</h3><button type="button" class="icon" :aria-label="t('取消')" :disabled="busy" @click="closeForm"><X :size="18"/></button></div>
      <fieldset :disabled="busy"><div class="integration-grid"><label>{{t('收款服务')}}<select v-model="form.code" :disabled="!!editing"><option value="epay">{{t('易支付')}} · V1 MD5</option><option value="stripe">Stripe</option><option v-if="editing?.code==='webhook'" value="webhook">{{t('通用 Webhook')}}</option></select></label><label>{{t('显示名称')}}<input v-model.trim="form.name" maxlength="80" required :placeholder="t('例如：支付宝')"/></label>
      <template v-if="form.code==='epay'"><label>{{t('网关地址')}}<input v-model.trim="form.base_url" type="url" required pattern="https://.*" placeholder="https://pay.example.com"/></label><label>{{t('商户号')}}<input v-model.trim="form.merchant_id" required maxlength="120"/></label><label>{{t('支付渠道')}}<select v-model="form.channel"><option value="alipay">{{t('支付宝')}}</option><option value="wxpay">{{t('微信支付')}}</option><option value="qqpay">{{t('QQ 钱包')}}</option><option value="">{{t('由收银台选择')}}</option></select></label></template>
      <label>{{form.code==='stripe'?t('Stripe API 密钥'):t('签名密钥')}}<input v-model="form.secret" type="password" :required="!editing?.has_secret" autocomplete="new-password" :placeholder="editing?.has_secret?t('留空保持原密钥'):form.code==='stripe'?'sk_test_…':t('请输入签名密钥')"/></label>
      <label v-if="form.code==='stripe'">{{t('Stripe Webhook 签名密钥')}}<input v-model="form.webhook_secret" type="password" autocomplete="new-password" :required="form.enabled&&!editing?.has_webhook_secret" :placeholder="editing?.has_webhook_secret?t('留空保持原密钥'):'whsec_…'"/></label></div>
      <p v-if="form.code==='epay'" class="integration-muted">{{t('使用兼容 V1 MD5 的 HTTPS 网关。支付宝、微信可分别新增为两个支付方式。')}}</p>
      <div v-if="form.code==='stripe'" class="integration-muted"><p>{{t('商户号由密钥自动识别。API 密钥和 Webhook 密钥须属于同一环境。')}}</p><p>{{t('首次接入请先停用保存，取得回调地址；在 Stripe 创建该地址的 Webhook 后，回填签名密钥并启用。')}}<br/>{{t('在 Stripe 控制台为回调地址订阅以下事件：')}}</p><code class="stripe-events">checkout.session.completed<br/>checkout.session.async_payment_succeeded<br/>refund.created · refund.updated · refund.failed</code></div>
      <label class="inline-check"><input v-model="form.enabled" type="checkbox"/>{{t('启用该支付方式')}}</label><label>{{t('管理员当前密码')}}<input v-model="adminPassword" type="password" autocomplete="current-password" required/></label><button class="primary" :disabled="busy"><LoaderCircle v-if="busy" :size="16" class="spin"/>{{t('保存支付方式')}}</button></fieldset>
    </form>
    <label v-if="liveMethods.length&&!showForm&&!refundTarget" class="integration-password">{{t('管理员当前密码')}}<input v-model="adminPassword" type="password" autocomplete="current-password" :placeholder="t('检查或修改支付方式前验证密码')"/></label>
    <details class="integration-review" :open="unresolved.length>0"><summary>{{t('收款与退款处理')}} <span>{{unresolved.length}}</span></summary><div class="integration-actions"><button :disabled="busy||loading" @click="load"><RefreshCw :size="15" :class="{spin:loading}"/>{{t('刷新')}}</button></div><p class="integration-muted">{{t('套餐已开通时，请先在订单管理中审核退款并撤回权益，再处理原路退款。')}}</p>
      <p v-if="!receipts.length" class="integration-empty">{{t('暂无已确认收款')}}</p><article v-for="receipt in receipts" :key="receipt.id" class="payment-review-row"><div><strong>{{receipt.currency==='CNY'?money(receipt.amount):receipt.amount+' '+receipt.currency+' '+t('最小货币单位')}}</strong><span>{{provider(receipt.provider_id)?.name||receipt.provider_id}} · {{paymentStateText(receipt.state)}}</span><small>{{stamp(receipt.created)}}<template v-if="receipt.user_id"> · {{t('用户')}} #{{receipt.user_id}}</template></small><p>{{t(receipt.reason)}}</p><details><summary>{{t('交易详情')}}</summary><small>{{t('收款编号')}} · {{receipt.id}}</small><small>{{t('平台交易号')}} · {{receipt.external_ref}}</small><RouterLink v-if="receipt.order_id" :to="{path:'/orders',query:{checkout:receipt.order_id,user_id:receipt.user_id}}">{{t('查看订单')}}</RouterLink></details></div><button v-if="['review_required','refund_required'].includes(receipt.state)" :disabled="busy" @click="reviewRefund(receipt)">{{receipt.state==='review_required'?t('核对实际退款'):t('处理退款')}}</button></article>
    </details>
    <form v-if="refundTarget" ref="refundElement" class="integration-form" @submit.prevent="refund"><div class="provider-heading"><h3>{{manualRefund?t('确认商户后台退款'):t('发起原路退款')}}</h3><button type="button" class="icon" :disabled="busy" :aria-label="t('取消')" @click="closeRefund"><X :size="18"/></button></div><p class="integration-muted">{{t('收款编号')}} · {{refundTarget.id}}</p><fieldset :disabled="busy"><p v-if="manualRefund" class="integration-muted">{{t('先在支付平台完成实际退款，再填写退款凭据。此操作仅记录核实结果，不会自动转账或退入钱包。')}}</p><p v-else class="integration-muted">{{t('将请求 Stripe 按此笔实收金额原路退款，成功后自动更新状态。')}}</p><label>{{t('退款处理说明')}}<input v-model.trim="refundReason" required maxlength="300"/></label><label v-if="manualRefund">{{t('实际退款凭据')}}<input v-model.trim="refundReference" required maxlength="200" autocomplete="off"/></label><label v-if="manualRefund" class="inline-check"><input v-model="manualConfirmed" type="checkbox" required/>{{t('我已在支付平台核实，这笔资金已实际退回')}}</label><label>{{t('管理员当前密码')}}<input v-model="adminPassword" type="password" autocomplete="current-password" required/></label><button class="primary" :disabled="busy||!adminPassword||!refundReason||(manualRefund&&(!manualConfirmed||!refundReference))"><LoaderCircle v-if="busy" :size="16" class="spin"/>{{manualRefund?t('确认已实际退款'):t('提交原路退款')}}</button></fieldset></form>
    <details class="integration-review"><summary>{{t('全部支付尝试')}} · {{payments.length}}</summary><article v-for="payment in payments" :key="payment.id" class="payment-review-row"><div><strong>{{money(payment.amount)}}</strong><span>{{payment.purpose==='topup'?t('钱包充值'):t('套餐支付')}} · {{paymentStateText(payment.state)}}</span><small>{{stamp(payment.created)}} · {{payment.id}}</small><p v-if="payment.message">{{t(payment.message)}}</p><p v-if="payment.refund_status">{{t('退款状态')}} · {{paymentStateText(payment.refund_status)}}</p></div></article><p v-if="!payments.length" class="integration-empty">{{t('暂无支付记录')}}</p></details>
    <CryptoWalletSettings :active="active !== false" />
  </section>
</template>
<style scoped>
.payment-diagnostic{display:grid;gap:5px;font-size:12px;color:var(--secondary);padding:12px;margin-top:12px;border-radius:8px;background:var(--surface-raised)}.payment-diagnostic p{margin:0}.stripe-events{display:block;font-size:12px;overflow-wrap:anywhere;line-height:1.8}.payment-review-row details{margin-top:8px}.payment-review-row summary{cursor:pointer}.payment-review-row a{display:inline-block;margin-top:8px}.integration-form .inline-check{align-items:flex-start}.integration-form .inline-check input{margin-top:3px}
</style>
