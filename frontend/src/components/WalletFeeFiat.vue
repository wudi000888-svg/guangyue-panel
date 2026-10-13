<script setup lang="ts">
import {computed} from 'vue';
import {feeFiat,fiatPriceCurrent,nativeFiatKnown,type NativeFiatQuote} from '../lib/cryptoFiat';
import {stamp} from '../lib/commerce';
import {t} from '../i18n';
const props=withDefaults(defineProps<{atoms?:string;decimals:number;quote?:NativeFiatQuote;now?:number;details?:boolean}>(),{details:false});
const amount=computed(()=>feeFiat(props.atoms,props.decimals,props.quote));
const current=computed(()=>fiatPriceCurrent(props.quote,props.now));
const known=computed(()=>nativeFiatKnown(props.quote));
</script>
<template><span class="wallet-fee-fiat" :class="{stale:known&&!current}" :data-price-state="known?current?'current':'stale':'unknown'" :title="quote?.price_checked_at?t('行情时间')+' '+stamp(quote.price_checked_at)+(quote.fiat_source?' · '+quote.fiat_source:''):t('等待费用汇率查询')"><span>{{amount}}</span><small v-if="!known">{{t('等待费用汇率查询')}}</small><small v-else-if="!current">{{t('上次行情估值')}}</small><small v-if="details&&known&&quote?.price_checked_at">{{t('行情时间')}} {{stamp(quote.price_checked_at)}}<span v-if="quote.fiat_source"> · {{quote.fiat_source}}</span></small></span></template>
<style scoped>.wallet-fee-fiat{display:flex;flex-direction:column;gap:4px;font-size:12px;line-height:1.6;white-space:normal;overflow-wrap:anywhere;color:var(--secondary);font-weight:400}.wallet-fee-fiat>small{font-size:12px;color:var(--muted)}.wallet-fee-fiat.stale>small{color:var(--warning)}</style>
