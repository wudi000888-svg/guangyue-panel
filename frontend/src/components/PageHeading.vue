<script setup lang="ts">
defineProps<{ title: string; description?: string; eyebrow?: string }>();
</script>
<template>
  <header class="ui-page-heading">
    <div class="ui-page-heading-copy"><span v-if="eyebrow" class="ui-eyebrow">{{eyebrow}}</span><h1>{{title}}</h1><p v-if="description">{{description}}</p><slot/></div>
    <div v-if="$slots.actions" class="ui-page-heading-actions"><slot name="actions"/></div>
  </header>
</template>
