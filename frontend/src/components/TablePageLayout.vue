<script setup lang="ts">
defineProps<{ title: string; description?: string }>();
</script>
<template>
  <section class="table-page-layout">
    <header class="page-heading"><div><h1>{{ title }}</h1><p v-if="description" class="section-subtitle">{{ description }}</p></div><div class="page-actions"><slot name="actions" /></div></header>
    <div v-if="$slots.filters" class="table-filters"><slot name="filters" /></div>
    <slot name="notice" />
    <div class="table-wrap"><slot /></div>
    <footer v-if="$slots.pagination" class="table-pagination"><slot name="pagination" /></footer>
  </section>
</template>
<style scoped>
.table-page-layout{display:flex;flex-direction:column;gap:20px;min-width:0}.table-page-layout .page-heading{margin-bottom:0;justify-content:space-between}.table-page-layout .page-heading>div:first-child{display:block}.table-page-layout h1{font-size:19px}.table-wrap{overflow-x:auto;border:1px solid var(--border);border-radius:10px;background:var(--surface)}.page-actions{display:flex;gap:10px;align-items:center}.table-filters{display:flex;gap:12px;align-items:center;flex-wrap:wrap}.table-pagination{display:flex;justify-content:space-between;align-items:center;gap:16px;color:var(--muted);font-size:12px}
@media(max-width:650px){.table-page-layout .page-heading{align-items:flex-start;flex-wrap:wrap}.page-actions{flex-wrap:wrap}.table-pagination{flex-wrap:wrap}}
</style>
