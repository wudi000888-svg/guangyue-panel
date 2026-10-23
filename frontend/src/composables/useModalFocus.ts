import { nextTick, onBeforeUnmount, onMounted, watch, type Ref } from 'vue';

export interface ModalFocusOptions {
  /** Listen on document (default). Set false for many-instance widgets and bind the returned onKeydown to the dialog instead. */
  listen?: boolean;
  /** Mark #app inert while open (for dialogs teleported out of #app). */
  isolate?: boolean;
  /** Focus target when the element focused before opening is gone, hidden or inert. */
  fallbackFocus?: () => HTMLElement | null | undefined;
}

const FOCUSABLE = 'button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),a[href],summary,[tabindex="0"]';
const focusable = (el: HTMLElement) => !!el.getClientRects().length && !el.closest('[inert]') && getComputedStyle(el).visibility !== 'hidden';

/** Keep modal keyboard navigation and page scrolling within the active layer. */
export function useModalFocus(open: Ref<boolean>, target: Ref<HTMLElement|null>, close: () => void, options: ModalFocusOptions = {}) {
  let locked=false, overflow='', previous:HTMLElement|null=null, previousInert=false;
  function release(){
    if(!locked)return;
    document.body.style.overflow=overflow;
    if(options.isolate){const app=document.getElementById('app');if(app)app.inert=previousInert;}
    locked=false;
    void nextTick(()=>{if(previous?.isConnected&&focusable(previous))previous.focus();else (options.fallbackFocus?.()??previous)?.focus();});
  }
  watch([open,target],async()=>{
    if(!open.value){release();return;}
    if(!locked){
      previous=document.activeElement as HTMLElement;overflow=document.body.style.overflow;document.body.style.overflow='hidden';locked=true;
      if(options.isolate){const app=document.getElementById('app');previousInert=app?.inert||false;if(app)app.inert=true;}
    }
    await nextTick();if(open.value&&!target.value?.contains(document.activeElement))target.value?.focus();
  },{flush:'post'});
  function onKeydown(event:KeyboardEvent){
    if(!open.value||!target.value)return;
    if(event.key==='Escape'){event.preventDefault();close();}
    if(event.key!=='Tab')return;
    const box=target.value;
    const items=Array.from(box.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(el=>el.tabIndex>=0&&focusable(el));
    const first=items[0],last=items.at(-1),active=document.activeElement;
    if(!first){event.preventDefault();box.focus();}
    else if(event.shiftKey&&(active===first||active===box||!box.contains(active))){event.preventDefault();last?.focus();}
    else if(!event.shiftKey&&(active===last||!box.contains(active))){event.preventDefault();first.focus();}
  }
  if(options.listen!==false){
    onMounted(()=>document.addEventListener('keydown',onKeydown));
    onBeforeUnmount(()=>document.removeEventListener('keydown',onKeydown));
  }
  onBeforeUnmount(release);
  return { onKeydown };
}
