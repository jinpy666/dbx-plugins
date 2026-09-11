<script setup lang="ts">
// DN 树：scope=one 懒展开 + 关键字远程子树过滤 + 右键菜单。
// 交互语义对照 tiny-rdm LdapConsolePage 的树区块（fetchTreeChildren /
// buildTreeKeywordFilter / searchTreeFilterRemote），组件按 DBX 插件形态重实现。
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { RefreshCw, Search, X } from "@lucide/vue";
import { ldapApi, type LdapEntry } from "../lib/api";
import { buildTreeKeywordFilter } from "../lib/ldapFilter";
import { friendlyLdapError } from "../lib/ldapErrors";
import { splitFirstDnRdn } from "../lib/dn";
import { nextFocusIndex } from "../lib/modal";
import { t } from "../lib/i18n";
import { compareDnByLabel, compareDnForTree, flattenDnTree, isFetchTruncated, nextFetchLimit, nextTreeFocusIndex, TREE_FETCH_PAGE, type DnTreeNode } from "../lib/dnTree";
import VirtualList from "./VirtualList.vue";
import TreeBranch from "./TreeBranch.vue";
import TreeNodeIcon from "./TreeNodeIcon.vue";

const props = defineProps<{
  baseDn: string;
  canWrite: boolean;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: "select", dn: string): void;
  (e: "searchHere", dn: string): void;
  (e: "view", dn: string): void;
  (e: "members", dn: string): void;
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
// 请求列（只用于展示/补全，服务器对未知属性名回缺席名不报错）：树过滤命中
// 面已扩到 dc/o/sn/givenName（与 buildTreeKeywordFilter 的属性集对齐）。
const FILTER_ATTRIBUTES = ["dn", "name", "cn", "ou", "dc", "uid", "o", "sn", "givenName", "displayName", "mail", "objectClass"];
// 关键字远程过滤的单次抓取上限：达到即视为"可能有更多"，给可见截断提示。
const TREE_FILTER_LIMIT = 100;

// 过滤结果按 RDN 标签字母排序（服务器返回序无保障；字母序可预期、可扫描）。
const sortedFilterResults = computed(() => [...filterResults.value].sort((left, right) => compareDnByLabel(left.dn, right.dn)));

// 虚拟滚动固定行高（与 .tree-vlist CSS 保持一致）；零依赖实现见 VirtualList.vue。
const TREE_ROW_HEIGHT = 28;

// -- 侧栏宽度（右缘 resizer 拖拽，localStorage 记忆，双击重置默认宽）----------
// 宿主 webview 禁存储时静默降级为仅内存态。数值口径与 kafka TopicTree 同族。

const TREE_WIDTH_KEY = "dbx.ldap.ui.treeWidth";
const TREE_WIDTH_DEFAULT = 280;
const TREE_WIDTH_MIN = 200;
const TREE_WIDTH_MAX = 480;

function readStoredWidth(): number {
  try {
    const parsed = Number.parseInt(localStorage.getItem(TREE_WIDTH_KEY) ?? "", 10);
    return Number.isFinite(parsed) ? Math.min(TREE_WIDTH_MAX, Math.max(TREE_WIDTH_MIN, parsed)) : TREE_WIDTH_DEFAULT;
  } catch {
    return TREE_WIDTH_DEFAULT;
  }
}

function persistWidth(value: number) {
  try {
    localStorage.setItem(TREE_WIDTH_KEY, String(value));
  } catch {
    /* 存储不可用（隐私模式等）：仅内存态 */
  }
}

const paneWidth = ref(readStoredWidth());

function clampWidth(value: number): number {
  return Math.min(TREE_WIDTH_MAX, Math.max(TREE_WIDTH_MIN, Math.round(value)));
}

// resizer：pointer capture 拖拽（移出面板也不丢），双击重置默认宽。
const resizing = ref(false);
let dragStartX = 0;
let dragStartWidth = 0;

function onResizeStart(event: PointerEvent) {
  event.preventDefault();
  dragStartX = event.clientX;
  dragStartWidth = paneWidth.value;
  resizing.value = true;
  (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
}

function onResizeMove(event: PointerEvent) {
  if (!resizing.value) return;
  paneWidth.value = clampWidth(dragStartWidth + event.clientX - dragStartX);
}

function onResizeEnd(event: PointerEvent) {
  if (!resizing.value) return;
  resizing.value = false;
  (event.currentTarget as HTMLElement).releasePointerCapture(event.pointerId);
  persistWidth(paneWidth.value);
}

function onResizeReset() {
  paneWidth.value = TREE_WIDTH_DEFAULT;
  persistWidth(TREE_WIDTH_DEFAULT);
}

const hasBaseDn = computed(() => props.baseDn.trim() !== "");
const hasFilter = computed(() => filterKeyword.value.trim() !== "");

// 可见行扁平投影：VirtualList 只渲染视口 ± 缓冲窗口，千行子条目不再一次
// 性挂载全量 DOM。展开/选中/右键语义不变（状态仍在 node 对象上）。
const treeRows = computed(() => flattenDnTree(rootNode.value));

function nodeLabel(dn: string): string {
  const { rdn } = splitFirstDnRdn(dn);
  return rdn || dn || "-";
}

function makeNode(dn: string): DnTreeNode {
  return { dn, label: nodeLabel(dn), expanded: false, loaded: false, loading: false, children: [] };
}

// 懒加载单页抓取（scope=one + sizeLimit=单页）。返回截断标记：达到上限即
// 视为"可能还有更多"（UI 扫描 P1-2）——截断必须可见化，徽标显示"已加载+"
// 并提供加载更多入口，而不是静默丢掉后继条目。
async function fetchChildren(dn: string, limit: number = TREE_FETCH_PAGE): Promise<{ children: DnTreeNode[]; truncated: boolean }> {
  const result = await ldapApi.search({
    baseDn: dn,
    filter: "(objectClass=*)",
    scope: "one",
    attributes: TREE_ATTRIBUTES,
    sizeLimit: limit,
    pageSize: limit,
    derefAliases: "never",
  });
  const children = result.entries
    .map((entry) => entry.dn)
    .filter((child) => child && child !== dn)
    // 同级容器优先：ou 组在前、cn 组居中、其余类型殿后，组内沿用原字典序。
    .sort(compareDnForTree)
    .map(makeNode);
  return { children, truncated: isFetchTruncated(result.entries.length, limit) };
}

// "加载更多"重取替换 children 时，保留已加载子节点的展开/加载状态（续载
// 不折叠树、不丢深层展开）。DN 排序稳定，重取结果覆盖既有集合。
function preserveExpansion(next: DnTreeNode[], previous: DnTreeNode[]) {
  const byDn = new Map(previous.map((child) => [child.dn.toLowerCase(), child]));
  for (const child of next) {
    const old = byDn.get(child.dn.toLowerCase());
    if (!old) continue;
    child.expanded = old.expanded;
    child.loaded = old.loaded;
    child.loading = old.loading;
    child.children = old.children;
    child.childCount = old.childCount;
    child.truncated = old.truncated;
  }
}

// 懒加载后异步取精确子条目数（ldap/count，one 层精确总数、上限 5000）：
// 已加载数少于它即仍截断，同时纠正"返回条数恰好等于单页上限"的
// isFetchTruncated 边界误判（如恰好 1000 条的 OU 首载会误标截断）。
// count 失败静默保留估算值，不阻塞展开。
async function refreshChildCount(node: DnTreeNode) {
  try {
    const { count } = await ldapApi.count(node.dn);
    node.childCount = count;
    node.truncated = node.children.length < count;
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
    const { children, truncated } = await fetchChildren(node.dn);
    node.children = children;
    node.loaded = true;
    node.truncated = truncated;
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
      const { children, truncated } = await fetchChildren(node.dn);
      node.children = children;
      node.loaded = true;
      node.truncated = truncated;
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

// P1-2 截断续载：按"已加载数 + 一页"重取并整体替换 children（DN 排序稳定，
// 结果覆盖既有集合），保留已展开子节点状态。全部加载完 truncated 归 false、
// 徽标恢复精确数；失败走错误横幅、节点保持当前已加载状态可重试。
async function loadMore(node: DnTreeNode) {
  if (props.disabled || node.loading) return;
  node.loading = true;
  try {
    const { children, truncated } = await fetchChildren(node.dn, nextFetchLimit(node.children.length));
    preserveExpansion(children, node.children);
    node.children = children;
    node.truncated = truncated;
    if (!truncated) node.childCount = children.length;
    void refreshChildCount(node);
    treeError.value = "";
    treeErrorRaw.value = "";
  } catch (cause) {
    const raw = cause instanceof Error ? cause.message : String(cause);
    treeError.value = friendlyLdapError(raw);
    treeErrorRaw.value = treeError.value === raw ? "" : raw;
  } finally {
    node.loading = false;
  }
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
      sizeLimit: TREE_FILTER_LIMIT,
      pageSize: TREE_FILTER_LIMIT,
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

const contextMenuEl = ref<HTMLElement>();
let contextMenuTrigger: HTMLElement | null = null;

function openContextMenu(event: MouseEvent, dn: string) {
  contextMenuTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  contextMenu.value = { x: event.clientX, y: event.clientY, dn };
  // 渲染后按实际菜单尺寸夹回视口（右缘/底缘右键不裁切），焦点进菜单容器
  // 以便键盘操作（↑/↓ 移项、Esc 关闭）。
  void nextTick(() => {
    const menu = contextMenuEl.value;
    const state = contextMenu.value;
    if (!menu || !state) return;
    const rect = menu.getBoundingClientRect();
    state.x = Math.max(4, Math.min(state.x, window.innerWidth - rect.width - 4));
    state.y = Math.max(4, Math.min(state.y, window.innerHeight - rect.height - 4));
    menu.focus({ preventScroll: true });
  });
}

function closeContextMenu() {
  contextMenu.value = undefined;
}

function onMenuKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    event.preventDefault();
    const restore = contextMenuTrigger;
    closeContextMenu();
    restore?.focus({ preventScroll: true });
    return;
  }
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  event.preventDefault();
  const items = Array.from(contextMenuEl.value?.querySelectorAll<HTMLButtonElement>("button:not([disabled])") ?? []);
  const index = items.indexOf(document.activeElement as HTMLButtonElement);
  items[nextFocusIndex(items.length, index, event.key === "ArrowUp")]?.focus({ preventScroll: true });
}

// -- keyboard navigation（↑/↓ 在可见行间移动焦点；Enter 由原生 button click 选中）
// UI 扫描 P2-10：树此前只能 Tab 逐节点走（每节点 2 个停止位），虚拟滚动下
// 未挂载行不可达。↑/↓ 在当前渲染出的 .tree-node 按钮间移动焦点并滚入视野。

function onTreeKeydown(event: KeyboardEvent) {
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
  const container = event.currentTarget as HTMLElement | null;
  if (!container) return;
  const nodes = Array.from(container.querySelectorAll<HTMLButtonElement>(".tree-node"));
  const target = nextTreeFocusIndex(nodes.length, nodes.indexOf(document.activeElement as HTMLButtonElement), event.key);
  if (target < 0) return;
  event.preventDefault();
  const node = nodes[target];
  node?.focus({ preventScroll: true });
  node?.scrollIntoView({ block: "nearest" });
}

function menuAction(action: string) {
  const dn = contextMenu.value?.dn;
  closeContextMenu();
  if (!dn) return;
  if (action === "search") emit("searchHere", dn);
  else if (action === "view") emit("view", dn);
  else if (action === "members") emit("members", dn);
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
        node.truncated = false;
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
  <section class="tree-pane" :class="{ 'is-resizing': resizing }" :style="{ '--tree-pane-width': `${paneWidth}px` }">
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
    <div class="tree-rows" role="tree" :aria-label="t('tree.title')" @click="closeContextMenu" @keydown="onTreeKeydown">
      <div v-if="!hasBaseDn" class="tree-state">{{ t("tree.missingBaseDn") }}</div>
      <template v-else-if="hasFilter">
        <div v-if="filterLoading" class="tree-state">{{ t("tree.loading") }}</div>
        <div v-else-if="filterError" class="tree-error" :title="filterErrorRaw || filterError">{{ filterError }}</div>
        <div v-else-if="filterResults.length === 0" class="tree-state">{{ t("tree.filterNoMatch", { keyword: filterKeyword.trim() }) }}</div>
        <template v-else>
          <VirtualList :items="sortedFilterResults" :row-height="TREE_ROW_HEIGHT" :reset-key="filterKeyword" class="tree-vlist">
            <template #default="{ item }">
              <button class="tree-node" :title="item.dn" @click.stop="selectNode(makeNode(item.dn))" @dblclick.stop="emit('view', item.dn)" @contextmenu.prevent.stop="openContextMenu($event, item.dn)">
                <span class="tree-row" :class="{ selected: selectedDn === item.dn }">
                  <span class="tree-label">
                    <TreeNodeIcon :dn="item.dn" />
                    <span class="tree-name">{{ nodeLabel(item.dn) }}</span>
                  </span>
                </span>
              </button>
            </template>
          </VirtualList>
          <!-- 命中数达到单次抓取上限：可能有更多，可见化提示而不是静默截断 -->
          <div v-if="filterResults.length >= TREE_FILTER_LIMIT" class="tree-more-hint">
            {{ t("tree.filterTruncated", { limit: TREE_FILTER_LIMIT }) }}
          </div>
        </template>
      </template>
      <template v-else>
        <div v-if="loadingRoot" class="tree-state">{{ t("tree.loading") }}</div>
        <template v-else>
          <!-- 懒展开失败：错误横幅与树并存，不吞掉已加载的树 -->
          <div v-if="treeError" class="tree-error" :title="treeErrorRaw || treeError">{{ treeError }}</div>
          <div v-if="!rootNode && !treeError" class="tree-state">{{ t("tree.empty") }}</div>
          <VirtualList
            v-else-if="rootNode"
            :items="treeRows"
            :row-height="TREE_ROW_HEIGHT"
            :reset-key="props.baseDn"
            class="tree-vlist"
          >
            <template #default="{ item }">
              <TreeBranch
                :node="item.node"
                :depth="item.depth"
                :selected-dn="selectedDn"
                :base-dn="props.baseDn"
                :disabled="disabled"
                @toggle="toggleNode"
                @select="selectNode"
                @menu="openContextMenu"
                @load-more="loadMore"
              />
            </template>
          </VirtualList>
        </template>
      </template>
    </div>
    <Teleport to="body">
      <div
        v-if="contextMenu"
        ref="contextMenuEl"
        class="context-menu"
        role="menu"
        tabindex="-1"
        :style="{ left: `${contextMenu.x}px`, top: `${contextMenu.y}px` }"
        @click.stop
        @keydown="onMenuKeydown"
      >
        <button role="menuitem" @click="menuAction('search')">{{ t("tree.searchHere") }}</button>
        <button role="menuitem" @click="menuAction('view')">{{ t("tree.viewEntry") }}</button>
        <button role="menuitem" @click="menuAction('members')">{{ t("associations.members") }}</button>
        <button role="menuitem" :disabled="!canWrite" @click="menuAction('add')">{{ t("tree.addEntry") }}</button>
        <button role="menuitem" :disabled="!canWrite" @click="menuAction('rename')">{{ t("tree.renameEntry") }}</button>
        <button role="menuitem" :disabled="!canWrite" class="danger" @click="menuAction('delete')">{{ t("tree.deleteEntry") }}</button>
        <hr />
        <button role="menuitem" @click="menuAction('export')">{{ t("tree.exportSubtree") }}</button>
        <button role="menuitem" @click="menuAction('copy')">{{ t("copyDn") }}</button>
      </div>
    </Teleport>
    <!-- 右缘拖宽把手：pointer capture 拖拽调宽，双击重置默认宽 -->
    <div
      class="tree-resizer"
      :class="{ active: resizing }"
      role="separator"
      aria-orientation="vertical"
      @pointerdown="onResizeStart"
      @pointermove="onResizeMove"
      @pointerup="onResizeEnd"
      @pointercancel="onResizeEnd"
      @dblclick="onResizeReset"
    />
  </section>
</template>
