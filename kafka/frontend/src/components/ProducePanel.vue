<script setup lang="ts">
// 生产面板：key/value/headers(JSON 校验)/partition/count≤1000/compression，
// 发送回显 partition/offset。read_only 下整体禁用 + 提示（后端同规则拒绝）。
import { computed, ref } from "vue";
import { Send } from "@lucide/vue";
import { kafkaApi, type Compression, type ProduceResult } from "../lib/api";
import { parseHeadersJson } from "../lib/kafkaModel";
import { t } from "../lib/i18n";

const props = defineProps<{
  topic: string;
  canWrite: boolean;
}>();

const emit = defineEmits<{
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
}>();

const key = ref("");
const value = ref("");
const headersText = ref("");
const partitionText = ref("");
const count = ref("1");
const compression = ref<Compression>("none");
const sending = ref(false);
const lastResult = ref<ProduceResult | null>(null);
const localError = ref("");

const disabled = computed(() => !props.canWrite || sending.value || !props.topic);

function positiveInt(value_: string, max: number, fallback: number): number {
  const parsed = Number.parseInt(value_.trim(), 10);
  if (!Number.isFinite(parsed) || parsed <= 0) return fallback;
  return Math.min(parsed, max);
}

async function send() {
  if (disabled.value) return;
  localError.value = "";
  emit("error", "");
  if (!value.value) {
    localError.value = t("produce.valueRequired");
    return;
  }
  const headers = parseHeadersJson(headersText.value);
  if ("error" in headers) {
    localError.value = t("produce.headersInvalid", { error: headers.error });
    return;
  }
  const partition = partitionText.value.trim() === "" ? undefined : Number.parseInt(partitionText.value, 10);
  if (partition !== undefined && (!Number.isInteger(partition) || partition < 0)) {
    localError.value = t("err.partition");
    return;
  }
  sending.value = true;
  try {
    lastResult.value = await kafkaApi.messagesProduce({
      topic: props.topic,
      ...(key.value ? { key: key.value } : {}),
      value: value.value,
      ...(Object.keys(headers.headers).length > 0 ? { headers: headers.headers } : {}),
      ...(partition !== undefined ? { partition } : {}),
      count: positiveInt(count.value, 1000, 1),
      ...(compression.value !== "none" ? { compression: compression.value } : {}),
    });
    emit(
      "notify",
      `${t("produce.sent")} · ${t("produce.sentTo", { partition: lastResult.value.partition, offset: lastResult.value.offset })}`,
    );
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    sending.value = false;
  }
}
</script>

<template>
  <section class="section-block">
    <p v-if="!canWrite" class="form-error" style="padding: 6px 8px 0">{{ t("produce.readOnlyHint") }}</p>
    <div class="kafka-form" style="flex-direction: column; align-items: stretch">
      <label class="field" style="flex: 1 1 100%">
        <span>{{ t("messages.topic") }}</span>
        <input :value="topic" type="text" class="mono" readonly />
      </label>
      <label class="field" style="flex: 1 1 100%">
        <span>Key</span>
        <input v-model="key" type="text" :placeholder="t('produce.keyPlaceholder')" :disabled="disabled" spellcheck="false" />
      </label>
      <label class="field" style="flex: 1 1 100%">
        <span>Value</span>
        <textarea v-model="value" rows="6" :placeholder="t('produce.valuePlaceholder')" :disabled="disabled" spellcheck="false" />
      </label>
      <label class="field" style="flex: 1 1 100%">
        <span>{{ t("produce.headers") }}</span>
        <textarea v-model="headersText" rows="2" :placeholder="t('produce.headersPlaceholder')" :disabled="disabled" spellcheck="false" />
      </label>
      <div class="kafka-form" style="border: 0; padding: 0">
        <label class="field">
          <span>{{ t("produce.partition") }}</span>
          <input v-model="partitionText" type="number" min="0" :placeholder="t('produce.partitionAny')" :disabled="disabled" spellcheck="false" />
        </label>
        <label class="field">
          <span>{{ t("produce.count") }} (≤1000)</span>
          <input v-model="count" type="number" min="1" max="1000" :disabled="disabled" />
        </label>
        <label class="field">
          <span>{{ t("produce.compression") }}</span>
          <select v-model="compression" :disabled="disabled">
            <option value="none">none</option>
            <option value="gzip">gzip</option>
            <option value="lz4">lz4</option>
            <option value="zstd">zstd</option>
            <option value="snappy">snappy</option>
          </select>
        </label>
        <button class="primary-button compact" type="button" :disabled="disabled" @click="send">
          <Send aria-hidden="true" />{{ t("produce.send") }}
        </button>
      </div>
      <p v-if="localError" class="form-error">{{ localError }}</p>
      <p v-if="lastResult" class="hint">
        {{ t("produce.sent") }} · {{ t("produce.sentTo", { partition: lastResult.partition, offset: lastResult.offset }) }}
      </p>
    </div>
  </section>
</template>
