<script setup lang="ts">
// ACL 面板：list（过滤条件至少一项具体值，过宽被后端拒绝，前端预检）/
// create / delete（按过滤条件删除，回显 matched 数）。
// 枚举值（资源类型/操作/许可）为 Kafka 协议术语，保持原文不翻译。
import { computed, onMounted, ref } from "vue";
import { Plus, RefreshCw, Trash2 } from "@lucide/vue";
import { kafkaApi, type AclFilter, type KafkaAcl } from "../lib/api";
import { t } from "../lib/i18n";

const props = defineProps<{
  canWrite: boolean;
  canDelete: boolean;
}>();

const emit = defineEmits<{
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
}>();

const RESOURCE_TYPES = ["ANY", "TOPIC", "GROUP", "CLUSTER", "TRANSACTIONAL_ID", "DELEGATION_TOKEN"];
const PATTERN_TYPES = ["ANY", "MATCH", "LITERAL", "PREFIXED"];
const OPERATIONS = ["ANY", "ALL", "READ", "WRITE", "CREATE", "DELETE", "ALTER", "DESCRIBE", "CLUSTER_ACTION", "DESCRIBE_CONFIGS", "ALTER_CONFIGS", "IDEMPOTENT_WRITE"];
const PERMISSIONS = ["ANY", "ALLOW", "DENY"];

const acls = ref<KafkaAcl[]>([]);
const loading = ref(false);
const busy = ref(false);

const filter = ref<AclFilter>({});
const filterLocalError = ref("");

const createOpen = ref(false);
const createForm = ref<KafkaAcl>({ resourceType: "TOPIC", resourceName: "", principal: "", host: "*", operation: "READ", permission: "ALLOW", patternType: "LITERAL" });

const deleteOpen = ref(false);

const hasConcreteFilter = computed(() => {
  const candidate = filter.value;
  return Boolean(candidate.resourceName?.trim() || candidate.principal?.trim());
});

async function load() {
  filterLocalError.value = "";
  if (!hasConcreteFilter.value) {
    filterLocalError.value = t("acls.filterTooBroad");
    acls.value = [];
    return;
  }
  loading.value = true;
  emit("error", "");
  try {
    const response = await kafkaApi.aclsList(cleanFilter(filter.value));
    acls.value = response.acls ?? [];
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    loading.value = false;
  }
}

function cleanFilter(input: AclFilter): AclFilter {
  const cleaned: AclFilter = {};
  for (const [key, value] of Object.entries(input)) {
    const text = typeof value === "string" ? value.trim() : "";
    if (text) (cleaned as Record<string, string>)[key] = text;
  }
  return cleaned;
}

async function submitCreate() {
  const acl = { ...createForm.value, resourceName: createForm.value.resourceName.trim(), principal: createForm.value.principal.trim() };
  if (!acl.resourceName || !acl.principal) {
    emit("error", t("topics.createInvalid"));
    return;
  }
  busy.value = true;
  try {
    await kafkaApi.aclsCreate(acl);
    createOpen.value = false;
    emit("notify", t("acls.created"));
    filter.value = { resourceType: acl.resourceType, resourceName: acl.resourceName, patternType: acl.patternType, principal: acl.principal };
    await load();
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

async function submitDelete() {
  busy.value = true;
  try {
    const response = await kafkaApi.aclsDelete(cleanFilter(filter.value));
    emit("notify", t("acls.deleted", { count: response.matched ?? 0 }));
    deleteOpen.value = false;
    await load();
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    busy.value = false;
  }
}

onMounted(() => {
  filter.value = { resourceType: "ANY", resourceName: "", principal: "" };
});
</script>

<template>
  <section class="section-block">
    <p class="subpanel-title">{{ t("acls.filterTitle") }}</p>
    <div class="kafka-form">
      <label class="field">
        <span>{{ t("acls.resourceType") }}</span>
        <select v-model="filter.resourceType">
          <option v-for="value in RESOURCE_TYPES" :key="value" :value="value">{{ value }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ t("acls.resourceName") }}</span>
        <input v-model="filter.resourceName" type="text" spellcheck="false" />
      </label>
      <label class="field">
        <span>{{ t("acls.patternType") }}</span>
        <select v-model="filter.patternType">
          <option :value="undefined">{{ t("acls.anyValue") }}</option>
          <option v-for="value in PATTERN_TYPES" :key="value" :value="value">{{ value }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ t("acls.principal") }}</span>
        <input v-model="filter.principal" type="text" spellcheck="false" />
      </label>
      <label class="field">
        <span>{{ t("acls.operation") }}</span>
        <select v-model="filter.operation">
          <option :value="undefined">{{ t("acls.anyValue") }}</option>
          <option v-for="value in OPERATIONS" :key="value" :value="value">{{ value }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ t("acls.permission") }}</span>
        <select v-model="filter.permission">
          <option :value="undefined">{{ t("acls.anyValue") }}</option>
          <option v-for="value in PERMISSIONS" :key="value" :value="value">{{ value }}</option>
        </select>
      </label>
      <button class="primary-button compact" type="button" :disabled="loading" @click="load">
        <RefreshCw :class="{ spinning: loading }" aria-hidden="true" />{{ t("acls.filterRun") }}
      </button>
      <button class="toolbar-button" type="button" :disabled="!canWrite" :title="canWrite ? t('acls.create') : t('readOnly')" @click="createOpen = true">
        <Plus aria-hidden="true" /><span>{{ t("acls.create") }}</span>
      </button>
      <button class="danger-button compact" type="button" :disabled="!canDelete || acls.length === 0" :title="canDelete ? t('acls.delete') : t('noDelete')" @click="deleteOpen = true">
        <Trash2 aria-hidden="true" /><span>{{ t("acls.delete") }}</span>
      </button>
    </div>
    <p v-if="filterLocalError" class="form-error" style="padding: 0 8px 4px">{{ filterLocalError }}</p>

    <div class="kafka-table">
      <div class="kafka-table-header acl-cols">
        <span>{{ t("acls.resourceType") }}</span>
        <span>{{ t("acls.resourceName") }}</span>
        <span>{{ t("acls.patternType") }}</span>
        <span>{{ t("acls.principal") }}</span>
        <span>{{ t("acls.host") }}</span>
        <span>{{ t("acls.operation") }}</span>
        <span>{{ t("acls.permission") }}</span>
      </div>
      <div class="kafka-table-rows">
        <p v-if="acls.length === 0 && !loading" class="empty compact">{{ t("acls.empty") }}</p>
        <div v-for="(acl, index) in acls" :key="index" class="kafka-table-row acl-cols" style="cursor: default">
          <span class="mono-s">{{ acl.resourceType }}</span>
          <span class="mono-s">{{ acl.resourceName }}</span>
          <span class="mono-s">{{ acl.patternType || "LITERAL" }}</span>
          <span class="mono-s">{{ acl.principal }}</span>
          <span class="mono-s">{{ acl.host || "*" }}</span>
          <span class="mono-s">{{ acl.operation }}</span>
          <span class="mono-s">{{ acl.permission }}</span>
        </div>
      </div>
    </div>

    <teleport to="body">
      <div v-if="createOpen" class="modal-backdrop" @click.self="createOpen = false">
        <div class="modal">
          <header>
            <h2>{{ t("acls.createTitle") }}</h2>
            <button class="icon-button" :title="t('close')" @click="createOpen = false">✕</button>
          </header>
          <div class="settings-body">
            <label class="settings-field">
              <span>{{ t("acls.resourceType") }}</span>
              <select v-model="createForm.resourceType">
                <option v-for="value in RESOURCE_TYPES.filter((v) => v !== 'ANY')" :key="value" :value="value">{{ value }}</option>
              </select>
            </label>
            <label class="settings-field">
              <span>{{ t("acls.resourceName") }}</span>
              <input v-model="createForm.resourceName" type="text" spellcheck="false" />
            </label>
            <label class="settings-field">
              <span>{{ t("acls.patternType") }}</span>
              <select v-model="createForm.patternType">
                <option v-for="value in PATTERN_TYPES.filter((v) => v !== 'ANY')" :key="value" :value="value">{{ value }}</option>
              </select>
            </label>
            <label class="settings-field">
              <span>{{ t("acls.principal") }}</span>
              <input v-model="createForm.principal" type="text" placeholder="User:app" spellcheck="false" />
            </label>
            <label class="settings-field">
              <span>{{ t("acls.host") }}</span>
              <input v-model="createForm.host" type="text" placeholder="*" spellcheck="false" />
            </label>
            <label class="settings-field">
              <span>{{ t("acls.operation") }}</span>
              <select v-model="createForm.operation">
                <option v-for="value in OPERATIONS.filter((v) => v !== 'ANY')" :key="value" :value="value">{{ value }}</option>
              </select>
            </label>
            <label class="settings-field">
              <span>{{ t("acls.permission") }}</span>
              <select v-model="createForm.permission">
                <option v-for="value in PERMISSIONS.filter((v) => v !== 'ANY')" :key="value" :value="value">{{ value }}</option>
              </select>
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
            <h2>{{ t("acls.deleteTitle") }}</h2>
            <button class="icon-button" :title="t('close')" @click="deleteOpen = false">✕</button>
          </header>
          <div class="destructive-copy">
            <div class="destructive-icon"><Trash2 aria-hidden="true" /></div>
            <div>
              <strong>{{ t("acls.deleteTitle") }}</strong>
              <p class="mono-s">{{ JSON.stringify(cleanFilter(filter)) }}</p>
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
.acl-cols {
  grid-template-columns: minmax(80px, 1fr) minmax(110px, 1.4fr) 70px minmax(120px, 1.2fr) 80px minmax(100px, 1fr) 80px;
}
</style>
