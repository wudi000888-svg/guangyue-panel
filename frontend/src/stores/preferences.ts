import { defineStore } from 'pinia';
import { computed, ref, watch } from 'vue';
import { readPreference, writePreference } from '../lib/preferences';
import type { ViewMode } from '../ViewModeSwitcher.vue';

export const usePreferencesStore = defineStore('preferences', () => {
  const theme = ref(readPreference('guangyue-theme') === 'light' ? 'light' : 'dark');
  const sideCollapsed = ref(readPreference('guangyue-sidebar') === 'collapsed');
  const viewMode = ref<ViewMode>(readPreference('guangyue-ui-mode') === 'simple' ? 'simple' : 'professional');
  const simpleMode = computed(() => viewMode.value === 'simple');
  watch(theme, value => {
    document.documentElement.dataset.theme = value;
    writePreference('guangyue-theme', value);
  }, { immediate: true });
  watch(sideCollapsed, value => writePreference('guangyue-sidebar', value ? 'collapsed' : 'expanded'));
  watch(viewMode, value => writePreference('guangyue-ui-mode', value));
  function toggleTheme() { theme.value = theme.value === 'dark' ? 'light' : 'dark'; }
  return { theme, sideCollapsed, viewMode, simpleMode, toggleTheme };
});
