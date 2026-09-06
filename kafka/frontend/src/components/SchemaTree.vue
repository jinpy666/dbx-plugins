<script setup lang="ts">
// F5 树视图递归节点：AVRO / JSON Schema 详情树（record/array/union/类型/默认值，
// 可折叠）。折叠态本地内存（不持久化——详情树随选中 schema 重建，记忆无意义）。
import { computed, ref } from "vue";
import { ChevronDown } from "@lucide/vue";
import type { SchemaTreeNode } from "../lib/kafkaModel";

const props = defineProps<{
  node: SchemaTreeNode;
  /** 默认展开层级：根层展开、更深层默认折叠（长 schema 不至于一次性全开）。 */
  defaultExpanded?: boolean;
}>();

const hasChildren = computed(() => (props.node.children?.length ?? 0) > 0);
const expanded = ref(props.defaultExpanded === true);

function toggle() {
  if (hasChildren.value) expanded.value = !expanded.value;
}
</script>

<template>
  <div class="schema-tree-node">
    <div class="schema-tree-row">
      <button
        v-if="hasChildren"
        type="button"
        class="schema-tree-toggle"
        :aria-expanded="expanded"
        :title="expanded ? undefined : node.type"
        @click="toggle"
      >
        <ChevronDown class="chev" :class="{ folded: !expanded }" aria-hidden="true" />
      </button>
      <span v-else class="schema-tree-toggle schema-tree-toggle--leaf" />
      <span class="schema-tree-name mono-s">{{ node.name }}</span>
      <span class="schema-tree-type mono-s">{{ node.type }}</span>
      <span v-if="node.defaultValue !== undefined" class="schema-tree-default mono-s" :title="String(node.defaultValue)">
        = {{ node.defaultValue }}
      </span>
    </div>
    <div v-if="hasChildren && expanded" class="schema-tree-children">
      <SchemaTree v-for="child in node.children" :key="child.name" :node="child" />
    </div>
  </div>
</template>

<style scoped>
.schema-tree-node {
  min-width: 0;
}
.schema-tree-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
  padding: 1px 2px;
}
.schema-tree-toggle {
  display: inline-flex;
  flex: 0 0 16px;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--muted-foreground);
  cursor: pointer;
}
.schema-tree-toggle--leaf {
  cursor: default;
}
.schema-tree-toggle svg {
  width: 12px;
  height: 12px;
}
.chev {
  transition: transform 0.12s ease;
}
.chev.folded {
  transform: rotate(-90deg);
}
.schema-tree-name {
  flex: 0 0 auto;
  max-width: 40%;
  overflow: hidden;
  color: var(--foreground);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.schema-tree-type {
  overflow: hidden;
  color: var(--primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.schema-tree-default {
  flex: 1 1 auto;
  overflow: hidden;
  color: var(--muted-foreground);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.schema-tree-children {
  margin-left: 14px;
  border-left: 1px solid color-mix(in srgb, var(--border) 60%, transparent);
  padding-left: 6px;
}
</style>
