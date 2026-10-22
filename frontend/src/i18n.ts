import { ref, shallowRef, watch } from 'vue';
import { readPreference, writePreference } from './lib/preferences';
export type Locale = 'zh-CN' | 'en';
const saved = readPreference('guangyue-locale');
export const locale = ref<Locale>(saved === 'en' ? 'en' : 'zh-CN');
// The Chinese source strings are the keys, so the English table is only
// fetched once English is actually selected instead of shipping in the entry.
const en = shallowRef<Record<string, string> | null>(null);
let enLoading: Promise<void> | undefined;
export function loadLocaleMessages(value: Locale = locale.value): Promise<void> {
  if (value !== 'en' || en.value) return Promise.resolve();
  enLoading ??= import('./locales/en.json').then(m => { en.value = m.default as Record<string, string>; }, error => { enLoading = undefined; throw error; });
  return enLoading;
}
export function setLocale(value: Locale) { locale.value = value; writePreference('guangyue-locale', value); }
export function applyDefaultLocale(value: string) { if (!readPreference('guangyue-locale')) locale.value = value === 'en' ? 'en' : 'zh-CN'; }
export function t(value: string): string { return locale.value === 'en' ? en.value?.[value] ?? value : value; }
watch(locale, value => {
  if (typeof document !== 'undefined') document.documentElement.lang = value;
  loadLocaleMessages(value).catch(() => {});
}, { immediate: true });
