<script setup lang="ts">
// 流式消费面板：start/stop/pause/resume + `kafka/stream/messages` 事件实时追加
// + `kafka/stream/error` 事件 + 环形缓冲历史分页（kafka/stream/messages invoke）。
// 前端展示上限 MAX_ROWS（超出丢最旧并计数 droppedRows 提示，防 OOM）；
// 自动滚动在用户上滚时暂停，回到底部恢复。
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Pause, Play, Square } from "@lucide/vue";
import { kafkaApi, type KafkaMessage, type KafkaStreamErrorEvent, type KafkaStreamMessagesEvent, type MatchMode, type OffsetStrategy, type StreamStatus } from "../lib/api";
import { appendStreamRows, formatTimestamp, previewText } from "../lib/kafkaModel";
import { t } from "../lib/i18n";

const MAX_ROWS = 1000;

const props = defineProps<{
  topic: string;
  canWrite: boolean;
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
  if ("error" in event) {
    if (event.sessionId === sessionId.value) emit("error", t("stream.statusError", { error: String(event.error) }));
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
});

function positiveInt(value: string, fallback: number): number {
  const parsed = Number.parseInt(value.trim(), 10);
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

    <p class="hint" style="padding: 4px 8px 0">{{ t("stream.sessionHint") }}</p>

    <div class="stream-meta">
      <span class="badge" :class="{ 'badge-ok': sessionActive && !paused, 'badge-warn': sessionActive && paused }">{{ stateLabel }}</span>
      <span class="mono-s">{{ sessionId || "—" }}</span>
      <span>{{ t("stream.scanned", { count: totalScanned }) }}</span>
      <span>{{ t("stream.matched", { count: totalMatched }) }}</span>
      <span>{{ t("stream.buffer", { count: bufferSize }) }}</span>
      <span v-if="droppedRows > 0" class="badge badge-warn">{{ t("stream.dropped", { count: droppedRows }) }}</span>
      <span class="tab-spacer" />
      <button class="qb-add" type="button" :disabled="!sessionActive" @click="loadOlder">{{ t("stream.loadOlder") }}</button>
      <button class="qb-add" type="button" :disabled="!sessionActive" @click="loadNewer">{{ t("stream.loadNewer") }}</button>
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
        <div v-for="message in rows" :key="`${message.partition}:${message.offset}`" class="stream-row">
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
