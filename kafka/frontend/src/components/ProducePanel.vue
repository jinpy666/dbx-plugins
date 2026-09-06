<script setup lang="ts">
// 生产面板：key/value/headers(JSON 校验)/partition/count≤1000/compression，
// 发送回显 partition/offset。read_only 下整体禁用 + 提示（后端同规则拒绝）。
// 布局（用户反馈重构）：单列竖排全宽——topic 只读行 → key(+base64) → value
// 大编辑器（CodeEditor json 模式，flex-grow 占面板剩余高度）→ headers 编辑器
// （实时 JSON 校验红框+错误提示）→ 发送选项栅格（partition/count/compression/
// schema 挂载区，Glue 禁用逻辑保留）→ 底部大号主色发送按钮 + 清空按钮；
// 发送结果渲染为醒目成功条。不写死 inline style，尺寸走 DBX 令牌与 class。
import { computed, ref, watch } from "vue";
import { CircleCheck, Send } from "@lucide/vue";
import CodeEditor from "./CodeEditor.vue";
import { kafkaApi, type Compression, type ProduceResult, type SchemaAttach, type SchemaSubject } from "../lib/api";
import { isInternalTopicName, parseHeadersJson } from "../lib/kafkaModel";
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

const key = ref("");
const value = ref("");
const headersText = ref("");
const partitionText = ref("");
const count = ref("1");
const compression = ref<Compression>("none");
const sending = ref(false);
const lastResult = ref<ProduceResult | null>(null);
const localError = ref("");

const topicInternal = computed(() => isInternalTopicName(props.topic));

// Phase 2：base64 直发切换（key/value 输入按 base64 解释，载荷走 keyBase64/valueBase64）
const keyIsBase64 = ref(false);
const valueIsBase64 = ref(false);
// Phase 2：schema 挂载（注册侧字段映射由 sidecar 完成）
const schemaEnabled = ref(false);
const schemaSubjects = ref<SchemaSubject[]>([]);
const schemaSubject = ref("");
const schemaVersionText = ref("");
const schemaFormat = ref<"avro" | "json">("avro");
// Phase P：Glue 仅管理面（消息编解码仅 Confluent wire format，后端 -32000 拒绝），
// 前端同步禁用挂载区并提示（保留 discoverability，不隐藏）。
const glueSchemaDisabled = computed(() => props.srProvider === "glue");
watch(glueSchemaDisabled, (glueDisabled) => {
  if (glueDisabled) schemaEnabled.value = false;
});

// headers 编辑器实时校验：错误既驱动 CodeEditor 红框，也保留发送前拦截。
const headersInvalid = computed(() => {
  const headers = parseHeadersJson(headersText.value);
  return "error" in headers ? headers.error : "";
});

const sendCount = computed(() => positiveInt(count.value, 1000, 1));

const schemaVersions = computed(() => {
  const subject = schemaSubjects.value.find((row) => row.subject === schemaSubject.value);
  const latest = subject?.latestVersion ?? 0;
  return Array.from({ length: Math.max(latest, 0) }, (_unused, index) => latest - index);
});

watch(schemaSubject, () => {
  schemaVersionText.value = "";
  const found = schemaSubjects.value.find((row) => row.subject === schemaSubject.value);
  if (found?.formats?.length) schemaFormat.value = (found.formats[0] as "avro" | "json") ?? "avro";
});

const disabled = computed(() => !props.canWrite || sending.value || !props.topic);

// 校验门禁：headers JSON 非法或 value 为空时禁用发送（原因透出到 title/aria-label）。
const sendDisabled = computed(() => disabled.value || !value.value || headersInvalid.value !== "");
const sendDisabledReason = computed(() => {
  if (!props.canWrite) return t("produce.readOnlyHint");
  if (!value.value) return t("produce.valueRequired");
  if (headersInvalid.value) return t("produce.headersInvalid", { error: headersInvalid.value });
  return "";
});

async function loadSchemaSubjects() {
  try {
    const response = await kafkaApi.schemaSubjectsList();
    schemaSubjects.value = response.subjects ?? [];
  } catch {
    schemaSubjects.value = [];
  }
}

function toggleSchema() {
  if (glueSchemaDisabled.value) return;
  schemaEnabled.value = !schemaEnabled.value;
  if (schemaEnabled.value && schemaSubjects.value.length === 0) void loadSchemaSubjects();
}

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

// 入参归一为 String（type=number 的 v-model 可能给出 number，直接 .trim 会运行时抛错）。
function positiveInt(value_: unknown, max: number, fallback: number): number {
  const parsed = Number.parseInt(String(value_ ?? "").trim(), 10);
  if (!Number.isFinite(parsed) || parsed <= 0) return fallback;
  return Math.min(parsed, max);
}

/** 清空消息草稿（key/value/headers/base64 标记/回显），发送选项保持不动。 */
function clearDraft() {
  key.value = "";
  value.value = "";
  headersText.value = "";
  keyIsBase64.value = false;
  valueIsBase64.value = false;
  lastResult.value = null;
  localError.value = "";
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
  // String 归一：type=number 的 v-model 运行时可能给 number，直接 .trim 会抛错。
  const partitionRaw = String(partitionText.value ?? "").trim();
  const partition = partitionRaw === "" ? undefined : Number.parseInt(partitionRaw, 10);
  if (partition !== undefined && (!Number.isInteger(partition) || partition < 0)) {
    localError.value = t("err.partition");
    return;
  }
  sending.value = true;
  try {
    const schema = buildSchemaAttach();
    lastResult.value = await kafkaApi.messagesProduce({
      topic: props.topic,
      ...(key.value
        ? keyIsBase64.value
          ? { keyBase64: key.value }
          : { key: key.value }
        : {}),
      ...(valueIsBase64.value ? { valueBase64: value.value } : { value: value.value }),
      ...(Object.keys(headers.headers).length > 0 ? { headers: headers.headers } : {}),
      ...(partition !== undefined ? { partition } : {}),
      count: positiveInt(count.value, 1000, 1),
      ...(compression.value !== "none" ? { compression: compression.value } : {}),
      ...(schema ? { schema } : {}),
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
  <section class="section-block produce-panel">
    <p v-if="!canWrite" class="form-error produce-readonly-hint">{{ t("produce.readOnlyHint") }}</p>
    <div class="kafka-form produce-form">
      <div class="field produce-field-full">
        <span>{{ t("messages.topic") }}</span>
        <div class="produce-topic-row">
          <input :value="topic" type="text" class="mono" readonly />
          <span v-if="topicInternal" class="badge badge-internal">{{ t("tree.internal") }}</span>
        </div>
      </div>

      <div class="produce-row">
        <label class="field produce-grow-field">
          <span>{{ t("produce.keyLabel") }}</span>
          <input v-model="key" type="text" :placeholder="t('produce.keyPlaceholder')" :disabled="disabled" spellcheck="false" />
        </label>
        <label class="checkbox">
          <input v-model="keyIsBase64" type="checkbox" :disabled="disabled" />
          <span>{{ t("produce.base64Key") }}</span>
        </label>
      </div>

      <div class="produce-editor-block produce-editor-block--value">
        <div class="produce-editor-head">
          <span>{{ t("produce.valueLabel") }}</span>
          <span v-if="value" class="produce-char-count">{{ t("produce.charCount", { count: value.length }) }}</span>
          <span class="produce-head-spacer"></span>
          <label class="checkbox">
            <input v-model="valueIsBase64" type="checkbox" :disabled="disabled" />
            <span>{{ t("produce.base64Value") }}</span>
          </label>
        </div>
        <CodeEditor
          v-model="value"
          language="json"
          class="produce-value-editor"
          :disabled="disabled"
          :placeholder="t('produce.valuePlaceholder')"
          min-height="240px"
        />
      </div>

      <div class="produce-editor-block">
        <div class="produce-editor-head">
          <span>{{ t("produce.headers") }}</span>
        </div>
        <CodeEditor
          v-model="headersText"
          language="json"
          :disabled="disabled"
          :invalid="headersInvalid !== ''"
          :placeholder="t('produce.headersPlaceholder')"
          min-height="96px"
          max-height="160px"
        />
        <p v-if="headersInvalid" class="form-error">{{ t("produce.headersInvalid", { error: headersInvalid }) }}</p>
      </div>

      <div class="produce-options">
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
        <div class="field produce-schema-field">
          <label class="checkbox">
            <input type="checkbox" :checked="schemaEnabled" :disabled="disabled || glueSchemaDisabled" @change="toggleSchema" />
            <span>{{ t("messages.schemaMount") }}</span>
          </label>
          <div v-if="schemaEnabled" class="produce-schema-grid">
            <label class="field">
              <span>{{ t("messages.schemaSubject") }}</span>
              <select v-model="schemaSubject" :disabled="disabled || glueSchemaDisabled">
                <option value="">{{ t("acls.anyValue") }}</option>
                <option v-for="subject in schemaSubjects" :key="subject.subject" :value="subject.subject">{{ subject.subject }}</option>
              </select>
            </label>
            <label class="field">
              <span>{{ t("messages.schemaVersion") }}</span>
              <select v-model="schemaVersionText" :disabled="disabled || glueSchemaDisabled">
                <option value="">{{ t("messages.schemaLatest") }}</option>
                <option v-for="version in schemaVersions || []" :key="version" :value="String(version)">{{ version }}</option>
              </select>
            </label>
            <label class="field">
              <span>{{ t("schemas.colFormat") }}</span>
              <select v-model="schemaFormat" :disabled="disabled || glueSchemaDisabled">
                <option value="avro">avro</option>
                <option value="json">json</option>
              </select>
            </label>
          </div>
          <p v-if="glueSchemaDisabled" class="hint">{{ t("messages.schemaGlueDisabled") }}</p>
        </div>
      </div>

      <div v-if="lastResult" class="produce-success" role="status">
        <CircleCheck aria-hidden="true" />
        <span>
          <strong>{{ t("produce.sent") }}</strong> ·
          {{ t("produce.sentTo", { partition: lastResult.partition, offset: lastResult.offset }) }}
        </span>
      </div>

      <div class="produce-actions">
        <p v-if="localError" class="form-error produce-error">{{ localError }}</p>
        <span class="produce-head-spacer"></span>
        <button class="produce-ghost-button" type="button" :disabled="disabled" @click="clearDraft">
          {{ t("produce.clear") }}
        </button>
        <button
          class="primary-button produce-send-button"
          type="button"
          :disabled="sendDisabled"
          :title="sendDisabledReason || undefined"
          :aria-label="sendDisabledReason || t('produce.send')"
          @click="send"
        >
          <Send aria-hidden="true" />
          {{ sendCount > 1 ? t("produce.sendCount", { count: sendCount }) : t("produce.send") }}
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* 面板撑满 main-pane 剩余高度，供 value 编辑器 flex-grow。 */
.produce-panel {
  flex: 1 1 auto;
  min-height: 0;
}
.produce-readonly-hint {
  padding: 6px 8px 0;
}
/* 单列竖排：覆盖 .kafka-form 的横排 wrap/border，其余（.field/.checkbox/输入
   尺寸 26px/字号 12px）沿用全局 DBX 令牌样式。 */
.produce-form {
  flex: 1 1 auto;
  min-height: 0;
  flex-direction: column;
  flex-wrap: nowrap;
  align-items: stretch;
  gap: 10px;
  overflow: auto;
  border-bottom: 0;
}
.produce-field-full {
  /* 竖排：topic 只读行不参与剩余高度分配（否则与 value 编辑器争抢 flex 空间） */
  flex: 0 0 auto;
}
.produce-topic-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
}
.produce-topic-row input {
  min-width: 0;
  flex: 1;
}
.produce-row {
  display: flex;
  min-width: 0;
  align-items: flex-end;
  gap: 8px;
}
.produce-grow-field {
  min-width: 0;
  flex: 1;
}
.produce-editor-block {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  gap: 4px;
}
.produce-editor-block--value {
  /* value 编辑器占面板剩余高度（下限 240px） */
  flex: 1 1 auto;
  min-height: 240px;
}
.produce-editor-head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted-foreground);
  font-size: 10px;
}
.produce-head-spacer {
  flex: 1;
}
.produce-char-count {
  color: var(--primary);
}
.produce-value-editor {
  flex: 1 1 auto;
}
/* 发送选项一行栅格：窄容器自动换行（~700px 不破版）。 */
.produce-options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 8px;
  align-items: start;
  border-top: 1px solid var(--border);
  padding-top: 10px;
}
.produce-schema-field {
  gap: 4px;
}
.produce-schema-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
  gap: 6px;
}
/* 发送成功条：醒目绿色横条（替代原小字 hint）。 */
.produce-success {
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid color-mix(in srgb, var(--success) 55%, var(--border));
  border-radius: 4px;
  padding: 8px 12px;
  background: color-mix(in srgb, var(--success) 10%, var(--background));
  color: color-mix(in srgb, var(--success) 75%, var(--foreground));
  font-size: 12px;
}
.produce-success svg {
  width: 15px;
  height: 15px;
  flex: 0 0 15px;
}
/* 底部操作行：右侧大号主色发送按钮 + 清空按钮。 */
.produce-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.produce-error {
  flex: 1 1 auto;
}
.produce-ghost-button {
  min-height: 32px;
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 4px 14px;
  background: var(--background);
  color: var(--foreground);
  cursor: pointer;
}
.produce-ghost-button:hover:not(:disabled) {
  background: var(--accent);
}
.produce-send-button {
  min-height: 32px;
  padding: 4px 20px;
  font-size: 12px;
  font-weight: 600;
}
.produce-send-button svg {
  width: 14px;
  height: 14px;
}
/* P2-7：只读/校验失败的禁用态视觉强化——主色按钮降饱和
   （title/aria 已由发送逻辑给出原因），dark/light 均成立。
   通用 cursor/复选框禁用规则已收敛至全局 style.css。 */
.produce-send-button:disabled {
  filter: grayscale(0.65) saturate(0.4);
  opacity: 0.55;
}
.produce-ghost-button:disabled {
  opacity: 0.45;
}
</style>
