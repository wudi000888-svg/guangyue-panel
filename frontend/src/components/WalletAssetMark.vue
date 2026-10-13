<script setup lang="ts">
import { computed } from 'vue';
import {SOLANA_CHAIN_ID,PLASMA_CHAIN_ID} from '../lib/cryptoNetworks';
const props = withDefaults(defineProps<{ symbol?: string; chainId?: number; network?: boolean; size?: number }>(), { size: 40 });
const mark = computed(() => props.network ? props.chainId===SOLANA_CHAIN_ID?'SOL':props.chainId===PLASMA_CHAIN_ID?'XPL':props.chainId === 56 ? 'BNB' : 'ETH' : (props.symbol || 'ETH').toUpperCase());
</script>
<template>
 <svg :width="size" :height="size" viewBox="0 0 48 48" class="wallet-asset-mark" aria-hidden="true" focusable="false">
  <template v-if="mark==='USDT'||mark==='USDT0'"><circle cx="24" cy="24" r="24" fill="#26A17B"/><path d="M12 10h24v6H27v6h-6v-6h-9z" fill="#fff"/><ellipse cx="24" cy="23" rx="15" ry="4" fill="none" stroke="#fff" stroke-width="2.3"/><path d="M21 23h6v15h-6z" fill="#fff"/></template>
  <template v-else-if="mark==='USDC'"><circle cx="24" cy="24" r="24" fill="#2775CA"/><path d="M28.5 17c-1.4-1.3-6.5-2.3-8.1.9-2.9 5.9 10.3 3.8 7.5 10.8-1.5 3.7-7.6 3.2-9.1 1.7M24 12.5v23M14 13.7a14 14 0 0 0 0 20.6m20-20.6a14 14 0 0 1 0 20.6" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round"/></template>
  <template v-else-if="mark==='BNB'"><circle cx="24" cy="24" r="24" fill="#F3BA2F"/><g fill="#171B24"><path d="m24 8 5 5-5 5-5-5zm0 22 5 5-5 5-5-5zM8 24l5-5 5 5-5 5zm22 0 5-5 5 5-5 5zm-6-6 6 6-6 6-6-6z"/><path d="m16 16 3-3 5 5 5-5 3 3-8 8zM16 32l3 3 5-5 5 5 3-3-8-8z"/></g></template>
  <template v-else-if="mark==='SOL'"><circle cx="24" cy="24" r="24" fill="#12131B"/><path d="m14 12-5 5h25l5-5z" fill="#14F195"/><path d="m9 22 5 5h25l-5-5z" fill="#6FC9E6"/><path d="m14 32-5 5h25l5-5z" fill="#9945FF"/></template>
  <template v-else-if="mark==='XPL'"><circle cx="24" cy="24" r="24" fill="#12352D"/><path d="m14 12 20 24m0-24L14 36" stroke="#F4FBE9" stroke-width="6" stroke-linecap="round"/><circle cx="24" cy="24" r="4" fill="#84D7AA"/></template>
  <template v-else><circle cx="24" cy="24" r="24" fill="#EEF0F8"/><path d="M24 6 13 24l11 6 11-6z" fill="#687AAB"/><path d="M24 6v24l11-6z" fill="#8996BB"/><path d="m13 26 11 16 11-16-11 6z" fill="#6979A2"/><path d="M24 32v10l11-16z" fill="#9DA8C8"/></template>
 </svg>
</template>
<style scoped>.wallet-asset-mark{display:block;flex-shrink:0;border-radius:50%}</style>
