import { describe, expect, it } from "vitest";
import { childBadgeText, flattenDnTree, isFetchTruncated, nextFetchLimit, nextTreeFocusIndex, TREE_FETCH_PAGE, type DnTreeNode } from "./dnTree";

// Node factory kept local so the spec stays free of component imports.
function node(dn: string, overrides: Partial<DnTreeNode> = {}): DnTreeNode {
  return { dn, label: dn, expanded: false, loaded: false, loading: false, children: [], ...overrides };
}

describe("flattenDnTree", () => {
  it("returns an empty list for a missing root", () => {
    expect(flattenDnTree(undefined)).toEqual([]);
  });

  it("renders a collapsed root as a single depth-0 row", () => {
    const root = node("dc=demo,dc=dbx");
    expect(flattenDnTree(root)).toEqual([{ node: root, depth: 0 }]);
  });

  it("lists expanded children at depth 1 in order", () => {
    const a = node("ou=a,dc=demo,dc=dbx");
    const b = node("ou=b,dc=demo,dc=dbx");
    const root = node("dc=demo,dc=dbx", { expanded: true, children: [a, b] });
    expect(flattenDnTree(root).map((row) => [row.node.dn, row.depth])).toEqual([
      ["dc=demo,dc=dbx", 0],
      ["ou=a,dc=demo,dc=dbx", 1],
      ["ou=b,dc=demo,dc=dbx", 1],
    ]);
  });

  it("descends only through expanded ancestors (multi-level)", () => {
    const leaf = node("uid=x,ou=a,dc=demo,dc=dbx");
    const hiddenLeaf = node("uid=y,ou=b,dc=demo,dc=dbx");
    const a = node("ou=a,dc=demo,dc=dbx", { expanded: true, children: [leaf] });
    const b = node("ou=b,dc=demo,dc=dbx", { expanded: true, children: [hiddenLeaf] });
    const root = node("dc=demo,dc=dbx", { expanded: true, children: [a, b] });
    // ou=b is expanded but sits under a collapsed root: never projected.
    root.expanded = false;
    expect(flattenDnTree(root)).toHaveLength(1);
    root.expanded = true;
    expect(flattenDnTree(root).map((row) => [row.node.dn, row.depth])).toEqual([
      ["dc=demo,dc=dbx", 0],
      ["ou=a,dc=demo,dc=dbx", 1],
      ["uid=x,ou=a,dc=demo,dc=dbx", 2],
      ["ou=b,dc=demo,dc=dbx", 1],
      ["uid=y,ou=b,dc=demo,dc=dbx", 2],
    ]);
  });

  it("does not emit phantom rows for an expanded node without children", () => {
    const root = node("dc=demo,dc=dbx", { expanded: true, children: [] });
    expect(flattenDnTree(root)).toEqual([{ node: root, depth: 0 }]);
  });
});

describe("lazy-load truncation helpers (P1-2)", () => {
  it("uses a 500-entry page shared with fetchChildren sizeLimit", () => {
    expect(TREE_FETCH_PAGE).toBe(500);
  });

  it("flags a fetch as truncated once the requested page limit is reached", () => {
    expect(isFetchTruncated(500)).toBe(true);
    expect(isFetchTruncated(501)).toBe(true);
    expect(isFetchTruncated(499)).toBe(false);
    expect(isFetchTruncated(3, 3)).toBe(true);
  });

  it("steps the next load-more limit by one page over the loaded count", () => {
    expect(nextFetchLimit(500)).toBe(1000);
    expect(nextFetchLimit(1000)).toBe(1500);
    expect(nextFetchLimit(120, 40)).toBe(160);
  });

  it("never endorses the exact total while truncated: badge shows loaded count + '+'", () => {
    // ldap/count 回了精确 1000、实际只加载 500：徽标必须如实反映截断。
    expect(childBadgeText({ truncated: true, childCount: 1000 }, 500)).toBe("500+");
    expect(childBadgeText({ truncated: true }, 500)).toBe("500+");
  });

  it("keeps the exact-count badge once fully loaded (count falls back to loaded)", () => {
    expect(childBadgeText({ truncated: false, childCount: 1000 }, 1000)).toBe("1000");
    expect(childBadgeText({ truncated: false }, 7)).toBe("7");
  });
});

describe("tree keyboard navigation (P2-10)", () => {
  it("moves to the adjacent visible row in the pressed direction", () => {
    expect(nextTreeFocusIndex(4, 0, "ArrowDown")).toBe(1);
    expect(nextTreeFocusIndex(4, 2, "ArrowDown")).toBe(3);
    expect(nextTreeFocusIndex(4, 2, "ArrowUp")).toBe(1);
  });

  it("clamps at the visible edges instead of wrapping into unmounted rows", () => {
    expect(nextTreeFocusIndex(4, 3, "ArrowDown")).toBe(3);
    expect(nextTreeFocusIndex(4, 0, "ArrowUp")).toBe(0);
  });

  it("enters at the first/last visible row when focus is outside the tree", () => {
    expect(nextTreeFocusIndex(4, -1, "ArrowDown")).toBe(0);
    expect(nextTreeFocusIndex(4, -1, "ArrowUp")).toBe(3);
  });

  it("does not take over other keys or an empty tree", () => {
    expect(nextTreeFocusIndex(4, 1, "Enter")).toBe(-1);
    expect(nextTreeFocusIndex(4, 1, "Tab")).toBe(-1);
    expect(nextTreeFocusIndex(0, -1, "ArrowDown")).toBe(-1);
  });
});
