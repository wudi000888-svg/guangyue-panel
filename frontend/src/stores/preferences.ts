import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { readPreference, writePreference } from '../lib/preferences';

export const usePreferencesStore = defineStore('preferences', () => {
  const theme = ref(readPreference('guangyue-theme') === 'light' ? 'light' : 'dark');
  const sideCollapsed = ref(readPreference('guangyue-sidebar') === 'collapsed');
  const publicFeaturesEnabled = ref(readPreference('guangyue-public-features') === 'enabled');
  watch(theme, value => {
    document.documentElement.dataset.theme = value;
    writePreference('guangyue-theme', value);
  }, { immediate: true });
  watch(sideCollapsed, value => writePreference('guangyue-sidebar', value ? 'collapsed' : 'expanded'));
  watch(publicFeaturesEnabled, value => writePreference('guangyue-public-features', value ? 'enabled' : 'disabled'));
  function toggleTheme() { theme.value = theme.value === 'dark' ? 'light' : 'dark'; }
  return { theme, sideCollapsed, publicFeaturesEnabled, toggleTheme };
});
