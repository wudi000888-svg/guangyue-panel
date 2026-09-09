<script setup lang="ts">
import { Languages } from 'lucide-vue-next';
import { locale, setLocale, type Locale } from './i18n';
</script>
<template><label class="language-switch"><Languages :size="16"/><select :value="locale" aria-label="Language / 语言" @change="setLocale(($event.target as HTMLSelectElement).value as Locale)"><option value="zh-CN">简体中文</option><option value="en">English</option></select></label></template>
