<script setup lang="ts">
// 一次性消费面板：消费表单覆盖 §5.3 全参数（5 种 offset 策略、per-partition
// 精确 seek、isolation/commit 互斥、三通道过滤 + matchMode + fieldFilters、
// 时间/offset 范围、decode/decompression），消息表 + 详情抽屉（本地二次
// decode/format、valueBase64 完整查看/下载）+ JSON/CSV 导出 + 消费预设。
// commit×过滤互斥等校验在 lib/kafkaModel.validateConsumeForm（纯函数，有单测）。
// 布局压缩（R 路）：有结果后表单默认收起为一行摘要 chips 条（开合记忆
// dbx.kafka.ui.msgFormOpen），结果表格吃满剩余高度；大数据量防护见各标注。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, triggerRef, watch } from "vue";
import { ChevronDown, ChevronsDown, Download, Play, Plus, Save, Trash2, X } from "@lucide/vue";
import type { ColDef } from "ag-grid-community";
import DbxAgGrid from "./DbxAgGrid.vue";
import {
  kafkaApi,
  type ConsumeParams,
  type ConsumeResult,
  type DecodeMode,
  type Decompression,
  type FieldFilter,
  type IsolationLevel,
  type KafkaMessage,
  type MatchMode,
  type OffsetStrategy,
  type SchemaAttach,
  type SchemaSubject,
} from "../lib/api";
import {
  MINIMAL_MESSAGE_FIELDS,
  messageColumns,
  toMessageRows,
  type MessageRow,
} from "../lib/kafkaColumns";
import {
  capRows,
  decideModalKeydown,
  fieldFilterIssue,
  focusableElements,
  formatMessageValue,
  formatTimestamp,
  isRangeReversed,
  messageFullValueText,
  nowDatetimeLocal,
  offsetTimeToParam,
  offsetTimeToUnixMs,
  parsePartitionList,
  parsePartitionOffsetsText,
  switchTimeInputMode,
  truncatedValuePreview,
  validateConsumeForm,
  type DecodedValue,
  type ValueFormat,
} from "../lib/kafkaModel";
import { t } from "../lib/i18n";

const props = defineProps<{
  topic: string;
  canWrite: boolean;
  /** 连接 SR provider（Phase P）：glue 时 schema 挂载区禁用并提示（管理面 only）。 */
  srProvider?: string;
}>();

const emit = defineEmits<{
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
}>();

// -- form state ---------------------------------------------------------------

const groupId = ref("");
const offsetStrategy = ref<OffsetStrategy>("latest");
const offsetTimeText = ref("");
const partitionsText = ref("");
const partitionOffsetsText = ref("");
const limit = ref("100");
const timeoutMs = ref("5000");
const maxScanRecords = ref("10000");
const isolationLevel = ref<IsolationLevel>("read_uncommitted");
const commit = ref(false);
const filterText = ref("");
const keyFilterText = ref("");
const valueFilterText = ref("");
const headerFilterText = ref("");
const matchMode = ref<MatchMode>("contains");
const fieldFilters = ref<FieldFilter[]>([]);
const timestampFrom = ref("");
const timestampTo = ref("");
const offsetFrom = ref("");
const offsetTo = ref("");
const decode = ref<DecodeMode>("none");
const decompression = ref<Decompression>("none");

const consuming = ref(false);
// 大数据量防护：result/行数组/详情 raw 均浅响应（shallowRef）——大数组不做深度
// 代理，整体替换引用驱动更新；行上限裁剪见 applyResult/capRows。
const result = shallowRef<ConsumeResult | null>(null);
const formIssues = ref<string[]>([]);
const presets = ref<Array<{ id: string; name: string }>>([]);
const presetName = ref("");

// -- 摘要条（收起态）：开合记忆 dbx.kafka.ui.msgFormOpen；无记忆时
// 「未选 topic 或尚无结果」默认展开、消费成功后自动收起为摘要条。-------------

const MSG_FORM_OPEN_KEY = "dbx.kafka.ui.msgFormOpen";

function loadStoredFormOpen(): boolean | null {
  try {
    const raw = localStorage.getItem(MSG_FORM_OPEN_KEY);
    if (raw === "1") return true;
    if (raw === "0") return false;
  } catch {
    /* 存储不可用：走默认 */
  }
  return null;
}

const storedFormOpen = loadStoredFormOpen();
const formOpen = ref(storedFormOpen ?? true);

watch(formOpen, (open) => {
  try {
    localStorage.setItem(MSG_FORM_OPEN_KEY, open ? "1" : "0");
  } catch {
    /* 内存态即可 */
  }
});

function toggleFormOpen() {
  formOpen.value = !formOpen.value;
}

// 摘要 chips：策略文案映射（无新增 i18n key，复用既有 strategy*/formatRaw）。
const STRATEGY_LABEL_KEYS: Record<OffsetStrategy, string> = {
  latest: "messages.strategyLatest",
  earliest: "messages.strategyEarliest",
  committed: "messages.strategyCommitted",
  timestamp: "messages.strategyTimestamp",
  offset: "messages.strategyOffset",
};
const strategyLabel = computed(() => t(STRATEGY_LABEL_KEYS[offsetStrategy.value]));
const decodeLabel = computed(() =>
  decompression.value !== "none" ? `${decode.value} · ${decompression.value}` : decode.value === "none" ? t("messages.formatRaw") : decode.value,
);
// 生效过滤条件数：三+1 通道文本非空 + 启用且有值的 fieldFilters 行。
const filterCount = computed(() => {
  let count = 0;
  if (filterText.value.trim()) count += 1;
  if (keyFilterText.value.trim()) count += 1;
  if (valueFilterText.value.trim()) count += 1;
  if (headerFilterText.value.trim()) count += 1;
  count += fieldFilters.value.filter((row) => row.enabled && row.value.trim().length > 0).length;
  return count;
});

// -- 筛选区分组（基础/定位/时间与范围/过滤/解码）：前两组（基础、定位）默认展开；
// 后三组可折叠，开态记忆在 dbx.kafka.ui.msgFilters（JSON 对象，高级项折叠）。

type ConsumeGroupKey = "basic" | "locate" | "timeRange" | "filter" | "decode";
const MSG_FILTERS_KEY = "dbx.kafka.ui.msgFilters";
const GROUP_DEFAULTS: Record<ConsumeGroupKey, boolean> = { basic: true, locate: true, timeRange: true, filter: true, decode: false };
// 可折叠记忆的组 = 除「基础」「定位」外的三组（前两组常驻展开，不落盘）。
const COLLAPSIBLE_GROUPS = ["timeRange", "filter", "decode"] as const;

function loadOpenGroups(): Record<ConsumeGroupKey, boolean> {
  const open = { ...GROUP_DEFAULTS };
  try {
    const raw = JSON.parse(localStorage.getItem(MSG_FILTERS_KEY) ?? "") as Partial<Record<ConsumeGroupKey, unknown>> | null;
    if (raw && typeof raw === "object") {
      for (const key of COLLAPSIBLE_GROUPS) {
        if (typeof raw[key] === "boolean") open[key] = raw[key] as boolean;
      }
    }
  } catch {
    /* 无记忆/损坏 → 默认 */
  }
  return open;
}

const openGroups = ref(loadOpenGroups());

watch(
  openGroups,
  (value) => {
    try {
      localStorage.setItem(
        MSG_FILTERS_KEY,
        JSON.stringify(Object.fromEntries(COLLAPSIBLE_GROUPS.map((key) => [key, value[key]]))),
      );
    } catch {
      /* 存储不可用：仅内存态 */
    }
  },
  { deep: true },
);

function toggleGroup(key: ConsumeGroupKey) {
  openGroups.value[key] = !openGroups.value[key];
}

// -- 时间与范围输入（timestampFrom/To：datetime-local ↔ unix ms 双模式）---------

const tsMode = ref<"datetime" | "unix">("datetime");

const tsFromMs = computed(() => offsetTimeToUnixMs(timestampFrom.value));
const tsToMs = computed(() => offsetTimeToUnixMs(timestampTo.value));
const tsRangeReversed = computed(() => isRangeReversed(tsFromMs.value, tsToMs.value));
const tsFromInvalid = computed(() => Boolean(timestampFrom.value.trim()) && tsFromMs.value === null);
const tsToInvalid = computed(() => Boolean(timestampTo.value.trim()) && tsToMs.value === null);

function toggleTsMode() {
  const next = tsMode.value === "datetime" ? "unix" : "datetime";
  timestampFrom.value = switchTimeInputMode(timestampFrom.value, next);
  timestampTo.value = switchTimeInputMode(timestampTo.value, next);
  tsMode.value = next;
}

function setNow(target: "from" | "to") {
  if (tsMode.value === "unix") {
    if (target === "from") timestampFrom.value = String(Date.now());
    else timestampTo.value = String(Date.now());
    return;
  }
  if (target === "from") timestampFrom.value = nowDatetimeLocal();
  else timestampTo.value = nowDatetimeLocal();
}

// -- fieldFilters 行校验（数值比较 operator 需要 value 可转数字）------------------

function fieldFilterIssueKey(index: number): string | null {
  const row = fieldFilters.value[index];
  if (!row) return null;
  // fieldFilterIssue 纯函数返回片段（fieldValueNumeric）；展示统一走 uiFilterValueRequired。
  return fieldFilterIssue(row) ? "messages.uiFilterValueRequired" : null;
}

function fieldFilterIssueText(index: number): string {
  const key = fieldFilterIssueKey(index);
  return key ? t(key) : "";
}

// -- schema mount（Phase 2：SR 解码挂载，version 空 = latest）---------------------

const schemaEnabled = ref(false);
const schemaSubjects = ref<SchemaSubject[]>([]);
const schemaSubject = ref("");
const schemaVersionText = ref("");
const schemaFormat = ref<"avro" | "json">("avro");
// Phase P：Glue 仅管理面（消息编解码仅 Confluent wire format，后端 -32000 拒绝），
// 前端同步禁用挂载区并提示（保留 discoverability，不隐藏）。
const glueSchemaDisabled = computed(() => props.srProvider === "glue");
watch(glueSchemaDisabled, (disabled) => {
  if (disabled) schemaEnabled.value = false;
});
const schemaVersions = computed(() => {
  const subject = schemaSubjects.value.find((row) => row.subject === schemaSubject.value);
  const latest = subject?.latestVersion ?? 0;
  return Array.from({ length: Math.max(latest, 0) }, (_unused, index) => latest - index);
});

async function loadSchemaSubjects() {
  try {
    // registry 参数省略 = 连接默认提供方（Glue 下挂载区已禁用，此列表仅供展示兜底）。
    const response = await kafkaApi.schemaSubjectsList();
    schemaSubjects.value = response.subjects ?? [];
  } catch {
    // SR 未启用/旧 sidecar：挂载区下拉为空且可关闭，不阻断消费主流程。
    schemaSubjects.value = [];
  }
}

watch(schemaEnabled, (enabled) => {
  if (enabled && schemaSubjects.value.length === 0) void loadSchemaSubjects();
});

watch(schemaSubject, () => {
  schemaVersionText.value = "";
  const found = schemaSubjects.value.find((row) => row.subject === schemaSubject.value);
  if (found?.formats?.length) schemaFormat.value = (found.formats[0] as "avro" | "json") ?? "avro";
});

function buildSchemaAttach(): SchemaAttach | undefined {
  if (glueSchemaDisabled.value) return undefined;
  if (!schemaEnabled.value || !schemaSubject.value) return undefined;
  const version = Number.parseInt(schemaVersionText.value, 10);
  return {
    subject: schemaSubject.value,
    ...(Number.isFinite(version) && version > 0 ? { version } : {}),
    format: schemaFormat.value,
  };
}

// commit 开启后过滤通道全部禁用（§5.3：commit 与过滤互斥，后端同规则）。
const filtersDisabled = computed(() => commit.value);
// 显式 partitions 与 groupId 互斥（§5.3）。
const groupDisabled = computed(() => parsePartitionList(partitionsText.value).length > 0);
const partitionsDisabled = computed(() => commit.value || Boolean(groupId.value.trim()));
const hasFilters = computed(() => filterCount.value > 0);
const detail = shallowRef<KafkaMessage | null>(null);
// 行数组浅响应 + 引用替换（不逐条改）；rowsTotal 为裁前行数（裁剪提示用）。
const messageRows = shallowRef<MessageRow[]>([]);
const rowsTotal = ref(0);
const rowsDropped = computed(() => Math.max(0, rowsTotal.value - messageRows.value.length));
const messageCols = computed(() => messageColumns() as ColDef<MessageRow>[]);
// P1-1：结果区统计行锚点（消费后滚动目标）。
const resultMetaEl = ref<HTMLElement | null>(null);
// 表格容器（跳到最新时定位 ag-grid 视口滚动）。
const gridBoxEl = ref<HTMLElement | null>(null);

/** 消费结果落地（唯一入口）：capRows 裁剪（保留最新 N 条）→ 批量构建行数组 →
 *  引用替换一次性提交（DbxAgGrid 以单次 setGridOption 批量应用，配合稳定
 *  getRowId，无逐条更新）。 */
function applyResult(next: ConsumeResult | null) {
  result.value = next;
  const capped = capRows(next?.messages ?? []);
  messageRows.value = toMessageRows(capped.rows);
  triggerRef(messageRows);
  rowsTotal.value = capped.total;
}

function openDetail(row: MessageRow) {
  // 详情 raw 单份存储：直接引用行内 raw（与 result.messages 同一对象，不拷贝）。
  detail.value = row.raw;
}

/** 跳到最新（R 路）：滚回结果区锚点 + ag-grid 纵向视口滚到底（最新数据行可见）。
 *  ag-grid v36 起 center 视口为 .ag-grid-viewport（旧版 .ag-body-viewport），
 *  纵向滚动条 viewport 一并同步，避免滚动条与行视口脱钩。 */
function jumpToLatest() {
  resultMetaEl.value?.scrollIntoView({ block: "start" });
  const grid = gridBoxEl.value;
  if (!grid) return;
  for (const selector of [".ag-grid-viewport", ".ag-body-viewport", ".ag-body-vertical-scroll-viewport"]) {
    const viewport = grid.querySelector<HTMLElement>(selector);
    if (viewport) viewport.scrollTop = viewport.scrollHeight;
  }
}

function positiveInt(value: unknown, fallback: number): number {
  const parsed = Number.parseInt(String(value ?? "").trim(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function optionalNumber(value: string): number | undefined {
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
}

// -- field filters editor -------------------------------------------------------

function addFieldFilter() {
  fieldFilters.value.push({ source: "value", path: "", operator: "contains", value: "", enabled: true });
}

function removeFieldFilter(index: number) {
  fieldFilters.value.splice(index, 1);
}

// -- params build / validation ---------------------------------------------------

function buildParams(): ConsumeParams {
  const offsetTime = offsetStrategy.value === "timestamp" ? offsetTimeToParam(offsetTimeText.value) : null;
  const partitions = parsePartitionList(partitionsText.value);
  const partitionOffsets = parsePartitionOffsetsText(partitionOffsetsText.value);
  const params: ConsumeParams = {
    topic: props.topic,
    offsetStrategy: offsetStrategy.value,
    isolationLevel: isolationLevel.value,
    limit: positiveInt(limit.value, 100),
    timeoutMs: positiveInt(timeoutMs.value, 5000),
    maxScanRecords: positiveInt(maxScanRecords.value, 10000),
    decode: decode.value,
    decompression: decompression.value,
  };
  const schema = buildSchemaAttach();
  if (schema) params.schema = schema;
  if (groupId.value.trim() && partitions.length === 0) params.groupId = groupId.value.trim();
  if (partitions.length > 0 && !params.groupId) params.partitions = partitions;
  if (offsetStrategy.value === "offset" && Object.keys(partitionOffsets).length > 0) {
    params.partitionOffsets = partitionOffsets;
  }
  if (offsetTime !== null) params.offsetTime = offsetTime;
  if (commit.value && params.groupId) {
    params.commit = true;
    return params; // commit 与过滤互斥，不再附带任何过滤字段
  }
  if (filterText.value.trim()) params.filter = filterText.value.trim();
  if (keyFilterText.value.trim()) params.keyFilter = keyFilterText.value.trim();
  if (valueFilterText.value.trim()) params.valueFilter = valueFilterText.value.trim();
  if (headerFilterText.value.trim()) params.headerFilter = headerFilterText.value.trim();
  if (hasFilters.value) params.matchMode = matchMode.value;
  const enabledFilters = fieldFilters.value.filter((row) => row.enabled && row.value.trim().length > 0);
  if (enabledFilters.length > 0) {
    params.fieldFilters = enabledFilters.map((row) => ({
      source: row.source,
      operator: row.operator,
      value: row.value,
      ...(row.path && row.path.trim() ? { path: row.path.trim() } : {}),
    }));
  }
  const tsFrom = offsetTimeToUnixMs(timestampFrom.value); // datetime-local/unix ms/RFC3339 → unix ms；空/非法 → null
  const tsTo = offsetTimeToUnixMs(timestampTo.value);
  const offFrom = optionalNumber(offsetFrom.value);
  const offTo = optionalNumber(offsetTo.value);
  if (tsFrom !== null) params.timestampFrom = tsFrom;
  if (tsTo !== null) params.timestampTo = tsTo;
  if (offFrom !== undefined) params.offsetFrom = offFrom;
  if (offTo !== undefined) params.offsetTo = offTo;
  return params;
}

async function runConsume() {
  if (consuming.value || !props.topic) return;
  const issues = validateConsumeForm({
    commit: commit.value,
    groupId: groupId.value,
    partitionsText: partitionsText.value,
    offsetStrategy: offsetStrategy.value,
    offsetTimeText: offsetTimeText.value,
    partitionOffsetsText: partitionOffsetsText.value,
    hasFilters: hasFilters.value,
  }).map((issue) => t(`messages.${issue.key}`));
  if (tsRangeReversed.value) issues.push(t("messages.uiTimeRangeInvalid"));
  formIssues.value = issues;
  if (formIssues.value.length > 0) return;
  consuming.value = true;
  emit("error", "");
  try {
    applyResult(await kafkaApi.messagesConsume(buildParams()));
    // 有结果后表单默认收起为摘要条（localStorage 已有开合记忆时以记忆为准）。
    if (storedFormOpen === null) formOpen.value = false;
    // P1-1：消费后自动滚到结果区顶部，数据行立即可见（空态/无滚动时为 no-op）。
    await nextTick();
    resultMetaEl.value?.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    consuming.value = false;
  }
}

// -- presets ---------------------------------------------------------------------

async function loadPresets() {
  try {
    const response = await kafkaApi.presetsList();
    // type=monitor 的预设归 MonitorPanel 管（同一 store，互不混显）。
    presets.value = (response.presets ?? [])
      .filter((preset) => preset.params?.type !== "monitor")
      .map((preset) => ({ id: preset.id, name: preset.name }));
  } catch {
    presets.value = [];
  }
}

function currentFormParams(): ConsumeParams {
  return { ...buildParams(), topic: "" };
}

async function savePreset() {
  const name = presetName.value.trim();
  if (!name) return;
  try {
    await kafkaApi.presetsSave({ id: `preset-${Date.now()}`, name, params: currentFormParams() });
    presetName.value = "";
    emit("notify", t("messages.presetSaved"));
    await loadPresets();
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

async function applyPreset(id: string) {
  try {
    const response = await kafkaApi.presetsList();
    const preset = (response.presets ?? []).find((row) => row.id === id);
    if (!preset) return;
    const params = preset.params ?? {};
    groupId.value = params.groupId ?? "";
    offsetStrategy.value = params.offsetStrategy ?? "latest";
    offsetTimeText.value = typeof params.offsetTime === "string" ? params.offsetTime : params.offsetTime ? String(params.offsetTime) : "";
    partitionsText.value = (params.partitions ?? []).join(",");
    partitionOffsetsText.value = Object.entries(params.partitionOffsets ?? {})
      .map(([partition, offset]) => `${partition}=${offset}`)
      .join(",");
    limit.value = String(params.limit ?? 100);
    timeoutMs.value = String(params.timeoutMs ?? 5000);
    maxScanRecords.value = String(params.maxScanRecords ?? 10000);
    isolationLevel.value = params.isolationLevel ?? "read_uncommitted";
    commit.value = params.commit === true;
    filterText.value = params.filter ?? "";
    keyFilterText.value = params.keyFilter ?? "";
    valueFilterText.value = params.valueFilter ?? "";
    headerFilterText.value = params.headerFilter ?? "";
    matchMode.value = params.matchMode ?? "contains";
    fieldFilters.value = (params.fieldFilters ?? []).map((row) => ({ ...row, enabled: true }));
    decode.value = params.decode ?? "none";
    decompression.value = params.decompression ?? "none";
    schemaEnabled.value = Boolean(params.schema);
    schemaSubject.value = params.schema?.subject ?? "";
    schemaVersionText.value = params.schema?.version !== undefined ? String(params.schema.version) : "";
    schemaFormat.value = params.schema?.format ?? "avro";
    emit("notify", t("messages.presetApplied"));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

async function removePreset(id: string) {
  try {
    await kafkaApi.presetsRemove(id);
    await loadPresets();
    emit("notify", t("messages.presetRemoved"));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

// -- export ------------------------------------------------------------------------

async function exportMessages(format: "json" | "csv") {
  if (!result.value || result.value.messages.length === 0) return;
  try {
    const response = await kafkaApi.messagesExport({
      ...buildParams(),
      format,
      limit: Math.max(result.value.messages.length, positiveInt(limit.value, 100), 1),
    });
    downloadText(response.filename || `kafka-messages.${format}`, response.contentType, response.content);
    emit("notify", t("messages.exportDone", { name: format.toUpperCase() }));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

function downloadText(name: string, contentType: string, text: string) {
  // Host API 1.0 无 save-file 桥，Blob URL 下载为约定兜底。
  const blob = new Blob([text], { type: `${contentType};charset=utf-8` });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

// -- detail drawer（本地二次 decode/format，valueBase64 保真来源）-----------------

const viewFormat = ref<ValueFormat>("raw");
const viewDecode = ref<DecodeMode>("none");
const viewDecompression = ref<Decompression>("none");
const viewResult = ref<DecodedValue>({ text: "" });
const viewBusy = ref(false);
const showFullBase64 = ref(false);

watch(detail, (message) => {
  showFullBase64.value = false;
  if (!message) return;
  viewFormat.value = message.valueBase64 && looksJson(message) ? "json" : "raw";
  viewDecode.value = "none";
  viewDecompression.value = "none";
  void renderView();
});

function looksJson(message: KafkaMessage): boolean {
  const text = (message.valueText ?? "").trim();
  return text.startsWith("{") || text.startsWith("[");
}

async function renderView() {
  const message = detail.value;
  if (!message) return;
  viewBusy.value = true;
  try {
    viewResult.value = await formatMessageValue(message, {
      decode: viewDecode.value,
      decompression: viewDecompression.value,
      format: viewFormat.value,
    });
  } finally {
    viewBusy.value = false;
  }
}

// 大 value 截断预览（R 路）：默认只渲染头尾（DETAIL_VALUE_PREVIEW_MAX），
// 512KB 级文本不整段塞 DOM 文本节点；「查看完整 / 下载」复用既有入口。
const valuePreview = computed(() => truncatedValuePreview(viewResult.value.text));

function downloadValue() {
  const message = detail.value;
  if (!message) return;
  downloadText(`${message.topic}-p${message.partition}-o${message.offset}.txt`, "text/plain", messageFullValueText(message));
}

// -- 弹层交互（P1-2/P1-3）：抽屉 Esc 关闭 + Tab 焦点陷阱 + 关闭归还触发元素 --------
// 决策逻辑在 kafkaModel.decideModalKeydown（纯函数，有单测），这里只做 DOM 接线。

const drawerEl = ref<HTMLElement | null>(null);
let drawerTrigger: HTMLElement | null = null;

watch(detail, (message, previous) => {
  if (message && !previous) {
    // 打开：记住触发元素，下一帧焦点进抽屉（首个可交互控件，兜底抽屉容器）。
    drawerTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    void nextTick(() => {
      const drawer = drawerEl.value;
      if (!drawer) return;
      const first = focusableElements(drawer)[0];
      (first ?? drawer).focus({ preventScroll: true });
    });
  } else if (!message && previous) {
    // 关闭（Esc/✕/遮罩）：焦点归还触发元素，遮罩随 v-if 一并卸载、无残留。
    drawerTrigger?.focus({ preventScroll: true });
    drawerTrigger = null;
  }
});

function onWindowKeydown(event: KeyboardEvent) {
  if (!detail.value) return;
  // 更高层弹窗（连接弹窗 / teleport 助手弹窗）在场时让位，不抢 Esc/Tab。
  if (document.querySelector(".workbench .modal-backdrop, body > .modal-backdrop")) return;
  const drawer = drawerEl.value;
  if (!drawer) return;
  const focusables = focusableElements(drawer);
  const currentIndex = focusables.indexOf(document.activeElement as HTMLElement);
  const decision = decideModalKeydown(event.key, event.shiftKey, focusables.length, currentIndex);
  if (decision.kind === "close") {
    event.preventDefault();
    event.stopPropagation();
    detail.value = null;
  } else if (decision.kind === "focus") {
    event.preventDefault();
    event.stopPropagation();
    focusables[decision.index]?.focus();
  }
}

onMounted(() => window.addEventListener("keydown", onWindowKeydown));
onBeforeUnmount(() => window.removeEventListener("keydown", onWindowKeydown));

// topic 切换后清空旧结果（跨 topic 结果混排会误导）；无开合记忆时回到默认展开
// （「尚无结果默认展开」语义），有记忆则维持记忆。
watch(
  () => props.topic,
  () => {
    applyResult(null);
    detail.value = null;
    if (storedFormOpen === null) formOpen.value = true;
  },
);

watch(() => props.topic, () => void loadPresets(), { immediate: true });
</script>

<template>
  <section class="section-block messages-panel" :class="{ 'has-result': Boolean(result) }">
    <!-- 收起态：一行摘要条（chips 概括当前消费条件，表格吃满剩余高度） -->
    <div v-if="!formOpen" class="msg-summary-bar">
      <span class="msg-chip mono" :title="t('messages.topic')">{{ topic || "—" }}</span>
      <span class="msg-chip" :title="t('messages.offsetStrategy')">{{ strategyLabel }}</span>
      <span class="msg-chip" :title="t('messages.limit')">{{ t("messages.limit") }} {{ limit }}</span>
      <span class="msg-chip" :class="{ 'msg-chip--active': filterCount > 0 }" :title="t('messages.uiGroupFilter')">
        {{ t("messages.uiGroupFilter") }} ×{{ filterCount }}
      </span>
      <span v-if="groupId.trim()" class="msg-chip mono" :title="t('messages.groupId')">{{ groupId }}</span>
      <span class="msg-chip" :title="t('messages.decode')">{{ decodeLabel }}</span>
      <span class="msg-summary-spacer" />
      <button class="mini-button" type="button" :disabled="consuming || !topic || tsRangeReversed" @click="runConsume">
        <Play aria-hidden="true" />{{ consuming ? t("messages.running") : t("messages.run") }}
      </button>
      <button class="toolbar-button" type="button" @click="toggleFormOpen">{{ t("messages.uiShowFilters") }}</button>
    </div>

    <template v-if="formOpen">
    <div class="consume-groups">
      <!-- 基础：常用项前置 -->
      <div class="filter-group">
        <button type="button" class="filter-group-head" :aria-expanded="openGroups.basic" @click="toggleGroup('basic')">
          <ChevronDown class="chev" :class="{ folded: !openGroups.basic }" aria-hidden="true" />
          <span>{{ t("messages.uiGroupBasic") }}</span>
        </button>
        <div v-if="openGroups.basic" class="filter-group-body">
          <div class="group-grid">
            <label class="field field--wide">
              <span>{{ t("messages.topic") }}</span>
              <input :value="topic" type="text" class="mono" readonly />
            </label>
            <label class="field">
              <span>{{ t("messages.groupId") }}</span>
              <input v-model="groupId" type="text" :placeholder="t('messages.groupIdPlaceholder')" :disabled="groupDisabled" spellcheck="false" />
            </label>
            <label class="field">
              <span>{{ t("messages.limit") }}</span>
              <input v-model="limit" type="number" min="1" />
            </label>
            <label class="field">
              <span>{{ t("messages.timeoutMs") }}</span>
              <input v-model="timeoutMs" type="number" min="1" />
            </label>
            <label class="field">
              <span>{{ t("messages.maxScanRecords") }}</span>
              <input v-model="maxScanRecords" type="number" min="1" />
            </label>
          </div>
        </div>
      </div>

      <!-- 定位：offset 策略 / 分区 / 隔离 / commit -->
      <div class="filter-group">
        <button type="button" class="filter-group-head" :aria-expanded="openGroups.locate" @click="toggleGroup('locate')">
          <ChevronDown class="chev" :class="{ folded: !openGroups.locate }" aria-hidden="true" />
          <span>{{ t("messages.uiGroupPosition") }}</span>
        </button>
        <div v-if="openGroups.locate" class="filter-group-body">
          <div class="group-grid">
            <label class="field">
              <span>{{ t("messages.offsetStrategy") }}</span>
              <select v-model="offsetStrategy">
                <option value="latest">{{ t("messages.strategyLatest") }}</option>
                <option value="earliest">{{ t("messages.strategyEarliest") }}</option>
                <option value="committed">{{ t("messages.strategyCommitted") }}</option>
                <option value="timestamp">{{ t("messages.strategyTimestamp") }}</option>
                <option value="offset">{{ t("messages.strategyOffset") }}</option>
              </select>
            </label>
            <label v-if="offsetStrategy === 'timestamp'" class="field">
              <span>{{ t("messages.offsetTime") }} ({{ t("messages.offsetTimeHint") }})</span>
              <div class="time-input-row">
                <input v-model="offsetTimeText" type="text" placeholder="2026-09-05T08:30" spellcheck="false" />
                <button class="mini-button" type="button" :title="t('messages.uiTimeNow')" @click="offsetTimeText = nowDatetimeLocal()">
                  {{ t("messages.uiTimeNow") }}
                </button>
              </div>
            </label>
            <label class="field" :class="{ muted: partitionsDisabled }">
              <span>{{ t("messages.partitions") }} ({{ t("messages.partitionsHint") }})</span>
              <input v-model="partitionsText" type="text" placeholder="0,1,2" :disabled="partitionsDisabled" spellcheck="false" />
            </label>
            <label v-if="offsetStrategy === 'offset'" class="field">
              <span>{{ t("messages.partitionOffsets") }}</span>
              <input v-model="partitionOffsetsText" type="text" :placeholder="t('messages.partitionOffsetsPlaceholder')" spellcheck="false" />
            </label>
            <label class="field">
              <span>{{ t("messages.isolation") }}</span>
              <select v-model="isolationLevel">
                <option value="read_uncommitted">{{ t("messages.isolationReadUncommitted") }}</option>
                <option value="read_committed">{{ t("messages.isolationReadCommitted") }}</option>
              </select>
            </label>
            <label class="field--wide checkbox" :title="t('messages.commitHint')">
              <input v-model="commit" type="checkbox" :disabled="!canWrite" />
              <span>{{ t("messages.commit") }}</span>
            </label>
          </div>
        </div>
      </div>

      <!-- 时间与范围：时间选择器（datetime-local 秒级 / unix ms 切换）+ offset 范围 -->
      <div class="filter-group">
        <button type="button" class="filter-group-head" :aria-expanded="openGroups.timeRange" @click="toggleGroup('timeRange')">
          <ChevronDown class="chev" :class="{ folded: !openGroups.timeRange }" aria-hidden="true" />
          <span>{{ t("messages.uiGroupTime") }}</span>
          <span v-if="tsRangeReversed" class="group-summary form-error">{{ t("messages.uiTimeRangeInvalid") }}</span>
        </button>
        <div v-if="openGroups.timeRange" class="filter-group-body">
          <div class="group-grid">
            <label class="field" :class="{ 'is-invalid': tsFromInvalid }">
              <span>{{ t("messages.tsFrom") }} · {{ tsMode === "datetime" ? t("messages.timeModeDatetime") : t("messages.timeModeUnix") }}</span>
              <div class="time-input-row">
                <input
                  v-if="tsMode === 'datetime'"
                  v-model="timestampFrom"
                  type="datetime-local"
                  step="1"
                  :disabled="filtersDisabled"
                  spellcheck="false"
                />
                <input
                  v-else
                  v-model="timestampFrom"
                  type="text"
                  inputmode="numeric"
                  placeholder="1700000000000"
                  :disabled="filtersDisabled"
                  spellcheck="false"
                />
                <button class="mini-button" type="button" :title="t('messages.uiTimeNow')" :disabled="filtersDisabled" @click="setNow('from')">
                  {{ t("messages.uiTimeNow") }}
                </button>
                <button class="mini-button" type="button" :title="t('messages.uiTimeModeSwitch')" :disabled="filtersDisabled" @click="toggleTsMode">
                  {{ tsMode === "datetime" ? t("messages.timeModeUnix") : t("messages.timeModeDatetime") }}
                </button>
              </div>
            </label>
            <label class="field" :class="{ 'is-invalid': tsToInvalid }">
              <span>{{ t("messages.tsTo") }} · {{ tsMode === "datetime" ? t("messages.timeModeDatetime") : t("messages.timeModeUnix") }}</span>
              <div class="time-input-row">
                <input
                  v-if="tsMode === 'datetime'"
                  v-model="timestampTo"
                  type="datetime-local"
                  step="1"
                  :disabled="filtersDisabled"
                  spellcheck="false"
                />
                <input
                  v-else
                  v-model="timestampTo"
                  type="text"
                  inputmode="numeric"
                  placeholder="1700000000000"
                  :disabled="filtersDisabled"
                  spellcheck="false"
                />
                <button class="mini-button" type="button" :title="t('messages.uiTimeNow')" :disabled="filtersDisabled" @click="setNow('to')">
                  {{ t("messages.uiTimeNow") }}
                </button>
                <button class="mini-button" type="button" :title="t('messages.uiTimeModeSwitch')" :disabled="filtersDisabled" @click="toggleTsMode">
                  {{ tsMode === "datetime" ? t("messages.timeModeUnix") : t("messages.timeModeDatetime") }}
                </button>
              </div>
            </label>
            <label class="field">
              <span>{{ t("messages.offsetFrom") }}</span>
              <input v-model="offsetFrom" type="number" :disabled="filtersDisabled" spellcheck="false" />
            </label>
            <label class="field">
              <span>{{ t("messages.offsetTo") }}</span>
              <input v-model="offsetTo" type="number" :disabled="filtersDisabled" spellcheck="false" />
            </label>
          </div>
          <p v-if="tsRangeReversed" class="field-error">{{ t("messages.uiTimeRangeInvalid") }}</p>
        </div>
      </div>

      <!-- 过滤：三通道文本 + matchMode + fieldFilters 行式编辑器 -->
      <div class="filter-group">
        <button type="button" class="filter-group-head" :aria-expanded="openGroups.filter" @click="toggleGroup('filter')">
          <ChevronDown class="chev" :class="{ folded: !openGroups.filter }" aria-hidden="true" />
          <span>{{ t("messages.uiGroupFilter") }}</span>
        </button>
        <div v-if="openGroups.filter" class="filter-group-body">
          <div class="group-grid">
            <label class="field field--wide">
              <span>{{ t("messages.filter") }}</span>
              <input v-model="filterText" type="text" :placeholder="t('messages.filterPlaceholder')" :disabled="filtersDisabled" spellcheck="false" />
            </label>
            <label class="field">
              <span>{{ t("messages.keyFilter") }}</span>
              <input v-model="keyFilterText" type="text" :disabled="filtersDisabled" spellcheck="false" />
            </label>
            <label class="field">
              <span>{{ t("messages.valueFilter") }}</span>
              <input v-model="valueFilterText" type="text" :disabled="filtersDisabled" spellcheck="false" />
            </label>
            <label class="field">
              <span>{{ t("messages.headerFilter") }}</span>
              <input v-model="headerFilterText" type="text" :disabled="filtersDisabled" spellcheck="false" />
            </label>
            <label class="field">
              <span>{{ t("messages.matchMode") }}</span>
              <select v-model="matchMode" :disabled="filtersDisabled">
                <option value="contains">{{ t("messages.opContains") }}</option>
                <option value="prefix">{{ t("messages.opPrefix") }}</option>
                <option value="exact">{{ t("messages.opExact") }}</option>
                <option value="regex">{{ t("messages.opRegex") }}</option>
              </select>
            </label>
          </div>
          <div class="field-filters">
            <div class="filter-head">
              <span>{{ t("messages.fieldFilters") }}</span>
              <button class="qb-add" type="button" :disabled="filtersDisabled" @click="addFieldFilter">
                <Plus aria-hidden="true" />{{ t("messages.uiFilterAddCondition") }}
              </button>
            </div>
            <p v-if="fieldFilters.length === 0" class="hint">{{ t("messages.fieldFiltersEmpty") }}</p>
            <div
              v-for="(row, index) in fieldFilters"
              :key="index"
              class="field-filter-row"
              :class="{ 'is-invalid': Boolean(fieldFilterIssueKey(index)) }"
            >
              <select v-model="row.source" :disabled="filtersDisabled">
                <option value="value">{{ t("messages.sourceValue") }}</option>
                <option value="key">{{ t("messages.sourceKey") }}</option>
                <option value="header">{{ t("messages.sourceHeader") }}</option>
                <option value="topic">{{ t("messages.sourceTopic") }}</option>
                <option value="partition">{{ t("messages.sourcePartition") }}</option>
                <option value="offset">{{ t("messages.sourceOffset") }}</option>
                <option value="timestamp">{{ t("messages.sourceTimestamp") }}</option>
              </select>
              <input v-model="row.path" type="text" :placeholder="t('messages.fieldPath')" :disabled="filtersDisabled" spellcheck="false" />
              <select v-model="row.operator" :disabled="filtersDisabled">
                <option value="contains">{{ t("messages.opContains") }}</option>
                <option value="prefix">{{ t("messages.opPrefix") }}</option>
                <option value="exact">{{ t("messages.opExact") }}</option>
                <option value="regex">{{ t("messages.opRegex") }}</option>
                <option value="exists">{{ t("messages.opExists") }}</option>
                <option value="not_exists">{{ t("messages.opNotExists") }}</option>
                <option value="gt">{{ t("messages.opGt") }}</option>
                <option value="gte">{{ t("messages.opGte") }}</option>
                <option value="lt">{{ t("messages.opLt") }}</option>
                <option value="lte">{{ t("messages.opLte") }}</option>
              </select>
              <input v-model="row.value" type="text" :placeholder="t('messages.fieldValue')" :disabled="filtersDisabled" spellcheck="false" />
              <label class="checkbox" :title="t('messages.conditionEnabled')">
                <input v-model="row.enabled" type="checkbox" :disabled="filtersDisabled" />
              </label>
              <button class="row-remove" type="button" :disabled="filtersDisabled" @click="removeFieldFilter(index)">
                <Trash2 aria-hidden="true" />
              </button>
              <p v-if="fieldFilterIssueKey(index)" class="field-filter-error">{{ fieldFilterIssueText(index) }}</p>
            </div>
          </div>
        </div>
      </div>

      <!-- 解码：内层解码 / 解压 / SR 挂载（高级项，默认折叠） -->
      <div class="filter-group">
        <button type="button" class="filter-group-head" :aria-expanded="openGroups.decode" @click="toggleGroup('decode')">
          <ChevronDown class="chev" :class="{ folded: !openGroups.decode }" aria-hidden="true" />
          <span>{{ t("messages.uiGroupDecode") }}</span>
        </button>
        <div v-if="openGroups.decode" class="filter-group-body">
          <div class="group-grid">
            <label class="field">
              <span>{{ t("messages.decode") }}</span>
              <select v-model="decode">
                <option value="none">none</option>
                <option value="base64">base64</option>
              </select>
            </label>
            <label class="field">
              <span>{{ t("messages.decompression") }}</span>
              <select v-model="decompression">
                <option value="none">none</option>
                <option value="gzip">gzip</option>
                <option value="lz4">lz4</option>
                <option value="zstd">zstd</option>
                <option value="snappy">snappy</option>
              </select>
            </label>
            <label class="field--wide checkbox">
              <input v-model="schemaEnabled" type="checkbox" :disabled="glueSchemaDisabled" />
              <span>{{ t("messages.schemaMount") }}</span>
            </label>
            <template v-if="schemaEnabled">
              <label class="field">
                <span>{{ t("messages.schemaSubject") }}</span>
                <select v-model="schemaSubject" :disabled="glueSchemaDisabled">
                  <option value="">{{ t("acls.anyValue") }}</option>
                  <option v-for="subject in schemaSubjects" :key="subject.subject" :value="subject.subject">
                    {{ subject.subject }}
                  </option>
                </select>
              </label>
              <label class="field">
                <span>{{ t("messages.schemaVersion") }}</span>
                <select v-model="schemaVersionText" :disabled="glueSchemaDisabled">
                  <option value="">{{ t("messages.schemaLatest") }}</option>
                  <option v-for="version in schemaVersions" :key="version" :value="String(version)">{{ version }}</option>
                </select>
              </label>
              <label class="field">
                <span>{{ t("schemas.colFormat") }}</span>
                <select v-model="schemaFormat" :disabled="glueSchemaDisabled">
                  <option value="avro">avro</option>
                  <option value="json">json</option>
                </select>
              </label>
            </template>
          </div>
          <p v-if="glueSchemaDisabled" class="hint">{{ t("messages.schemaGlueDisabled") }}</p>
        </div>
      </div>
    </div>

    <!-- 表单尾部固定 actions：预设左侧、收起条件 + 消费主按钮右侧 -->
    <div class="form-footer">
      <div class="inline-actions">
        <select :value="''" @change="applyPreset(($event.target as HTMLSelectElement).value)">
          <option value="">{{ t("messages.presets") }}</option>
          <option v-if="presets.length === 0" disabled value="">{{ t("messages.presetEmpty") }}</option>
          <option v-for="preset in presets" :key="preset.id" :value="preset.id">{{ preset.name }}</option>
        </select>
        <input v-model="presetName" type="text" class="preset-input" :placeholder="t('messages.presetName')" spellcheck="false" />
        <button class="qb-add" type="button" :title="t('messages.presetSave')" @click="savePreset">
          <Save aria-hidden="true" />
        </button>
        <button
          v-for="preset in presets"
          :key="`rm-${preset.id}`"
          class="qb-add"
          type="button"
          :title="`${t('messages.presetRemove')}: ${preset.name}`"
          @click="removePreset(preset.id)"
        >
          <X aria-hidden="true" />
        </button>
      </div>
      <span class="footer-spacer" />
      <button class="toolbar-button" type="button" @click="toggleFormOpen">{{ t("messages.uiHideFilters") }}</button>
      <button class="primary-button primary-button--lg" type="button" :disabled="consuming || !topic || tsRangeReversed" @click="runConsume">
        <Play aria-hidden="true" />{{ consuming ? t("messages.running") : t("messages.run") }}
      </button>
    </div>
    </template>

    <div v-if="formIssues.length > 0" class="kafka-form-errors">
      <p v-for="issue in formIssues" :key="issue" class="form-error">{{ issue }}</p>
    </div>

    <div v-if="result" ref="resultMetaEl" class="result-meta">
      <span>
        {{ t("messages.scanned", { count: result.scanned }) }} · {{ t("messages.matched", { count: result.matched }) }}
        <span v-if="result.limited" class="badge badge-warn">{{ t("messages.limited") }}</span>
        <span v-if="result.hasMore" class="badge badge-warn">{{ t("messages.hasMore") }}</span>
        <span v-if="rowsDropped > 0" class="badge badge-warn" :title="t('messages.uiRowsCapped', { shown: messageRows.length, total: rowsTotal })">
          {{ t("messages.uiRowsCapped", { shown: messageRows.length, total: rowsTotal }) }}
        </span>
      </span>
      <span class="inline-actions">
        <button class="toolbar-button" :title="t('messages.uiJumpLatest')" :disabled="messageRows.length === 0" @click="jumpToLatest">
          <ChevronsDown aria-hidden="true" /><span>{{ t("messages.uiJumpLatest") }}</span>
        </button>
        <button class="toolbar-button" :title="t('messages.exportJson')" :disabled="result.messages.length === 0" @click="exportMessages('json')">
          <Download aria-hidden="true" /><span>JSON</span>
        </button>
        <button class="toolbar-button" :title="t('messages.exportCsv')" :disabled="result.messages.length === 0" @click="exportMessages('csv')">
          <Download aria-hidden="true" /><span>CSV</span>
        </button>
      </span>
    </div>

    <div v-if="result" ref="gridBoxEl" class="grid-box grid-box--fill">
      <p v-if="result.messages.length === 0" class="empty compact">{{ t("messages.noMessages") }}</p>
      <DbxAgGrid
        v-else
        table-key="messages"
        :row-data="messageRows"
        :column-defs="messageCols"
        :compact-fields="MINIMAL_MESSAGE_FIELDS"
        row-selection="single"
        @row-click="(row: unknown) => openDetail(row as MessageRow)"
      />
    </div>
    <p v-else-if="!consuming" class="empty compact">{{ t("messages.noMessages") }}</p>

    <teleport to="body">
      <div v-if="detail" class="drawer-backdrop" @click="detail = null" />
      <div v-if="detail" class="drawer" ref="drawerEl" tabindex="-1" role="dialog" aria-modal="true">
        <header>
          <span class="mono">{{ detail.topic }} · {{ detail.partition }} / {{ detail.offset }}</span>
          <button class="icon-button" :title="t('close')" @click="detail = null"><X /></button>
        </header>
        <div class="drawer-body">
          <dl class="kv-grid">
            <dt>{{ t("messages.colTimestamp") }}</dt>
            <dd>{{ formatTimestamp(detail.timestamp) }}</dd>
            <dt>{{ t("messages.colKey") }}</dt>
            <dd>{{ detail.key ?? "—" }}</dd>
            <dt>{{ t("messages.colHeaders") }}</dt>
            <dd>{{ detail.headers && Object.keys(detail.headers).length > 0 ? JSON.stringify(detail.headers) : "—" }}</dd>
            <dt v-if="detail.schemaSubject">{{ t("messages.colSchema") }}</dt>
            <dd v-if="detail.schemaSubject" class="mono">{{ detail.schemaSubject }} v{{ detail.schemaVersion ?? "?" }} (id {{ detail.schemaId ?? "—" }})</dd>
            <dt v-if="detail.decodeError">{{ t("messages.decodeError") }}</dt>
            <dd v-if="detail.decodeError" class="form-error">{{ detail.decodeError }}</dd>
          </dl>
          <div class="kafka-form kafka-form--bare">
            <label class="field">
              <span>{{ t("messages.decode") }}</span>
              <select v-model="viewDecode" @change="renderView">
                <option value="none">none</option>
                <option value="base64">base64</option>
              </select>
            </label>
            <label class="field">
              <span>{{ t("messages.decompression") }}</span>
              <select v-model="viewDecompression" @change="renderView">
                <option value="none">none</option>
                <option value="gzip">gzip</option>
                <option value="lz4">lz4</option>
                <option value="zstd">zstd</option>
                <option value="snappy">snappy</option>
              </select>
            </label>
            <label class="field">
              <span>{{ t("messages.format") }}</span>
              <select v-model="viewFormat" @change="renderView">
                <option value="raw">{{ t("messages.formatRaw") }}</option>
                <option value="json">{{ t("messages.formatJson") }}</option>
                <option value="hex">{{ t("messages.formatHex") }}</option>
                <option value="bitset">{{ t("messages.formatBitset") }}</option>
              </select>
            </label>
            <button class="toolbar-button" type="button" :title="t('messages.fullValue')" @click="showFullBase64 = !showFullBase64">
              {{ showFullBase64 ? t("messages.formatRaw") : t("messages.fullValue") }}
            </button>
            <button class="toolbar-button" type="button" :title="t('messages.downloadValue')" @click="downloadValue">
              <Download aria-hidden="true" /><span>{{ t("messages.downloadValue") }}</span>
            </button>
          </div>
          <pre v-if="showFullBase64" class="value-view">{{ detail.valueBase64 ?? detail.valueText ?? "" }}</pre>
          <pre v-else class="value-view" :class="{ error: viewResult.error }">{{ viewResult.error || valuePreview.text }}</pre>
        </div>
      </div>
    </teleport>
  </section>
</template>
