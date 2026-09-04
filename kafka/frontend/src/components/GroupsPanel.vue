<script setup lang="ts">
// 消费组面板：组列表 + offsets 表（start/end/committed/lag + hasCommitted:false
// 标注）+ totalLag 聚合 + describe members + offset 重置（earliest/latest/
// timestamp/partitionOffset）+ 删除组。写操作按 readOnly/allowDelete 禁用。
import { onMounted, ref } from "vue";
import { RefreshCw, Trash2 } from "@lucide/vue";
import { kafkaApi, type GroupMember, type GroupOffsetRow, type KafkaGroup } from "../lib/api";
import { parsePartitionOffsetsText, sumLag } from "../lib/kafkaModel";
import { t } from "../lib/i18n";

defineProps<{
  canWrite: boolean;
  canDelete: boolean;
}>();

const emit = defineEmits<{
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
}>();

const groups = ref<KafkaGroup[]>([]);
const loading = ref(false);
const selected = ref<KafkaGroup | null>(null);
const offsetRows = ref<GroupOffsetRow[]>([]);
const totalLag = ref<number | null>(null);
const hasCommitted = ref(true);
const members = ref<GroupMember[]>([]);
const busy = ref(false);

// reset dialog
const resetOpen = ref(false);
const resetTopics = ref("");
const resetTo = ref<"earliest" | "latest" | "timestamp" | "partitionOffset">("earliest");
const resetTimestampMs = ref("");
const resetPartitionOffsets = ref("");

// delete dialog
const deleteOpen = ref(false);
const deleteTarget = ref("");

async function load() {
  loading.value = true;
  emit("error", "");
  try {
    const response = await kafkaApi.groupsList();
    groups.value = response.groups ?? [];
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    loading.value = false;
  }
}

async function selectGroup(group: KafkaGroup) {
  selected.value = group;
  offsetRows.value = [];
  totalLag.value = null;
  hasCommitted.value = true;
  members.value = [];
  busy.value = true;
  emit("error", "");
  try {
    const offsets = await kafkaApi.groupsOffsetsList(group.group);
    offsetRows.value = offsets.rows ?? [];
    totalLag.value = typeof offsets.totalLag === "number" ? offsets.totalLag : sumLag(offsetRows.value);
    hasCommitted.value = offsetRows.value.some((row) => row.hasCommitted !== false);
    const described = await kafkaApi.groupsDescribe(group.group);
    members.value = described.members ?? [];
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function askReset(group: KafkaGroup) {
  selected.value = group;
  resetTopics.value = "";
  resetTo.value = "earliest";
  resetTimestampMs.value = "";
  resetPartitionOffsets.value = "";
  resetOpen.value = true;
}

async function submitReset() {
  if (!selected.value) return;
  const topics = resetTopics.value
    .split(/[,，\s]+/)
    .map((entry) => entry.trim())
    .filter(Boolean);
  const extra: { timestampMs?: number; partitionOffsets?: Record<string, number> } = {};
  if (resetTo.value === "timestamp") {
    const parsed = Number.parseInt(resetTimestampMs.value.trim(), 10);
    if (!Number.isFinite(parsed)) {
      emit("error", t("messages.timestampRequired"));
      return;
    }
    extra.timestampMs = parsed;
  }
  if (resetTo.value === "partitionOffset") {
    const offsets = parsePartitionOffsetsText(resetPartitionOffsets.value);
    if (Object.keys(offsets).length === 0) {
      emit("error", t("messages.offsetsRequired"));
      return;
    }
    extra.partitionOffsets = offsets;
  }
  busy.value = true;
  try {
    const response = await kafkaApi.groupsOffsetsReset(selected.value.group, topics, resetTo.value, extra);
    const failed = (response.rows ?? []).filter((row) => !row.ok);
    if (failed.length > 0) {
      emit("error", failed.map((row) => `${row.topic}-${row.partition}: ${row.error ?? "failed"}`).join("; "));
    } else {
      emit("notify", t("groups.resetDone"));
    }
    resetOpen.value = false;
    await selectGroup(selected.value);
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function askDelete(group: KafkaGroup) {
  deleteTarget.value = group.group;
  deleteOpen.value = true;
}

async function submitDelete() {
  busy.value = true;
  try {
    await kafkaApi.groupsDelete(deleteTarget.value);
    deleteOpen.value = false;
    emit("notify", t("groups.deleted"));
    if (selected.value?.group === deleteTarget.value) selected.value = null;
    await load();
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function lagBadgeClass(lag: number): string {
  if (lag <= 0) return "badge-ok";
  if (lag > 1000) return "badge-danger";
  return "badge-warn";
}

function assignmentsText(member: GroupMember): string {
  return Object.entries(member.assignments ?? {})
    .map(([topic, partitions]) => `${topic}[${partitions.join(",")}]`)
    .join("; ");
}

onMounted(() => {
  void load();
});
</script>

<template>
  <section class="section-block">
    <div class="result-meta">
      <span>{{ t("groups.title") }} · {{ groups.length }}</span>
      <span class="inline-actions">
        <button class="icon-button" :title="t('refresh')" @click="load"><RefreshCw :class="{ spinning: loading }" /></button>
      </span>
    </div>

    <div class="kafka-table" style="max-height: 34%">
      <div class="kafka-table-header groups-cols">
        <span>{{ t("groups.colGroup") }}</span>
        <span>{{ t("groups.colState") }}</span>
        <span>{{ t("groups.colProtocol") }}</span>
        <span>{{ t("groups.colCoordinator") }}</span>
        <span />
      </div>
      <div class="kafka-table-rows">
        <p v-if="groups.length === 0 && !loading" class="empty compact">{{ t("groups.empty") }}</p>
        <div
          v-for="group in groups"
          :key="group.group"
          class="kafka-table-row groups-cols"
          :class="{ selected: selected?.group === group.group }"
          style="cursor: pointer"
          @click="selectGroup(group)"
        >
          <span class="mono-s">{{ group.group }}</span>
          <span>{{ group.state ?? "—" }}</span>
          <span>{{ group.protocolType ?? "—" }}</span>
          <span class="mono-s">{{ group.coordinator ?? "—" }}</span>
          <span class="inline-actions">
            <button class="qb-add" type="button" :disabled="!canWrite" :title="canWrite ? t('groups.reset') : t('readOnly')" @click.stop="askReset(group)">
              {{ t("groups.reset") }}
            </button>
            <button
              class="qb-add"
              type="button"
              :disabled="!canDelete"
              :title="canDelete ? t('groups.delete') : canWrite ? t('noDelete') : t('readOnly')"
              @click.stop="askDelete(group)"
            >
              <Trash2 aria-hidden="true" />
            </button>
          </span>
        </div>
      </div>
    </div>

    <template v-if="selected">
      <p class="subpanel-title">
        {{ t("groups.offsetsTitle", { group: selected.group }) }}
        <span v-if="totalLag !== null" class="badge" :class="lagBadgeClass(totalLag)">{{ t("groups.totalLag", { lag: totalLag }) }}</span>
        <span v-if="!hasCommitted" class="badge badge-warn">{{ t("groups.hasCommittedFalse") }}</span>
      </p>
      <div class="kafka-table" style="max-height: 26%">
        <div class="kafka-table-header group-offsets-cols">
          <span>{{ t("groups.colTopic") }}</span>
          <span>#</span>
          <span>{{ t("groups.colStart") }}</span>
          <span>{{ t("groups.colEnd") }}</span>
          <span>{{ t("groups.colCommitted") }}</span>
          <span>{{ t("groups.colLag") }}</span>
        </div>
        <div class="kafka-table-rows">
          <p v-if="offsetRows.length === 0" class="empty compact">{{ t("groups.offsetsEmpty") }}</p>
          <p v-else-if="!hasCommitted" class="hint" style="padding: 0 8px">{{ t("groups.noCommitted") }}</p>
          <div v-for="row in offsetRows" :key="`${row.topic}:${row.partition}`" class="kafka-table-row group-offsets-cols" style="cursor: default">
            <span class="mono-s">{{ row.topic }}</span>
            <span class="mono-s">{{ row.partition }}</span>
            <span class="mono-s">{{ row.startOffset ?? "—" }}</span>
            <span class="mono-s">{{ row.endOffset ?? "—" }}</span>
            <span class="mono-s">
              {{ row.hasCommitted === false ? t("groups.hasCommittedFalse") : (row.committedOffset ?? "—") }}
            </span>
            <span class="mono-s">
              <span v-if="row.lag !== undefined && row.lag !== null" class="badge" :class="lagBadgeClass(row.lag)">{{ row.lag }}</span>
              <template v-else>—</template>
            </span>
          </div>
        </div>
      </div>

      <p class="subpanel-title">{{ t("groups.describeTitle", { group: selected.group }) }}</p>
      <div class="kafka-table" style="max-height: 22%">
        <div class="kafka-table-rows">
          <p v-if="members.length === 0" class="empty compact">{{ t("groups.noMembers") }}</p>
          <div v-for="member in members" :key="member.memberId" class="kafka-table-row member-cols" style="cursor: default">
            <span class="mono-s" :title="member.memberId">{{ member.memberId }}</span>
            <span class="mono-s">{{ member.instanceId ?? "—" }}</span>
            <span class="mono-s">{{ member.clientId ?? "—" }}</span>
            <span class="mono-s">{{ member.clientHost ?? "—" }}</span>
            <span class="mono-s">{{ assignmentsText(member) || "—" }}</span>
          </div>
        </div>
      </div>
    </template>

    <teleport to="body">
      <div v-if="resetOpen" class="modal-backdrop" @click.self="resetOpen = false">
        <div class="modal">
          <header>
            <h2>{{ t("groups.resetTitle", { group: selected?.group ?? "" }) }}</h2>
            <button class="icon-button" :title="t('close')" @click="resetOpen = false">✕</button>
          </header>
          <div class="settings-body">
            <label class="settings-field">
              <span>{{ t("groups.resetTopics") }}</span>
              <input v-model="resetTopics" type="text" spellcheck="false" />
            </label>
            <label class="settings-field">
              <span>{{ t("groups.resetTo") }}</span>
              <select v-model="resetTo">
                <option value="earliest">{{ t("groups.resetEarliest") }}</option>
                <option value="latest">{{ t("groups.resetLatest") }}</option>
                <option value="timestamp">{{ t("groups.resetTimestamp") }}</option>
                <option value="partitionOffset">{{ t("groups.resetPartitionOffset") }}</option>
              </select>
            </label>
            <label v-if="resetTo === 'timestamp'" class="settings-field">
              <span>{{ t("groups.resetTimestampMs") }}</span>
              <input v-model="resetTimestampMs" type="number" min="0" />
            </label>
            <label v-if="resetTo === 'partitionOffset'" class="settings-field">
              <span>{{ t("groups.resetPartitionOffsets") }}</span>
              <input v-model="resetPartitionOffsets" type="text" :placeholder="t('groups.resetPartitionOffsetsHint')" class="mono" spellcheck="false" />
            </label>
          </div>
          <footer>
            <button type="button" @click="resetOpen = false">{{ t("cancel") }}</button>
            <button class="primary-button" type="button" :disabled="busy" @click="submitReset">{{ t("groups.resetRun") }}</button>
          </footer>
        </div>
      </div>

      <div v-if="deleteOpen" class="modal-backdrop" @click.self="deleteOpen = false">
        <div class="modal">
          <header>
            <h2>{{ t("groups.deleteTitle", { group: deleteTarget }) }}</h2>
            <button class="icon-button" :title="t('close')" @click="deleteOpen = false">✕</button>
          </header>
          <div class="destructive-copy">
            <div class="destructive-icon"><Trash2 aria-hidden="true" /></div>
            <div>
              <strong class="mono">{{ deleteTarget }}</strong>
              <p>{{ t("groups.deleteMessage") }}</p>
            </div>
          </div>
          <footer>
            <button type="button" @click="deleteOpen = false">{{ t("cancel") }}</button>
            <button class="danger-button" type="button" :disabled="busy" @click="submitDelete">{{ t("delete") }}</button>
          </footer>
        </div>
      </div>
    </teleport>
  </section>
</template>

<style scoped>
.groups-cols {
  grid-template-columns: minmax(140px, 2fr) minmax(90px, 1fr) minmax(90px, 1fr) 70px auto;
}
.group-offsets-cols {
  grid-template-columns: minmax(140px, 2fr) 44px repeat(4, minmax(70px, 1fr));
}
.member-cols {
  grid-template-columns: minmax(140px, 1.4fr) minmax(90px, 1fr) minmax(100px, 1fr) minmax(100px, 1fr) minmax(140px, 1.6fr);
}
</style>
