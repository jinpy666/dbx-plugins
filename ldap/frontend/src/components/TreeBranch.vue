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

const props = withDefaults(
  defineProps<{
    node: DnTreeNode;
    depth: number;
    selectedDn: string;
    disabled?: boolean;
  }>(),
  { depth: 0 },
);

const emit = defineEmits<{
  (e: "toggle", node: DnTreeNode): void;
  (e: "select", node: DnTreeNode): void;
  (e: "menu", event: MouseEvent, dn: string): void;
  (e: "view", dn: string): void;
  (e: "loadMore", node: DnTreeNode): void;
}>();

function onToggle(event: MouseEvent) {
  event.stopPropagation();
  if (!props.disabled) emit("toggle", props.node);
}

function onSelect(event: MouseEvent) {
  event.stopPropagation();
  if (!props.disabled) emit("select", props.node);
}

// 双击直接打开条目（对齐 tiny-rdm 习惯；右键菜单的「查看/编辑」保留）。
function onView(event: MouseEvent) {
  event.stopPropagation();
  if (!props.disabled) emit("view", props.node.dn);
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
  <button class="tree-node" :title="node.dn" @click="onSelect" @dblclick="onView" @contextmenu="onMenu">
    <span class="tree-row" :class="{ selected: selectedDn === node.dn }" :style="{ paddingLeft: `${6 + depth * 14}px` }">
      <button class="tree-twist" :aria-expanded="node.expanded" @click.stop="onToggle" @dblclick.stop>
        <Loader2 v-if="node.loading" class="spinning" />
        <ChevronDown v-else-if="node.expanded && node.children.length > 0" />
        <ChevronRight v-else />
      </button>
      <span class="tree-label"><span class="tree-name">{{ node.label }}</span></span>
      <span v-if="node.loading" class="tree-badge tree-badge--loading" :title="t('tree.loading')">…</span>
      <button
        v-else-if="node.loaded && node.truncated"
        class="tree-badge tree-badge--truncated"
        :title="truncatedTitle"
        :aria-label="truncatedTitle"
        @click="onLoadMore"
      >{{ childBadgeText(node, node.children.length) }}</button>
      <span
        v-else-if="node.loaded && node.childCount"
        class="tree-badge"
        :title="t('tree.childCount', { count: node.childCount })"
      >{{ childBadgeText(node, node.children.length) }}</span>
    </span>
  </button>
</template>
