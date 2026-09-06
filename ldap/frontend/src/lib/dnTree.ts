// Shared DN-tree node model (plain data so both DnTree.vue and TreeBranch.vue
// can import the type without a circular SFC reference).
export interface DnTreeNode {
  dn: string;
  label: string;
  expanded: boolean;
  loaded: boolean;
  loading: boolean;
  children: DnTreeNode[];
  /** Loaded direct-child count (scope=one search result length, may hit sizeLimit). */
  childCount?: number;
  /** 本轮懒加载被 sizeLimit 截断（还有未加载的子条目），徽标不得背书精确总数。 */
  truncated?: boolean;
}

/**
 * 懒加载单页抓取上限。DnTree.vue 的 fetchChildren sizeLimit/pageSize 与
 * "加载更多"的步长都取这个常量，截断判定（isFetchTruncated）与之配套。
 */
export const TREE_FETCH_PAGE = 500;

/** fetchChildren 截断判定：本次返回条数达到请求上限即视为"可能还有更多"
 *  （UI 扫描 P1-2：静默截断必须可见化，徽标不得在截断时背书精确总数）。 */
export function isFetchTruncated(fetchedCount: number, page: number = TREE_FETCH_PAGE): boolean {
  return fetchedCount >= page;
}

/** "加载更多"的下次抓取上限：在已加载数上再追加一页。 */
export function nextFetchLimit(loadedCount: number, page: number = TREE_FETCH_PAGE): number {
  return loadedCount + page;
}

/**
 * 子条目徽标文案：截断时只显示"已加载数+"（不背书完整性，精确总数放在
 * title 提示里）；未截断显示精确计数（ldap/count 刷新值，缺省回退已加载数）。
 */
export function childBadgeText(
  node: Pick<DnTreeNode, "truncated" | "childCount">,
  loadedCount: number,
): string {
  if (node.truncated) return `${loadedCount}+`;
  return String(node.childCount ?? loadedCount);
}

/** One visible row of the flattened tree (display order, ancestors expanded). */
export interface DnTreeRow {
  node: DnTreeNode;
  depth: number;
}

/**
 * Flatten the DN tree into the list of currently visible rows, in display
 * order. Virtual scrolling renders a window over this flat list instead of
 * recursing through nested components, so a node with hundreds of children
 * only contributes DOM for the rows on screen. Expansion state stays in the
 * node objects owned by DnTree.vue — this function is a pure projection.
 */
export function flattenDnTree(root?: DnTreeNode): DnTreeRow[] {
  const rows: DnTreeRow[] = [];
  const walk = (node: DnTreeNode, depth: number) => {
    rows.push({ node, depth });
    if (node.expanded) {
      for (const child of node.children) walk(child, depth + 1);
    }
  };
  if (root) walk(root, 0);
  return rows;
}

/**
 * 目录树 ↑/↓ 键盘导航的目标行下标（UI 扫描 P2-10：树此前只能 Tab 逐节点走，
 * 虚拟滚动下未挂载行永远不可达）。在"当前可见的 .tree-node 行"集合上做
 * roving 移动：ArrowDown/ArrowUp 返回相邻行下标（无焦点时从首/尾行进入），
 * 越界钳制在当前可见范围；非方向键返回 -1（不接管）。
 */
export function nextTreeFocusIndex(count: number, currentIndex: number, key: string): number {
  if (count <= 0 || (key !== "ArrowDown" && key !== "ArrowUp")) return -1;
  if (currentIndex < 0 || currentIndex >= count) return key === "ArrowDown" ? 0 : count - 1;
  return Math.min(Math.max(currentIndex + (key === "ArrowDown" ? 1 : -1), 0), count - 1);
}
