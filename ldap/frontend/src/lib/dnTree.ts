// Shared DN-tree node model (plain data so both DnTree.vue and TreeBranch.vue
// can import the type without a circular SFC reference).
import { splitFirstDnRdn } from "./dn";

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

/** 树节点图标种类：root 按 baseDn 命中判定，其余按首个 RDN 属性类型区分。 */
export type DnNodeKind = "root" | "dc" | "ou" | "cn" | "uid" | "o" | "other";

/**
 * 节点种类判定（图标选择用）：`baseDn` 命中即 root；否则取首个 RDN 的属性
 * 类型（多值 RDN 取第一段），识别 dc/ou/cn/uid/o，未识别回落 other。
 * 只看 DN 本身、不依赖 objectClass——树懒加载只取 dn 列，判定必须零请求。
 */
export function dnNodeKind(dn: string, baseDn = ""): DnNodeKind {
  const normalized = dn.trim().toLowerCase();
  if (baseDn && normalized && normalized === baseDn.trim().toLowerCase()) return "root";
  const rdn = splitFirstDnRdn(dn).rdn;
  const separatorIndex = rdn.indexOf("=");
  if (separatorIndex <= 0) return "other";
  const type = rdn.slice(0, separatorIndex).trim().toLowerCase();
  if (type === "dc" || type === "ou" || type === "cn" || type === "uid" || type === "o") return type;
  return "other";
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
 * Keyboard target in the full flattened row set, including unmounted rows.
 * Arrow keys clamp at the ends; Home/End jump to the first/last row.
 */
export function nextTreeFocusIndex(count: number, currentIndex: number, key: string): number {
  if (count <= 0) return -1;
  if (key === "Home") return 0;
  if (key === "End") return count - 1;
  if (key !== "ArrowDown" && key !== "ArrowUp") return -1;
  if (currentIndex < 0 || currentIndex >= count) return key === "ArrowDown" ? 0 : count - 1;
  return Math.min(Math.max(currentIndex + (key === "ArrowDown" ? 1 : -1), 0), count - 1);
}

/**
 * 树过滤结果的字母排序比较器：按 RDN 标签排序（大小写不敏感、数字感知，
 * "user2" 排在 "user10" 前），同标签按全 DN 稳定收尾。服务器返回序本身无
 * 保障（OpenLDAP/AD 都不承诺顺序），字母序是可预期的浏览序。
 */
export function compareDnByLabel(left: string, right: string): number {
  const byLabel = splitFirstDnRdn(left)
    .rdn.localeCompare(splitFirstDnRdn(right).rdn, undefined, { sensitivity: "base", numeric: true });
  return byLabel !== 0 ? byLabel : left.localeCompare(right, undefined, { numeric: true });
}

/**
 * 树同级子条目排序比较器：容器优先于叶子——按首个 RDN 属性类型分组，
 * ou 组最前、cn 组其次、其余类型（dc/o/uid 及未识别类型）排最后；组内沿用
 * 原全 DN 字典序（numeric 感知），相对次序不变。服务器返回序无保障，
 * 组织单元先于普通条目更贴合"先看结构、再查成员"的树浏览习惯。
 */
export function compareDnForTree(left: string, right: string): number {
  const groupRank = (dn: string): number => {
    const rdn = splitFirstDnRdn(dn).rdn;
    const separatorIndex = rdn.indexOf("=");
    const type = separatorIndex > 0 ? rdn.slice(0, separatorIndex).trim().toLowerCase() : "";
    if (type === "ou") return 0;
    if (type === "cn") return 1;
    return 2;
  };
  const byGroup = groupRank(left) - groupRank(right);
  return byGroup !== 0 ? byGroup : left.localeCompare(right, undefined, { numeric: true });
}
