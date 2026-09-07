<script setup lang="ts">
// Single-row DN-tree node renderer. DnTree.vue flattens the tree into visible
// rows and windows them through VirtualList; nesting depth arrives as a prop
// and becomes left padding (the old nested .tree-children indent).
// P1-2：懒加载被 sizeLimit 截断时，徽标显示"已加载数+"（绝不背书精确总数）
// 并作为"加载更多"入口（点击经 DnTree.loadMore 续载一页）。
import { computed } from "vue";
import { ChevronDown, ChevronRight, Loader2 } from "@lucide/vue";
import { t } from "../lib/i18n";
import { childBadgeText, type DnTreeNode } from "../lib/dnTree";
import TreeNodeIcon from "./TreeNodeIcon.vue";

const props = withDefaults(
  defineProps<{
    node: DnTreeNode;
    depth: number;
    selectedDn: string;
    baseDn?: string;
    disabled?: boolean;
  }>(),
  { baseDn: "", depth: 0 },
);

const emit = defineEmits<{
  (e: "toggle", node: DnTreeNode): void;
  (e: "select", node: DnTreeNode): void;
  (e: "menu", event: MouseEvent, dn: string): void;
  (e: "loadMore", node: DnTreeNode): void;
}>();

function onToggle(event: MouseEvent | KeyboardEvent) {
  event.stopPropagation();
  if (!props.disabled) emit("toggle", props.node);
}

function onSelect(event: MouseEvent) {
  event.stopPropagation();
  if (!props.disabled) emit("select", props.node);
}

function onMenu(event: MouseEvent) {
  event.preventDefault();
  event.stopPropagation();
  emit("menu", event, props.node.dn);
}

function onLoadMore(event: MouseEvent) {
  event.stopPropagation();
  if (!props.disabled) emit("loadMore", props.node);
}

// 截断徽标悬停提示：count 已回且与已加载数不一致时给出 "x / y" 全貌，
// 否则只承诺"已加载前 n 条（已截断）"。
const truncatedTitle = computed(() => {
  const loaded = props.node.children.length;
  if (props.node.childCount && props.node.childCount !== loaded) {
    return t("tree.childCountTruncated", { loaded, total: props.node.childCount });
  }
  return t("tree.childCountTruncatedUnknown", { loaded });
});
</script>

<template>
  <!-- P2-18/P2-19：外层节点带 treeitem 语义；twisty 与截断徽标降为
       span[role=button]（button 内不再嵌套 button，twisty 退出 Tab 序，
       树内导航仍走 DnTree 的 ↑/↓ roving 焦点）。 -->
  <button
    class="tree-node"
    role="treeitem"
    :aria-level="depth + 1"
    :aria-selected="selectedDn === node.dn"
    :aria-expanded="node.loaded && node.children.length > 0 ? node.expanded : undefined"
    :title="node.dn"
    @click="onSelect"
    @dblclick="onToggle"
    @contextmenu="onMenu"
  >
    <span class="tree-row" :class="{ selected: selectedDn === node.dn }" :style="{ paddingLeft: `${6 + depth * 14}px` }">
      <span
        class="tree-twist"
        role="button"
        tabindex="-1"
        :aria-expanded="node.expanded"
        :aria-label="node.expanded ? t('tree.collapse') : t('tree.expand')"
        @click.stop="onToggle"
        @dblclick.stop
        @keydown.enter.prevent="onToggle($event)"
        @keydown.space.prevent="onToggle($event)"
      >
        <Loader2 v-if="node.loading" class="spinning" />
        <ChevronDown v-else-if="node.expanded && node.children.length > 0" />
        <ChevronRight v-else />
      </span>
      <span class="tree-label">
        <TreeNodeIcon :dn="node.dn" :base-dn="baseDn" :expanded="node.expanded" />
        <span class="tree-name">{{ node.label }}</span>
      </span>
      <span v-if="node.loading" class="tree-badge tree-badge--loading" :title="t('tree.loading')">…</span>
      <span
        v-else-if="node.loaded && node.truncated"
        class="tree-badge tree-badge--truncated"
        role="button"
        tabindex="-1"
        :title="truncatedTitle"
        :aria-label="truncatedTitle"
        @click="onLoadMore"
      >{{ childBadgeText(node, node.children.length) }}</span>
      <span
        v-else-if="node.loaded && node.childCount"
        class="tree-badge"
        :title="t('tree.childCount', { count: node.childCount })"
      >{{ childBadgeText(node, node.children.length) }}</span>
    </span>
  </button>
</template>
