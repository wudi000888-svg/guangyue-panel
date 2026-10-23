import { onScopeDispose, ref } from 'vue';

/** Transient status line shown by PanelDialogs; each new message restarts the 4 s timer. */
export function useToast(duration = 4000) {
  const notice = ref('');
  let timer: ReturnType<typeof setTimeout> | undefined;
  function toast(text: string) {
    clearTimeout(timer);
    notice.value = text;
    timer = setTimeout(() => { notice.value = ''; }, duration);
  }
  onScopeDispose(() => clearTimeout(timer));
  return { notice, toast };
}
