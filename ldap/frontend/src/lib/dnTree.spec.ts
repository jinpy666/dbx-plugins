import { describe, expect, it } from "vitest";
import { flattenDnTree, type DnTreeNode } from "./dnTree";

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
