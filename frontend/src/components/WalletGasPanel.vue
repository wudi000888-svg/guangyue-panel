<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import QRCode from 'qrcode';
import { ArrowUpRight, Copy, ExternalLink, QrCode, ShieldAlert } from 'lucide-vue-next';
import type { CryptoChain } from '../lib/cryptoPayments';
import { chainExplorer, gasReceiveURI, portfolioAmount, type PortfolioSnapshot } from '../lib/cryptoPortfolio';
import { stamp } from '../lib/commerce';
import { t } from '../i18n';
import WalletAssetMark from './WalletAssetMark.vue';
const props = defineProps<{ chain?: CryptoChain; snapshot?: PortfolioSnapshot; fundingAddress: string; watchOnly: boolean; writable: boolean }>();
const emit = defineEmits<{ copy: [value: string]; withdraw: [] }>();
const qr = ref(''), qrError = ref(''), qrOpen = ref(true); let sequence = 0, disposed = false;
const explorer = computed(() => chainExplorer(props.chain?.chain_id || 0, props.fundingAddress));
const qrText = computed(() => gasReceiveURI(props.chain?.chain_id || 0, props.fundingAddress));
watch(qrText, async value => { const current = ++sequence; qr.value = ''; qrError.value = ''; if (!value) return; try { const image = await QRCode.toDataURL(value, { width: 260, margin: 2, errorCorrectionLevel: 'M', color: { dark: '#142a4a', light: '#ffffff' } }); if (!disposed && sequence === current) qr.value = image; } catch { if (!disposed && sequence === current) qrError.value = t('二维码生成失败，可复制地址充值'); } }, { immediate: true });
onUnmounted(() => { disposed = true; sequence++; });
</script>
<template>
 <section class="wallet-gas-panel">
  <div class="gas-coin-heading"><WalletAssetMark :symbol="chain?.native_symbol" :size="39"/><div><strong>{{chain?.native_symbol || 'Gas'}}</strong><span>{{chain?.name}}</span></div><span class="gas-network-badge">{{t('手续费资金')}}</span></div>
  <div class="gas-amount"><strong>{{portfolioAmount(snapshot?.funding_balance_atoms)}}</strong><span>{{chain?.native_symbol}}</span><small>{{t('固定 Gas 地址余额')}}</small></div>
  <p v-if="snapshot?.funding_error" class="gas-warning" role="status">{{t('余额读取失败，保留上次成功结果。')}}</p>
  <template v-if="fundingAddress && !watchOnly">
   <div v-if="qrOpen" class="wallet-gas-qr"><div><img v-if="qr" :src="qr" width="214" height="214" :alt="t('当前网络 Gas 地址二维码')"/><p v-else>{{qrError || t('正在生成二维码…')}}</p></div><strong>{{t('请仅使用当前网络充值')}} {{chain?.native_symbol}}</strong><span>{{chain?.name}}</span></div>
   <div class="wallet-gas-copy"><code>{{fundingAddress}}</code><button type="button" :aria-label="t('复制固定 Gas 地址')" @click="emit('copy',fundingAddress)"><Copy :size="17"/></button><a v-if="explorer" :href="explorer" target="_blank" rel="noopener noreferrer" :aria-label="t('在区块浏览器查看 Gas 地址')"><ExternalLink :size="17"/></a></div>
   <button type="button" class="gas-qr-toggle" :aria-expanded="qrOpen" @click="qrOpen=!qrOpen"><QrCode :size="15"/>{{qrOpen?t('收起二维码'):t('收款二维码')}}</button>
  </template>
  <div v-if="watchOnly" class="gas-readonly"><ShieldAlert :size="18"/><p>{{t('只读钱包不能自动补 Gas 或签名归集，请使用持有私钥的外部钱包。')}}</p></div>
  <dl class="gas-fee-list"><div><dt>{{t('当前 Gas 参考报价')}}</dt><dd>{{portfolioAmount(snapshot?.gas_price_atoms,9)}} <span>Gwei</span></dd></div><div><dt>{{t('普通原生币转账参考费')}}</dt><dd>{{portfolioAmount(snapshot?.reference_native_transfer_fee_atoms)}} <span>{{chain?.native_symbol}}</span></dd></div><div v-if="snapshot?.gas_checked_at"><dt>{{t('最近查询')}}</dt><dd>{{stamp(snapshot.gas_checked_at)}}</dd></div></dl>
  <p v-if="snapshot?.gas_error" class="gas-warning" role="status">{{t('Gas 报价查询失败，请在提现前重新预估费用。')}}</p>
  <details class="gas-notice"><summary><ShieldAlert :size="14"/>{{t('网络与费用说明')}}</summary><p>{{t('同一地址在不同网络的余额不能互用，请勿向此地址发送其他网络的资产。')}}</p><p>{{t('二维码包含当前网络标识，请在钱包中再次核对网络。')}}</p><p>{{t('按 21,000 Gas 估算，非代币归集报价')}}</p><p>{{t('归集时会为符合条件的地址估算补 Gas 差额及转账费，最终费用以链上收据为准。')}}</p></details>
  <button v-if="!watchOnly" type="button" class="primary gas-preview-button" :disabled="!writable" @click="emit('withdraw')"><ArrowUpRight :size="17"/>{{t('预览归集费用')}}</button>
 </section>
</template>
<style scoped>
.wallet-gas-panel{display:grid;gap:18px;min-width:0}.gas-coin-heading{display:flex;align-items:center;gap:10px}.gas-coin-heading>div{display:grid;gap:4px}.gas-coin-heading strong{font-size:15px;font-weight:600}.gas-coin-heading span{font-size:11px;color:var(--muted)}.gas-network-badge{margin-left:auto;border:1px solid var(--border);padding:5px 8px;border-radius:15px;font-size:10px!important;background:var(--surface-raised)}.gas-amount{display:flex;align-items:baseline;justify-content:center;gap:8px;flex-wrap:wrap;text-align:center;padding:10px 0 1px;font-variant-numeric:tabular-nums}.gas-amount strong{font-size:29px;font-weight:600;letter-spacing:-.7px;overflow-wrap:anywhere;max-width:100%}.gas-amount>span{font-size:14px;color:var(--muted)}.gas-amount small{flex-basis:100%;font-size:11px;color:var(--muted);margin-top:3px}.wallet-gas-qr{display:grid;justify-items:center;gap:8px;text-align:center}.wallet-gas-qr>div{display:grid;place-items:center;width:230px;max-width:100%;min-height:230px;padding:8px;border:1px solid var(--border);border-radius:15px;background:#fff}.wallet-gas-qr img{display:block;max-width:100%;height:auto}.wallet-gas-qr strong{font-size:12px;font-weight:550;margin-top:3px}.wallet-gas-qr>span{font-size:11px;color:var(--muted)}.wallet-gas-qr p{font-size:12px;color:var(--muted);line-height:1.8;margin:0;overflow-wrap:anywhere}.wallet-gas-copy{display:flex;align-items:center;gap:6px;padding:8px 10px;background:var(--surface-raised);border-radius:10px;border:1px solid var(--border);min-width:0}.wallet-gas-copy code{flex:1;min-width:0;font-size:11px;line-height:1.8;overflow-wrap:anywhere}.wallet-gas-copy button,.wallet-gas-copy a{min-width:40px;min-height:44px;padding:8px;display:grid;place-items:center;flex-shrink:0;border:0;background:none;color:var(--secondary)}.gas-qr-toggle{display:flex;align-items:center;justify-content:center;gap:7px;justify-self:center;min-height:44px;border:0;background:none;color:var(--muted);font-size:11px;margin-top:-10px}.gas-fee-list{display:grid;gap:15px;margin:0;padding-top:18px;border-top:1px solid var(--border)}.gas-fee-list>div{display:flex;align-items:baseline;gap:18px;justify-content:space-between;min-width:0}.gas-fee-list dt{font-size:11px;color:var(--muted)}.gas-fee-list dd{font-size:12px;margin:0;font-variant-numeric:tabular-nums;overflow-wrap:anywhere;max-width:60%;text-align:right}.gas-fee-list dd span{font-size:10px;color:var(--muted)}.gas-notice{font-size:11px;color:var(--muted);line-height:1.8}.gas-notice summary{display:flex;align-items:center;gap:6px;min-height:44px;cursor:pointer;color:var(--secondary)}.gas-notice p{margin:7px 0}.gas-preview-button{display:flex;align-items:center;justify-content:center;gap:7px;min-height:46px;width:100%}.gas-warning{margin:0;font-size:11px;color:var(--warning);line-height:1.8}.gas-readonly{display:flex;gap:9px;color:var(--muted);font-size:12px;line-height:1.8}.gas-readonly svg{flex-shrink:0;color:var(--warning)}.gas-readonly p{margin:0}@media(max-width:500px){.gas-amount strong{font-size:25px}.gas-fee-list>div{gap:12px}.gas-network-badge{font-size:9px!important;padding:5px 6px}}
</style>
