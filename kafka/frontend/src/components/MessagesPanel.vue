<script setup lang="ts">
// 一次性消费面板：消费表单覆盖 §5.3 全参数（5 种 offset 策略、per-partition
// 精确 seek、isolation/commit 互斥、三通道过滤 + matchMode + fieldFilters、
// 时间/offset 范围、decode/decompression），消息表 + 详情抽屉（本地二次
// decode/format、valueBase64 完整查看/下载）+ JSON/CSV 导出 + 消费预设。
// commit×过滤互斥等校验在 lib/kafkaModel.validateConsumeForm（纯函数，有单测）。
import { computed, ref, watch } from "vue";
import { Download, Play, Plus, Save, Trash2, X } from "@lucide/vue";
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
} from "../lib/api";
import {
  formatMessageValue,
  formatTimestamp,
  messageFullValueText,
  offsetTimeToParam,
  parsePartitionList,
  parsePartitionOffsetsText,
  previewText,
  validateConsumeForm,
  headersPreview,
  type DecodedValue,
  type ValueFormat,
} from "../lib/kafkaModel";
import { t } from "../lib/i18n";

const props = defineProps<{
  topic: string;
  canWrite: boolean;
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
const result = ref<ConsumeResult | null>(null);
const formIssues = ref<string[]>([]);
const presets = ref<Array<{ id: string; name: string }>>([]);
const presetName = ref("");

// commit 开启后过滤通道全部禁用（§5.3：commit 与过滤互斥，后端同规则）。
const filtersDisabled = computed(() => commit.value);
// 显式 partitions 与 groupId 互斥（§5.3）。
const groupDisabled = computed(() => parsePartitionList(partitionsText.value).length > 0);
const partitionsDisabled = computed(() => commit.value || Boolean(groupId.value.trim()));
const hasFilters = computed(
  () =>
    Boolean(filterText.value.trim() || keyFilterText.value.trim() || valueFilterText.value.trim() || headerFilterText.value.trim()) ||
    fieldFilters.value.some((row) => row.enabled && row.value.trim().length > 0),
);
const detail = ref<KafkaMessage | null>(null);

function positiveInt(value: string, fallback: number): number {
  const parsed = Number.parseInt(value.trim(), 10);
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
  const tsFrom = optionalNumber(timestampFrom.value);
  const tsTo = optionalNumber(timestampTo.value);
  const offFrom = optionalNumber(offsetFrom.value);
  const offTo = optionalNumber(offsetTo.value);
  if (tsFrom !== undefined) params.timestampFrom = tsFrom;
  if (tsTo !== undefined) params.timestampTo = tsTo;
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
  });
  formIssues.value = issues.map((issue) => t(`messages.${issue.key}`));
  if (formIssues.value.length > 0) return;
  consuming.value = true;
  emit("error", "");
  try {
    result.value = await kafkaApi.messagesConsume(buildParams());
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
    presets.value = (response.presets ?? []).map((preset) => ({ id: preset.id, name: preset.name }));
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

function downloadValue() {
  const message = detail.value;
  if (!message) return;
  downloadText(`${message.topic}-p${message.partition}-o${message.offset}.txt`, "text/plain", messageFullValueText(message));
}

// topic 切换后清空旧结果（跨 topic 结果混排会误导）。
watch(
  () => props.topic,
  () => {
    result.value = null;
    detail.value = null;
  },
);

watch(() => props.topic, () => void loadPresets(), { immediate: true });
</script>

<template>
  <section class="section-block">
    <div class="kafka-form">
      <label class="field" style="flex: 1 1 160px">
        <span>{{ t("messages.topic") }}</span>
        <input :value="topic" type="text" class="mono" readonly />
      </label>
      <label class="field" style="flex: 1 1 150px">
        <span>{{ t("messages.groupId") }}</span>
        <input v-model="groupId" type="text" :placeholder="t('messages.groupIdPlaceholder')" :disabled="groupDisabled" spellcheck="false" />
      </label>
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
        <input v-model="offsetTimeText" type="text" placeholder="2026-09-05T08:30" spellcheck="false" />
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
      <label class="field">
        <span>{{ t("messages.isolation") }}</span>
        <select v-model="isolationLevel">
          <option value="read_uncommitted">{{ t("messages.isolationReadUncommitted") }}</option>
          <option value="read_committed">{{ t("messages.isolationReadCommitted") }}</option>
        </select>
      </label>
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
      <label class="checkbox" :title="t('messages.commitHint')">
        <input v-model="commit" type="checkbox" :disabled="!canWrite" />
        <span>{{ t("messages.commit") }}</span>
      </label>
      <button class="primary-button compact" type="button" :disabled="consuming || !topic" @click="runConsume">
        <Play aria-hidden="true" />{{ consuming ? t("messages.running") : t("messages.run") }}
      </button>
    </div>

    <div class="kafka-form">
      <label class="field" style="flex: 1 1 200px">
        <span>{{ t("messages.filter") }}</span>
        <input v-model="filterText" type="text" :placeholder="t('messages.filterPlaceholder')" :disabled="filtersDisabled" spellcheck="false" />
      </label>
      <label class="field" style="flex: 1 1 140px">
        <span>{{ t("messages.keyFilter") }}</span>
        <input v-model="keyFilterText" type="text" :disabled="filtersDisabled" spellcheck="false" />
      </label>
      <label class="field" style="flex: 1 1 140px">
        <span>{{ t("messages.valueFilter") }}</span>
        <input v-model="valueFilterText" type="text" :disabled="filtersDisabled" spellcheck="false" />
      </label>
      <label class="field" style="flex: 1 1 140px">
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
      <label class="field">
        <span>{{ t("messages.tsFrom") }}</span>
        <input v-model="timestampFrom" type="number" :disabled="filtersDisabled" spellcheck="false" />
      </label>
      <label class="field">
        <span>{{ t("messages.tsTo") }}</span>
        <input v-model="timestampTo" type="number" :disabled="filtersDisabled" spellcheck="false" />
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

    <div class="kafka-form" style="flex-direction: column; align-items: stretch">
      <div class="field-filters">
        <div class="filter-head">
          <span>{{ t("messages.fieldFilters") }}</span>
          <button class="qb-add" type="button" :disabled="filtersDisabled" @click="addFieldFilter">
            <Plus aria-hidden="true" />{{ t("messages.fieldFiltersAdd") }}
          </button>
        </div>
        <p v-if="fieldFilters.length === 0" class="hint">{{ t("messages.fieldFiltersEmpty") }}</p>
        <div v-for="(row, index) in fieldFilters" :key="index" class="field-filter-row">
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
          <label class="checkbox"><input v-model="row.enabled" type="checkbox" :disabled="filtersDisabled" /></label>
          <button class="row-remove" type="button" :disabled="filtersDisabled" @click="removeFieldFilter(index)">
            <Trash2 aria-hidden="true" />
          </button>
        </div>
      </div>
      <div class="inline-actions" style="margin-top: 6px">
        <select :value="''" @change="applyPreset(($event.target as HTMLSelectElement).value)">
          <option value="">{{ t("messages.presets") }}</option>
          <option v-if="presets.length === 0" disabled value="">{{ t("messages.presetEmpty") }}</option>
          <option v-for="preset in presets" :key="preset.id" :value="preset.id">{{ preset.name }}</option>
        </select>
        <input v-model="presetName" type="text" :placeholder="t('messages.presetName')" style="width: 140px" spellcheck="false" />
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
    </div>

    <div v-if="formIssues.length > 0" class="kafka-form-errors">
      <p v-for="issue in formIssues" :key="issue" class="form-error">{{ issue }}</p>
    </div>

    <div v-if="result" class="result-meta">
      <span>
        {{ t("messages.scanned", { count: result.scanned }) }} · {{ t("messages.matched", { count: result.matched }) }}
        <span v-if="result.limited" class="badge badge-warn">{{ t("messages.limited") }}</span>
        <span v-if="result.hasMore" class="badge badge-warn">{{ t("messages.hasMore") }}</span>
      </span>
      <span class="inline-actions">
        <button class="toolbar-button" :title="t('messages.exportJson')" :disabled="result.messages.length === 0" @click="exportMessages('json')">
          <Download aria-hidden="true" /><span>JSON</span>
        </button>
        <button class="toolbar-button" :title="t('messages.exportCsv')" :disabled="result.messages.length === 0" @click="exportMessages('csv')">
          <Download aria-hidden="true" /><span>CSV</span>
        </button>
      </span>
    </div>

    <div v-if="result" class="kafka-table">
      <div class="kafka-table-header kafka-table-cols">
        <span>{{ t("messages.colPartition") }}</span>
        <span>{{ t("messages.colOffset") }}</span>
        <span>{{ t("messages.colTimestamp") }}</span>
        <span>{{ t("messages.colKey") }}</span>
        <span>{{ t("messages.colValue") }}</span>
        <span>{{ t("messages.colHeaders") }}</span>
      </div>
      <div class="kafka-table-rows">
        <p v-if="result.messages.length === 0" class="empty compact">{{ t("messages.noMessages") }}</p>
        <button
          v-for="message in result.messages"
          :key="`${message.partition}:${message.offset}`"
          type="button"
          class="kafka-table-row kafka-table-cols"
          @click="detail = message"
        >
          <span class="mono-s">{{ message.partition }}</span>
          <span class="mono-s">{{ message.offset }}</span>
          <span class="mono-s">{{ formatTimestamp(message.timestamp) }}</span>
          <span class="mono-s" :title="message.key">{{ previewText(message.key, 40) }}</span>
          <span :title="message.decodeError || message.valueText">
            {{ previewText(message.valueText, 80) }}
            <span v-if="message.truncated" class="badge badge-warn">{{ t("messages.truncated") }}</span>
            <span v-if="message.committed" class="badge badge-ok">✓</span>
          </span>
          <span class="mono-s">{{ headersPreview(message.headers) }}</span>
        </button>
      </div>
    </div>
    <p v-else-if="!consuming" class="empty compact">{{ t("messages.noMessages") }}</p>

    <teleport to="body">
      <div v-if="detail" class="drawer-backdrop" @click="detail = null" />
      <div v-if="detail" class="drawer">
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
            <dt v-if="detail.decodeError">{{ t("messages.decodeError") }}</dt>
            <dd v-if="detail.decodeError" class="form-error">{{ detail.decodeError }}</dd>
          </dl>
          <div class="kafka-form" style="border: 0; padding: 0">
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
          <pre v-else class="value-view" :class="{ error: viewResult.error }">{{ viewResult.error || viewResult.text }}</pre>
        </div>
      </div>
    </teleport>
  </section>
</template>

<style scoped>
.kafka-table-cols {
  grid-template-columns: 44px 80px 130px minmax(80px, 1fr) minmax(160px, 2.2fr) minmax(100px, 1fr);
}
</style>
