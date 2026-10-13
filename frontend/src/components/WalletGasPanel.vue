<script setup lang="ts">
import {computed,onUnmounted,ref,watch} from 'vue';
import QRCode from 'qrcode';
import {ArrowUpRight,Copy,ExternalLink,Fuel,QrCode,ShieldAlert} from 'lucide-vue-next';
import type {CryptoChain} from '../lib/cryptoPayments';
import {chainExplorer,gasReceiveURI,portfolioAmount,type PortfolioSnapshot} from '../lib/cryptoPortfolio';
import {stamp} from '../lib/commerce';
import {t} from '../i18n';
const props=defineProps<{chain?:CryptoChain;snapshot?:PortfolioSnapshot;fundingAddress:string;watchOnly:boolean;writable:boolean}>();
const emit=defineEmits<{copy:[value:string];withdraw:[]}>();
const qr=ref(''),qrError=ref(''),qrOpen=ref(false);let sequence=0,disposed=false;
const explorer=computed(()=>chainExplorer(props.chain?.chain_id||0,props.fundingAddress));
const qrText=computed(()=>gasReceiveURI(props.chain?.chain_id||0,props.fundingAddress));
watch(qrText,async value=>{const current=++sequence;qr.value='';qrError.value='';if(!value)return;try{const image=await QRCode.toDataURL(value,{width:260,margin:2,errorCorrectionLevel:'M',color:{dark:'#142a4a',light:'#ffffff'}});if(!disposed&&sequence===current)qr.value=image}catch{if(!disposed&&sequence===current)qrError.value=t('二维码生成失败，可复制地址充值')}},{immediate:true});
onUnmounted(()=>{disposed=true;sequence++});
</script>
<template>
 <section class="wallet-gas-panel">
  <header><span class="wallet-gas-icon"><Fuel :size="22"/></span><div><h4>{{t('Gas 资金管理')}}</h4><p>{{chain?.name}} · {{t('手续费币')}} {{chain?.native_symbol}}</p></div></header>
  <div class="wallet-gas-metrics">
   <article><span>{{t('Gas 资金地址余额')}}</span><strong>{{portfolioAmount(snapshot?.funding_balance_atoms)}} <small>{{chain?.native_symbol}}</small></strong><p>{{t('用于为收款地址补充转账手续费')}}</p><small v-if="snapshot?.funding_checked_at">{{stamp(snapshot.funding_checked_at)}}</small><p v-if="snapshot?.funding_error" class="gas-warning">{{t('余额读取失败，保留上次成功结果。')}} {{t(snapshot.funding_error)}}</p></article>
   <article><span>{{t('当前 Gas 参考报价')}}</span><strong>{{portfolioAmount(snapshot?.gas_price_atoms,9)}} <small>Gwei</small></strong><p>{{t('包含安全余量的参考 Gas 单价')}}</p><small v-if="snapshot?.gas_checked_at">{{stamp(snapshot.gas_checked_at)}}</small></article>
   <article><span>{{t('普通原生币转账参考费')}}</span><strong>{{portfolioAmount(snapshot?.reference_native_transfer_fee_atoms)}} <small>{{chain?.native_symbol}}</small></strong><p>{{t('按 21,000 Gas 估算，非代币归集报价')}}</p></article>
  </div>
  <p v-if="snapshot?.gas_error" class="gas-warning" role="status">{{t('Gas 报价查询失败，请在提现前重新预估费用。')}} {{t(snapshot.gas_error)}}</p>
  <article v-if="fundingAddress&&!watchOnly" class="wallet-gas-receive">
   <div class="gas-receive-heading"><div><h5>{{t('充值手续费币')}}</h5><p>{{t('请仅使用当前网络充值')}} {{chain?.native_symbol}}</p></div><button type="button" :aria-expanded="qrOpen" @click="qrOpen=!qrOpen"><QrCode :size="16"/>{{t('收款二维码')}}</button></div>
   <div class="wallet-gas-copy"><code>{{fundingAddress}}</code><button type="button" :aria-label="t('复制固定 Gas 地址')" @click="emit('copy',fundingAddress)"><Copy :size="17"/></button><a v-if="explorer" :href="explorer" target="_blank" rel="noopener noreferrer" :aria-label="t('在区块浏览器查看 Gas 地址')"><ExternalLink :size="17"/></a></div>
   <div v-if="qrOpen" class="wallet-gas-qr"><img v-if="qr" :src="qr" width="230" height="230" :alt="t('当前网络 Gas 地址二维码')"/><p v-else>{{qrError||t('正在生成二维码…')}}</p><strong>{{chain?.name}} · {{chain?.native_symbol}}</strong><p>{{t('二维码包含当前网络标识，请在钱包中再次核对网络。')}}</p></div>
   <p class="wallet-gas-network-note"><ShieldAlert :size="15"/><span>{{t('同一地址在不同网络的余额不能互用，请勿向此地址发送其他网络的资产。')}}</span></p>
  </article>
  <p v-else-if="watchOnly" class="wallet-gas-network-note"><ShieldAlert :size="16"/><span>{{t('只读钱包不能自动补 Gas 或签名归集，请使用持有私钥的外部钱包。')}}</span></p>
  <footer><p>{{t('归集时会为符合条件的地址估算补 Gas 差额及转账费，最终费用以链上收据为准。')}}</p><button v-if="!watchOnly" type="button" class="primary" :disabled="!writable" @click="emit('withdraw')"><ArrowUpRight :size="16"/>{{t('预览归集费用')}}</button></footer>
 </section>
</template>
<style scoped>
.wallet-gas-panel{display:grid;gap:18px;min-width:0}.wallet-gas-panel>header{display:flex;align-items:center;gap:12px}.wallet-gas-icon{display:grid;place-items:center;width:44px;height:44px;background:var(--accent-soft);color:var(--accent-text);border-radius:13px}.wallet-gas-panel h4{margin:0;font-size:18px}.wallet-gas-panel header p{font-size:12px;color:var(--secondary);margin:6px 0 0}.wallet-gas-metrics{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.wallet-gas-metrics article{display:grid;gap:10px;align-content:start;background:var(--surface-raised);padding:19px;border:1px solid var(--border);border-radius:14px;min-width:0}.wallet-gas-metrics article>span{font-size:12px;color:var(--secondary)}.wallet-gas-metrics strong{font-size:22px;font-weight:650;letter-spacing:-.4px;overflow-wrap:anywhere;font-variant-numeric:tabular-nums}.wallet-gas-metrics strong small{font-size:12px;color:var(--secondary);font-weight:400}.wallet-gas-metrics p{font-size:11px;color:var(--secondary);line-height:1.8;margin:0}.wallet-gas-metrics article>small{font-size:10px;color:var(--muted)}.wallet-gas-receive{border:1px solid var(--border);border-radius:15px;padding:21px;background:var(--surface);display:grid;gap:15px;min-width:0}.gas-receive-heading{display:flex;align-items:center;justify-content:space-between;gap:12px}.gas-receive-heading h5{font-size:15px;margin:0}.gas-receive-heading p{font-size:12px;color:var(--secondary);margin:7px 0 0}.gas-receive-heading button{display:flex;gap:7px;align-items:center;min-height:42px;flex-shrink:0}.wallet-gas-copy{display:flex;align-items:center;gap:8px;padding:12px;background:var(--accent-soft);border-radius:9px;min-width:0}.wallet-gas-copy code{flex:1;min-width:0;font-size:12px;line-height:1.8;overflow-wrap:anywhere}.wallet-gas-copy button,.wallet-gas-copy a{min-height:40px;min-width:40px;display:grid;place-items:center;flex-shrink:0;color:var(--accent-text)}.wallet-gas-network-note{display:flex;gap:8px;align-items:flex-start;font-size:12px;line-height:1.8;color:var(--secondary);margin:0}.wallet-gas-network-note svg{flex-shrink:0;color:var(--warning);margin-top:3px}.wallet-gas-qr{display:flex;align-items:center;flex-direction:column;gap:12px;padding:18px;min-width:0;text-align:center;background:var(--surface-raised);border-radius:12px}.wallet-gas-qr img{display:block;max-width:100%;height:auto;border-radius:10px;border:1px solid var(--border)}.wallet-gas-qr strong{font-size:13px}.wallet-gas-qr p,.wallet-gas-panel footer p{font-size:12px;line-height:1.8;color:var(--secondary);margin:0}.wallet-gas-panel footer{display:flex;justify-content:space-between;align-items:center;gap:15px}.wallet-gas-panel footer button{display:flex;align-items:center;justify-content:center;gap:7px;min-height:44px;flex-shrink:0}.gas-warning{font-size:12px;line-height:1.8;color:var(--warning)!important;margin:0;overflow-wrap:anywhere}@media(max-width:700px){.wallet-gas-metrics{grid-template-columns:1fr}.wallet-gas-metrics article{padding:16px}.wallet-gas-metrics strong{font-size:21px}.wallet-gas-receive{padding:16px}.gas-receive-heading{align-items:flex-start;flex-direction:column}.wallet-gas-panel footer{align-items:stretch;flex-direction:column}}
</style>
