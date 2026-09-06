<script setup lang="ts">
// 搜索表单：可视化条件构建器（FilterGroup 递归树，AND/OR 嵌套 ≤2 层）+
// 源码模式（RFC 4515 串直接编辑，双向：串→结构尽力解析，失败保持源码模式）
// + scope/attributes/sizeLimit/pageSize/typesOnly/derefAliases
// + 预设（结构化条件与过滤器串一并保存，sidecar ldap/presets/* 方法）。
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Play, Save, Trash2 } from "@lucide/vue";
import { getLdapConnectionId, ldapApi, type LdapSearchPreset, type LdapScope } from "../lib/api";
import { validateLDAPFilter, buildNodeFilter, collectBuilderErrors, parseFilterStructure, toBuilderRoot, reviveBuilderNode, createBuilderClause, createBuilderGroup, type BuilderGroup } from "../lib/ldapFilter";
import { deriveSchemaMetadata, useLdapSchemaCache } from "../lib/schemaCache";
import { t } from "../lib/i18n";
import FilterGroup from "./FilterGroup.vue";

export interface SearchFormModel {
  baseDn: string;
  filter: string;
  scope: LdapScope;
  attributes: string;
  sizeLimit: string;
  pageSize: string;
  typesOnly: boolean;
  derefAliases: "never" | "searching" | "finding" | "always";
}

const props = defineProps<{
  baseDn: string;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: "run", model: SearchFormModel): void;
  (e: "notify", message: string): void;
  (e: "error", message: string): void;
}>();

const draft = ref<SearchFormModel>({
  baseDn: props.baseDn,
  filter: "(objectClass=*)",
  scope: "sub",
  attributes: "",
  sizeLimit: "500",
  pageSize: "500",
  typesOnly: false,
  derefAliases: "never",
});

// -- filter builder / source modes --------------------------------------------

const builderMode = ref(true);
const builderRoot = ref<BuilderGroup>(createBuilderGroup({ children: [createBuilderClause()] }));
const sourceFilter = ref("");
const sourceParseError = ref(false);
const running = ref(false);

const ATTR_LIST_ID = "ldap-builder-attr-options";
// schema 不可用时的常用属性兜底（tiny-rdm 常用集）。
const COMMON_ATTRIBUTES = [
  "objectClass", "cn", "sn", "givenName", "uid", "mail", "ou", "o", "dc",
  "displayName", "telephoneNumber", "member", "description", "title",
  "sAMAccountName", "userPrincipalName", "createTimestamp", "modifyTimestamp",
];

const { attributeNames, ensureLoaded } = useLdapSchemaCache({
  loader: () => ldapApi.schema().then((result) => deriveSchemaMetadata(result.attributeTypes, result.objectClasses)),
});

const attributeOptions = computed(() => {
  const seen = new Set<string>();
  const options: string[] = [];
  for (const name of [...COMMON_ATTRIBUTES, ...attributeNames.value]) {
    const key = name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    options.push(name);
  }
  return options;
});

const generatedFilter = computed(() => buildNodeFilter(builderRoot.value));
const builderErrors = computed(() => collectBuilderErrors(builderRoot.value));
const builderValid = computed(() => builderErrors.value.length === 0);

const builderErrorMessage = computed(() => {
  const first = builderErrors.value[0];
  if (!first) return "";
  if (first.code === "attribute_required" || first.code === "attribute_invalid") return t("search.builderAttrRequired");
  return t("search.builderValueRequired");
});

const sourceValid = computed(() => {
  const value = sourceFilter.value.trim();
  // 空过滤器 = 匹配全部（运行时回退 (objectClass=*)），合法不算错。
  return value === "" || validateLDAPFilter(value);
});

const filterValid = computed(() => (builderMode.value ? builderValid.value : sourceValid.value));

function switchToSource() {
  if (props.disabled) return;
  sourceFilter.value = generatedFilter.value || draft.value.filter;
  sourceParseError.value = false;
  builderMode.value = false;
}

function switchToBuilder() {
  if (props.disabled) return;
  const parsed = toBuilderRoot(parseFilterStructure(sourceFilter.value));
  if (!parsed) {
    // 解析失败：保持源码模式（源码是唯一权威表示）。
    sourceParseError.value = true;
    return;
  }
  builderRoot.value = parsed;
  sourceParseError.value = false;
  builderMode.value = true;
}

// -- base DN / fields ---------------------------------------------------------

// 树节点联动改写 Base DN 的一次性高亮（UI 扫描 P2-7：此前静默改写，
// 整树搜索悄然变单条目搜索）。变化时高亮 1.6s + title 说明。
const baseDnHighlighted = ref(false);
let baseDnHighlightTimer = 0;

// highlight=false 供初始化/自动定位使用（首次回填不算"跟随选中"），
// 并取消进行中的高亮。
function applyBaseDn(next: string, highlight = true) {
  const changed = next !== draft.value.baseDn && next.trim() !== "";
  draft.value.baseDn = next;
  window.clearTimeout(baseDnHighlightTimer);
  if (!changed || !highlight) {
    baseDnHighlighted.value = false;
    return;
  }
  baseDnHighlighted.value = true;
  baseDnHighlightTimer = window.setTimeout(() => (baseDnHighlighted.value = false), 1600);
}

onBeforeUnmount(() => window.clearTimeout(baseDnHighlightTimer));

defineExpose({ applyBaseDn });

function parseAttributes(): string[] | undefined {
  const list = draft.value.attributes
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean);
  return list.length > 0 ? list : undefined;
}

function positiveNumber(value: string): number | undefined {
  const parsed = Number.parseInt(value.trim(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
}

// 数值字段输入校验（UI 扫描 P2-16）：非法输入不再静默按"无限制"发出；
// 留空或 0 合法（= 不限制），负数/非数字给行内红字。
function numericInvalid(value: string): boolean {
  const trimmed = value.trim();
  return trimmed !== "" && trimmed !== "0" && positiveNumber(trimmed) === undefined;
}
const sizeLimitInvalid = computed(() => numericInvalid(draft.value.sizeLimit));
const pageSizeInvalid = computed(() => numericInvalid(draft.value.pageSize));

function activeFilter(): string {
  return builderMode.value ? generatedFilter.value : sourceFilter.value.trim();
}

function toModel(): SearchFormModel {
  return { ...draft.value, filter: activeFilter() || "(objectClass=*)" };
}

function run() {
  if (props.disabled || running.value || !filterValid.value) return;
  emit("run", toModel());
}

// -- presets -------------------------------------------------------------------

const presets = ref<LdapSearchPreset[]>([]);
const selectedPresetId = ref("");
const presetNameDraft = ref("");

async function loadPresets() {
  if (props.disabled) return;
  try {
    const result = await ldapApi.presetsList();
    presets.value = Array.isArray(result.presets) ? result.presets : [];
  } catch {
    // presets are a convenience feature; a missing/failed backend must not
    // break the workbench — keep the local list empty.
    presets.value = [];
  }
}

function applyPreset() {
  const preset = presets.value.find((entry) => entry.id === selectedPresetId.value);
  if (!preset) return;
  draft.value = {
    ...draft.value,
    baseDn: preset.baseDn || draft.value.baseDn,
    filter: preset.filter || draft.value.filter,
    scope: preset.scope || draft.value.scope,
    attributes: Array.isArray(preset.attributes) ? preset.attributes.join(", ") : "",
    sizeLimit: preset.sizeLimit != null ? String(preset.sizeLimit) : draft.value.sizeLimit,
  };
  // 结构化条件优先；无结构时尽力把过滤器串解析回构建器。
  const revived = toBuilderRoot(reviveBuilderNode(preset.conditions));
  const fromFilter = revived ?? toBuilderRoot(parseFilterStructure(preset.filter));
  if (fromFilter) {
    builderRoot.value = fromFilter;
    builderMode.value = true;
    sourceParseError.value = false;
  } else {
    sourceFilter.value = preset.filter || "(objectClass=*)";
    builderMode.value = false;
  }
  emit("notify", t("search.presetApplied"));
}

async function savePreset() {
  if (props.disabled) return;
  const name = presetNameDraft.value.trim();
  if (!name) return;
  const filter = activeFilter() || "(objectClass=*)";
  // 重名处理：未选中预设时输入已存在的名字 → 原地更新该预设（沿用其 id），
  // 不产生同名双条目；选中状态下保存仍按所选 id 覆盖（含改名）。
  const existingByName = selectedPresetId.value ? undefined : presets.value.find((entry) => entry.name === name);
  const preset: LdapSearchPreset = {
    id: selectedPresetId.value || existingByName?.id || `${Date.now()}`,
    name,
    baseDn: draft.value.baseDn.trim(),
    filter,
    scope: draft.value.scope,
    attributes: parseAttributes(),
    sizeLimit: positiveNumber(draft.value.sizeLimit),
    // 构建器模式存结构化条件（串由其派生），源码模式只存串。
    ...(builderMode.value ? { conditions: JSON.parse(JSON.stringify(builderRoot.value)) as unknown } : {}),
  };
  try {
    const result = await ldapApi.presetsSave(preset);
    presets.value = Array.isArray(result.presets) ? result.presets : [...presets.value, preset];
    selectedPresetId.value = preset.id;
    presetNameDraft.value = name;
    emit("notify", t("search.presetSaved"));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

async function removePreset() {
  if (props.disabled || !selectedPresetId.value) return;
  // 预设是持久化数据（UI 扫描 P2-21）：删除前确认，误删不可恢复。
  const name = presets.value.find((preset) => preset.id === selectedPresetId.value)?.name ?? "";
  if (!window.confirm(t("search.presetRemoveConfirm", { name }))) return;
  try {
    const result = await ldapApi.presetsRemove(selectedPresetId.value);
    presets.value = Array.isArray(result.presets) ? result.presets : presets.value.filter((entry) => entry.id !== selectedPresetId.value);
    selectedPresetId.value = "";
    emit("notify", t("search.presetRemoved"));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

watch(
  () => props.baseDn,
  (next) => {
    draft.value.baseDn = next;
    void loadPresets();
  },
);

onMounted(() => {
  void loadPresets();
  // schema 属性下拉：失败静默（下拉仍有 COMMON_ATTRIBUTES 兜底）。
  void ensureLoaded(getLdapConnectionId()).catch(() => undefined);
});

const scopeOptions = computed(() => [
  { value: "base" as LdapScope, label: t("search.scopeBase") },
  { value: "one" as LdapScope, label: t("search.scopeOne") },
  { value: "sub" as LdapScope, label: t("search.scopeSub") },
]);

const derefOptions = computed(() => [
  { value: "never" as const, label: t("search.derefNever") },
  { value: "searching" as const, label: t("search.derefSearching") },
  { value: "finding" as const, label: t("search.derefFinding") },
  { value: "always" as const, label: t("search.derefAlways") },
]);
</script>

<template>
  <form class="search-form" @submit.prevent="run">
    <label class="field">
      <span>{{ t("search.baseDn") }}</span>
      <input
        v-model="draft.baseDn"
        type="text"
        class="mono"
        :class="{ 'base-dn-flash': baseDnHighlighted }"
        :title="baseDnHighlighted ? t('search.baseFollowed') : undefined"
        :disabled="disabled"
        spellcheck="false"
      />
    </label>
    <label class="field">
      <span>{{ t("search.scope") }}</span>
      <select v-model="draft.scope" :disabled="disabled">
        <option v-for="option in scopeOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
      </select>
    </label>
    <div class="field" style="justify-content: flex-end">
      <button
        class="primary-button compact"
        type="submit"
        :disabled="disabled || running || !filterValid"
        :title="!filterValid ? t('search.filterInvalid') : activeFilter() || t('search.filterAll')"
      >
        <Play aria-hidden="true" />{{ running ? t("search.running") : t("search.run") }}
      </button>
      <span v-if="!filterValid" class="form-error">{{ builderMode ? builderErrorMessage : t("search.filterInvalid") }}</span>
    </div>

    <div class="filter-block">
      <div class="filter-head">
        <span>{{ t("search.filter") }}</span>
        <span class="mode-switch">
          <button type="button" :class="{ 'is-active': builderMode }" :disabled="disabled" @click="builderMode || switchToBuilder()">
            {{ t("search.modeBuilder") }}
          </button>
          <button type="button" :class="{ 'is-active': !builderMode }" :disabled="disabled" @click="builderMode && switchToSource()">
            {{ t("search.modeSource") }}
          </button>
        </span>
      </div>

      <div v-if="builderMode" class="filter-builder">
        <FilterGroup :group="builderRoot" :depth="0" :disabled="disabled" :list-id="ATTR_LIST_ID" />
        <p class="qb-preview mono" :title="t('search.filter')">{{ generatedFilter || "(objectClass=*)" }}</p>
      </div>
      <div v-else class="filter-source">
        <input
          v-model="sourceFilter"
          type="text"
          class="mono"
          :placeholder="t('search.filterPlaceholder')"
          :disabled="disabled"
          spellcheck="false"
        />
        <span v-if="sourceParseError" class="form-error">{{ t("search.builderParseFailed") }}</span>
      </div>
      <datalist :id="ATTR_LIST_ID">
        <option v-for="name in attributeOptions" :key="name" :value="name" />
      </datalist>
      <p class="filter-hint">{{ t("search.filterEmptyHint") }}</p>
    </div>

    <div class="search-extra">
      <label class="field">
        <span>{{ t("search.attributes") }}</span>
        <input v-model="draft.attributes" type="text" :disabled="disabled" spellcheck="false" />
      </label>
      <label class="field" :title="t('search.numericHint')">
        <span>{{ t("search.sizeLimit") }}</span>
        <input v-model="draft.sizeLimit" type="text" inputmode="numeric" :disabled="disabled" class="numeric" :aria-invalid="sizeLimitInvalid" />
        <span v-if="sizeLimitInvalid" class="form-error">{{ t("search.invalidNumber") }}</span>
      </label>
      <label class="field" :title="t('search.numericHint')">
        <span>{{ t("search.pageSize") }}</span>
        <input v-model="draft.pageSize" type="text" inputmode="numeric" :disabled="disabled" class="numeric" :aria-invalid="pageSizeInvalid" />
        <span v-if="pageSizeInvalid" class="form-error">{{ t("search.invalidNumber") }}</span>
      </label>
      <label class="field">
        <span>{{ t("search.typesOnly") }}</span>
        <select v-model="draft.typesOnly" :disabled="disabled">
          <option :value="false">{{ t("search.typesOnlyNo") }}</option>
          <option :value="true">{{ t("search.typesOnlyYes") }}</option>
        </select>
      </label>
      <label class="field">
        <span>{{ t("search.deref") }}</span>
        <select v-model="draft.derefAliases" :disabled="disabled">
          <option v-for="option in derefOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </label>
    </div>
    <div class="search-presets">
      <span class="muted">{{ t("search.presets") }}</span>
      <select v-model="selectedPresetId" :disabled="disabled" @change="applyPreset">
        <option value="">{{ t("search.presetsEmpty") }}</option>
        <option v-for="preset in presets" :key="preset.id" :value="preset.id">{{ preset.name }}</option>
      </select>
      <input v-model="presetNameDraft" type="text" class="preset-input" :placeholder="t('search.presetName')" :disabled="disabled" />
      <button type="button" class="toolbar-button" :disabled="disabled || !presetNameDraft.trim()" :title="t('search.presetSave')" @click="savePreset">
        <Save aria-hidden="true" /><span>{{ t("search.presetSave") }}</span>
      </button>
      <button type="button" class="toolbar-button" :disabled="disabled || !selectedPresetId" :title="t('search.presetRemove')" :aria-label="t('search.presetRemove')" @click="removePreset">
        <Trash2 aria-hidden="true" />
      </button>
    </div>
  </form>
</template>
