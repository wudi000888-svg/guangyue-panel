import { inject, type InjectionKey } from 'vue';
import type { usePanel } from './usePanel';
export type PanelContext = ReturnType<typeof usePanel>;
export const panelKey: InjectionKey<PanelContext> = Symbol('panel');
export function usePanelContext(): PanelContext {
 const context=inject(panelKey);
 if (!context) throw new Error('Panel context is unavailable');
 return context;
}
