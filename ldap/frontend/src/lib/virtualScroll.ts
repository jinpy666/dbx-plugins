// Zero-dependency virtual scrolling math (fixed row height).
// The Vue side (VirtualList.vue) renders only rows inside [start, end);
// this module keeps the clamping rules pure and unit-testable.

export interface VirtualWindow {
  /** Index of the first rendered row (inclusive, clamped to [0, totalCount]). */
  start: number;
  /** Index after the last rendered row (inclusive-bound exclusive). */
  end: number;
}

/**
 * Compute the visible row window for a fixed-row-height list.
 *
 * - `overscan` rows are rendered above and below the viewport so slow scrolls
 *   do not flash empty space.
 * - Degenerate inputs (empty list, non-positive row height / viewport) return
 *   an empty window.
 * - Scroll positions past the end of the content clamp to the list bounds,
 *   never producing a start > end window.
 */
export function computeWindow(
  scrollTop: number,
  viewportHeight: number,
  totalCount: number,
  rowHeight: number,
  overscan = 8,
): VirtualWindow {
  if (totalCount <= 0 || rowHeight <= 0 || viewportHeight <= 0) {
    return { start: 0, end: 0 };
  }
  const rawStart = Math.floor(scrollTop / rowHeight) - overscan;
  const rawEnd = Math.ceil((scrollTop + viewportHeight) / rowHeight) + overscan;
  const start = Math.max(0, Math.min(rawStart, totalCount));
  const end = Math.max(start, Math.min(rawEnd, totalCount));
  return { start, end };
}
