<script setup lang="ts">
// 结果表：列 = attributes 并集（dn 首列）；排序/分页在前端完成。
// 替代 tiny-rdm 的 AgGrid（无新依赖的轻量表格）。交互：
// - 列头点击排序（dn 列默认升序）
// - 列头右缘拖拽调宽，持久化 localStorage（按列名记录）
// - 行双击 / Enter 打开条目编辑器（ldap/entry/get），↑/↓ 键盘导航
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { ChevronLeft, ChevronRight, FileDown, FileJson, FileSpreadsheet } from "@lucide/vue";
import type { LdapEntry } from "../lib/api";
import { extractEntryAttributeNames } from "../lib/ldapExporter";
import { t } from "../lib/i18n";

const props = defineProps<{
  entries: LdapEntry[];
  count: number;
  truncated: boolean;
  /** count 恰好等于 sizeLimit 的"可能截断"信号（UI 扫描 P2-15）。 */
  atLimit?: boolean;
  /** 最近一次搜索的 sizeLimit（提示文案展示上限值）。 */
  sizeLimit?: number;
  /** 已执行过至少一次搜索：0 条时区分"无匹配"与"未搜索"（UI 扫描 P2-3）。 */
  searched?: boolean;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: "open", dn: string): void;
  (e: "export", format: "ldif" | "csv" | "json"): void;
}>();

const PAGE_SIZE = 50;
const COLUMN_WIDTHS_KEY = "ldap.result.columnWidths.v1";
const MIN_COLUMN_WIDTH = 60;
const DEFAULT_DN_WIDTH = "minmax(200px, 2fr)";
const DEFAULT_COLUMN_WIDTH = "minmax(90px, 1fr)";

const sortColumn = ref<string>("dn");
const sortDirection = ref<"asc" | "desc">("asc");
const page = ref(0);
const selectedDn = ref("");

const columns = computed(() => extractEntryAttributeNames(props.entries).slice(0, 12));

// -- column widths（拖拽 + localStorage 持久化） -------------------------------

const columnWidths = ref<Record<string, number>>({});

function loadColumnWidths() {
  try {
    const raw = window.localStorage.getItem(COLUMN_WIDTHS_KEY);
    const parsed = raw ? (JSON.parse(raw) as unknown) : null;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const widths: Record<string, number> = {};
      for (const [name, width] of Object.entries(parsed as Record<string, unknown>)) {
        const numeric = Number(width);
        if (Number.isFinite(numeric) && numeric >= MIN_COLUMN_WIDTH) widths[name] = numeric;
      }
      columnWidths.value = widths;
    }
  } catch {
    columnWidths.value = {};
  }
}

function persistColumnWidths() {
  try {
    window.localStorage.setItem(COLUMN_WIDTHS_KEY, JSON.stringify(columnWidths.value));
  } catch {
    // storage may be unavailable (private mode); drag still works for the session
  }
}

function startColumnResize(event: PointerEvent, column: string) {
  event.preventDefault();
  event.stopPropagation();
  const headerButton = (event.currentTarget as HTMLElement).parentElement;
  const startX = event.clientX;
  const startWidth = headerButton?.getBoundingClientRect().width ?? 120;
  const onMove = (move: PointerEvent) => {
    const width = Math.max(MIN_COLUMN_WIDTH, Math.round(startWidth + move.clientX - startX));
    columnWidths.value = { ...columnWidths.value, [column]: width };
  };
  const onUp = () => {
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("pointerup", onUp);
    persistColumnWidths();
  };
  window.addEventListener("pointermove", onMove);
  window.addEventListener("pointerup", onUp);
}

onMounted(loadColumnWidths);

const gridStyle = computed(() => ({
  gridTemplateColumns: [
    columnWidths.value.dn ? `${columnWidths.value.dn}px` : DEFAULT_DN_WIDTH,
    ...columns.value.map((column) => (columnWidths.value[column] ? `${columnWidths.value[column]}px` : DEFAULT_COLUMN_WIDTH)),
  ].join(" "),
}));

const sortedEntries = computed(() => {
  const direction = sortDirection.value === "asc" ? 1 : -1;
  return [...props.entries].sort((left, right) => {
    const leftValue = sortColumn.value === "dn" ? left.dn : (left.attributes[sortColumn.value]?.[0] ?? "");
    const rightValue = sortColumn.value === "dn" ? right.dn : (right.attributes[sortColumn.value]?.[0] ?? "");
    return leftValue.localeCompare(rightValue, undefined, { numeric: true, sensitivity: "base" }) * direction;
  });
});

const pageCount = computed(() => Math.max(1, Math.ceil(sortedEntries.value.length / PAGE_SIZE)));
const pageEntries = computed(() => sortedEntries.value.slice(page.value * PAGE_SIZE, (page.value + 1) * PAGE_SIZE));

function toggleSort(column: string) {
  if (sortColumn.value === column) {
    sortDirection.value = sortDirection.value === "asc" ? "desc" : "asc";
  } else {
    sortColumn.value = column;
    sortDirection.value = "asc";
  }
}

// 表头 aria-sort（UI 扫描 P2-18）：排序列映射 ascending/descending，其余 none。
function ariaSortFor(column: string): "ascending" | "descending" | "none" {
  if (sortColumn.value !== column) return "none";
  return sortDirection.value === "asc" ? "ascending" : "descending";
}

function cellText(entry: LdapEntry, column: string): string {
  const values = entry.attributes[column];
  if (!Array.isArray(values) || values.length === 0) return "";
  const text = values.join(" | ");
  return text.length > 120 ? `${text.slice(0, 117)}…` : text;
}

function rowClick(entry: LdapEntry) {
  selectedDn.value = entry.dn;
}

function rowActivate(entry: LdapEntry) {
  selectedDn.value = entry.dn;
  emit("open", entry.dn);
}

// -- keyboard navigation（↑/↓ 移动、Enter 打开；跨页自动翻页） ----------------

function showSelected(entry: LdapEntry) {
  const index = sortedEntries.value.indexOf(entry);
  if (index >= 0 && (index < page.value * PAGE_SIZE || index >= (page.value + 1) * PAGE_SIZE)) {
    page.value = Math.floor(index / PAGE_SIZE);
  }
  void nextTick(() => {
    document.querySelector(".result-row.selected")?.scrollIntoView({ block: "nearest" });
  });
}

function moveSelection(delta: number) {
  if (sortedEntries.value.length === 0) return;
  const currentIndex = sortedEntries.value.findIndex((entry) => entry.dn === selectedDn.value);
  const nextIndex = currentIndex < 0 ? 0 : Math.min(Math.max(currentIndex + delta, 0), sortedEntries.value.length - 1);
  const entry = sortedEntries.value[nextIndex];
  selectedDn.value = entry.dn;
  showSelected(entry);
}

function onRowsKeydown(event: KeyboardEvent) {
  if (event.key === "ArrowDown") {
    event.preventDefault();
    moveSelection(1);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    moveSelection(-1);
  } else if (event.key === "Enter") {
    const entry = sortedEntries.value.find((candidate) => candidate.dn === selectedDn.value);
    if (entry) {
      event.preventDefault();
      rowActivate(entry);
    }
  }
}

watch(
  () => props.entries,
  () => {
    page.value = 0;
    selectedDn.value = "";
  },
);

const hasEntries = computed(() => props.entries.length > 0);
</script>

<template>
  <section class="result-pane">
    <div class="result-meta">
      <span>{{ t("result.count", { count }) }}<span v-if="truncated" class="truncated-badge" style="margin-left: 8px">{{ t("result.truncated") }}</span><span v-else-if="atLimit" class="truncated-badge" style="margin-left: 8px" :title="t('result.atLimit', { limit: sizeLimit ?? 0 })">{{ t("result.atLimitBadge") }}</span></span>
      <span class="pager">
        <button v-if="hasEntries" :disabled="disabled || page <= 0" :title="t('result.prevPage')" :aria-label="t('result.prevPage')" @click="page -= 1">
          <ChevronLeft aria-hidden="true" />
        </button>
        <span v-if="hasEntries" class="muted">{{ t("result.page", { page: page + 1, pages: pageCount }) }}</span>
        <button v-if="hasEntries" :disabled="disabled || page >= pageCount - 1" :title="t('result.nextPage')" :aria-label="t('result.nextPage')" @click="page += 1">
          <ChevronRight aria-hidden="true" />
        </button>
        <button v-if="hasEntries" :disabled="disabled" :title="t('result.exportLdif')" @click="emit('export', 'ldif')"><FileDown aria-hidden="true" /></button>
        <button v-if="hasEntries" :disabled="disabled" :title="t('result.exportCsv')" @click="emit('export', 'csv')"><FileSpreadsheet aria-hidden="true" /></button>
        <button v-if="hasEntries" :disabled="disabled" :title="t('result.exportJson')" @click="emit('export', 'json')"><FileJson aria-hidden="true" /></button>
      </span>
    </div>
    <div v-if="!hasEntries" class="empty">{{ props.searched ? t("result.emptyNoMatch") : t("result.empty") }}</div>
    <div
      v-else
      class="result-table"
      tabindex="0"
      :title="t('result.keyboardHint')"
      @keydown="onRowsKeydown"
    >
      <div class="result-header" :style="gridStyle">
        <button type="button" :aria-sort="ariaSortFor('dn')" @click="toggleSort('dn')">
          dn<span v-if="sortColumn === 'dn'"> {{ sortDirection === "asc" ? "▲" : "▼" }}</span>
          <span class="col-resize" @pointerdown="startColumnResize($event, 'dn')" />
        </button>
        <button v-for="column in columns" :key="column" type="button" :title="column" :aria-sort="ariaSortFor(column)" @click="toggleSort(column)">
          {{ column }}<span v-if="sortColumn === column"> {{ sortDirection === "asc" ? "▲" : "▼" }}</span>
          <span class="col-resize" @pointerdown="startColumnResize($event, column)" />
        </button>
      </div>
      <div class="result-rows">
        <button
          v-for="entry in pageEntries"
          :key="entry.dn"
          class="result-row"
          :class="{ selected: selectedDn === entry.dn }"
          :style="gridStyle"
          :title="entry.dn"
          @click="rowClick(entry)"
          @dblclick="rowActivate(entry)"
        >
          <span class="cell-dn">{{ entry.dn }}</span>
          <span v-for="column in columns" :key="column" class="cell-value">{{ cellText(entry, column) }}</span>
        </button>
      </div>
    </div>
  </section>
</template>
