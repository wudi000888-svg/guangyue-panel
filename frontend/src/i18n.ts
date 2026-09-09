import { ref, watch } from 'vue';
import en from './locales/en.json';
import { readPreference, writePreference } from './lib/preferences';
export type Locale = 'zh-CN' | 'en';
const saved = readPreference('guangyue-locale');
export const locale = ref<Locale>(saved === 'en' ? 'en' : 'zh-CN');
export function setLocale(value: Locale) { locale.value = value; writePreference('guangyue-locale', value); }
export function applyDefaultLocale(value: string) { if (!readPreference('guangyue-locale')) locale.value = value === 'en' ? 'en' : 'zh-CN'; }
export function t(value: string): string { return locale.value === 'en' ? (en as Record<string,string>)[value] ?? value : value; }
watch(locale, value => { if (typeof document !== 'undefined') document.documentElement.lang = value; }, { immediate: true });
