<script setup lang="ts">
// 连接状态面板：kafka/connections/statuses（多连接 status/lastError/lastUsedAt，
// 外加 allowDelete 门禁徽标）。
import { ref, watch } from "vue";
import { Network, RefreshCw, X } from "@lucide/vue";
import { kafkaApi, type KafkaConnectionStatus } from "../lib/api";
import { t } from "../lib/i18n";

const props = defineProps<{
  open: boolean;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "error", message: string): void;
}>();

const statuses = ref<KafkaConnectionStatus[]>([]);
const loading = ref(false);

async function load() {
  if (props.disabled || loading.value) return;
  loading.value = true;
  try {
    const result = await kafkaApi.connectionStatuses();
    statuses.value = Array.isArray(result.statuses) ? result.statuses : [];
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    loading.value = false;
  }
}

function stateLabel(state: string): string {
  if (state === "connected") return t("connections.stateConnected");
  if (state === "error") return t("connections.stateError");
  return t("connections.stateIdle");
}

function stateDotClass(state: string): string {
  if (state === "connected") return "connected";
  if (state === "error") return "error";
  return "idle";
}

function formatTime(value?: number | string): string {
  if (value === undefined || value === null || value === "") return "";
  const parsed = typeof value === "number" ? value : Date.parse(value);
  if (!Number.isFinite(parsed)) return String(value);
  return new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" }).format(new Date(parsed));
}

watch(
  () => props.open,
  (open) => {
    if (open) void load();
  },
  { immediate: true },
);
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="emit('close')">
    <div class="modal small-modal">
      <header>
        <h2><Network aria-hidden="true" style="width: 14px; height: 14px" /> {{ t("connections.title") }}</h2>
        <span class="actions" style="display: flex; gap: 2px">
          <button class="icon-button" :title="t('refresh')" @click="load"><RefreshCw :class="{ spinning: loading }" /></button>
          <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
        </span>
      </header>
      <ul v-if="statuses.length > 0" class="settings-list">
        <li v-for="status in statuses" :key="status.connectionId">
          <span class="state-dot" :class="stateDotClass(status.status)" :title="stateLabel(status.status)" />
          <div class="settings-list-main">
            <strong class="mono">{{ status.connectionId }}</strong>
            <span v-if="status.lastUsedAt">{{ t("connections.lastUsed") }}: {{ formatTime(status.lastUsedAt) }}</span>
            <span v-if="status.lastError" class="form-error">{{ t("connections.lastError") }}: {{ status.lastError }}</span>
          </div>
          <span class="inline-actions">
            <span v-if="status.readOnly" class="badge">{{ t("readOnly") }}</span>
            <span v-if="status.allowDelete === false" class="badge badge-danger">{{ t("connections.noDelete") }}</span>
            <span class="muted">{{ stateLabel(status.status) }}</span>
          </span>
        </li>
      </ul>
      <p v-else class="empty compact">{{ t("connections.empty") }}</p>
      <footer>
        <button type="button" @click="emit('close')">{{ t("close") }}</button>
      </footer>
    </div>
  </div>
</template>
