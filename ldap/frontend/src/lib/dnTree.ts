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
