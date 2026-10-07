<script setup lang="ts">
defineProps<{ title: string; description?: string }>();
</script>
<template><div class="ui-empty-state"><div v-if="$slots.icon" class="ui-empty-state-icon" aria-hidden="true"><slot name="icon"/></div><h2>{{title}}</h2><p v-if="description">{{description}}</p><div v-if="$slots.actions" class="ui-empty-state-actions"><slot name="actions"/></div></div></template>
