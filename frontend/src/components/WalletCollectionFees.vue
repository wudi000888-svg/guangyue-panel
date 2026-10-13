<script setup lang="ts">
import {computed} from 'vue';
import {Check,Clock3,Gauge,SlidersHorizontal,Zap} from 'lucide-vue-next';
import {t} from '../i18n';
import {tokenAmount} from '../lib/cryptoPayments';
import type {SweepFeeMode,SweepPreview} from '../lib/cryptoTreasury';
import WalletFeeFiat from './WalletFeeFiat.vue';
const props=defineProps<{preview?:SweepPreview;mode:SweepFeeMode;customGwei:string;solana:boolean;loading:boolean;error?:string;now:number}>();
const emit=defineEmits<{'update:mode':[value:SweepFeeMode];'update:customGwei':[value:string]}>();
const choices=[{id:'economy',label:'经济',hint:'按当前参考价',icon:Clock3},{id:'standard',label:'标准',hint:'推荐 · 10% 价格缓冲',icon:Gauge},{id:'priority',label:'优先',hint:'20% 价格缓冲',icon:Zap},{id:'custom',label:'自定义',hint:'设置单次费用上限',icon:SlidersHorizontal}] as const;
const option=(mode:string)=>props.preview?.fee_options?.find(value=>value.id===mode);
const cost=(mode:string)=>mode==='custom'?(props.preview?.fee_mode==='custom'?props.preview.total_gas_atoms:undefined):option(mode)?.total_gas_atoms;
const gasLimit=computed(()=>props.preview?.items.reduce((max,item)=>{const n=BigInt(item.gas_limit);return n>max?n:max},0n).toString());
</script>
<template>
 <section class="collection-fees" aria-labelledby="collection-fees-title">
  <header><h5 id="collection-fees-title">{{t('网络费用')}}</h5><span>{{loading?t('正在估算'):t('每 5 秒刷新')}}</span></header>
  <p v-if="solana" class="collection-fee-note">{{t('Solana 按实际消息查询手续费，新代币账户租金单独计算，不使用 Gwei 档位。')}}</p>
  <template v-else>
   <div class="collection-fee-options" role="group" :aria-label="t('归集费用档位')">
    <button v-for="choice in choices" :key="choice.id" type="button" class="collection-fee-option" :class="{selected:mode===choice.id}" :aria-pressed="mode===choice.id" :disabled="loading" @click="emit('update:mode',choice.id)">
     <component :is="choice.icon" :size="19" class="collection-fee-icon"/>
     <span class="collection-fee-label"><strong>{{t(choice.label)}}</strong><small>{{choice.id==='custom'&&customGwei?customGwei+' Gwei':option(choice.id)?tokenAmount(option(choice.id)!.gas_price_atoms,9)+' Gwei':t(choice.hint)}}</small></span>
     <span class="collection-fee-cost" v-if="cost(choice.id)!==undefined"><strong>{{tokenAmount(cost(choice.id)!,18)}} {{preview?.native_symbol}}</strong><WalletFeeFiat :atoms="cost(choice.id)" :decimals="18" :quote="preview" :now="now"/></span>
     <Check v-if="mode===choice.id" :size="15" class="collection-fee-check"/>
    </button>
   </div>
   <div v-if="mode==='custom'" class="collection-fee-custom">
    <label>{{t('Gas 单价上限')}}<span><input :value="customGwei" inputmode="decimal" autocomplete="off" spellcheck="false" :aria-label="t('Gas 单价上限')" :aria-invalid="!!error" @input="emit('update:customGwei',($event.target as HTMLInputElement).value)"/><strong>Gwei</strong></span></label>
    <p v-if="error" class="error" role="alert">{{error}}</p><p>{{t('不得低于当前参考价，最高为当前参考价的 1.25 倍；实际用量仍由系统估算。')}}</p>
   </div>
   <dl v-if="preview"><div v-if="gasLimit&&gasLimit!=='0'"><dt>{{t('每地址 Gas 用量上限')}}</dt><dd>{{gasLimit}}</dd></div><div><dt>{{t('本次归集费用上限')}}</dt><dd>{{tokenAmount(preview.max_gas_atoms,18)}} {{preview.native_symbol}}</dd></div></dl>
   <details class="collection-fee-note"><summary>{{t('费用说明')}}</summary><p>{{t('档位按本次归集的实际交易估算，包含必要的补款交易费。确认时间取决于网络，无法保证固定秒数。')}}</p></details>
  </template>
 </section>
</template>
<style scoped>
.collection-fees{display:grid;gap:12px;min-width:0;margin-top:4px}.collection-fees>header{display:flex;justify-content:space-between;align-items:center;gap:12px}.collection-fees h5{font-size:14px;font-weight:600;margin:0}.collection-fees header>span{font-size:12px;color:var(--muted)}.collection-fee-options{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:9px}.collection-fee-option{position:relative;display:flex;align-items:center;flex-wrap:wrap;gap:8px;text-align:left;border:1px solid var(--border);background:var(--surface-raised);border-radius:12px;padding:13px;min-height:86px;color:var(--text);min-width:0;cursor:pointer}.collection-fee-option.selected{border-color:var(--secondary);box-shadow:inset 0 0 0 1px var(--secondary)}.collection-fee-option:focus-visible{outline:2px solid var(--secondary);outline-offset:3px}.collection-fee-icon{color:var(--secondary);flex-shrink:0}.collection-fee-label{display:grid;gap:5px;min-width:0;flex:1;padding-right:13px}.collection-fee-label strong{font-size:13px;font-weight:600}.collection-fee-label small{font-size:12px;color:var(--muted);overflow-wrap:anywhere}.collection-fee-cost{flex-basis:100%;padding-left:27px;display:grid;gap:4px;font-variant-numeric:tabular-nums;overflow-wrap:anywhere;min-width:0}.collection-fee-cost>strong{font-size:12px;font-weight:500}.collection-fee-check{position:absolute;right:10px;top:14px;color:var(--secondary)}.collection-fee-custom{display:grid;gap:7px;background:var(--surface-raised);padding:12px;border:1px solid var(--border);border-radius:10px}.collection-fee-custom label{display:grid;gap:7px;font-size:12px}.collection-fee-custom label>span{display:flex;align-items:center;gap:10px}.collection-fee-custom input{flex:1;min-width:0;width:100%}.collection-fee-custom strong{font-size:12px;font-weight:500;color:var(--muted)}.collection-fee-custom p,.collection-fee-note{font-size:12px;color:var(--muted);line-height:1.8;margin:0}.collection-fee-note summary{cursor:pointer;min-height:32px;display:flex;align-items:center;color:var(--secondary)}.collection-fees dl{display:grid;gap:8px;margin:0}.collection-fees dl>div{display:flex;justify-content:space-between;gap:12px;font-size:12px}.collection-fees dt{color:var(--muted)}.collection-fees dd{margin:0;text-align:right;overflow-wrap:anywhere;max-width:65%;font-variant-numeric:tabular-nums}@media(max-width:420px){.collection-fee-options{grid-template-columns:minmax(0,1fr)}.collection-fee-option{min-height:74px;padding:12px 32px 12px 12px}.collection-fee-label{flex:1 0 95px}.collection-fee-cost{flex:1 1 130px;padding-left:27px;align-items:flex-end;text-align:right}.collection-fee-cost :deep(.wallet-fee-fiat){text-align:right}.collection-fees dl>div{flex-wrap:wrap}.collection-fees dd{max-width:100%}}
</style>
