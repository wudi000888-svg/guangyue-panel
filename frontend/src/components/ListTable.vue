<script setup lang="ts">
import { ChevronLeft, ChevronRight } from 'lucide-vue-next';
import { t } from '../i18n';
defineProps<{total:number;page:number;pages:number}>();
defineEmits<{'update:page':[value:number]}>();
</script>
<template>
  <div class="list-table" :class="{'has-mobile-cards':!!$slots.mobile}"><div class="table-scroll"><slot/></div><div v-if="$slots.mobile" class="list-mobile"><slot name="mobile"/></div><footer class="list-pagination"><span>{{t('共')}} {{total}} {{t('项')}}</span><div><button class="icon" :disabled="page<=1" :aria-label="t('上一页')" @click="$emit('update:page',page-1)"><ChevronLeft :size="16"/></button><span>{{page}} / {{pages}}</span><button class="icon" :disabled="page>=pages" :aria-label="t('下一页')" @click="$emit('update:page',page+1)"><ChevronRight :size="16"/></button></div></footer></div>
</template>
<style scoped>
.list-table{min-width:0}.list-pagination{display:flex;align-items:center;justify-content:space-between;padding:14px 18px;border-top:1px solid var(--border);font-size:12px;color:var(--muted)}.list-pagination>div{display:flex;align-items:center;gap:14px}
.list-mobile{display:none}@media(max-width:700px){.has-mobile-cards>.table-scroll{display:none}.list-mobile{display:block}.list-pagination{padding:16px 0}}
</style>
