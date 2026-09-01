<script setup lang="ts">
// DN 树：scope=one 懒展开 + 关键字远程子树过滤 + 右键菜单。
// 交互语义对照 tiny-rdm LdapConsolePage 的树区块（fetchTreeChildren /
// buildTreeKeywordFilter / searchTreeFilterRemote），组件按 DBX 插件形态重实现。
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { RefreshCw, Search, X } from "@lucide/vue";
import { ldapApi, type LdapEntry } from "../lib/api";
import { buildTreeKeywordFilter } from "../lib/ldapFilter";
import { friendlyLdapError } from "../lib/ldapErrors";
import { splitFirstDnRdn } from "../lib/dn";
import { t } from "../lib/i18n";
import type { DnTreeNode } from "../lib/dnTree";
import TreeBranch from "./TreeBranch.vue";

const props = defineProps<{
  baseDn: string;
  canWrite: boolean;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: "select", dn: string): void;
  (e: "searchHere", dn: string): void;
  (e: "view", dn: string): void;
  (e: "add", dn: string): void;
  (e: "rename", dn: string): void;
  (e: "remove", dn: string): void;
  (e: "export", dn: string): void;
  (e: "copyDn", dn: string): void;
}>();
const rootNode = ref<DnTreeNode>();
const treeError = ref("");
const treeErrorRaw = ref("");
const loadingRoot = ref(false);
const selectedDn = ref("");
const filterKeyword = ref("");
const filterLoading = ref(false);
const filterError = ref("");
const filterErrorRaw = ref("");
const filterResults = ref<LdapEntry[]>([]);
const contextMenu = ref<{ x: number; y: number; dn: string }>();
let filterSequence = 0;
let filterTimer = 0;

const TREE_ATTRIBUTES = ["dn", "name", "cn", "ou", "objectClass"];
const FILTER_ATTRIBUTES = ["dn", "name", "cn", "ou", "uid", "displayName", "mail", "objectClass"];

const hasBaseDn = computed(() => props.baseDn.trim() !== "");
const hasFilter = computed(() => filterKeyword.value.trim() !== "");

function nodeLabel(dn: string): string {
  const { rdn } = splitFirstDnRdn(dn);
  return rdn || dn || "-";
}

function makeNode(dn: string): DnTreeNode {
  return { dn, label: nodeLabel(dn), expanded: false, loaded: false, loading: false, children: [] };
}

async function fetchChildren(dn: string): Promise<DnTreeNode[]> {
  const result = await ldapApi.search({
    baseDn: dn,
    filter: "(objectClass=*)",
    scope: "one",
    attributes: TREE_ATTRIBUTES,
    sizeLimit: 500,
    pageSize: 500,
    derefAliases: "never",
  });
  return result.entries
    .map((entry) => entry.dn)
    .filter((child) => child && child !== dn)
    .sort((left, right) => left.localeCompare(right, undefined, { numeric: true }))
    .map(makeNode);
}

// 懒加载后异步取精确子条目数（ldap/count）刷新徽章：children.length 受
// sizeLimit=500 截断影响只是下限；count 失败静默保留估算值，不阻塞展开。
async function refreshChildCount(node: DnTreeNode) {
  try {
    const { count } = await ldapApi.count(node.dn);
    node.childCount = count;
  } catch {
    // 静默：徽章保留 fetchChildren 的估算值
  }
}

async function loadRoot() {
  if (!hasBaseDn.value || props.disabled) return;
  if (loadingRoot.value) return; // 刷新连点去重
  loadingRoot.value = true;
  treeError.value = "";
  treeErrorRaw.value = "";
  try {
    const node = makeNode(props.baseDn.trim());
    node.loading = true;
    rootNode.value = node;
    node.children = await fetchChildren(node.dn);
    node.loaded = true;
    node.childCount = node.children.length;
    void refreshChildCount(node);
    node.expanded = true;
  } catch (cause) {
    rootNode.value = undefined;
    const raw = cause instanceof Error ? cause.message : String(cause);
    treeError.value = friendlyLdapError(raw);
    treeErrorRaw.value = treeError.value === raw ? "" : raw;
  } finally {
    loadingRoot.value = false;
    if (rootNode.value) rootNode.value.loading = false;
  }
}

async function toggleNode(node: DnTreeNode) {
  if (props.disabled) return;
  selectedDn.value = node.dn;
  emit("select", node.dn);
  // 加载中忽略重复点击（同一节点并发去重）；已加载节点直接折叠/展开。
  if (node.loading) return;
  if (!node.expanded && !node.loaded) {
    node.loading = true;
    try {
      node.children = await fetchChildren(node.dn);
      node.loaded = true;
      node.childCount = node.children.length;
      void refreshChildCount(node);
      // 本次懒展开成功：清除上一次失败的错误横幅（节点保持折叠，可重试）。
      treeError.value = "";
      treeErrorRaw.value = "";
    } catch (cause) {
      // 懒展开失败不清空树：错误以横幅显示，节点保持未加载状态，
      // 再次点击即重试（根加载失败才整树替换为错误态）。
      const raw = cause instanceof Error ? cause.message : String(cause);
      treeError.value = friendlyLdapError(raw);
      treeErrorRaw.value = treeError.value === raw ? "" : raw;
      return;
    } finally {
      node.loading = false;
    }
  }
  node.expanded = !node.expanded;
}

function selectNode(node: DnTreeNode) {
  selectedDn.value = node.dn;
  emit("select", node.dn);
}

// -- keyword remote filter ---------------------------------------------------

function clearFilter() {
  filterSequence += 1;
  filterKeyword.value = "";
  filterLoading.value = false;
  filterError.value = "";
  filterErrorRaw.value = "";
  filterResults.value = [];
}

async function runFilter() {
  const keyword = filterKeyword.value.trim();
  const seq = ++filterSequence;
  filterError.value = "";
  if (!keyword || !hasBaseDn.value || props.disabled) {
    filterLoading.value = false;
    filterResults.value = [];
    return;
  }
  filterLoading.value = true;
  try {
    const result = await ldapApi.search({
      baseDn: props.baseDn.trim(),
      filter: buildTreeKeywordFilter(keyword),
      scope: "sub",
      attributes: FILTER_ATTRIBUTES,
      sizeLimit: 100,
      pageSize: 100,
      derefAliases: "never",
    });
    if (seq !== filterSequence) return;
    filterResults.value = result.entries;
  } catch (cause) {
    if (seq !== filterSequence) return;
    filterResults.value = [];
    // 树内过滤错误同样走友好映射；原始串在 title 悬停可见。
    const raw = cause instanceof Error ? cause.message : String(cause);
    filterError.value = friendlyLdapError(raw);
    filterErrorRaw.value = filterError.value === raw ? "" : raw;
  } finally {
    if (seq === filterSequence) filterLoading.value = false;
  }
}

function onFilterInput() {
  window.clearTimeout(filterTimer);
  if (!filterKeyword.value.trim()) {
    clearFilter();
    return;
  }
  filterTimer = window.setTimeout(() => void runFilter(), 300);
}

// -- context menu ------------------------------------------------------------

function openContextMenu(event: MouseEvent, dn: string) {
  contextMenu.value = { x: event.clientX, y: event.clientY, dn };
}

function closeContextMenu() {
  contextMenu.value = undefined;
}

function menuAction(action: string) {
  const dn = contextMenu.value?.dn;
  closeContextMenu();
  if (!dn) return;
  if (action === "search") emit("searchHere", dn);
  else if (action === "view") emit("view", dn);
  else if (action === "add") emit("add", dn);
  else if (action === "rename") emit("rename", dn);
  else if (action === "delete") emit("remove", dn);
  else if (action === "export") emit("export", dn);
  else if (action === "copy") emit("copyDn", dn);
}

watch(
  () => props.baseDn,
  () => {
    clearFilter();
    void loadRoot();
  },
);

watch(
  () => props.disabled,
  (next) => {
    if (next) clearFilter();
  },
);

defineExpose({
  refresh: loadRoot,
  /** After writes, drop the cached children of `dn` (or reload everything). */
  invalidate(dn?: string) {
    if (!dn) {
      void loadRoot();
      return;
    }
    const walk = (node?: DnTreeNode): boolean => {
      if (!node) return false;
      if (node.dn === dn) {
        node.loaded = false;
        node.expanded = false;
        node.children = [];
        return true;
      }
      return node.children.some((child) => walk(child));
    };
    if (!walk(rootNode.value)) void loadRoot();
  },
});

function onMountedCleanup() {
  document.removeEventListener("click", closeContextMenu);
}

window.setTimeout(() => document.addEventListener("click", closeContextMenu), 0);
onBeforeUnmount(onMountedCleanup);
</script>

<template>
  <section class="tree-pane">
    <header class="panel-header">
      <span class="panel-title"><Search class="icon-neutral" aria-hidden="true" />{{ t("tree.title") }}</span>
      <span class="actions">
        <button class="icon-button" :title="t('refresh')" :disabled="disabled || !hasBaseDn || loadingRoot" @click.stop="loadRoot">
          <RefreshCw :class="{ spinning: loadingRoot }" />
        </button>
      </span>
    </header>
    <div class="tree-filter">
      <input
        v-model="filterKeyword"
        type="text"
        :placeholder="t('tree.filterPlaceholder')"
        :disabled="disabled || !hasBaseDn"
        @input="onFilterInput"
        @keydown.esc="clearFilter"
      />
      <!-- 过滤激活时左栏切换为匹配列表：给显式还原入口，避免"树不见了" -->
      <button v-if="hasFilter" class="icon-button" :title="t('tree.clearFilter')" @click.stop="clearFilter">
        <X aria-hidden="true" />
      </button>
    </div>
    <div class="tree-rows" @click="closeContextMenu">
      <div v-if="!hasBaseDn" class="tree-state">{{ t("tree.missingBaseDn") }}</div>
      <template v-else-if="hasFilter">
        <div v-if="filterLoading" class="tree-state">{{ t("tree.loading") }}</div>
        <div v-else-if="filterError" class="tree-error" :title="filterErrorRaw || filterError">{{ filterError }}</div>
        <div v-else-if="filterResults.length === 0" class="tree-state">{{ t("tree.filterNoMatch", { keyword: filterKeyword.trim() }) }}</div>
        <ul v-else class="filter-list">
          <li v-for="entry in filterResults" :key="entry.dn">
            <button class="tree-node" :title="entry.dn" @click.stop="selectNode(makeNode(entry.dn))" @dblclick.stop="emit('view', entry.dn)" @contextmenu.prevent.stop="openContextMenu($event, entry.dn)">
              <span class="tree-row" :class="{ selected: selectedDn === entry.dn }">
                <span class="tree-label"><span class="tree-name">{{ nodeLabel(entry.dn) }}</span></span>
              </span>
            </button>
          </li>
        </ul>
      </template>
      <template v-else>
        <div v-if="loadingRoot" class="tree-state">{{ t("tree.loading") }}</div>
        <template v-else>
          <!-- 懒展开失败：错误横幅与树并存，不吞掉已加载的树 -->
          <div v-if="treeError" class="tree-error" :title="treeErrorRaw || treeError">{{ treeError }}</div>
          <div v-if="!rootNode && !treeError" class="tree-state">{{ t("tree.empty") }}</div>
          <TreeBranch
            v-else-if="rootNode"
            :node="rootNode"
            :selected-dn="selectedDn"
            :disabled="disabled"
            @toggle="toggleNode"
            @select="selectNode"
            @view="(dn: string) => emit('view', dn)"
            @menu="openContextMenu"
          />
        </template>
      </template>
    </div>
    <Teleport to="body">
      <div v-if="contextMenu" class="context-menu" :style="{ left: `${contextMenu.x}px`, top: `${contextMenu.y}px` }" @click.stop>
        <button @click="menuAction('search')">{{ t("tree.searchHere") }}</button>
        <button @click="menuAction('view')">{{ t("tree.viewEntry") }}</button>
        <button :disabled="!canWrite" @click="menuAction('add')">{{ t("tree.addEntry") }}</button>
        <button :disabled="!canWrite" @click="menuAction('rename')">{{ t("tree.renameEntry") }}</button>
        <button :disabled="!canWrite" class="danger" @click="menuAction('delete')">{{ t("tree.deleteEntry") }}</button>
        <hr />
        <button @click="menuAction('export')">{{ t("tree.exportSubtree") }}</button>
        <button @click="menuAction('copy')">{{ t("copyDn") }}</button>
      </div>
    </Teleport>
  </section>
</template>
