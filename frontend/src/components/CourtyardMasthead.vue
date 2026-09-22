<script setup lang="ts">
import {t} from '../i18n';
defineProps<{title:string;description:string;eyebrow:string}>();
</script>
<template>
<header class="courtyard-masthead">
 <div class="courtyard-masthead-copy"><span class="courtyard-kicker">{{eyebrow}}</span><h1>{{title}}</h1><p>{{description}}</p><div v-if="$slots.default" class="courtyard-masthead-actions"><slot/></div></div>
 <div class="courtyard-masthead-art" aria-hidden="true"><img src="/guangyue-courtyard.svg" alt="" width="1000" height="760"/><span>{{t('月映珠江')}}</span></div>
</header>
</template>
