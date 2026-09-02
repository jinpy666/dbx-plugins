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
