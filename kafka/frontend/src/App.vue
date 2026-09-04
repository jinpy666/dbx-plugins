<script setup lang="ts">
// Kafka 工作台外壳：布局 + 连接上下文（照 ldap App.vue 的宿主桥用法）。
// 连接生命周期由宿主驱动（connection/test|connect|disconnect），工作台只持有
// connectionId；所有 kafka/* 调用经 lib/api.ts 注入 connectionId。
// 面板：messages / stream / produce / topics / groups / brokers / acls。
import { computed, ref, onBeforeUnmount, onMounted } from "vue";
import { Network, RefreshCw } from "@lucide/vue";
import { DBX_POPOVER, resolveAppearance, type DbxPluginAppearanceInput } from "./lib/appearance";
import { isDbxPluginTheme, onHostThemeChange, themeToAppearance } from "./lib/hostTheme";
import { setWorkbenchLocale, t } from "./lib/i18n";
import { kafkaApi, setKafkaConnectionId, type KafkaStreamErrorEvent, type KafkaStreamMessagesEvent, type KafkaTopic } from "./lib/api";
import { friendlyKafkaError } from "./lib/kafkaErrors";
import { parseAuditEvent, pushAuditItem, type AuditFeedItem } from "./lib/auditFeed";
import TopicTree from "./components/TopicTree.vue";
import MessagesPanel from "./components/MessagesPanel.vue";
import StreamPanel from "./components/StreamPanel.vue";
import ProducePanel from "./components/ProducePanel.vue";
import TopicsPanel from "./components/TopicsPanel.vue";
import GroupsPanel from "./components/GroupsPanel.vue";
import BrokersPanel from "./components/BrokersPanel.vue";
import AclsPanel from "./components/AclsPanel.vue";
import ConnectionsPanel from "./components/ConnectionsPanel.vue";
import AuditFeedPanel from "./components/AuditFeedPanel.vue";

interface ConnectionSummary {
  name?: string;
  host?: string;
  port?: number;
  username?: string;
  color?: string;
  readOnly?: boolean;
  external_config?: Record<string, unknown>;
}

type PanelKey = "messages" | "stream" | "produce" | "topics" | "groups" | "brokers" | "acls";

const hostContext = ref<Record<string, unknown>>({});
const appearance = ref(resolveAppearance());
const ready = ref(false);
const initError = ref("");
const kafkaError = ref("");
const kafkaErrorDetail = ref("");
const notice = ref("");
const activePanel = ref<PanelKey>("messages");

const topics = ref<KafkaTopic[]>([]);
const topicsLoading = ref(false);
const topicsError = ref("");
const selectedTopic = ref("");

const streamRef = ref<InstanceType<typeof StreamPanel>>();
const connectionsOpen = ref(false);

const auditItems = ref<AuditFeedItem[]>([]);
let auditSeq = 0;

let noticeTimer = 0;
const unsubscribeAppearance: Array<() => void> = [];
const unsubscribeLocale: Array<() => void> = [];
const unsubscribeContext: Array<() => void> = [];
const unsubscribeEvent: Array<() => void> = [];

const connectionId = computed(() => String(hostContext.value.connectionId || ""));
const connection = computed<ConnectionSummary>(() => {
  const value = hostContext.value.connection;
  return value && typeof value === "object" ? (value as ConnectionSummary) : {};
});
// 写权限 = 宿主 context 未标记只读 且 后端策略层未开启只读门禁；
// 删除门禁 = 策略层 allow_delete（read_only 下后端强制无效，两门同时生效）。
const backendReadOnly = ref(false);
const backendAllowDelete = ref(false);
const canWrite = computed(() => !connection.value.readOnly && !backendReadOnly.value);
const canDelete = computed(() => canWrite.value && backendAllowDelete.value);

async function refreshBackendPolicy() {
  try {
    const result = await kafkaApi.connectionStatuses();
    const mine = (result.statuses || []).find((row) => row.connectionId === connectionId.value);
    backendReadOnly.value = mine?.readOnly === true;
    // 旧 sidecar 缺 allowDelete 字段时不主动禁用删除按钮（后端仍会拒绝）。
    backendAllowDelete.value = mine?.allowDelete !== false;
  } catch {
    backendReadOnly.value = false;
    backendAllowDelete.value = true;
  }
}

const connectionIdentity = computed(() => {
  const host = connection.value.host || connection.value.name || connectionId.value;
  const identity = connection.value.username ? `${connection.value.username}@${host}` : host;
  const port = connection.value.port ? `:${connection.value.port}` : "";
  return `${identity}${port}`;
});
const toolbarStyle = computed(() => {
  const color = connection.value.color;
  if (!color) return undefined;
  return { backgroundColor: colorWithAlpha(color, 0.1), boxShadow: `inset 0 1px 0 ${colorWithAlpha(color, 0.18)}` };
});

function applyAppearance(next?: DbxPluginAppearanceInput | null) {
  // 宿主可能缺字段（1.0 部分下发、1.1 theme 通道只带颜色令牌），按 DBX 规范色板补齐。
  const resolved = resolveAppearance(next);
  appearance.value = resolved;
  const root = document.documentElement;
  root.dataset.theme = resolved.colorScheme;
  root.style.colorScheme = resolved.colorScheme;
  root.style.setProperty("--background", resolved.colors.background);
  root.style.setProperty("--foreground", resolved.colors.foreground);
  root.style.setProperty("--muted", resolved.colors.muted);
  root.style.setProperty("--muted-foreground", resolved.colors.mutedForeground);
  root.style.setProperty("--accent", resolved.colors.accent);
  root.style.setProperty("--accent-foreground", resolved.colors.accentForeground);
  root.style.setProperty("--border", resolved.colors.border);
  root.style.setProperty("--destructive", resolved.colors.destructive);
  root.style.setProperty("--popover", DBX_POPOVER[resolved.colorScheme]);
  root.style.setProperty("--ui-font-family", resolved.ui.fontFamily);
}

function showNotice(message: string) {
  notice.value = message;
  window.clearTimeout(noticeTimer);
  noticeTimer = window.setTimeout(() => (notice.value = ""), 3500);
}

function showError(messageOrCause: unknown) {
  const message = messageOrCause instanceof Error ? messageOrCause.message : String(messageOrCause ?? "");
  // 横幅展示本地化的可行动文案；原始错误串挂在 title 悬停里供排查。
  kafkaError.value = friendlyKafkaError(message);
  kafkaErrorDetail.value = kafkaError.value === message ? "" : message;
}

function dismissError() {
  kafkaError.value = "";
}

function colorWithAlpha(color: string, alpha: number) {
  const match = color.trim().match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i);
  if (!match) return `color-mix(in srgb, ${color} ${Math.round(alpha * 100)}%, transparent)`;
  const hex = match[1].length === 3 ? [...match[1]].map((part) => `${part}${part}`).join("") : match[1];
  const red = Number.parseInt(hex.slice(0, 2), 16);
  const green = Number.parseInt(hex.slice(2, 4), 16);
  const blue = Number.parseInt(hex.slice(4, 6), 16);
  return `rgb(${red} ${green} ${blue} / ${alpha})`;
}

// -- topics ---------------------------------------------------------------------

async function loadTopics() {
  if (topicsLoading.value) return;
  topicsLoading.value = true;
  topicsError.value = "";
  try {
    const response = await kafkaApi.topicsList(true);
    topics.value = Array.isArray(response.topics) ? response.topics : [];
  } catch (cause) {
    topicsError.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    topicsLoading.value = false;
  }
}

function selectTopic(topic: string) {
  selectedTopic.value = topic;
}

// -- host bridge ------------------------------------------------------------------

function handleEvent(event: { method: string; params: Record<string, unknown> }) {
  if (event.method === "kafka/audit") {
    // 数据面：进入最近操作面板（denied/error 高亮）；即时反馈走横幅/通知。
    auditItems.value = pushAuditItem(auditItems.value, parseAuditEvent(event.params, auditSeq++, Date.now()));
    const result = String(event.params.result ?? "");
    if (result === "denied" || result === "error") {
      showError(`${event.params.action ?? "kafka"}: ${event.params.detail ?? result}`);
    }
    return;
  }
  if (event.method === "kafka/stream/messages" || event.method === "kafka/stream/error") {
    streamRef.value?.pushEvent(event.params as unknown as KafkaStreamMessagesEvent | KafkaStreamErrorEvent);
  }
}

function clearAuditFeed() {
  auditItems.value = [];
}

async function waitForHostApi(timeoutMs = 8000) {
  const deadline = Date.now() + timeoutMs;
  while (!window.dbxPlugin && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 50));
  if (!window.dbxPlugin) throw new Error(t("hostApiUnavailable"));
  return window.dbxPlugin;
}

async function initialize() {
  const api = await waitForHostApi();
  hostContext.value = await Promise.any([
    api.ready,
    api.request<Record<string, unknown>>("host.getContext"),
  ]);
  setWorkbenchLocale(api.locale || "zh-CN");
  if (api.appearance) applyAppearance(api.appearance);
  else if (isDbxPluginTheme(api.theme)) applyAppearance(themeToAppearance(api.theme));
  if (api.onAppearanceChange) unsubscribeAppearance.push(api.onAppearanceChange(applyAppearance));
  // appearance 契约缺失（当前 1.1 桥只推 theme）时订阅 env 主题推送，两套不同时挂。
  else unsubscribeAppearance.push(onHostThemeChange((theme) => applyAppearance(themeToAppearance(theme))));
  if (api.onLocaleChange) unsubscribeLocale.push(api.onLocaleChange((next) => setWorkbenchLocale(next || "zh-CN")));
  if (api.onContextChange) unsubscribeContext.push(api.onContextChange((context) => {
    hostContext.value = context;
    syncConnectionContext();
  }));
  if (api.onEvent) unsubscribeEvent.push(api.onEvent(handleEvent));
  if (!connectionId.value) throw new Error(t("connectionMissing"));
  syncConnectionContext();
  ready.value = true;
  void loadTopics();
  void refreshBackendPolicy();
}

function syncConnectionContext() {
  setKafkaConnectionId(connectionId.value);
  void refreshBackendPolicy();
}

onMounted(() => {
  void initialize().catch((cause) => {
    initError.value = cause instanceof Error ? cause.message : String(cause);
  });
});

onBeforeUnmount(() => {
  window.clearTimeout(noticeTimer);
  for (const dispose of [...unsubscribeAppearance, ...unsubscribeLocale, ...unsubscribeContext, ...unsubscribeEvent]) dispose();
});
</script>

<template>
  <div class="workbench">
    <header class="toolbar" :style="toolbarStyle">
      <div class="identity">
        <span class="connection-color" :style="connection.color ? { background: connection.color } : undefined" />
        <strong :title="connectionIdentity">{{ connectionIdentity }}</strong>
        <span v-if="!canWrite" class="read-only-badge">{{ t("readOnly") }}</span>
        <span v-if="!canDelete" class="read-only-badge">{{ t("noDelete") }}</span>
      </div>
      <div class="toolbar-actions">
        <button class="toolbar-button" :disabled="!ready" :title="t('connections.title')" @click="connectionsOpen = true">
          <Network class="icon-cyan" aria-hidden="true" /><span>{{ t("connections.title") }}</span>
        </button>
        <span class="toolbar-separator" />
        <button class="icon-button" :disabled="!ready" :title="t('refresh')" @click="loadTopics">
          <RefreshCw aria-hidden="true" />
        </button>
      </div>
    </header>

    <div v-if="initError" class="tree-state">{{ initError }}</div>
    <div v-else-if="!ready" class="tree-state">{{ t("tree.loading") }}</div>

    <template v-else>
      <nav class="tab-bar">
        <button type="button" :class="{ 'is-active': activePanel === 'messages' }" @click="activePanel = 'messages'">{{ t("tabs.messages") }}</button>
        <button type="button" :class="{ 'is-active': activePanel === 'stream' }" @click="activePanel = 'stream'">{{ t("tabs.stream") }}</button>
        <button type="button" :class="{ 'is-active': activePanel === 'produce' }" @click="activePanel = 'produce'">{{ t("tabs.produce") }}</button>
        <button type="button" :class="{ 'is-active': activePanel === 'topics' }" @click="activePanel = 'topics'">{{ t("tabs.topics") }}</button>
        <button type="button" :class="{ 'is-active': activePanel === 'groups' }" @click="activePanel = 'groups'">{{ t("tabs.groups") }}</button>
        <button type="button" :class="{ 'is-active': activePanel === 'brokers' }" @click="activePanel = 'brokers'">{{ t("tabs.brokers") }}</button>
        <button type="button" :class="{ 'is-active': activePanel === 'acls' }" @click="activePanel = 'acls'">{{ t("tabs.acls") }}</button>
        <span class="tab-spacer" />
        <span v-if="selectedTopic" class="badge">{{ selectedTopic }}</span>
      </nav>

      <div class="panes">
        <TopicTree
          :topics="topics"
          :loading="topicsLoading"
          :error="topicsError"
          :selected-topic="selectedTopic"
          @refresh="loadTopics"
          @select="selectTopic"
        />
        <div class="divider" />
        <main class="main-pane">
          <MessagesPanel
            v-show="activePanel === 'messages'"
            :topic="selectedTopic"
            :can-write="canWrite"
            @error="showError"
            @notify="showNotice"
          />
          <StreamPanel
            v-show="activePanel === 'stream'"
            ref="streamRef"
            :topic="selectedTopic"
            :can-write="canWrite"
            @error="showError"
            @notify="showNotice"
          />
          <ProducePanel
            v-show="activePanel === 'produce'"
            :topic="selectedTopic"
            :can-write="canWrite"
            @error="showError"
            @notify="showNotice"
          />
          <TopicsPanel
            v-show="activePanel === 'topics'"
            :topics="topics"
            :loading="topicsLoading"
            :can-write="canWrite"
            :can-delete="canDelete"
            @error="showError"
            @notify="showNotice"
            @refresh="loadTopics"
          />
          <GroupsPanel v-show="activePanel === 'groups'" :can-write="canWrite" :can-delete="canDelete" @error="showError" @notify="showNotice" />
          <BrokersPanel v-show="activePanel === 'brokers'" @error="showError" />
          <AclsPanel v-show="activePanel === 'acls'" :can-write="canWrite" :can-delete="canDelete" @error="showError" @notify="showNotice" />
          <AuditFeedPanel :items="auditItems" @clear="clearAuditFeed" />
        </main>
      </div>
    </template>

    <div v-if="kafkaError" class="error-banner">
      <span :title="kafkaErrorDetail || kafkaError">{{ kafkaError }}</span>
      <button type="button" @click="dismissError">✕</button>
    </div>
    <div v-if="notice" class="notice">{{ notice }}</div>

    <ConnectionsPanel :open="connectionsOpen" :disabled="!ready" @close="connectionsOpen = false" @error="showError" />
  </div>
</template>
