import { nextTick, onBeforeUnmount, onMounted, watch, type Ref } from 'vue';

/** Keep modal keyboard navigation and page scrolling within the active layer. */
export function useModalFocus(open: Ref<boolean>, target: Ref<HTMLElement|null>, close: () => void) {
  let locked=false, overflow='', previous:HTMLElement|null=null;
  function release(){if(!locked)return;document.body.style.overflow=overflow;locked=false;void nextTick(()=>previous?.focus());}
  watch([open,target],async()=>{
    if(!open.value){release();return;}
    if(!locked){previous=document.activeElement as HTMLElement;overflow=document.body.style.overflow;document.body.style.overflow='hidden';locked=true;}
    await nextTick();if(open.value&&!target.value?.contains(document.activeElement))target.value?.focus();
  },{flush:'post'});
  function key(event:KeyboardEvent){
    if(!open.value||!target.value)return;
    if(event.key==='Escape'){event.preventDefault();close();}
    if(event.key!=='Tab')return;
    const items=Array.from(target.value.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),a[href],summary,[tabindex="0"]')).filter(el=>el.getClientRects().length);
    const first=items[0],last=items.at(-1),active=document.activeElement;
    if(!first){event.preventDefault();target.value.focus();}
    else if(event.shiftKey&&(active===first||active===target.value)){event.preventDefault();last?.focus();}
    else if(!event.shiftKey&&(active===last||!target.value.contains(active))){event.preventDefault();first.focus();}
  }
  onMounted(()=>document.addEventListener('keydown',key));
  onBeforeUnmount(()=>{document.removeEventListener('keydown',key);release();});
}
