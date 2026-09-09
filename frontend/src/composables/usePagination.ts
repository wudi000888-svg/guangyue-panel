import { computed, ref, watch, type Ref } from 'vue';
export function usePagination<T extends {id: string|number}>(items: Ref<T[]>, size = 25) {
  const page = ref(1);
  const pages = computed(() => Math.max(1, Math.ceil(items.value.length / size)));
  watch(() => items.value.map(row => row.id).join('|'), () => { page.value = 1; });
  const rows = computed(() => items.value.slice((page.value - 1) * size, page.value * size));
  return {page, pages, rows};
}
