<script setup lang="ts">
// 消费组面板（Phase 2 ag-grid 版）：组列表 / offsets 表 / members 表均走
// DbxAgGrid（列排序 + 列内过滤 + 分页持久化），行选择驱动 describe/load；
// reset/delete 从行内按钮收敛为工具栏动作（作用于选中组），门禁不变：
// reset 按 canWrite、delete 按 canDelete（read_only 下后端同规则拒绝）。
import { computed, onMounted, ref } from "vue";
import { RefreshCw, Trash2 } from "@lucide/vue";
import type { ColDef } from "ag-grid-community";
import DbxAgGrid from "./DbxAgGrid.vue";
import { kafkaApi, type GroupMember, type GroupOffsetRow, type KafkaGroup } from "../lib/api";
import {
  MINIMAL_GROUP_FIELDS,
  MINIMAL_GROUP_OFFSET_FIELDS,
  MINIMAL_MEMBER_FIELDS,
  groupColumns,
  groupOffsetColumns,
  memberColumns,
  toGroupOffsetRows,
  toGroupRows,
  toMemberRows,
  type GroupOffsetVm,
  type GroupRow,
  type MemberVm,
} from "../lib/kafkaColumns";
import { parseGroupOffsetTargetsText, sumLag } from "../lib/kafkaModel";
import { friendlyKafkaError } from "../lib/kafkaErrors";
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

const groupGridRows = computed(() => toGroupRows(groups.value));
const groupGridCols = computed(() => groupColumns() as ColDef<GroupRow>[]);
const offsetGridRows = computed(() => toGroupOffsetRows(offsetRows.value));
const offsetGridCols = computed(() => groupOffsetColumns() as ColDef<GroupOffsetVm>[]);
const memberGridRows = computed(() => toMemberRows(members.value));
const memberGridCols = computed(() => memberColumns() as ColDef<MemberVm>[]);

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

function selectGroup(row: GroupRow | null) {
  const group = row?.raw ?? null;
  selected.value = group;
  offsetRows.value = [];
  totalLag.value = null;
  hasCommitted.value = true;
  members.value = [];
  if (!group) return;
  void loadGroupDetail(group);
}

async function loadGroupDetail(group: KafkaGroup) {
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

function askReset() {
  if (!selected.value) return;
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
  const extra: { timestampMs?: number; partitionOffsets?: Record<string, Record<string, number>> } = {};
  if (resetTo.value === "timestamp") {
    const parsed = Number.parseInt(resetTimestampMs.value.trim(), 10);
    if (!Number.isFinite(parsed)) {
      emit("error", t("messages.timestampRequired"));
      return;
    }
    extra.timestampMs = parsed;
  }
  if (resetTo.value === "partitionOffset") {
    const parsed = parseGroupOffsetTargetsText(resetPartitionOffsets.value, topics);
    if (Object.keys(parsed.targets).length === 0) {
      emit("error", t("messages.offsetsRequired"));
      return;
    }
    if (parsed.invalid.length > 0) {
      emit("error", t("groups.resetOffsetsInvalid") + " " + parsed.invalid.join(", "));
      return;
    }
    extra.partitionOffsets = parsed.targets;
  }
  busy.value = true;
  try {
    const response = await kafkaApi.groupsOffsetsReset(selected.value.group, topics, resetTo.value, extra);
    const failed = (response.rows ?? []).filter((row) => !row.ok);
    if (failed.length > 0) {
      // 行级错误经 friendlyKafkaError 归一（未映射原文兜底）。
      emit("error", failed.map((row) => `${row.topic}-${row.partition}: ${row.error ? friendlyKafkaError(row.error) : "failed"}`).join("; "));
    } else {
      emit("notify", t("groups.resetDone"));
    }
    resetOpen.value = false;
    await loadGroupDetail(selected.value);
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

function askDelete() {
  if (!selected.value) return;
  deleteTarget.value = selected.value.group;
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
        <button class="qb-add" type="button" :disabled="!canWrite || !selected || busy" :title="canWrite ? t('groups.reset') : t('readOnly')" @click="askReset">
          {{ t("groups.reset") }}
        </button>
        <button class="qb-add" type="button" :disabled="!canDelete || !selected || busy" :title="canDelete ? t('groups.delete') : canWrite ? t('noDelete') : t('readOnly')" @click="askDelete">
          <Trash2 aria-hidden="true" />
        </button>
      </span>
    </div>

    <div class="grid-box" style="height: 240px">
      <p v-if="groups.length === 0 && !loading" class="empty compact">{{ t("groups.empty") }}</p>
      <DbxAgGrid
        v-else
        table-key="groups"
        :row-data="groupGridRows"
        :column-defs="groupGridCols"
        :compact-fields="MINIMAL_GROUP_FIELDS"
        row-selection="single"
        @selection-changed="(row: unknown) => selectGroup(row as GroupRow | null)"
      />
    </div>

    <template v-if="selected">
      <p class="subpanel-title">
        {{ t("groups.offsetsTitle", { group: selected.group }) }}
        <span v-if="totalLag !== null" class="badge" :class="lagBadgeClass(totalLag)">{{ t("groups.totalLag", { lag: totalLag }) }}</span>
        <span v-if="!hasCommitted" class="badge badge-warn">{{ t("groups.hasCommittedFalse") }}</span>
      </p>
      <div class="grid-box" style="height: 200px">
        <p v-if="offsetRows.length === 0" class="empty compact">{{ t("groups.offsetsEmpty") }}</p>
        <template v-else>
          <p v-if="!hasCommitted" class="hint" style="padding: 0 8px">{{ t("groups.noCommitted") }}</p>
          <DbxAgGrid
            table-key="group-offsets"
            :row-data="offsetGridRows"
            :column-defs="offsetGridCols"
            :compact-fields="MINIMAL_GROUP_OFFSET_FIELDS"
            :row-selection="false"
            :emit-row-click="false"
          />
        </template>
      </div>

      <p class="subpanel-title">{{ t("groups.describeTitle", { group: selected.group }) }}</p>
      <div class="grid-box" style="height: 170px">
        <p v-if="members.length === 0" class="empty compact">{{ t("groups.noMembers") }}</p>
        <DbxAgGrid
          v-else
          table-key="group-members"
          :row-data="memberGridRows"
          :column-defs="memberGridCols"
          :compact-fields="MINIMAL_MEMBER_FIELDS"
          :row-selection="false"
          :emit-row-click="false"
        />
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
/* P2 统一禁用态：只读下重置/删除按钮补强。 */
button:disabled,
input:disabled,
select:disabled {
  cursor: not-allowed;
}
button:disabled {
  filter: grayscale(0.4);
}
</style>
