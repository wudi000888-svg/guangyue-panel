<script setup lang="ts">
import { PanelsTopLeft } from 'lucide-vue-next';
import { t } from './i18n';

export type ViewMode = 'simple' | 'professional';
defineProps<{ modelValue: ViewMode }>();
const emit = defineEmits<{ 'update:modelValue': [value: ViewMode] }>();
</script>

<template>
  <label class="view-mode-switch" :title="t('选择适合你的工作界面')">
    <PanelsTopLeft :size="15" aria-hidden="true" />
    <select :value="modelValue" :aria-label="t('界面模式')" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value as ViewMode)">
      <option value="simple">{{ t('简易模式') }}</option>
      <option value="professional">{{ t('专业模式') }}</option>
    </select>
  </label>
</template>

<style scoped>
.view-mode-switch{display:flex;flex-direction:row;align-items:center;gap:5px;flex-shrink:0;margin:0;padding:0 5px 0 9px;border:1px solid var(--border);border-radius:7px;background:var(--surface-raised);color:var(--secondary)}
.view-mode-switch select{width:auto;max-width:130px;height:31px;min-height:31px;padding:0 23px 0 2px;border:0;box-shadow:none;background-color:transparent;font-size:11px;color:var(--secondary)}
.view-mode-switch:focus-within{outline:2px solid var(--accent);outline-offset:2px}
@media(max-width:650px){.view-mode-switch select{font-size:10px;height:29px;min-height:29px}.view-mode-switch{padding-left:7px;gap:3px}}
</style>
