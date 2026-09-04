<script setup lang="ts">
// Topic 管理面板：list（业务排序照 TopicTree 同源函数）/create/delete/
// 扩分区/config get+alter/offsets 查询。高危操作分级：
// - create/alter/扩分区：read_only 禁用
// - delete：read_only 或 allowDelete=false 禁用，且需输入与 topic 同名的
//   confirmTopic 确认文本（§6 门禁，后端同样校验）
import { computed, ref, watch } from "vue";
import { Plus, RefreshCw, Trash2, TrendingUp, Wrench } from "@lucide/vue";
import { kafkaApi, type ConfigEntry, type KafkaTopic, type TopicOffsetRow, type TopicPartitionInfo } from "../lib/api";
import { formatTimestamp, offsetTimeToParam, parseHeadersJson } from "../lib/kafkaModel";
import { t } from "../lib/i18n";

const props = defineProps<{
  topics: KafkaTopic[];
  loading: boolean;
  canWrite: boolean;
  canDelete: boolean;
}>();

const emit = defineEmits<{
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
  (e: "refresh"): void;
}>();

const selected = ref<KafkaTopic | null>(null);
const partitions = ref<TopicPartitionInfo[]>([]);
const offsetRows = ref<TopicOffsetRow[]>([]);
const configEntries = ref<ConfigEntry[]>([]);
const busy = ref(false);

// create dialog
const createOpen = ref(false);
const createName = ref("");
const createPartitions = ref("1");
const createReplication = ref("1");
const createConfigText = ref("{}");

// delete dialog
const deleteOpen = ref(false);
const deleteTarget = ref("");
const deleteConfirmText = ref("");

// expand dialog
const expandOpen = ref(false);
const expandCount = ref("");

// offsets form
const offsetTimeMode = ref<"earliest" | "latest" | "custom">("latest");
const offsetCustomTime = ref("");

// config editor
const configOpen = ref(false);
const configEdits = ref<Array<{ key: string; value: string; remove?: boolean }>>([]);

const canManage = computed(() => props.canWrite);
const canDeleteTopic = computed(() => props.canWrite && props.canDelete);

function selectTopic(topic: KafkaTopic) {
  selected.value = topic;
  partitions.value = [];
  offsetRows.value = [];
  configEntries.value = [];
}

async function describeSelected() {
  if (!selected.value) return;
  busy.value = true;
  emit("error", "");
  try {
    const response = await kafkaApi.topicsDescribe(selected.value.name);
    partitions.value = response.partitions ?? [];
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function openCreate() {
  createName.value = "";
  createPartitions.value = "1";
  createReplication.value = "1";
  createConfigText.value = "{}";
  createOpen.value = true;
}

async function submitCreate() {
  const name = createName.value.trim();
  const partitionsCount = Number.parseInt(createPartitions.value, 10);
  const replication = Number.parseInt(createReplication.value, 10);
  if (!name || !Number.isInteger(partitionsCount) || partitionsCount <= 0 || !Number.isInteger(replication) || replication <= 0) {
    emit("error", t("topics.createInvalid"));
    return;
  }
  const config = parseHeadersJson(createConfigText.value);
  if ("error" in config) {
    emit("error", `${t("topics.config")}: ${config.error}`);
    return;
  }
  busy.value = true;
  try {
    await kafkaApi.topicsCreate([name], partitionsCount, replication, config.headers);
    createOpen.value = false;
    emit("notify", `${t("topics.created")}: ${name}`);
    emit("refresh");
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function askDelete(topic: KafkaTopic) {
  deleteTarget.value = topic.name;
  deleteConfirmText.value = "";
  deleteOpen.value = true;
}

async function submitDelete() {
  // confirmTopic 必须与 topic 同名（防误删，§6）。
  if (deleteConfirmText.value.trim() !== deleteTarget.value) return;
  busy.value = true;
  try {
    await kafkaApi.topicsDelete([deleteTarget.value], deleteConfirmText.value.trim());
    deleteOpen.value = false;
    emit("notify", `${t("topics.deleted")}: ${deleteTarget.value}`);
    if (selected.value?.name === deleteTarget.value) selected.value = null;
    emit("refresh");
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function openExpand(topic: KafkaTopic) {
  selected.value = topic;
  expandCount.value = String(topic.partitionCount + 1);
  expandOpen.value = true;
}

async function submitExpand() {
  if (!selected.value) return;
  const next = Number.parseInt(expandCount.value, 10);
  if (!Number.isInteger(next) || next <= selected.value.partitionCount) {
    emit("error", t("err.partition"));
    return;
  }
  busy.value = true;
  try {
    await kafkaApi.topicsPartitionsUpdate({ [selected.value.name]: next });
    expandOpen.value = false;
    emit("notify", t("topics.expanded"));
    emit("refresh");
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

async function queryOffsets() {
  if (!selected.value) return;
  busy.value = true;
  emit("error", "");
  try {
    const offsetTime = offsetTimeMode.value === "custom" ? offsetTimeToParam(offsetCustomTime.value) : offsetTimeMode.value;
    const response = await kafkaApi.topicsOffsetsList(
      [selected.value.name],
      offsetTime === undefined ? undefined : (offsetTime as string | number),
    );
    offsetRows.value = response.rows ?? [];
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

async function openConfig(topic: KafkaTopic) {
  selected.value = topic;
  busy.value = true;
  try {
    const response = await kafkaApi.topicsConfigGet(topic.name);
    configEntries.value = response.entries ?? [];
    configEdits.value = [];
    configOpen.value = true;
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function addConfigEdit() {
  configEdits.value.push({ key: "", value: "" });
}

async function submitConfig() {
  if (!selected.value) return;
  const config: Record<string, string> = {};
  const deleteKeys: string[] = [];
  for (const edit of configEdits.value) {
    const key = edit.key.trim();
    if (!key) continue;
    if (edit.remove) deleteKeys.push(key);
    else config[key] = edit.value;
  }
  busy.value = true;
  try {
    const response = await kafkaApi.topicsConfigAlter(selected.value.name, config, deleteKeys);
    configEntries.value = response.entries ?? [];
    configEdits.value = [];
    emit("notify", t("topics.altered"));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function partitionHealthClass(partition: TopicPartitionInfo): string {
  return partition.isHealthy === false ? "badge-danger" : "badge-ok";
}

function healthLabel(partition: TopicPartitionInfo): string {
  return partition.isHealthy === false ? t("topics.unhealthy") : t("topics.healthy");
}

watch(
  () => props.topics,
  (next) => {
    if (selected.value) {
      const refreshed = next.find((topic) => topic.name === selected.value?.name);
      selected.value = refreshed ?? null;
    }
  },
);
</script>

<template>
  <section class="section-block">
    <div class="result-meta">
      <span>{{ t("topics.title") }} · {{ topics.length }}</span>
      <span class="inline-actions">
        <button class="icon-button" :title="t('topics.refresh')" @click="emit('refresh')">
          <RefreshCw :class="{ spinning: loading }" />
        </button>
        <button class="toolbar-button" :disabled="!canManage" :title="canManage ? t('topics.create') : t('readOnly')" @click="openCreate">
          <Plus aria-hidden="true" /><span>{{ t("topics.create") }}</span>
        </button>
      </span>
    </div>

    <div class="kafka-table">
      <div class="kafka-table-header topics-cols">
        <span>{{ t("topics.colTopic") }}</span>
        <span>{{ t("topics.colPartitions") }}</span>
        <span>{{ t("topics.colReplication") }}</span>
        <span />
        <span />
      </div>
      <div class="kafka-table-rows">
        <p v-if="topics.length === 0" class="empty compact">{{ t("topics.empty") }}</p>
        <div v-for="topic in topics" :key="topic.name" class="kafka-table-row topics-cols" style="cursor: default" @click="selectTopic(topic)">
          <span class="mono-s">
            {{ topic.name }}
            <span v-if="topic.isInternal || topic.name.startsWith('_')" class="badge badge-internal">{{ t("topics.colInternal") }}</span>
          </span>
          <span class="mono-s">{{ topic.partitionCount }}</span>
          <span class="mono-s">{{ topic.replicationFactor }}</span>
          <span class="inline-actions">
            <button class="qb-add" type="button" :title="t('topics.describe')" @click.stop="selectTopic(topic); describeSelected()">
              {{ t("topics.describe") }}
            </button>
            <button class="qb-add" type="button" :title="t('topics.offsets')" @click.stop="selectTopic(topic); queryOffsets()">
              {{ t("topics.offsets") }}
            </button>
            <button class="qb-add" type="button" :title="t('topics.configGet')" @click.stop="openConfig(topic)">
              <Wrench aria-hidden="true" />
            </button>
          </span>
          <span class="inline-actions">
            <button
              class="qb-add"
              type="button"
              :disabled="!canManage"
              :title="canManage ? t('topics.expand') : t('readOnly')"
              @click.stop="openExpand(topic)"
            >
              <TrendingUp aria-hidden="true" />
            </button>
            <button
              class="qb-add"
              type="button"
              :disabled="!canDeleteTopic"
              :title="canDeleteTopic ? t('topics.delete') : canDelete ? t('readOnly') : t('noDelete')"
              @click.stop="askDelete(topic)"
            >
              <Trash2 aria-hidden="true" />
            </button>
          </span>
        </div>
      </div>
    </div>

    <div v-if="selected && partitions.length > 0">
      <p class="subpanel-title">{{ t("topics.describeTitle", { topic: selected.name }) }}</p>
      <div class="kafka-table" style="max-height: 200px">
        <div class="kafka-table-header partitions-cols">
          <span>#</span>
          <span>{{ t("topics.colLeader") }}</span>
          <span>{{ t("topics.colReplicas") }}</span>
          <span>{{ t("topics.colIsr") }}</span>
          <span>{{ t("topics.colOffline") }}</span>
          <span>{{ t("topics.colHealthy") }}</span>
        </div>
        <div class="kafka-table-rows">
          <div v-for="partition in partitions" :key="partition.partition" class="kafka-table-row partitions-cols" style="cursor: default">
            <span class="mono-s">{{ partition.partition }}</span>
            <span class="mono-s">{{ partition.leader }}</span>
            <span class="mono-s">{{ partition.replicas.join(",") }}</span>
            <span class="mono-s">{{ partition.isr.join(",") }}</span>
            <span class="mono-s">{{ partition.offlineReplicas.length > 0 ? partition.offlineReplicas.join(",") : "—" }}</span>
            <span><span class="badge" :class="partitionHealthClass(partition)">{{ healthLabel(partition) }}</span></span>
          </div>
        </div>
      </div>
    </div>

    <div v-if="selected && offsetRows.length > 0">
      <p class="subpanel-title">{{ t("topics.offsetsTitle", { topic: selected.name }) }}</p>
      <div class="kafka-form" style="border: 0; padding: 0 0 4px">
        <label class="field">
          <span>{{ t("topics.offsetTime") }}</span>
          <select v-model="offsetTimeMode">
            <option value="earliest">{{ t("topics.timeEarliest") }}</option>
            <option value="latest">{{ t("topics.timeLatest") }}</option>
            <option value="custom">{{ t("topics.timeCustom") }}</option>
          </select>
        </label>
        <label v-if="offsetTimeMode === 'custom'" class="field">
          <span>{{ t("messages.offsetTime") }} ({{ t("messages.offsetTimeHint") }})</span>
          <input v-model="offsetCustomTime" type="text" spellcheck="false" />
        </label>
        <button class="primary-button compact" type="button" :disabled="busy" @click="queryOffsets">{{ t("acls.filterRun") }}</button>
      </div>
      <div class="kafka-table" style="max-height: 200px">
        <div class="kafka-table-header offsets-cols">
          <span>#</span>
          <span>Offset</span>
          <span>{{ t("messages.colTimestamp") }}</span>
          <span>Epoch</span>
        </div>
        <div class="kafka-table-rows">
          <p v-if="offsetRows.length === 0" class="empty compact">{{ t("topics.offsetsEmpty") }}</p>
          <div v-for="row in offsetRows" :key="row.partition" class="kafka-table-row offsets-cols" style="cursor: default">
            <span class="mono-s">{{ row.partition }}</span>
            <span class="mono-s">{{ row.offset }}</span>
            <span class="mono-s">{{ formatTimestamp(row.timestamp) }}</span>
            <span class="mono-s">{{ row.leaderEpoch ?? "—" }}</span>
          </div>
        </div>
      </div>
    </div>

    <teleport to="body">
      <div v-if="createOpen" class="modal-backdrop" @click.self="createOpen = false">
        <div class="modal">
          <header>
            <h2>{{ t("topics.createTitle") }}</h2>
            <button class="icon-button" :title="t('close')" @click="createOpen = false">✕</button>
          </header>
          <div class="settings-body">
            <label class="settings-field">
              <span>{{ t("topics.name") }}</span>
              <input v-model="createName" type="text" :placeholder="t('topics.namePlaceholder')" spellcheck="false" />
            </label>
            <label class="settings-field">
              <span>{{ t("topics.partitions") }}</span>
              <input v-model="createPartitions" type="number" min="1" />
            </label>
            <label class="settings-field">
              <span>{{ t("topics.replicationFactor") }}</span>
              <input v-model="createReplication" type="number" min="1" />
            </label>
            <label class="settings-field">
              <span>{{ t("topics.config") }}</span>
              <textarea v-model="createConfigText" rows="3" :placeholder="t('topics.configPlaceholder')" class="mono" spellcheck="false" />
            </label>
          </div>
          <footer>
            <button type="button" @click="createOpen = false">{{ t("cancel") }}</button>
            <button class="primary-button" type="button" :disabled="busy" @click="submitCreate">{{ t("save") }}</button>
          </footer>
        </div>
      </div>

      <div v-if="deleteOpen" class="modal-backdrop" @click.self="deleteOpen = false">
        <div class="modal">
          <header>
            <h2>{{ t("topics.deleteTitle") }}</h2>
            <button class="icon-button" :title="t('close')" @click="deleteOpen = false">✕</button>
          </header>
          <p>{{ t("topics.deleteMessage") }}</p>
          <div class="destructive-copy">
            <div class="destructive-icon"><Trash2 aria-hidden="true" /></div>
            <div>
              <strong class="mono">{{ deleteTarget }}</strong>
              <p>
                <label class="settings-field">
                  <span>{{ t("topics.deleteConfirmLabel", { topic: deleteTarget }) }}</span>
                  <input v-model="deleteConfirmText" type="text" class="mono" spellcheck="false" @keyup.enter="submitDelete" />
                </label>
              </p>
            </div>
          </div>
          <footer>
            <button type="button" @click="deleteOpen = false">{{ t("cancel") }}</button>
            <button class="danger-button" type="button" :disabled="busy || deleteConfirmText.trim() !== deleteTarget" @click="submitDelete">
              {{ t("delete") }}
            </button>
          </footer>
        </div>
      </div>

      <div v-if="expandOpen" class="modal-backdrop" @click.self="expandOpen = false">
        <div class="modal small-modal">
          <header>
            <h2>{{ t("topics.expandTitle", { topic: selected?.name ?? "" }) }}</h2>
            <button class="icon-button" :title="t('close')" @click="expandOpen = false">✕</button>
          </header>
          <label class="settings-field">
            <span>{{ t("topics.expandNewCount") }}</span>
            <input v-model="expandCount" type="number" min="1" />
          </label>
          <footer>
            <button type="button" @click="expandOpen = false">{{ t("cancel") }}</button>
            <button class="primary-button" type="button" :disabled="busy" @click="submitExpand">{{ t("confirm") }}</button>
          </footer>
        </div>
      </div>

      <div v-if="configOpen" class="modal-backdrop" @click.self="configOpen = false">
        <div class="modal panel-modal">
          <header>
            <h2>{{ t("topics.configTitle", { topic: selected?.name ?? "" }) }}</h2>
            <button class="icon-button" :title="t('close')" @click="configOpen = false">✕</button>
          </header>
          <div class="settings-body">
            <table class="config-table">
              <thead>
                <tr>
                  <th>{{ t("topics.configKey") }}</th>
                  <th>{{ t("topics.configValue") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="entry in configEntries" :key="entry.name">
                  <td class="mono-s">{{ entry.name }}</td>
                  <td class="mono-s">
                    {{ entry.sensitive ? t("brokers.sensitiveMasked") : entry.value }}
                    <span v-if="entry.isDefault" class="badge">{{ t("brokers.colDefault") }}</span>
                  </td>
                </tr>
              </tbody>
            </table>
            <p class="hint">{{ t("topics.configDeleteKeys") }} → {{ t("topics.configRemove") }}</p>
            <div v-for="(edit, index) in configEdits" :key="index" class="field-filter-row" style="grid-template-columns: minmax(120px, 1fr) minmax(120px, 1fr) 26px 26px">
              <input v-model="edit.key" type="text" :placeholder="t('topics.configKey')" spellcheck="false" />
              <input v-model="edit.value" type="text" :placeholder="t('topics.configValue')" spellcheck="false" />
              <label class="checkbox"><input v-model="edit.remove" type="checkbox" :title="t('topics.configRemove')" /></label>
              <button class="row-remove" type="button" @click="configEdits.splice(index, 1)">✕</button>
            </div>
            <button class="qb-add" style="align-self: flex-start" type="button" @click="addConfigEdit">+ {{ t("topics.configAdd") }}</button>
          </div>
          <footer>
            <button type="button" @click="configOpen = false">{{ t("close") }}</button>
            <button class="primary-button" type="button" :disabled="busy || !canManage" :title="canManage ? undefined : t('readOnly')" @click="submitConfig">
              {{ t("save") }}
            </button>
          </footer>
        </div>
      </div>
    </teleport>
  </section>
</template>

<style scoped>
.topics-cols {
  grid-template-columns: minmax(140px, 2fr) 70px 50px minmax(200px, 2fr) auto;
}
.partitions-cols {
  grid-template-columns: 40px 70px minmax(90px, 1fr) minmax(90px, 1fr) 70px 80px;
}
.offsets-cols {
  grid-template-columns: 40px minmax(80px, 1fr) minmax(140px, 1fr) 70px;
}
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
</style>
