<script setup lang="ts">
import { nextTick } from 'vue';
type Tab = { id: string; label: string; count?: number };
const props = defineProps<{ modelValue: string; items: Tab[]; label: string; id: string }>();
const emit = defineEmits<{ 'update:modelValue': [value: string] }>();
function keyboard(event: KeyboardEvent, index: number) {
  let target = index;
  if (event.key === 'ArrowRight') target = (index + 1) % props.items.length;
  else if (event.key === 'ArrowLeft') target = (index - 1 + props.items.length) % props.items.length;
  else if (event.key === 'Home') target = 0;
  else if (event.key === 'End') target = props.items.length - 1;
  else return;
  event.preventDefault();
  emit('update:modelValue', props.items[target].id);
  nextTick(() => document.getElementById(`${props.id}-tab-${props.items[target].id}`)?.focus());
}
</script>
<template>
  <div class="resource-tabs" role="tablist" :aria-label="label">
    <button v-for="(tab, index) in items" :id="`${id}-tab-${tab.id}`" :key="tab.id" type="button" role="tab" :aria-selected="modelValue === tab.id" :aria-controls="`${id}-panel`" :tabindex="modelValue === tab.id ? 0 : -1" @click="emit('update:modelValue', tab.id)" @keydown="keyboard($event, index)">
      <span>{{ tab.label }}</span><b v-if="tab.count !== undefined">{{ tab.count }}</b>
    </button>
  </div>
</template>
<style>
.resource-tabs{display:flex;align-items:stretch;gap:6px;border-bottom:1px solid var(--border);margin:0 0 24px;overflow-x:auto;scrollbar-width:thin;max-width:100%}.resource-tabs>button{position:relative;display:flex;align-items:center;justify-content:center;gap:9px;white-space:nowrap;padding:14px 17px;border:0;background:transparent;color:var(--muted);border-radius:8px 8px 0 0;font-size:12px;flex-shrink:0;line-height:1.4}.resource-tabs>button:hover{color:var(--text);background:var(--surface)}.resource-tabs>button[aria-selected=true]{color:var(--accent-text);font-weight:600;background:var(--accent-soft)}.resource-tabs>button[aria-selected=true]:after{content:'';position:absolute;height:2px;bottom:0;left:0;right:0;background:var(--accent-text);border-radius:3px}.resource-tabs b{font-size:10px;font-weight:600;min-width:20px;padding:2px 6px;border-radius:5px;background:var(--surface);font-variant-numeric:tabular-nums}.resource-tabs>button:focus-visible{outline:2px solid var(--accent-text);outline-offset:-3px}@media(max-width:650px){.resource-tabs{gap:0;margin-bottom:20px}.resource-tabs>button{padding:12px 11px;gap:6px;font-size:11px}}
</style>
