<script setup lang="ts">
// Feature search (Ctrl/⌘ K). Focus trap, Escape and scroll lock come from useModalFocus.
import { computed, nextTick, ref, toRef, watch, type Component } from "vue";
import { ChevronRight, Search, X } from "lucide-vue-next";
import { searchNavigation } from "../../lib/navigation";
import { useModalFocus } from "../../composables/useModalFocus";
import { t } from "../../i18n";

export interface CommandEntry { id: string; label: string; icon: Component; group: string; description: string }
const props = defineProps<{ open: boolean; entries: CommandEntry[] }>();
const emit = defineEmits<{ close: []; navigate: [id: string] }>();
const query = ref(""), index = ref(0);
const dialog = ref<HTMLElement | null>(null), input = ref<HTMLInputElement | null>(null);
const results = computed(() => searchNavigation(props.entries, query.value));
const close = () => emit("close");
useModalFocus(toRef(props, "open"), dialog, close);
// Registered after useModalFocus so the input wins over the dialog container.
watch(() => props.open, async open => {
  if (!open) return;
  query.value = "";
  await nextTick();
  input.value?.focus();
}, { flush: "post", immediate: true });
watch(query, () => { index.value = 0; });
watch(() => results.value.map(item => item.id).join("|"), () => { index.value = Math.min(index.value, Math.max(0, results.value.length - 1)); });
watch(index, async () => { await nextTick(); document.getElementById(`navigation-result-${index.value}`)?.scrollIntoView({ block: "nearest" }); });
function keydown(e: KeyboardEvent) {
  if (!["ArrowDown", "ArrowUp", "Enter"].includes(e.key)) return;
  e.preventDefault();
  if (e.key === "Enter") { const item = results.value[index.value]; if (item) emit("navigate", item.id); }
  else if (results.value.length) index.value = (index.value + (e.key === "ArrowDown" ? 1 : -1) + results.value.length) % results.value.length;
}
function clear() { query.value = ""; input.value?.focus(); }
</script>
<template>
  <div v-if="open" class="navigation-shade" @click.self="close">
    <section ref="dialog" class="navigation-palette" role="dialog" aria-modal="true" :aria-label="t('搜索功能')" tabindex="-1">
      <div class="navigation-palette-input"><Search :size="21"/><input ref="input" v-model="query" :placeholder="t('搜索页面、节点、套餐…')" :aria-label="t('搜索功能')" role="combobox" aria-autocomplete="list" aria-expanded="true" aria-controls="navigation-results" :aria-activedescendant="results.length?'navigation-result-'+index:undefined" autocomplete="off" @keydown="keydown"/><button class="icon" :aria-label="t('关闭')" @click="close"><X :size="19"/></button></div>
      <div class="navigation-palette-summary" aria-live="polite">{{query.trim()?t('搜索结果'):t('全部功能')}} <span>{{results.length}}</span><button v-if="query" class="text-button" @click="clear">{{t('清空搜索')}}</button></div>
      <div id="navigation-results" class="navigation-results" role="listbox" :aria-label="t('搜索结果')">
        <button v-for="(item,i) in results" :id="'navigation-result-'+i" :key="item.id" role="option" tabindex="-1" :aria-selected="index===i" :class="{active:index===i}" @mouseenter="index=i" @click="emit('navigate', item.id)"><span class="navigation-result-icon"><component :is="item.icon" :size="19"/></span><span class="navigation-result-copy"><strong>{{item.label}}<small>{{item.group}}</small></strong><span>{{item.description}}</span></span><ChevronRight :size="16"/></button>
      </div>
      <div v-if="!results.length" class="navigation-no-results"><Search :size="28"/><strong>{{t('没有找到相关功能')}}</strong><p>{{t('试试搜索「节点」「套餐」或「设置」。')}}</p><button @click="clear">{{t('查看全部功能')}}</button></div>
      <footer><span><kbd>↑</kbd><kbd>↓</kbd>{{t('选择')}}</span><span><kbd>Enter</kbd>{{t('打开')}}</span><span><kbd>Esc</kbd>{{t('关闭')}}</span></footer>
    </section>
  </div>
</template>
