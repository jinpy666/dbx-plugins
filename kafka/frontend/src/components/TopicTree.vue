<script setup lang="ts">
// 左栏 topic 树：业务 topic 按评分排序、internal（_ 前缀 / 后端标记）沉底，
// 过滤框本地过滤，选中态由父级持有（selectedTopic 单一来源）。
// 侧栏收空间：右缘 resizer 拖拽调宽（180–480px，localStorage 记忆，双击重置）、
// 折叠成 40px 竖条（折叠时不渲染树内容），过滤框 / 快捷键聚焦 + 匹配/总数徽章。
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { ChevronsLeft, ChevronsRight, HardDrive, RefreshCw, Search, X } from "@lucide/vue";
import type { KafkaTopic } from "../lib/api";
import { filterTopics, sortTopics } from "../lib/kafkaModel";
import { friendlyKafkaError } from "../lib/kafkaErrors";
import { t } from "../lib/i18n";

const props = defineProps<{
  topics: KafkaTopic[];
  loading: boolean;
  error: string;
  selectedTopic: string;
}>();

const emit = defineEmits<{
  (e: "refresh"): void;
  (e: "select", topic: string): void;
}>();

// -- 侧栏宽度 / 折叠（localStorage 记忆，宿主 webview 禁存储时静默降级）---------

const TREE_WIDTH_KEY = "dbx.kafka.ui.treeWidth";
const TREE_COLLAPSED_KEY = "dbx.kafka.ui.treeCollapsed";
const TREE_WIDTH_DEFAULT = 240;
const TREE_WIDTH_MIN = 180;
const TREE_WIDTH_MAX = 480;

function readStoredWidth(): number {
  try {
    const parsed = Number.parseInt(localStorage.getItem(TREE_WIDTH_KEY) ?? "", 10);
    return Number.isFinite(parsed) ? Math.min(TREE_WIDTH_MAX, Math.max(TREE_WIDTH_MIN, parsed)) : TREE_WIDTH_DEFAULT;
  } catch {
    return TREE_WIDTH_DEFAULT;
  }
}

function persist(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* 存储不可用（隐私模式等）：仅内存态 */
  }
}

const width = ref(readStoredWidth());
const collapsed = ref((() => {
  try {
    return localStorage.getItem(TREE_COLLAPSED_KEY) === "1";
  } catch {
    return false;
  }
})());

function clampWidth(value: number): number {
  return Math.min(TREE_WIDTH_MAX, Math.max(TREE_WIDTH_MIN, Math.round(value)));
}

function setCollapsed(next: boolean) {
  collapsed.value = next;
  persist(TREE_COLLAPSED_KEY, next ? "1" : "0");
}

// resizer：pointer capture 拖拽，双击重置默认宽。
const resizing = ref(false);
let dragStartX = 0;
let dragStartWidth = 0;

function onResizeStart(event: PointerEvent) {
  event.preventDefault();
  dragStartX = event.clientX;
  dragStartWidth = width.value;
  resizing.value = true;
  (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
}

function onResizeMove(event: PointerEvent) {
  if (!resizing.value) return;
  width.value = clampWidth(dragStartWidth + event.clientX - dragStartX);
}

function onResizeEnd(event: PointerEvent) {
  if (!resizing.value) return;
  resizing.value = false;
  (event.currentTarget as HTMLElement).releasePointerCapture(event.pointerId);
  persist(TREE_WIDTH_KEY, String(width.value));
}

function onResizeReset() {
  width.value = TREE_WIDTH_DEFAULT;
  persist(TREE_WIDTH_KEY, String(TREE_WIDTH_DEFAULT));
}

// -- 树内容 ---------------------------------------------------------------------

const keyword = ref("");
const filterInput = ref<HTMLInputElement | null>(null);

const visible = computed(() => filterTopics(sortTopics(props.topics), keyword.value));

// P2-18：树错误区与 App 错误横幅同源——friendlyKafkaError 友好化正文，
// 未覆盖/与原文不同时把原始串留在 title 悬停里供排查。
const friendlyError = computed(() => (props.error ? friendlyKafkaError(props.error) : ""));
const errorDetail = computed(() => (props.error && friendlyError.value !== props.error ? props.error : ""));

function isInternal(topic: KafkaTopic): boolean {
  return topic.isInternal === true || topic.name.startsWith("_");
}

// F6-5：topic 级健康徽标（isHealthy===false 红点 + title「N 个分区不健康」）。
// 字段由 topics/list 直接下发（12.2.4），无额外请求；旧 sidecar 缺省 = 视为健康。
function unhealthyCount(topic: KafkaTopic): number {
  return topic.isHealthy === false ? Math.max(1, Number(topic.unhealthyPartitions ?? 0) || 0) : 0;
}

function clearFilter() {
  keyword.value = "";
}

function onFilterKeydown(event: KeyboardEvent) {
  // Escape 清空并还原列表，避免「过滤后找不到原项」的死角。
  if (event.key === "Escape" && keyword.value) {
    event.stopPropagation();
    clearFilter();
    return;
  }
  // P2-22：Enter 选中首个（或唯一）匹配项——过滤后逐 Tab 穿树在 big 模式下
  // 有数百个 tab stop，Enter 直达首个匹配是键盘主路径。
  if (event.key === "Enter" && visible.value.length > 0) {
    event.preventDefault();
    emit("select", visible.value[0]!.name);
  }
}

// 全局 "/" 聚焦过滤框（输入控件内不劫持）。
function onGlobalKeydown(event: KeyboardEvent) {
  if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey) return;
  const target = event.target as HTMLElement | null;
  if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT" || target.isContentEditable)) return;
  if (collapsed.value) setCollapsed(false);
  event.preventDefault();
  // 折叠态先展开再聚焦（v-if 重建输入框，等一帧）。
  requestAnimationFrame(() => filterInput.value?.focus());
}

onMounted(() => window.addEventListener("keydown", onGlobalKeydown));
onBeforeUnmount(() => window.removeEventListener("keydown", onGlobalKeydown));
</script>

<template>
  <aside
    class="tree-pane"
    :class="{ 'is-collapsed': collapsed, 'is-resizing': resizing }"
    :style="{ '--tree-pane-width': `${width}px` }"
  >
    <div v-if="collapsed" class="tree-rail">
      <button class="tree-rail-toggle" :title="t('messages.uiSidebarExpand')" :aria-label="t('messages.uiSidebarExpand')" @click="setCollapsed(false)">
        <ChevronsRight aria-hidden="true" />
      </button>
      <span class="tree-rail-label">{{ t("tree.title") }}</span>
    </div>
    <template v-else>
      <div class="panel-header">
        <span class="panel-title">
          <HardDrive aria-hidden="true" /> {{ t("tree.title") }}
          <span class="badge tree-count">{{ t("messages.uiTreeFilterCount", { matched: visible.length, total: props.topics.length }) }}</span>
        </span>
        <span class="actions">
          <button class="icon-button" :disabled="loading" :title="t('refresh')" @click="emit('refresh')">
            <RefreshCw :class="{ spinning: loading }" />
          </button>
          <button class="icon-button" :title="t('messages.uiSidebarCollapse')" :aria-label="t('messages.uiSidebarCollapse')" @click="setCollapsed(true)">
            <ChevronsLeft aria-hidden="true" />
          </button>
        </span>
      </div>
      <div class="tree-filter">
        <Search aria-hidden="true" class="icon-neutral icon-13" />
        <input
          ref="filterInput"
          v-model="keyword"
          :placeholder="t('tree.filterPlaceholder')"
          :title="t('tree.filterFocusHint')"
          type="text"
          spellcheck="false"
          @keydown="onFilterKeydown"
        />
        <!-- P2-22：清除钮移出 Tab 序（tabindex="-1"）——过滤激活时 Tab 从过滤框
             直达树行，不会先误停在清除钮上；键盘清空走既有 Esc 路径。 -->
        <button v-if="keyword" class="icon-button" tabindex="-1" :title="t('close')" :aria-label="t('close')" @click="clearFilter"><X /></button>
      </div>
      <div v-if="error" class="tree-error" :title="errorDetail">{{ friendlyError }}</div>
      <div v-else-if="loading && topics.length === 0" class="tree-state">{{ t("tree.loading") }}</div>
      <div v-else-if="visible.length === 0" class="tree-state">
        {{ keyword ? t("tree.noMatch", { keyword }) : t("tree.empty") }}
      </div>
      <div v-else class="tree-rows">
        <div class="tree-node">
          <button
            v-for="topic in visible"
            :key="topic.name"
            type="button"
            class="tree-row"
            :class="{ selected: topic.name === selectedTopic }"
            :title="topic.error ? `${topic.name}: ${topic.error}` : topic.name"
            @click="emit('select', topic.name)"
          >
            <span class="tree-label">
              <HardDrive aria-hidden="true" class="icon-13" :class="isInternal(topic) ? 'icon-neutral' : 'icon-violet'" />
              <span class="tree-name mono">{{ topic.name }}</span>
            </span>
            <!-- F6-5：不健康红点（title = N 个分区不健康），无额外请求 -->
            <span
              v-if="unhealthyCount(topic) > 0"
              class="tree-health-dot"
              :title="t('tree.unhealthyTitle', { count: unhealthyCount(topic) })"
              :aria-label="t('tree.unhealthyTitle', { count: unhealthyCount(topic) })"
            />
            <span v-if="isInternal(topic)" class="badge badge-internal">{{ t("tree.internal") }}</span>
            <span v-else class="tree-badge">{{ t("tree.partitions", { count: topic.partitionCount }) }}</span>
          </button>
        </div>
      </div>
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
    </template>
  </aside>
</template>
