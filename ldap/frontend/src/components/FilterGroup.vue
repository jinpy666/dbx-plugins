<script setup lang="ts">
// 递归条件组渲染器（对标 tiny-rdm 可视化查询构建器）：AND/OR 切换、
// 条件行（属性 + 运算符 + 值）、嵌套组增删。节点对象由 SearchForm.vue 持有
// （ref 深层响应式），此处直接原地变更以保持实现轻量（无外部状态库）。
// 层级限制：根组 depth=0，组内嵌组 depth=1 为最大（≤2 层嵌套）。
import { Plus, X } from "@lucide/vue";
import { createBuilderClause, createBuilderGroup, type BuilderClause, type BuilderGroup, type BuilderOp } from "../lib/ldapFilter";
import { t } from "../lib/i18n";

const props = defineProps<{
  group: BuilderGroup;
  depth: number;
  disabled?: boolean;
  listId: string;
}>();

const MAX_DEPTH = 1;

const operatorOptions: Array<{ value: BuilderOp; labelKey: string }> = [
  { value: "equals", labelKey: "search.opEquals" },
  { value: "notEquals", labelKey: "search.opNotEquals" },
  { value: "contains", labelKey: "search.opContains" },
  { value: "startsWith", labelKey: "search.opStartsWith" },
  { value: "endsWith", labelKey: "search.opEndsWith" },
  { value: "present", labelKey: "search.opPresent" },
  { value: "gte", labelKey: "search.opGte" },
  { value: "lte", labelKey: "search.opLte" },
  { value: "approx", labelKey: "search.opApprox" },
];

function setJoin(join: "and" | "or") {
  if (!props.disabled) props.group.join = join;
}

function addClause() {
  if (!props.disabled) props.group.children.push(createBuilderClause());
}

function addGroup() {
  if (!props.disabled && props.depth < MAX_DEPTH) {
    props.group.children.push(createBuilderGroup({ children: [createBuilderClause()] }));
  }
}

function removeChild(index: number) {
  if (!props.disabled) props.group.children.splice(index, 1);
}

function isClause(node: BuilderGroup["children"][number]): node is BuilderClause {
  return node.kind === "clause";
}
</script>

<template>
  <div class="qb-group" :class="{ 'qb-group--nested': depth > 0 }" data-qa="filter-group">
    <div class="qb-group-head">
      <span class="qb-join">
        <button
          type="button"
          :class="{ 'is-active': group.join === 'and' }"
          :disabled="disabled"
          :title="t('search.builderJoinAnd')"
          @click="setJoin('and')"
        >AND</button>
        <button
          type="button"
          :class="{ 'is-active': group.join === 'or' }"
          :disabled="disabled"
          :title="t('search.builderJoinOr')"
          @click="setJoin('or')"
        >OR</button>
      </span>
      <span class="qb-actions">
        <button type="button" class="qb-add" :disabled="disabled" :title="t('search.builderAddRow')" @click="addClause">
          <Plus aria-hidden="true" /><span>{{ t("search.builderAddRow") }}</span>
        </button>
        <button
          v-if="depth < MAX_DEPTH"
          type="button"
          class="qb-add"
          :disabled="disabled"
          :title="t('search.builderAddGroup')"
          @click="addGroup"
        >
          <Plus aria-hidden="true" /><span>{{ t("search.builderAddGroup") }}</span>
        </button>
      </span>
    </div>
    <div v-if="group.children.length === 0" class="qb-empty">{{ t("search.builderEmptyGroup") }}</div>
    <div v-for="(node, index) in group.children" :key="node.id" class="qb-node">
      <template v-if="isClause(node)">
        <input
          v-model="node.attribute"
          type="text"
          class="mono qb-attr"
          :list="listId"
          :placeholder="t('search.builderAttrPlaceholder')"
          :disabled="disabled"
          spellcheck="false"
        />
        <select v-model="node.op" :disabled="disabled">
          <option v-for="option in operatorOptions" :key="option.value" :value="option.value">{{ t(option.labelKey) }}</option>
        </select>
        <input
          v-if="node.op !== 'present'"
          v-model="node.value"
          type="text"
          class="mono qb-value"
          :placeholder="t('search.builderValuePlaceholder')"
          :disabled="disabled"
          spellcheck="false"
        />
        <span v-else class="qb-value qb-value--empty" />
        <button
          type="button"
          class="qb-remove"
          :disabled="disabled"
          :title="t('search.builderRemoveRow')"
          :aria-label="t('search.builderRemoveRow')"
          @click="removeChild(index)"
        >
          <X aria-hidden="true" />
        </button>
      </template>
      <template v-else>
        <FilterGroup :group="node" :depth="depth + 1" :disabled="disabled" :list-id="listId" />
        <button
          type="button"
          class="qb-remove qb-remove--group"
          :disabled="disabled"
          :title="t('search.builderRemoveGroup')"
          :aria-label="t('search.builderRemoveGroup')"
          @click="removeChild(index)"
        >
          <X aria-hidden="true" />
        </button>
      </template>
    </div>
  </div>
</template>
