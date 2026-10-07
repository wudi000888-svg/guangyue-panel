import { computed, onScopeDispose, ref, shallowRef, watch, type Ref } from 'vue';
import { ApiError, isCancelled, type api as panelAPI } from '../lib/api';
import { DEFAULT_ADMIN_SIDEBAR, DEFAULT_MEMBER_SIDEBAR } from '../lib/navigation';
import { t } from '../i18n';
import type { SiteSettings } from '../types';

export type SidebarRole = 'admin' | 'member';
type DragTarget = 'sidebar' | 'directory';
type NavigationResponse = { role: SidebarRole; items: string[]; revision: string; site_revision: string };
type Change = { role: SidebarRole; values: Record<string, boolean>; epoch: number; sequence: number };
const normalize = (items: readonly string[]) => [...new Set(items.filter(id => id !== 'settings')), 'settings'];
const equal = (a: string[], b: string[]) => a.length === b.length && a.every((id, i) => id === b[i]);
function applyChange(change: Change, items: string[]) {
  const values = new Set(items);
  for (const [id, pinned] of Object.entries(change.values)) { if (pinned) values.add(id); else values.delete(id); }
  return normalize([...values]);
}

/** One queue for navigation writes and full settings saves; optimistic moves stay visible through polling. */
export function useSidebarCustomization(options: {
  site: Ref<SiteSettings>;
  api: typeof panelAPI;
  canCustomize: () => boolean;
  available: () => readonly string[];
  context: () => string;
  onConfirmed?: () => void;
}) {
  const pending = shallowRef<Change[]>([]);
  const saved = ref(false), error = ref('');
  const dragging = ref<{ id: string; source: DragTarget } | null>(null);
  const pointerPosition = ref<{ x: number; y: number } | null>(null);
  const target = ref<DragTarget | null>(null), directoryRequest = ref(0);
  const saving = computed(() => pending.value.length > 0);
  let epoch = 0, sequence = 0, tail: Promise<unknown> = Promise.resolve(), failed: Change[] = [];
  const latestIntent = new Map<string, number>();
  let releasePointer: (() => void) | null = null;
  const remaining = (change: Change) => Object.fromEntries(Object.entries(change.values).filter(([id]) => latestIntent.get(change.role + ':' + id) === change.sequence));

  function selected(role: SidebarRole): string[] {
    const field = role === 'admin' ? 'sidebar_admin' : 'sidebar_member';
    let items = normalize(options.site.value[field] ?? (role === 'admin' ? DEFAULT_ADMIN_SIDEBAR : DEFAULT_MEMBER_SIDEBAR));
    for (const change of pending.value) if (change.role === role) items = applyChange(change, items);
    return items;
  }
  function withSettingsLock<T>(fn: () => Promise<T>): Promise<T> {
    const result = tail.then(fn, fn);
    tail = result.catch(() => undefined);
    return result;
  }
  function acceptSettings(value: SiteSettings) {
    options.onConfirmed?.();
    options.site.value = value;
  }
  function confirm(value: NavigationResponse, role: SidebarRole) {
    if (value.role !== role || !Array.isArray(value.items) || !value.items.every(id => typeof id === 'string') || typeof value.revision !== 'string' || typeof value.site_revision !== 'string') {
      throw new Error(t('服务器响应无效，请稍后重试'));
    }
    options.onConfirmed?.();
    options.site.value = { ...options.site.value, [role === 'admin' ? 'sidebar_admin' : 'sidebar_member']: normalize(value.items), revision: value.site_revision };
    return value;
  }
  const current = (change: Change) => change.epoch === epoch && options.canCustomize();
  async function persist(change: Change) {
    if (!current(change)) return;
    let attempted: string[] | undefined, success = false;
    try {
      for (let attempt = 0; attempt < 2; attempt++) {
        const latest = await options.api<NavigationResponse>('/settings/navigation?role=' + change.role);
        if (!current(change)) return;
        confirm(latest, change.role);
        attempted = applyChange(change, normalize(latest.items));
        if (equal(attempted, normalize(latest.items))) { success = true; break; }
        try {
          const result = await options.api<NavigationResponse>('/settings/navigation', 'PUT', { role: change.role, items: attempted, revision: latest.revision });
          if (!current(change)) return;
          confirm(result, change.role);
          success = true;
          break;
        } catch (reason) {
          if (reason instanceof ApiError && reason.status === 409 && attempt === 0) continue;
          throw reason;
        }
      }
    } catch (reason) {
      if (!current(change) || isCancelled(reason)) return;
      // A lost mutation response may still have committed. Read back before rolling the display back.
      let confirmed = false;
      try {
        const latest = await options.api<NavigationResponse>('/settings/navigation?role=' + change.role);
        if (!current(change)) return;
        confirm(latest, change.role);
        confirmed = true;
        success = !!attempted && equal(normalize(latest.items), attempted);
      } catch { /* Retain the last confirmed navigation; never report an unconfirmed save as successful. */ }
      if (!current(change)) return;
      if (!success) {
        const values = remaining(change);
        if (Object.keys(values).length) {
          failed.push({ ...change, values });
          error.value = t(confirmed ? '侧边栏保存失败，已恢复已确认的入口。' : '侧边栏保存未确认，请重试。') + ' ' + (reason instanceof Error ? reason.message : String(reason));
        }
      }
    } finally {
      if (change.epoch === epoch) {
        pending.value = pending.value.filter(item => item !== change);
        saved.value = success && !pending.value.length && !error.value;
      }
    }
  }
  function enqueue(role: SidebarRole, values: Change['values']) {
    if (!options.canCustomize()) return;
    const change = { role, values, epoch, sequence: ++sequence };
    for (const id of Object.keys(values)) latestIntent.set(role + ':' + id, change.sequence);
    failed = failed.flatMap(item => {
      const active = remaining(item);
      return Object.keys(active).length ? [{ ...item, values: active }] : [];
    });
    if (!failed.length) error.value = '';
    pending.value = [...pending.value, change];
    saved.value = false;
    void withSettingsLock(() => persist(change));
  }
  function setSelected(role: SidebarRole, values: string[]) {
    const before = selected(role), next = normalize(values);
    const added = next.filter(id => !before.includes(id)), removed = before.filter(id => !next.includes(id));
    if (!added.length && !removed.length) return;
    enqueue(role, Object.fromEntries([...removed.map(id => [id, false]), ...added.map(id => [id, true])]));
  }
  function setPinned(id: string, pinned: boolean) {
    if (id === 'settings' || !options.available().includes(id)) return;
    const items = selected('admin');
    if (items.includes(id) === pinned) return;
    enqueue('admin', { [id]: pinned });
  }
  function retry() {
    const changes = failed.filter(change => change.epoch === epoch).map(change => ({ role: change.role, values: remaining(change) })).filter(change => Object.keys(change.values).length);
    failed = [];
    error.value = '';
    for (const change of changes) enqueue(change.role, change.values);
  }
  function endDrag() { releasePointer?.(); releasePointer = null; pointerPosition.value = null; dragging.value = null; target.value = null; }
  function startPointer(event: PointerEvent, id: string, source: DragTarget) {
    if (!options.canCustomize() || id === 'settings' || !options.available().includes(id) || event.button !== 0) return;
    event.preventDefault();
    endDrag();
    const origin = { x: event.clientX, y: event.clientY }, pointerID = event.pointerId, startedEpoch = epoch;
    const sourceElement = event.currentTarget as HTMLElement;
    sourceElement.setPointerCapture?.(pointerID);
    function destination(x: number, y: number): DragTarget | null {
      const element = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-sidebar-drop-target]');
      const value = element?.dataset.sidebarDropTarget;
      return element && !element.closest('[inert]') && (value === 'sidebar' || value === 'directory') && value !== source ? value : null;
    }
    function move(next: PointerEvent) {
      if (next.pointerId !== pointerID) return;
      if (startedEpoch !== epoch || !options.canCustomize()) { endDrag(); return; }
      if (!dragging.value && Math.hypot(next.clientX - origin.x, next.clientY - origin.y) < 6) return;
      next.preventDefault();
      dragging.value = { id, source };
      pointerPosition.value = { x: next.clientX, y: next.clientY };
      target.value = destination(next.clientX, next.clientY);
      const scrollable = document.elementFromPoint(next.clientX, next.clientY)?.closest<HTMLElement>('.grouped-nav');
      if (scrollable) {
        const box = scrollable.getBoundingClientRect();
        if (next.clientY < box.top + 35) scrollable.scrollTop -= 20;
        else if (next.clientY > box.bottom - 35) scrollable.scrollTop += 20;
      } else if (next.clientY < 45) window.scrollBy(0, -20);
      else if (next.clientY > window.innerHeight - 45) window.scrollBy(0, 20);
    }
    function finish(next: PointerEvent) {
      if (next.pointerId !== pointerID) return;
      const result = destination(next.clientX, next.clientY);
      if (dragging.value && result && startedEpoch === epoch && options.canCustomize()) setPinned(id, result === 'sidebar');
      endDrag();
    }
    function cancel(next?: Event) { if (!next || !('pointerId' in next) || (next as PointerEvent).pointerId === pointerID) endDrag(); }
    window.addEventListener('pointermove', move, { passive: false });
    window.addEventListener('pointerup', finish);
    window.addEventListener('pointercancel', cancel);
    window.addEventListener('blur', cancel);
    releasePointer = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', cancel);
      window.removeEventListener('blur', cancel);
      if (sourceElement.hasPointerCapture?.(pointerID)) sourceElement.releasePointerCapture(pointerID);
    };
  }
  function startDrag(event: DragEvent, id: string, source: DragTarget) {
    if (!options.canCustomize() || id === 'settings' || !options.available().includes(id) || !event.dataTransfer) { event.preventDefault(); return; }
    dragging.value = { id, source };
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('application/x-guangyue-navigation', id);
    event.dataTransfer.setData('text/plain', id);
  }
  function acceptDrag(event: DragEvent, destination: DragTarget) {
    if (!options.canCustomize() || !dragging.value || dragging.value.source === destination) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
    target.value = destination;
  }
  function drop(event: DragEvent, destination: DragTarget) {
    const move = dragging.value;
    if (!options.canCustomize() || !move || move.source === destination || event.dataTransfer?.getData('application/x-guangyue-navigation') !== move.id) return;
    event.preventDefault();
    event.stopPropagation();
    setPinned(move.id, destination === 'sidebar');
    endDrag();
  }
  const stopContext = watch(options.context, () => {
    epoch++;
    pending.value = [];
    saved.value = false;
    error.value = '';
    failed = [];
    latestIntent.clear();
    endDrag();
  }, { flush: 'sync' });
  onScopeDispose(() => { stopContext(); epoch++; pending.value = []; endDrag(); });
  return { selected, setSelected, setPinned, withSettingsLock, acceptSettings, retry, saving, saved, error, dragging, pointerPosition, target, directoryRequest, startPointer, startDrag, acceptDrag, drop, endDrag };
}
