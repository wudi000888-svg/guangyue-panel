<script setup lang="ts">
import { t } from './i18n'
import { computed } from 'vue'
import { Globe2 } from 'lucide-vue-next'
const props = defineProps<{ code?: string; country?: string }>()
const code = computed(() => (props.code || '').toUpperCase())
const flag = computed(() => /^[A-Z]{2}$/.test(code.value)
  ? String.fromCodePoint(...Array.from(code.value, c => 127397 + c.charCodeAt(0))) : '')
</script>
<template>
  <span class="country-mark" :title="country || t('国家待识别')">
    <span v-if="flag" role="img" :aria-label="country || code" style="font-size:24px">{{ flag }}</span>
    <span v-else-if="code">{{ code }}</span>
    <Globe2 v-else :size="20" />
  </span>
</template>
