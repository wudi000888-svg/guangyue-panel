<script setup lang="ts">
import { computed, ref } from 'vue';
import type { Client } from '../../lib/clientDownloads';
import { t } from '../../i18n';

const props = withDefaults(defineProps<{ client: Client; size?: number }>(), { size: 48 });
const failed = ref(false);
const description = computed(() => props.client.icon_kind === 'official'
  ? props.client.name
  : props.client.name + ' · ' + t('文字识别标记，非官方图标'));
</script>
<template>
  <span class="client-brand" :style="{width:size+'px',height:size+'px'}" :title="description" aria-hidden="true">
    <img v-if="!failed" :src="client.icon" alt="" :width="size" :height="size" loading="lazy" @error="failed=true"/>
    <span v-else>{{client.name.slice(0,2)}}</span>
  </span>
</template>
<style scoped>
.client-brand{display:inline-flex;align-items:center;justify-content:center;flex-shrink:0;border-radius:12px;overflow:hidden}.client-brand img{width:100%;height:100%;object-fit:contain;display:block}.client-brand>span{display:grid;place-items:center;width:100%;height:100%;color:var(--accent-text);background:var(--accent-soft);font-size:16px;font-weight:650}
</style>
