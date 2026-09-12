<script setup lang="ts">
// 搜索表单：可视化条件构建器（FilterGroup 递归树，AND/OR 嵌套 ≤2 层）+
// 源码模式（RFC 4515 串直接编辑，双向：串→结构尽力解析，失败保持源码模式）
// + scope/attributes/sizeLimit/pageSize/typesOnly/derefAliases
// + 预设（持久化过滤器串，应用时重建构建器；sidecar 不存 conditions）。
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from "vue";
import { Play, Save, Trash2 } from "@lucide/vue";
import { getLdapConnectionId, ldapApi, type LdapSearchPreset, type LdapScope } from "../lib/api";
import { validateLDAPFilter, buildNodeFilter, collectBuilderErrors, parseFilterStructure, toBuilderRoot, createBuilderClause, createBuilderGroup, type BuilderGroup } from "../lib/ldapFilter";
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
  running?: boolean;
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
const filterErrorId = useId();
const sizeLimitErrorId = useId();
const pageSizeErrorId = useId();

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

defineExpose({ applyBaseDn, runSubtreeAt, runFilterAt, applyIntentSearch });

// 树右键「搜索此子树」直达（此前只改 Base 不执行，用户预期是马上出结果集）：
// Base 指到该节点、范围强制切到子树并立即运行。过滤器沿用表单当前配置；
// 构建器存在半填/未填子句（表单本身不可运行）时回退匹配全部
// (objectClass=*)——右键动作是浏览意图，必须保证出结果集而不是静默失败。
function runSubtreeAt(dn: string) {
  if (props.disabled || !numericValid.value) return;
  applyBaseDn(dn);
  draft.value.scope = "sub";
  const model = toModel();
  emit("run", filterValid.value ? model : { ...model, filter: "(objectClass=*)" });
}

/** 树右键「成员」直达：立即以指定过滤器在指定 Base 下做子树搜索。
 * 过滤器由调用方组织（如 (memberOf=<组DN>)）；源码模式承载该过滤器，
 * 用户可在结果区继续改；Base/范围语义同 runSubtreeAt。 */
function runFilterAt(baseDn: string, filter: string) {
  if (props.disabled || !numericValid.value) return;
  // 防呆：空过滤器视为匹配全部，与 toModel 的兜底语义一致，保证必出结果集。
  const effectiveFilter = filter.trim() === "" ? "(objectClass=*)" : filter;
  // 切到源码模式承载调用方过滤器（源码是唯一权威表示，构建器无需逆向解析）。
  sourceFilter.value = effectiveFilter;
  sourceParseError.value = false;
  builderMode.value = false;
  applyBaseDn(baseDn);
  draft.value.scope = "sub";
  emit("run", { ...toModel(), filter: effectiveFilter });
}

/** MCP UI intent（useUiIntent）落表：条件整体替换走既有 preset 反序列化
 * 路径（过滤器串 → 构建器，解析失败保持源码模式）；scope/sizeLimit 越界
 * 值兜底当前值。返回组装好的模型供 App 触发 runSearch。 */
function applyIntentSearch(params: {
  baseDn?: string;
  filter?: string;
  scope?: string;
  attributes?: string[];
  sizeLimit?: number;
}): SearchFormModel {
  const filter = (params.filter ?? "").trim() || "(objectClass=*)";
  const fromFilter = toBuilderRoot(parseFilterStructure(filter));
  if (fromFilter) {
    builderRoot.value = fromFilter;
    builderMode.value = true;
    sourceParseError.value = false;
  } else {
    sourceFilter.value = filter;
    builderMode.value = false;
  }
  const scope = (params.scope ?? "").trim().toLowerCase();
  const sizeLimit = Number(params.sizeLimit);
  draft.value = {
    ...draft.value,
    baseDn: (params.baseDn ?? "").trim() || draft.value.baseDn,
    filter,
    scope: scope === "base" || scope === "one" || scope === "sub" ? scope : draft.value.scope,
    attributes: Array.isArray(params.attributes) && params.attributes.length ? params.attributes.join(", ") : draft.value.attributes,
    sizeLimit: Number.isFinite(sizeLimit) && sizeLimit > 0 ? String(sizeLimit) : draft.value.sizeLimit,
  };
  return toModel();
}

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
  return trimmed !== "" && (!/^\d+$/u.test(trimmed) || !Number.isSafeInteger(Number(trimmed)));
}
const sizeLimitInvalid = computed(() => numericInvalid(draft.value.sizeLimit));
const pageSizeInvalid = computed(() => numericInvalid(draft.value.pageSize));
const numericValid = computed(() => !sizeLimitInvalid.value && !pageSizeInvalid.value);

function activeFilter(): string {
  return builderMode.value ? generatedFilter.value : sourceFilter.value.trim();
}

function toModel(): SearchFormModel {
  return { ...draft.value, filter: activeFilter() || "(objectClass=*)" };
}

function run() {
  if (props.disabled || props.running || !filterValid.value || !numericValid.value) return;
  emit("run", toModel());
}

// -- presets -------------------------------------------------------------------

const presets = ref<LdapSearchPreset[]>([]);
const selectedPresetId = ref("");
const presetNameDraft = ref("");
const presetPending = ref(false);

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
  presetNameDraft.value = preset.name;
  draft.value = {
    ...draft.value,
    baseDn: preset.baseDn || "",
    filter: preset.filter || "(objectClass=*)",
    scope: preset.scope || "sub",
    attributes: Array.isArray(preset.attributes) ? preset.attributes.join(", ") : "",
    sizeLimit: String(preset.sizeLimit ?? 0),
  };
  // 过滤器串是后端持久化的权威值；旧 mock 的 conditions 不参与恢复。
  const fromFilter = toBuilderRoot(parseFilterStructure(preset.filter || "(objectClass=*)"));
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
  if (props.disabled || presetPending.value || !filterValid.value || !numericValid.value) return;
  const name = presetNameDraft.value.trim();
  if (!name) return;
  const filter = activeFilter() || "(objectClass=*)";
  // 重名处理：未选中预设时输入已存在的名字 → 原地更新该预设（沿用其 id），
  // 不产生同名双条目；选中状态下保存仍按所选 id 覆盖（含改名）。
  const existingByName = selectedPresetId.value ? undefined : presets.value.find((entry) => entry.name === name);
  const preset: LdapSearchPreset = {
    id: selectedPresetId.value || existingByName?.id || "",
    name,
    baseDn: draft.value.baseDn.trim(),
    filter,
    scope: draft.value.scope,
    attributes: parseAttributes(),
    sizeLimit: positiveNumber(draft.value.sizeLimit),
  };
  presetPending.value = true;
  try {
    const result = await ldapApi.presetsSave(preset);
    const saved = result.preset;
    presets.value = [...presets.value.filter((entry) => entry.id !== saved.id), saved];
    selectedPresetId.value = saved.id;
    presetNameDraft.value = saved.name;
    emit("notify", t("search.presetSaved"));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    presetPending.value = false;
  }
}

async function removePreset() {
  if (props.disabled || presetPending.value || !selectedPresetId.value) return;
  const id = selectedPresetId.value;
  // 预设是持久化数据（UI 扫描 P2-21）：删除前确认，误删不可恢复。
  const name = presets.value.find((preset) => preset.id === selectedPresetId.value)?.name ?? "";
  if (!window.confirm(t("search.presetRemoveConfirm", { name }))) return;
  presetPending.value = true;
  try {
    await ldapApi.presetsRemove(id);
    presets.value = presets.value.filter((entry) => entry.id !== id);
    selectedPresetId.value = "";
    emit("notify", t("search.presetRemoved"));
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    presetPending.value = false;
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
        :disabled="disabled || running || !filterValid || !numericValid"
        :title="!numericValid ? t('search.invalidNumber') : !filterValid ? t('search.filterInvalid') : activeFilter() || t('search.filterAll')"
      >
        <Play aria-hidden="true" />{{ running ? t("search.running") : t("search.run") }}
      </button>
      <span v-if="builderMode && !filterValid" class="form-error">{{ builderErrorMessage }}</span>
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
          :aria-label="t('search.filter')"
          :aria-invalid="!sourceValid"
          :aria-describedby="!sourceValid || sourceParseError ? filterErrorId : undefined"
          :disabled="disabled"
          spellcheck="false"
          @input="sourceParseError = false"
        />
        <span v-if="!sourceValid || sourceParseError" :id="filterErrorId" class="form-error" role="alert">{{ !sourceValid ? t("search.filterInvalid") : t("search.builderParseFailed") }}</span>
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
        <input v-model="draft.sizeLimit" type="text" inputmode="numeric" :disabled="disabled" class="numeric" :aria-invalid="sizeLimitInvalid" :aria-describedby="sizeLimitInvalid ? sizeLimitErrorId : undefined" />
        <span v-if="sizeLimitInvalid" :id="sizeLimitErrorId" class="form-error" role="alert">{{ t("search.invalidNumber") }}</span>
      </label>
      <label class="field" :title="t('search.numericHint')">
        <span>{{ t("search.pageSize") }}</span>
        <input v-model="draft.pageSize" type="text" inputmode="numeric" :disabled="disabled" class="numeric" :aria-invalid="pageSizeInvalid" :aria-describedby="pageSizeInvalid ? pageSizeErrorId : undefined" />
        <span v-if="pageSizeInvalid" :id="pageSizeErrorId" class="form-error" role="alert">{{ t("search.invalidNumber") }}</span>
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
      <select v-model="selectedPresetId" :disabled="disabled || presetPending" @change="applyPreset">
        <option value="">{{ t("search.presetsEmpty") }}</option>
        <option v-for="preset in presets" :key="preset.id" :value="preset.id">{{ preset.name }}</option>
      </select>
      <input v-model="presetNameDraft" type="text" class="preset-input" :placeholder="t('search.presetName')" :disabled="disabled || presetPending" />
      <button type="button" class="toolbar-button" :disabled="disabled || presetPending || !presetNameDraft.trim() || !filterValid || !numericValid" :title="t('search.presetSave')" @click="savePreset">
        <Save aria-hidden="true" /><span>{{ t("search.presetSave") }}</span>
      </button>
      <button type="button" class="toolbar-button" :disabled="disabled || presetPending || !selectedPresetId" :title="t('search.presetRemove')" :aria-label="t('search.presetRemove')" @click="removePreset">
        <Trash2 aria-hidden="true" />
      </button>
    </div>
  </form>
</template>
