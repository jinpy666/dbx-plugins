<script setup lang="ts">
// 流式消费面板：start/stop/pause/resume + `kafka/stream/messages` 事件实时追加
// + `kafka/stream/error` 事件 + 环形缓冲历史分页（kafka/stream/messages invoke）。
// 前端展示上限 MAX_ROWS（超出丢最旧并计数 droppedRows 提示，防 OOM）；
// 自动滚动在用户上滚时暂停，回到底部恢复。
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Pause, Play, Square } from "@lucide/vue";
import { kafkaApi, type KafkaMessage, type KafkaStreamErrorEvent, type KafkaStreamMessagesEvent, type MatchMode, type OffsetStrategy, type SchemaAttach, type SchemaFormat, type SchemaSubject, type StreamStatus } from "../lib/api";
import { appendStreamRows, debounce, filterMessagesByKeyword, formatTimestamp, previewText } from "../lib/kafkaModel";
import { friendlyKafkaError } from "../lib/kafkaErrors";
import { t } from "../lib/i18n";

const MAX_ROWS = 1000;

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

const sessionId = ref("");
const paused = ref(false);
const totalScanned = ref(0);
const totalMatched = ref(0);
const bufferSize = ref(0);
const droppedRows = ref(0);
const rows = ref<KafkaMessage[]>([]);
const starting = ref(false);
const autoScroll = ref(true);
const filterText = ref("");
const matchMode = ref<MatchMode>("contains");
const limit = ref("100");
const scrollBox = ref<HTMLElement>();

// schema mount（Phase 2：流式消费同样支持 SR 解码挂载，version 空 = latest）
const schemaEnabled = ref(false);
const schemaSubjects = ref<SchemaSubject[]>([]);
const schemaSubject = ref("");
const schemaVersionText = ref("");
const schemaFormat = ref<SchemaFormat>("avro");
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
    const response = await kafkaApi.schemaSubjectsList();
    schemaSubjects.value = response.subjects ?? [];
  } catch {
    schemaSubjects.value = [];
  }
}

watch(schemaEnabled, (enabled) => {
  if (enabled && schemaSubjects.value.length === 0) void loadSchemaSubjects();
});

watch(schemaSubject, () => {
  schemaVersionText.value = "";
  const found = schemaSubjects.value.find((row) => row.subject === schemaSubject.value);
  if (found?.formats?.length) schemaFormat.value = (found.formats[0] as SchemaFormat) ?? "avro";
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

// -- lifecycle ------------------------------------------------------------------

async function start() {
  if (starting.value || !props.topic) {
    if (!props.topic) emit("notify", t("stream.topicRequired"));
    return;
  }
  starting.value = true;
  emit("error", "");
  try {
    const response = await kafkaApi.streamStart({
      topic: props.topic,
      offsetStrategy: "latest" as OffsetStrategy,
      limit: positiveInt(limit.value, 100),
      ...(filterText.value.trim() ? { filter: filterText.value.trim(), matchMode: matchMode.value } : {}),
      ...(buildSchemaAttach() ? { schema: buildSchemaAttach() } : {}),
    });
    sessionId.value = response.sessionId;
    rows.value = [];
    droppedRows.value = 0;
    historyOffset.value = 0;
    totalScanned.value = 0;
    totalMatched.value = 0;
    paused.value = false;
    emit("notify", `${t("stream.title")}: ${response.sessionId}`);
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    starting.value = false;
  }
}

async function stop() {
  if (!sessionId.value) return;
  const current = sessionId.value;
  sessionId.value = "";
  paused.value = false;
  try {
    await kafkaApi.streamStop(current);
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

async function togglePause() {
  if (!sessionId.value) return;
  try {
    const response = paused.value ? await kafkaApi.streamResume(sessionId.value) : await kafkaApi.streamPause(sessionId.value);
    applyStatus(response.status ?? {});
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

async function refreshStatus() {
  if (!sessionId.value) return;
  try {
    const response = await kafkaApi.streamStatus(sessionId.value);
    applyStatus(response.status ?? {});
  } catch {
    // 状态轮询失败不打断流；错误经事件通道到达。
  }
}

function applyStatus(status: StreamStatus) {
  paused.value = status.paused === true;
  if (typeof status.totalScanned === "number") totalScanned.value = status.totalScanned;
  if (typeof status.totalMatched === "number") totalMatched.value = status.totalMatched;
  if (typeof status.bufferSize === "number") bufferSize.value = status.bufferSize;
}

// -- event ingestion（App.vue handleEvent 转发）-----------------------------------

function pushEvent(event: KafkaStreamMessagesEvent | KafkaStreamErrorEvent) {
    // 事件内嵌错误先经 friendlyKafkaError 归一（与 App 错误横幅同源规则），
    // 未映射的原文兜底保留。
    if ("error" in event) {
      if (event.sessionId === sessionId.value) emit("error", t("stream.statusError", { error: friendlyKafkaError(String(event.error)) }));
      return;
    }
  if (event.sessionId !== sessionId.value) return;
  applyStatus({
    paused: event.paused,
    totalScanned: event.totalScanned,
    totalMatched: event.totalMatched,
    bufferSize: event.bufferSize,
  });
  if (Array.isArray(event.messages) && event.messages.length > 0) {
    const appended = appendStreamRows(rows.value, event.messages, MAX_ROWS, droppedRows.value);
    rows.value = appended.rows;
    droppedRows.value = appended.dropped;
    if (autoScroll.value) void scrollToBottom();
  }
}

const sessionActive = computed(() => Boolean(sessionId.value));
const stateLabel = computed(() => {
  if (!sessionActive.value) return t("stream.stateIdle");
  return paused.value ? t("stream.statePaused") : t("stream.stateRunning");
});

// -- scroll -----------------------------------------------------------------------

function onScroll() {
  const box = scrollBox.value;
  if (!box) return;
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
  if (!atBottom && autoScroll.value) autoScroll.value = false;
}

watch(autoScroll, (enabled) => {
  if (enabled) void scrollToBottom();
});

async function scrollToBottom() {
  await nextTick();
  const box = scrollBox.value;
  if (box) box.scrollTop = box.scrollHeight;
}

// -- history paging（环形缓冲分页；historyOffset 为当前展示窗口起点）--------------

const historyOffset = ref(0);

async function loadOlder() {
  await pageHistory(-1);
}

async function loadNewer() {
  await pageHistory(1);
}

async function pageHistory(direction: number) {
  if (!sessionId.value) return;
  try {
    const pageSize = positiveInt(limit.value, 100);
    const maxStart = Math.max(0, bufferSize.value - pageSize);
    historyOffset.value = Math.min(maxStart, Math.max(0, historyOffset.value + direction * pageSize));
    const response = await kafkaApi.streamMessages(sessionId.value, historyOffset.value, pageSize);
    if (Array.isArray(response.messages)) {
      rows.value = response.messages.slice(-MAX_ROWS);
      droppedRows.value = Math.max(0, bufferSize.value - historyOffset.value - rows.value.length);
    }
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

// 会话存在时轮询 status（轻量、失败静默），展示 buffer/paused 最新值。
let statusTimer = 0;
watch(sessionActive, (active) => {
  window.clearInterval(statusTimer);
  if (active) statusTimer = window.setInterval(() => void refreshStatus(), 5000);
});

onBeforeUnmount(() => {
  window.clearInterval(statusTimer);
  applyQuickFilter.cancel();
});

// -- 即时搜索（F6-1）：流面板是自绘行而非 ag-grid，语义对齐 Messages 表的
// quickFilter——输入防抖 150ms，只过滤已加载（缓冲内）行，不触发任何请求。
const quickFilterInput = ref("");
const quickFilter = ref("");
const applyQuickFilter = debounce((value: string) => {
  quickFilter.value = value;
}, 150);

const visibleRows = computed(() => filterMessagesByKeyword(rows.value, quickFilter.value));

function positiveInt(value: unknown, fallback: number): number {
  const parsed = Number.parseInt(String(value ?? "").trim(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

defineExpose({ pushEvent });
</script>

<template>
  <section class="section-block">
    <div class="kafka-form">
      <label class="field" style="flex: 1 1 160px">
        <span>{{ t("messages.topic") }}</span>
        <input :value="topic" type="text" class="mono" readonly />
      </label>
      <label class="field" style="flex: 1 1 180px">
        <span>{{ t("messages.filter") }}</span>
        <input v-model="filterText" type="text" :placeholder="t('messages.filterPlaceholder')" spellcheck="false" />
      </label>
      <label class="field">
        <span>{{ t("messages.matchMode") }}</span>
        <select v-model="matchMode">
          <option value="contains">{{ t("messages.opContains") }}</option>
          <option value="prefix">{{ t("messages.opPrefix") }}</option>
          <option value="exact">{{ t("messages.opExact") }}</option>
          <option value="regex">{{ t("messages.opRegex") }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ t("messages.limit") }}</span>
        <input v-model="limit" type="number" min="1" />
      </label>
      <template v-if="schemaEnabled">
        <label class="field">
          <span>{{ t("messages.schemaSubject") }}</span>
          <select v-model="schemaSubject" :disabled="glueSchemaDisabled">
            <option value="">{{ t("acls.anyValue") }}</option>
            <option v-for="subject in schemaSubjects" :key="subject.subject" :value="subject.subject">{{ subject.subject }}</option>
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
            <option value="protobuf">protobuf</option>
              </select>
        </label>
      </template>
      <label class="checkbox">
        <input v-model="schemaEnabled" type="checkbox" :disabled="glueSchemaDisabled" :title="glueSchemaDisabled ? t('messages.schemaGlueDisabled') : undefined" />
        <span>{{ t("messages.schemaMount") }}</span>
      </label>
      <button v-if="!sessionActive" class="primary-button compact" type="button" :disabled="starting || !topic" @click="start">
        <Play aria-hidden="true" />{{ t("stream.start") }}
      </button>
      <template v-else>
        <button class="toolbar-button" type="button" @click="togglePause">
          <Pause v-if="!paused" aria-hidden="true" />
          <Play v-else aria-hidden="true" />
          <span>{{ paused ? t("stream.resume") : t("stream.pause") }}</span>
        </button>
        <button class="danger-button compact" type="button" @click="stop">
          <Square aria-hidden="true" /><span>{{ t("stream.stop") }}</span>
        </button>
      </template>
      <label class="checkbox">
        <input v-model="autoScroll" type="checkbox" />
        <span>{{ t("stream.autoScroll") }}</span>
      </label>
    </div>

    <p v-if="glueSchemaDisabled" class="hint" style="padding: 0 8px">{{ t("messages.schemaGlueDisabled") }}</p>
    <p class="hint" style="padding: 4px 8px 0">{{ t("stream.sessionHint") }}</p>

    <div class="stream-meta">
      <span class="badge" :class="{ 'badge-ok': sessionActive && !paused, 'badge-warn': sessionActive && paused }">{{ stateLabel }}</span>
      <span class="mono-s">{{ sessionId || "—" }}</span>
      <span>{{ t("stream.scanned", { count: totalScanned }) }}</span>
      <span>{{ t("stream.matched", { count: totalMatched }) }}</span>
      <span>{{ t("stream.buffer", { count: bufferSize }) }}</span>
      <span v-if="droppedRows > 0" class="badge badge-warn">{{ t("stream.dropped", { count: droppedRows }) }}</span>
      <span class="tab-spacer" />
      <!-- F6-1：即时搜索（只过滤已加载行，防抖 150ms） -->
      <input
        v-model="quickFilterInput"
        class="quick-filter-input"
        type="text"
        :placeholder="t('messages.quickFilterPlaceholder')"
        :title="t('messages.quickFilterTitle')"
        spellcheck="false"
        data-testid="stream-quick-filter"
        @input="applyQuickFilter(quickFilterInput)"
      />
      <!-- P2-4：分页按钮 ≥32px 热区（原 36×20px 易脱靶），禁用态带说明 title -->
      <button class="qb-add stream-pager" type="button" :disabled="!sessionActive" :title="t('stream.loadOlder')" @click="loadOlder">{{ t("stream.loadOlder") }}</button>
      <button class="qb-add stream-pager" type="button" :disabled="!sessionActive" :title="t('stream.loadNewer')" @click="loadNewer">{{ t("stream.loadNewer") }}</button>
    </div>

    <div class="stream-log">
      <div class="stream-row" style="color: var(--muted-foreground); font-size: 10px">
        <span>P</span>
        <span>{{ t("messages.colOffset") }}</span>
        <span>{{ t("messages.colTimestamp") }}</span>
        <span>{{ t("messages.colKey") }}</span>
        <span>{{ t("messages.colValue") }}</span>
      </div>
      <div ref="scrollBox" class="stream-scroll" @scroll="onScroll">
        <p v-if="rows.length === 0" class="empty compact">{{ t("stream.noMessages") }}</p>
        <p v-else-if="visibleRows.length === 0" class="empty compact">{{ t("stream.quickFilterNoMatch") }}</p>
        <div v-for="message in visibleRows" :key="`${message.partition}:${message.offset}`" class="stream-row">
          <span class="mono-s">{{ message.partition }}</span>
          <span class="mono-s">{{ message.offset }}</span>
          <span class="mono-s">{{ formatTimestamp(message.timestamp) }}</span>
          <span class="mono-s" :title="message.key">{{ previewText(message.key, 24) }}</span>
          <span :title="message.valueText">{{ previewText(message.valueText, 140) }}</span>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* P2-4：环形缓冲「更早/更新」按钮 ≥32px 热区（原 .qb-add 20px 高易脱靶）。 */
.stream-pager {
  min-height: 32px;
  padding: 4px 14px;
  font-size: 11px;
}
/* 禁用态通用规则（cursor/复选框）已收敛至全局 style.css，此处不再重复。 */
</style>
