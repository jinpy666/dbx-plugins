import { describe, expect, it } from "vitest";
import { childBadgeText, compareDnByLabel, compareDnForTree, dnNodeKind, flattenDnTree, isFetchTruncated, nextFetchLimit, nextTreeFocusIndex, TREE_FETCH_PAGE, type DnTreeNode } from "./dnTree";

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

describe("dnNodeKind (tree kind icons)", () => {
  it("matches the base DN (case-insensitive) as root", () => {
    expect(dnNodeKind("DC=Demo,DC=DBX", "dc=demo,dc=dbx")).toBe("root");
    expect(dnNodeKind("dc=demo,dc=dbx", "dc=demo,dc=dbx")).toBe("root");
    // 非 root 节点照常按 RDN 类型归类
    expect(dnNodeKind("ou=people,dc=demo,dc=dbx", "dc=demo,dc=dbx")).toBe("ou");
  });

  it("classifies by the first RDN attribute type (case-insensitive)", () => {
    expect(dnNodeKind("dc=example,dc=com")).toBe("dc");
    expect(dnNodeKind("OU=People,dc=example,dc=com")).toBe("ou");
    expect(dnNodeKind("cn=alice,dc=example,dc=com")).toBe("cn");
    expect(dnNodeKind("UID=bob,ou=people,dc=example,dc=com")).toBe("uid");
    expect(dnNodeKind("o=acme")).toBe("o");
  });

  it("falls back to other for unknown RDN types / malformed DNs", () => {
    expect(dnNodeKind("c=CN,o=acme")).toBe("other");
    expect(dnNodeKind("l=Beijing,c=CN")).toBe("other");
    expect(dnNodeKind("noequalsign")).toBe("other");
    expect(dnNodeKind("")).toBe("other");
  });
});

describe("compareDnByLabel (filter-result alphabetical sort)", () => {
  const sortDns = (dns: string[]) => [...dns].sort(compareDnByLabel);

  it("sorts by the RDN label case-insensitively", () => {
    expect(sortDns(["cn=bob,dc=x", "cn=Alice,dc=x", "cn=carol,dc=x"])).toEqual([
      "cn=Alice,dc=x",
      "cn=bob,dc=x",
      "cn=carol,dc=x",
    ]);
  });

  it("is numeric-aware: user2 sorts before user10", () => {
    expect(sortDns(["uid=user10,dc=x", "uid=user2,dc=x", "uid=user1,dc=x"])).toEqual([
      "uid=user1,dc=x",
      "uid=user2,dc=x",
      "uid=user10,dc=x",
    ]);
  });

  it("breaks label ties by the full DN for stable output", () => {
    expect(sortDns(["cn=ali,ou=b,dc=x", "cn=ali,ou=a,dc=x"])).toEqual([
      "cn=ali,ou=a,dc=x",
      "cn=ali,ou=b,dc=x",
    ]);
  });
});

describe("compareDnForTree (tree sibling sort: containers before leaves)", () => {
  const sortDns = (dns: string[]) => [...dns].sort(compareDnForTree);

  it("puts ou entries before cn entries regardless of alphabetical order", () => {
    // 字典序下 cn 本会排在 ou 前（"c" < "o"）：分组后容器必须整体前移。
    expect(sortDns(["cn=admins,dc=x", "ou=people,dc=x", "cn=alice,dc=x", "ou=groups,dc=x"])).toEqual([
      "ou=groups,dc=x",
      "ou=people,dc=x",
      "cn=admins,dc=x",
      "cn=alice,dc=x",
    ]);
  });

  it("keeps the existing alphabetical order inside each group", () => {
    expect(sortDns(["cn=zoe,dc=x", "cn=alice,dc=x", "ou=b,dc=x", "ou=a,dc=x"])).toEqual([
      "ou=a,dc=x",
      "ou=b,dc=x",
      "cn=alice,dc=x",
      "cn=zoe,dc=x",
    ]);
  });

  it("sorts other RDN types (o/uid etc.) after both groups", () => {
    expect(sortDns(["uid=u1,dc=x", "cn=alice,dc=x", "o=acme", "ou=people,dc=x"])).toEqual([
      "ou=people,dc=x",
      "cn=alice,dc=x",
      "o=acme",
      "uid=u1,dc=x",
    ]);
  });

  it("matches the RDN type case-insensitively", () => {
    expect(sortDns(["cn=a,dc=x", "OU=people,dc=x"])).toEqual(["OU=people,dc=x", "cn=a,dc=x"]);
  });

  it("stays numeric-aware inside a group", () => {
    expect(sortDns(["ou=team10,dc=x", "ou=team2,dc=x"])).toEqual([
      "ou=team2,dc=x",
      "ou=team10,dc=x",
    ]);
  });
});
