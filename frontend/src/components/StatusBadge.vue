<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'neutral'|'success'|'warning'|'danger'|'info'; dot?: boolean }>(), { tone: 'neutral', dot: false });
</script>
<template><span class="ui-status" :class="'ui-status-'+tone"><span v-if="dot" class="ui-status-dot" aria-hidden="true"/><slot/></span></template>
