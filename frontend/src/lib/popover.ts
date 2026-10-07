export interface PopoverViewport { left: number; top: number; width: number; height: number }

/** Clamp against the visible viewport, including the on-screen keyboard. */
export function placePopover(viewport: PopoverViewport, anchor: { left: number; bottom: number } | undefined, contentHeight: number, compact: boolean) {
  const margin = Math.min(12, viewport.width / 4, viewport.height / 4);
  const width = Math.min(compact ? 360 : 336, viewport.width - margin * 2);
  const maxHeight = Math.max(0, viewport.height - margin * 2);
  const height = Math.min(Math.max(contentHeight, 0), maxHeight);
  const left = compact || !anchor ? viewport.left + (viewport.width - width) / 2 : Math.max(viewport.left + margin, Math.min(anchor.left, viewport.left + viewport.width - width - margin));
  const preferredTop = compact || !anchor ? viewport.top + (viewport.height - height) / 2 : anchor.bottom + 8;
  const top = Math.max(viewport.top + margin, Math.min(preferredTop, viewport.top + viewport.height - height - margin));
  return { left, top, width, maxHeight };
}
