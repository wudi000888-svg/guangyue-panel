<script setup lang="ts">
import PageHeading from './PageHeading.vue';
defineProps<{ title: string; description?: string }>();
</script>
<template>
  <section class="table-page-layout">
    <PageHeading :title="title" :description="description"><template #actions><slot name="actions"/></template></PageHeading>
    <div v-if="$slots.filters" class="table-filters"><slot name="filters" /></div>
    <slot name="notice" />
    <div class="table-wrap"><slot /></div>
    <footer v-if="$slots.pagination" class="table-pagination"><slot name="pagination" /></footer>
  </section>
</template>
<style scoped>
.table-page-layout{display:flex;flex-direction:column;gap:20px;min-width:0}.table-page-layout .ui-page-heading{margin-bottom:0}.table-wrap{overflow-x:auto;border:1px solid var(--border);border-radius:12px;background:var(--surface)}.table-filters{display:flex;gap:12px;align-items:center;flex-wrap:wrap}.table-pagination{display:flex;justify-content:space-between;align-items:center;gap:16px;color:var(--muted);font-size:13px}
@media(max-width:650px){.table-pagination{flex-wrap:wrap}}
</style>
