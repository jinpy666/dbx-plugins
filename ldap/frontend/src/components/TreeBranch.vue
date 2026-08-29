<script setup lang="ts">
// Recursive DN-tree branch renderer. Pure presentational: expansion/loading
// state lives in the node objects owned by DnTree.vue.
import { ChevronDown, ChevronRight, Loader2 } from "@lucide/vue";
import { t } from "../lib/i18n";
import type { DnTreeNode } from "../lib/dnTree";

const props = defineProps<{
  node: DnTreeNode;
  selectedDn: string;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: "toggle", node: DnTreeNode): void;
  (e: "select", node: DnTreeNode): void;
  (e: "menu", event: MouseEvent, dn: string): void;
  (e: "view", dn: string): void;
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
</script>

<template>
  <div class="tree-branch">
    <button class="tree-node" :title="node.dn" @click="onSelect" @dblclick="onView" @contextmenu="onMenu">
      <span class="tree-row" :class="{ selected: selectedDn === node.dn }">
        <button class="tree-twist" :aria-expanded="node.expanded" @click.stop="onToggle" @dblclick.stop>
          <Loader2 v-if="node.loading" class="spinning" />
          <ChevronDown v-else-if="node.expanded && node.children.length > 0" />
          <ChevronRight v-else />
        </button>
        <span class="tree-label"><span class="tree-name">{{ node.label }}</span></span>
        <span v-if="node.loading" class="tree-badge tree-badge--loading" :title="t('tree.loading')">…</span>
        <span
          v-else-if="node.loaded && node.childCount"
          class="tree-badge"
          :title="t('tree.childCount', { count: node.childCount })"
        >{{ node.childCount }}</span>
      </span>
    </button>
    <div v-if="node.expanded && node.children.length > 0" class="tree-children">
      <TreeBranch
        v-for="child in node.children"
        :key="child.dn"
        :node="child"
        :selected-dn="selectedDn"
        :disabled="disabled"
        @toggle="emit('toggle', $event)"
        @select="emit('select', $event)"
        @view="emit('view', $event)"
        @menu="(event, dn) => emit('menu', event, dn)"
      />
    </div>
  </div>
</template>
