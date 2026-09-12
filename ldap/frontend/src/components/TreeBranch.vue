<script setup lang="ts">
// Single-row DN-tree node renderer. DnTree.vue flattens the tree into visible
// rows and windows them through VirtualList; nesting depth arrives as a prop
// and becomes left padding (the old nested .tree-children indent).
// P1-2：懒加载被 sizeLimit 截断时，徽标显示"已加载数+"（绝不背书精确总数）
// 并作为"加载更多"入口（点击经 DnTree.loadMore 续载一页）。
import { computed, ref } from "vue";
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
const rowElement = ref<HTMLElement>();

function onToggle(event: MouseEvent | KeyboardEvent) {
  event.stopPropagation();
  if (!props.disabled) emit("toggle", props.node);
}

function onSelect(event: MouseEvent | KeyboardEvent) {
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
  if (props.disabled || props.node.loading) return;
  // The badge disappears during loading; retain focus on the owning row.
  rowElement.value?.focus({ preventScroll: true });
  emit("loadMore", props.node);
}

function onKeydown(event: KeyboardEvent) {
  if (event.target !== event.currentTarget) return;
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    onSelect(event);
    return;
  }
  if (event.key !== "ArrowRight" && event.key !== "ArrowLeft") return;
  event.preventDefault();
  if (props.disabled || props.node.loading) {
    event.stopPropagation();
    return;
  }
  if (event.key === "ArrowRight" && !props.node.expanded && (!props.node.loaded || props.node.children.length > 0)) onToggle(event);
  else if (event.key === "ArrowLeft" && props.node.expanded && props.node.children.length > 0) onToggle(event);
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
  <!-- A focusable treeitem can contain native action buttons without nesting
       buttons. The load-more action is also reachable through Tab. -->
  <div
    ref="rowElement"
    class="tree-node"
    role="treeitem"
    :tabindex="disabled ? -1 : 0"
    :aria-level="depth + 1"
    :aria-selected="selectedDn === node.dn"
    :aria-expanded="!node.loaded || node.children.length > 0 ? node.expanded : undefined"
    :aria-busy="node.loading"
    :aria-disabled="disabled || undefined"
    :title="node.dn"
    @click="onSelect"
    @dblclick="onToggle"
    @contextmenu="onMenu"
    @keydown="onKeydown"
  >
    <span class="tree-row" :class="{ selected: selectedDn === node.dn }" :style="{ paddingLeft: `${6 + depth * 14}px` }">
      <button
        class="tree-twist"
        type="button"
        tabindex="-1"
        :disabled="disabled || node.loading"
        :aria-expanded="node.expanded"
        :aria-label="node.expanded ? t('tree.collapse') : t('tree.expand')"
        @click.stop="onToggle"
        @dblclick.stop
      >
        <Loader2 v-if="node.loading" class="spinning" />
        <ChevronDown v-else-if="node.expanded && node.children.length > 0" />
        <ChevronRight v-else />
      </button>
      <span class="tree-label">
        <TreeNodeIcon :dn="node.dn" :base-dn="baseDn" :expanded="node.expanded" />
        <span class="tree-name">{{ node.label }}</span>
      </span>
      <span v-if="node.loading" class="tree-badge tree-badge--loading" :title="t('tree.loading')">…</span>
      <button
        v-else-if="node.loaded && node.truncated"
        class="tree-badge tree-badge--truncated"
        type="button"
        :disabled="disabled"
        :title="truncatedTitle"
        :aria-label="truncatedTitle"
        @click="onLoadMore"
        @dblclick.stop
      >{{ childBadgeText(node, node.children.length) }}</button>
      <span
        v-else-if="node.loaded && node.childCount"
        class="tree-badge"
        :title="t('tree.childCount', { count: node.childCount })"
      >{{ childBadgeText(node, node.children.length) }}</span>
    </span>
  </div>
</template>
