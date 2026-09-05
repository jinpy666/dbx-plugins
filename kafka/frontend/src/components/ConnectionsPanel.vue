<script setup lang="ts">
// 连接状态面板（Phase 2 增强）：kafka/connections/statuses（status/lastError/
// lastUsedAt + allowDelete 门禁徽标）+ SR/Kerberos/ZK 连接摘要徽标 +
// 「Confluent properties 导入助手」：粘贴 → 纯解析出 bootstrap/protocol/sasl/
// jaas 用户名密码/SR URL → 只读键值表并提示对应宿主表单字段；
// 明文密码掩码展示、不落 localStorage（组件关闭即丢弃）。
// Phase P：SR 徽标标注 provider（Confluent/AWS Glue）；Glue 连接摘要展示
// region/registryName/authMode，secret 类字段只显示「已配置/未配置」（值不展示）。
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { ClipboardPaste, Network, RefreshCw, X } from "@lucide/vue";
import { getKafkaConnectionId, kafkaApi, type KafkaConnectionStatus } from "../lib/api";
import { buildPropertyMappings, parsePropertiesText, type PropertyMappingRow } from "../lib/kafkaModel";
import { friendlyKafkaError } from "../lib/kafkaErrors";
import { t } from "../lib/i18n";

const props = defineProps<{
  open: boolean;
  disabled?: boolean;
  /** 宿主 context.connection 摘要（Phase P：glue_* manifest 字段展示用）。 */
  connection?: Record<string, unknown>;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "error", message: string): void;
}>();

const statuses = ref<KafkaConnectionStatus[]>([]);
const loading = ref(false);

// -- Glue 连接摘要（Phase P）-------------------------------------------------------
// manifest 字段可能直接挂在 connection 上（camelCase 表单字段）或在
// external_config（snake_case，照 ldap base_dn 双源模式）；secret 类字段
// （glue_secret_access_key/glue_session_token）走 secret binding 时宿主不下发值，
// 以 connection_secrets 名单判定「已配置」，任何来源都不展示值。

interface GlueSummary {
  region: string;
  registryName: string;
  authMode: string;
  accessKeyId: string;
  secretAccessKeyConfigured: boolean;
  sessionTokenConfigured: boolean;
}

function pickConnectionField(sources: Array<Record<string, unknown>>, keys: string[]): string {
  for (const source of sources) {
    for (const key of keys) {
      const value = source[key];
      if (typeof value === "string" && value.trim()) return value.trim();
    }
  }
  return "";
}

const glueSummary = computed<GlueSummary>(() => {
  const direct = (props.connection ?? {}) as Record<string, unknown>;
  const external = (direct.external_config && typeof direct.external_config === "object"
    ? direct.external_config
    : {}) as Record<string, unknown>;
  const sources = [direct, external];
  const secretNames = Array.isArray(direct.connection_secrets) ? direct.connection_secrets.map(String) : [];
  const configured = (snake: string, camel: string) =>
    pickConnectionField(sources, [snake, camel]).length > 0 || secretNames.includes(snake) || secretNames.includes(camel);
  return {
    region: pickConnectionField(sources, ["glue_region", "glueRegion"]),
    registryName: pickConnectionField(sources, ["glue_registry_name", "glueRegistryName"]),
    authMode: pickConnectionField(sources, ["glue_auth_mode", "glueAuthMode"]),
    accessKeyId: pickConnectionField(sources, ["glue_access_key_id", "glueAccessKeyId"]),
    secretAccessKeyConfigured: configured("glue_secret_access_key", "glueSecretAccessKey"),
    sessionTokenConfigured: configured("glue_session_token", "glueSessionToken"),
  };
});

function isGlueStatus(status: KafkaConnectionStatus): boolean {
  return status.schemaRegistry?.provider === "glue";
}

/** Glue 详情仅挂在当前连接行（manifest 字段只对当前连接可见）。 */
function showGlueDetail(status: KafkaConnectionStatus): boolean {
  return isGlueStatus(status) && status.connectionId === getKafkaConnectionId();
}

// 导入助手状态（仅内存，不持久化）
const assistantOpen = ref(false);
const propertiesText = ref("");
const mappingRows = ref<PropertyMappingRow[]>([]);
const parsedCount = ref(0);

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

function openAssistant() {
  propertiesText.value = "";
  mappingRows.value = [];
  parsedCount.value = 0;
  assistantOpen.value = true;
}

// P2（扫描遗留 P1 关联）：导入助手子弹层打开时以捕获阶段拦截 Esc，只关子弹层、
// 不再透传给连接弹窗（原先子弹层 Esc 会整层关闭连接弹窗）。子弹层关闭后不挂
// 监听，连接弹窗自身的 Esc 行为不受影响。
function onAssistantKeydown(event: KeyboardEvent) {
  if (event.key !== "Escape") return;
  event.stopPropagation();
  assistantOpen.value = false;
}

watch(assistantOpen, (open) => {
  if (open) window.addEventListener("keydown", onAssistantKeydown, true);
  else window.removeEventListener("keydown", onAssistantKeydown, true);
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", onAssistantKeydown, true);
});

function parseProperties() {
  const properties = parsePropertiesText(propertiesText.value);
  parsedCount.value = Object.keys(properties).length;
  mappingRows.value = buildPropertyMappings(properties);
}

function clearAssistant() {
  propertiesText.value = "";
  mappingRows.value = [];
  parsedCount.value = 0;
}

watch(
  () => props.open,
  (open) => {
    if (open) void load();
    else assistantOpen.value = false;
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
          <button class="icon-button" :title="t('connections.importAssistant')" @click="openAssistant"><ClipboardPaste /></button>
          <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
        </span>
      </header>
      <ul v-if="statuses.length > 0" class="settings-list">
        <li v-for="status in statuses" :key="status.connectionId">
          <span class="state-dot" :class="stateDotClass(status.status)" :title="stateLabel(status.status)" />
          <div class="settings-list-main">
            <strong class="mono">{{ status.connectionId }}</strong>
            <span v-if="status.lastUsedAt">{{ t("connections.lastUsed") }}: {{ formatTime(status.lastUsedAt) }}</span>
            <span v-if="status.lastError" class="form-error">{{ t("connections.lastError") }}: {{ friendlyKafkaError(status.lastError) }}</span>
            <span class="inline-actions" style="margin-top: 2px; flex-wrap: wrap">
              <span class="badge" :class="status.schemaRegistry?.enabled ? 'badge-ok' : ''">
                {{ status.schemaRegistry?.enabled ? t("connections.srOn") : t("connections.srOff") }}
              </span>
              <span v-if="isGlueStatus(status)" class="badge">{{ t("connections.glueBadge") }}</span>
              <span v-if="status.kerberos?.enabled" class="badge">{{ t("connections.kerberosOn") }}</span>
              <span v-if="status.connectionSource === 'zookeeper'" class="badge badge-warn">{{ t("connections.zkSource") }}</span>
            </span>
            <span v-if="showGlueDetail(status)" class="inline-actions" style="margin-top: 2px; flex-wrap: wrap; gap: 4px">
              <span v-if="glueSummary.region" class="badge">{{ t("connections.glueRegion", { region: glueSummary.region }) }}</span>
              <span v-if="glueSummary.registryName" class="badge">{{ t("connections.glueRegistryName", { name: glueSummary.registryName }) }}</span>
              <span v-if="glueSummary.authMode" class="badge">{{ t("connections.glueAuthMode", { mode: glueSummary.authMode }) }}</span>
              <span v-if="glueSummary.accessKeyId" class="badge mono-s">{{ t("connections.glueAccessKeyId", { key: glueSummary.accessKeyId }) }}</span>
              <span class="badge" :class="glueSummary.secretAccessKeyConfigured ? 'badge-ok' : 'badge-warn'">
                {{ t("connections.glueSecretLabel") }}: {{ glueSummary.secretAccessKeyConfigured ? t("connections.glueConfigured") : t("connections.glueNotConfigured") }}
              </span>
              <span class="badge" :class="glueSummary.sessionTokenConfigured ? 'badge-ok' : 'badge-warn'">
                {{ t("connections.glueSessionTokenLabel") }}: {{ glueSummary.sessionTokenConfigured ? t("connections.glueConfigured") : t("connections.glueNotConfigured") }}
              </span>
            </span>
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

  <teleport to="body">
    <div v-if="assistantOpen" class="modal-backdrop" @click.self="assistantOpen = false">
      <div class="modal panel-modal">
        <header>
          <h2>{{ t("connections.importTitle") }}</h2>
          <button class="icon-button" :title="t('close')" @click="assistantOpen = false"><X /></button>
        </header>
        <div class="settings-body">
          <label class="settings-field">
            <span>{{ t("connections.importPlaceholder") }}</span>
            <textarea v-model="propertiesText" rows="8" class="mono" spellcheck="false" />
          </label>
          <div class="inline-actions">
            <button class="primary-button compact" type="button" @click="parseProperties">{{ t("connections.importParse") }}</button>
            <button class="toolbar-button" type="button" @click="clearAssistant">{{ t("connections.importClear") }}</button>
          </div>
          <p v-if="mappingRows.length === 0" class="hint">{{ parsedCount > 0 ? t("connections.importEmpty") : t("connections.importHint") }}</p>
          <table v-else class="config-table">
            <thead>
              <tr>
                <th>{{ t("connections.colProperty") }}</th>
                <th>{{ t("connections.colValue") }}</th>
                <th>{{ t("connections.colFormField") }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in mappingRows" :key="row.property">
                <td class="mono-s">{{ row.property }}</td>
                <td class="mono-s" :class="{ 'form-error': row.masked }">
                  {{ row.masked ? `${row.value} ${t("connections.masked")}` : row.value }}
                </td>
                <td class="mono-s">{{ row.formField }}</td>
              </tr>
            </tbody>
          </table>
          <p class="hint">{{ t("connections.importHint") }}</p>
        </div>
        <footer>
          <button type="button" @click="assistantOpen = false">{{ t("close") }}</button>
        </footer>
      </div>
    </div>
  </teleport>
</template>

<style scoped>
.config-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
}
.config-table th,
.config-table td {
  border-bottom: 1px solid color-mix(in srgb, var(--border) 45%, transparent);
  padding: 3px 6px;
  text-align: left;
  overflow-wrap: anywhere;
}
.config-table th {
  color: var(--muted-foreground);
  font-weight: 600;
}
/* P2 统一禁用态。 */
button:disabled,
input:disabled,
select:disabled {
  cursor: not-allowed;
}
</style>
