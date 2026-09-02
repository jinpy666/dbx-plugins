import { describe, expect, it } from "vitest";
import { computeWindow } from "./virtualScroll";

describe("computeWindow", () => {
  it("returns an empty window for an empty list", () => {
    expect(computeWindow(0, 300, 0, 28)).toEqual({ start: 0, end: 0 });
  });

  it("guards against non-positive row height or viewport", () => {
    expect(computeWindow(0, 300, 100, 0)).toEqual({ start: 0, end: 0 });
    expect(computeWindow(0, 0, 100, 28)).toEqual({ start: 0, end: 0 });
    expect(computeWindow(0, -50, 100, 28)).toEqual({ start: 0, end: 0 });
  });

  it("renders viewport plus overscan at the top of the list", () => {
    // vh 200 / rh 28 → 8 visible rows; at the top the above-overscan clamps
    // away, so only the 8 rows below are added: end = 16 (exclusive).
    expect(computeWindow(0, 200, 1000, 28, 8)).toEqual({ start: 0, end: 16 });
  });

  it("keeps the window over rows scrolled through the middle", () => {
    // scrollTop 5600 = exactly 200 rows; window starts 8 rows earlier.
    expect(computeWindow(5600, 200, 1000, 28, 8)).toEqual({ start: 192, end: 216 });
  });

  it("never starts before row zero when overscanning the top", () => {
    const { start } = computeWindow(50, 200, 1000, 28, 8);
    expect(start).toBe(0);
  });

  it("clamps to the last row at the bottom of the list", () => {
    const { start, end } = computeWindow(1000 * 28, 200, 1000, 28, 8);
    expect(end).toBe(1000);
    expect(start).toBeLessThanOrEqual(end);
  });

  it("clamps overscroll past the end without producing an inverted window", () => {
    const { start, end } = computeWindow(999_999, 200, 1000, 28, 8);
    expect(start).toBe(1000);
    expect(end).toBe(1000);
  });

  it("renders the whole list when the viewport is taller than the content", () => {
    expect(computeWindow(0, 300, 5, 28, 8)).toEqual({ start: 0, end: 5 });
  });

  it("covers every row intersecting the viewport (no holes)", () => {
    const scrollTop = 137; // non-aligned offset on purpose
    const viewportHeight = 243;
    const { start, end } = computeWindow(scrollTop, viewportHeight, 1000, 28, 8);
    const firstVisible = Math.floor(scrollTop / 28);
    const lastVisible = Math.ceil((scrollTop + viewportHeight) / 28) - 1;
    expect(start).toBeLessThanOrEqual(firstVisible);
    expect(end).toBeGreaterThan(lastVisible);
  });
});
