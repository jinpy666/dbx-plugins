<script setup lang="ts">
// 条目编辑器：查看/编辑/新增三形态。
// 表单模式 = 属性行（属性名 + 多行值）；LDIF 模式 = RFC 2849 文本双向同步
// （serializeEntriesToLdif / parseLdif）；关联模式 = 只读关联视图
// （AssociationPanel，仅查看已有条目时开放，不触碰编辑状态）。新增走
// ldap/entry/add，修改走 ldap/entry/modify（按行 diff 生成 add/replace/delete
// changes）。
import { computed, nextTick, ref, useId, watch } from "vue";
import { Copy, Plus, Trash2, X } from "@lucide/vue";
import { ldapApi, type LdapEntry } from "../lib/api";
import { parseLdif, serializeEntriesToLdif } from "../lib/ldif";
import { attrRowsToAttributes, diffChanges, type AttrRowDraftSource } from "../lib/ldapDiff";
import { joinRdnAndParent, isLikelyDn, isLikelyRdn, splitFirstDnRdn } from "../lib/dn";
import { missingRequiredAttributes } from "../lib/entryValidation";
import type { LdapSchema } from "../lib/newEntryTemplates";
import { useModalA11y, decideBackdropClose } from "../lib/modal";
import { looksBinaryAttribute } from "../lib/binaryValue";
import { writeClipboardText } from "../lib/clipboard";
import { t } from "../lib/i18n";
import PasswordAttributeEditor from "./PasswordAttributeEditor.vue";
import BinaryValueEditor from "./BinaryValueEditor.vue";
import AssociationPanel from "./AssociationPanel.vue";

export interface AttrRowDraft extends AttrRowDraftSource {
  /** 源值自身含换行：编辑后按行重切分为多值（歧义提示用）。 */
  multiline?: boolean;
}

type EditorMode = "view" | "edit" | "add";
// 页签三态：form/ldif 双向同步沿用原 ldifMode 布尔语义；assoc 为只读关联视图。
type EditorTab = "form" | "ldif" | "assoc";

const props = defineProps<{
  canWrite: boolean;
  open: boolean;
  /** view/edit: an existing entry; add: the parent DN to create under. */
  entry?: LdapEntry;
  parentDn?: string;
  /** 关联视图的搜索根（透传给 AssociationPanel）。 */
  baseDn?: string;
  /** 打开时的初始页签；仅 view 态的非 form 值生效，默认不影响既有调用方。 */
  initialTab?: EditorTab;
  /** schema 推导的 DN 值属性名（透传给 AssociationPanel；缺省/空时 panel 用内置兜底表）。 */
  dnAttributes?: string[];
  schema?: LdapSchema;
  loading?: boolean;
  loadError?: string;
  loadErrorDetail?: string;
  requestedDn?: string;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "saved", dn: string, mode: "add" | "edit"): void;
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
  (e: "openEntry", dn: string): void;
  (e: "retry"): void;
}>();

const mode = ref<EditorMode>("view");
const dnDraft = ref("");
const rdnDraft = ref("");
const rows = ref<AttrRowDraft[]>([]);
const ldifText = ref("");
const editorTab = ref<EditorTab>("form");
const ldifError = ref("");
// LDIF 模式 DN 行锁定信号（UI 扫描 P2-13）：LDIF 里的 dn 与原条目不一致时提示。
const ldifDnChanged = ref(false);
const saving = ref(false);
const attrEditor = ref<HTMLElement>();
const requiredErrorId = useId();
const rdnErrorId = useId();
const parentErrorId = useId();
let suppressLdifSync = false;

const isAdd = computed(() => mode.value === "add");
const editable = computed(() => props.canWrite && !saving.value && !props.loading && !props.loadError);
const ldifDraft = computed(() => {
  if (editorTab.value !== "ldif") return undefined;
  const parsed = parseLdif(ldifText.value);
  return parsed.errors.length === 0 ? parsed.entries[0] : undefined;
});
const activeDnParts = computed(() => editorTab.value === "ldif"
  ? splitFirstDnRdn(ldifDraft.value?.dn ?? "")
  : { rdn: rdnDraft.value, parentDn: dnDraft.value });
const rdnField = computed({ get: () => activeDnParts.value.rdn, set: (value: string) => { rdnDraft.value = value; } });
// 新增态 RDN 客户端预检（UI 扫描 P2-6）：逗号/空段/缺 `=` 提前拦截，
// 不等服务器报 invalid DN。空值仍走原有"DN 为空"保存守卫，不在此提示。
const rdnInvalid = computed(() => {
  if (!isAdd.value) return false;
  const rdn = activeDnParts.value.rdn;
  return rdn !== "" && !isLikelyRdn(rdn);
});
const parentInvalid = computed(() => {
  if (!isAdd.value) return false;
  const parent = activeDnParts.value.parentDn;
  return parent !== "" && !isLikelyDn(parent);
});
const missingRequired = computed(() => {
  if (!props.canWrite || props.loading || props.loadError) return [];
  const attributes = editorTab.value === "ldif" ? ldifDraft.value?.attributes : rowsToAttributes();
  return attributes ? missingRequiredAttributes(attributes, isAdd.value ? undefined : props.entry?.attributes, props.schema) : [];
});
const fieldMissing = (name: string) => missingRequired.value.some((attribute) => attribute.toLowerCase() === name.split(";")[0].trim().toLowerCase());
const dirty = computed(() => {
  if (mode.value === "add") return true;
  const source = props.entry;
  if (!source) return false;
  const current = rowsToAttributes();
  const originalKeys = Object.keys(source.attributes);
  const currentKeys = Object.keys(current);
  if (originalKeys.length !== currentKeys.length) return true;
  return currentKeys.some((key) => (current[key] ?? []).join("\n") !== (source.attributes[key] ?? []).join("\n"));
});

function entryToRows(entry: LdapEntry): AttrRowDraft[] {
  return Object.keys(entry.attributes)
    .sort((left, right) => left.localeCompare(right))
    .map((name) => {
      const values = entry.attributes[name] ?? [];
      return {
        name,
        valuesText: values.join("\n"),
        sourceValues: values,
        // 值本身含换行时，编辑该行后的重切分歧义（round-3 保真只覆盖未编辑
        // 行）——标记出来提示用户：这一行编辑会按行拆分成多值。
        multiline: values.some((value) => value.includes("\n")),
      };
    });
}

function rowsToAttributes(): Record<string, string[]> {
  return attrRowsToAttributes(rows.value);
}

function syncLdifFromRows() {
  if (suppressLdifSync) return;
  const attributes = rowsToAttributes();
  if (isAdd.value) {
    const dn = joinRdnAndParent(rdnDraft.value, dnDraft.value);
    ldifText.value = serializeEntriesToLdif([{ dn, attributes }], { includeVersion: false });
  } else {
    ldifText.value = serializeEntriesToLdif([{ dn: dnDraft.value, attributes }], { includeVersion: false });
  }
}

function syncRowsFromLdif() {
  const result = parseLdif(ldifText.value);
  if (result.errors.length > 0) {
    ldifError.value = result.errors.map((error) => `L${error.line}: ${error.message}`).join("; ");
    return false;
  }
  ldifError.value = "";
  const entry = result.entries[0];
  if (!entry) {
    ldifError.value = "no entry";
    return false;
  }
  rows.value = entryToRows(entry);
  if (isAdd.value) {
    const { rdn, parentDn } = splitFirstDnRdn(entry.dn);
    rdnDraft.value = rdn;
    dnDraft.value = parentDn;
  } else {
    // 编辑态忽略 LDIF 中的 DN 变更（P2-13）：LDIF 的 dn 行不是改名入口
    // （改名走 Modify DN），照单全收会把 modify 发往不存在的 DN。
    ldifDnChanged.value = props.entry != null && entry.dn !== props.entry.dn;
    dnDraft.value = props.entry ? props.entry.dn : entry.dn;
  }
  return true;
}

// LDIF 模式内实时提示 dn 变更（UI 扫描 P2-23）：无需切回表单即可看到
// 「dn 行不能用于重命名」。编辑态仅在 ldif 页签下解析比对，add 态 dn 合法可编辑。
watch([ldifText, editorTab], ([text, tab]) => {
  if (tab !== "ldif" || isAdd.value) return;
  const result = parseLdif(text);
  const dn = result.entries[0]?.dn;
  ldifDnChanged.value = dn != null && props.entry != null && dn !== props.entry.dn;
});

function initFor(mode_: EditorMode, entry?: LdapEntry, parentDn?: string) {
  mode.value = mode_;
  // 页签初始态：view 态尊重 initialTab（树「查看成员」直开关联页），其余
  // （含 add）一律回落表单——默认值不影响既有调用方的打开行为。
  const requestedTab = props.initialTab;
  editorTab.value = mode_ === "view" && (requestedTab === "ldif" || requestedTab === "assoc") ? requestedTab : "form";
  ldifError.value = "";
  ldifDnChanged.value = false;
  saving.value = false;
  if (mode_ === "add") {
    dnDraft.value = parentDn || "";
    rdnDraft.value = "";
    rows.value = [{ name: "objectClass", valuesText: "top" }];
    ldifText.value = "";
    syncLdifFromRows();
    return;
  }
  if (!entry) return;
  dnDraft.value = entry.dn;
  rdnDraft.value = splitFirstDnRdn(entry.dn).rdn;
  rows.value = entryToRows(entry);
  ldifText.value = serializeEntriesToLdif([{ dn: entry.dn, attributes: entry.attributes }], { includeVersion: false });
}

watch(
  () => [props.open, props.entry, props.parentDn, props.loading, props.loadError] as const,
  ([open]) => {
    if (!open || props.loading || props.loadError) return;
    initFor(props.entry ? "view" : "add", props.entry, props.parentDn);
  },
  { immediate: true },
);

watch([rows, rdnDraft], () => {
  if (editorTab.value !== "form") return;
  syncLdifFromRows();
}, { deep: true });

function switchToLdif() {
  if (editorTab.value !== "ldif") syncLdifFromRows();
  editorTab.value = "ldif";
}

// 离开 LDIF 页签的共用守卫：先把 LDIF 文本解析回 rows，成功才允许切走。
// 切到关联页同样必须过这里——LDIF 里的编辑若不落回 rows，切回表单时会被
// rows→LDIF 重同步覆盖（用户编辑静默丢失）。
function leaveLdif(): boolean {
  if (editorTab.value !== "ldif") return true;
  suppressLdifSync = true;
  const ok = syncRowsFromLdif();
  suppressLdifSync = false;
  return ok;
}

function switchToForm() {
  if (!leaveLdif()) return;
  editorTab.value = "form";
}

function switchToAssoc() {
  // 关联本身是只读视图（不触碰 rows/LDIF 状态），但离开 LDIF 仍须先把文本
  // 解析回 rows；解析失败保持 LDIF 页签（与切表单同语义，错误提示在场）。
  if (!leaveLdif()) return;
  editorTab.value = "assoc";
}

function addRow() {
  rows.value.push({ name: "", valuesText: "" });
}

async function focusRequiredAttribute(name: string) {
  if (!leaveLdif()) return;
  editorTab.value = "form";
  let index = rows.value.findIndex((row) => row.name.split(";")[0].trim().toLowerCase() === name.toLowerCase());
  if (index < 0) {
    index = rows.value.length;
    rows.value.push({ name, valuesText: "" });
  }
  await nextTick();
  attrEditor.value?.querySelectorAll(".attr-row")[index]?.querySelector<HTMLElement>("textarea, .attr-value-cell input")?.focus();
}

// M6 N2/N3：按属性名分流行编辑器——密码属性走哈希编辑器（userPassword/
// unicodePwd 等 *password* 命名），二进制属性（jpegPhoto/*Certificate 等，
// looksBinaryAttribute 启发式）走查看/上传组件，其余保持多行文本。
// LDIF 模式始终是纯文本，不走分流。
type RowEditorKind = "password" | "binary" | "text";
function editorKind(name: string): RowEditorKind {
  const key = name.trim().toLowerCase();
  if (!key) return "text";
  if (key === "userpassword" || key === "unicodepwd" || key.endsWith("password")) return "password";
  if (looksBinaryAttribute(key)) return "binary";
  return "text";
}

function rowValues(row: AttrRowDraft): string[] {
  return row.valuesText === "" ? [] : row.valuesText.split("\n");
}

// 随机生成的明文只在通知里出现一次（不进 rows、不进 LDIF、不落盘）。
function onPlainGenerated(plain: string) {
  emit("notify", t("ldap.passwordEditor.plainNotice", { plain }));
}

// 复制入口（DN / 属性值 / LDIF 文本共用）：宿主桥缺失或写入失败时如实通知，
// 不假装"已复制"。复制是只读动作，不受 editable 门禁限制。
async function copyText(text: string) {
  emit("notify", (await writeClipboardText(text)) ? t("copied") : t("copyFailed"));
}

function removeRow(index: number) {
  rows.value.splice(index, 1);
}

async function save() {
  if (!editable.value) return;
  if (editorTab.value === "ldif" && !syncRowsFromLdif()) return;
  // Validate the parsed draft too; a valid form RDN cannot authorize a new LDIF DN.
  if (rdnInvalid.value || parentInvalid.value || missingRequired.value.length > 0) return;
  saving.value = true;
  try {
    if (isAdd.value) {
      const dn = joinRdnAndParent(rdnDraft.value, dnDraft.value);
      if (!dn) {
        emit("error", t("editor.rdn"));
        return;
      }
      if (!isLikelyDn(dn)) {
        emit("error", t("editor.dnInvalid"));
        return;
      }
      await ldapApi.entryAdd(dn, rowsToAttributes());
      emit("saved", dn, "add");
    } else {
      const source = props.entry!;
      const current = rowsToAttributes();
      const changes = diffChanges(source.attributes, current);
      if (changes.length === 0) {
        // 无差异不再静默关闭（P2-13）：区分"没有修改"与"修改被丢弃"。
        emit("notify", t("editor.noChanges"));
        emit("close");
        return;
      }
      await ldapApi.entryModify(dnDraft.value, changes);
      emit("saved", dnDraft.value, "edit");
    }
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  } finally {
    saving.value = false;
  }
}

const title = computed(() => (props.loading || props.loadError
  ? `${t("editor.viewTitle")} · ${props.requestedDn || ""}`
  : isAdd.value ? t("editor.addTitle") : `${t("editor.viewTitle")} · ${rdnDraft.value || dnDraft.value}`));

// 关闭守卫：提交在途 / 可写且有未保存修改时否决。Esc 与遮罩点击共用同一条
// 判定（UI 扫描 P1-1：此前遮罩 @click.self 直接 close 绕过保护丢改动）；
// ✕/取消仍为显式放弃入口。只读态无改动可做，始终放行。
function canRequestClose(): boolean {
  if (props.loading || props.loadError) return true;
  return !saving.value && !(props.canWrite && dirty.value);
}

useModalA11y(
  () => props.open,
  { close: () => emit("close"), allowClose: canRequestClose },
);

// 遮罩点击走与 Esc 相同的守卫：dirty 时静默否决（footer 已有"有未保存的
// 修改"提示），弹窗保持打开、输入不丢。
function onBackdropClick() {
  if (decideBackdropClose(canRequestClose()).kind !== "close") return;
  emit("close");
}
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="onBackdropClick">
    <div class="modal editor-modal" role="dialog" aria-modal="true" :aria-label="title" :aria-busy="loading || undefined">
      <header>
        <h2>{{ title }}</h2>
        <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
      </header>
      <div v-if="loading" class="empty" role="status">{{ t("editor.loading") }}</div>
      <div v-else-if="loadError" class="empty request-error" role="alert">
        <p :title="loadErrorDetail || loadError">{{ loadError }}</p>
        <button type="button" @click="emit('retry')">{{ t("retry") }}</button>
      </div>
      <template v-else>
      <div v-if="isAdd" class="attr-row">
        <label class="field">
          <span class="muted">{{ t("editor.rdn") }}</span>
          <input v-model="rdnField" type="text" name="attr-name" class="mono" :disabled="!editable || editorTab === 'ldif'" :aria-invalid="rdnInvalid" :aria-describedby="rdnInvalid ? rdnErrorId : undefined" spellcheck="false" />
          <span v-if="rdnInvalid" :id="rdnErrorId" class="form-error" role="alert">{{ t("editor.rdnInvalid") }}</span>
        </label>
        <label class="field">
          <span class="muted">{{ t("editor.parentDn") }}</span>
          <input :value="activeDnParts.parentDn" type="text" class="mono" :disabled="true" :aria-invalid="parentInvalid" :aria-describedby="parentInvalid ? parentErrorId : undefined" spellcheck="false" />
          <span v-if="parentInvalid" :id="parentErrorId" class="form-error" role="alert">{{ t("editor.dnInvalid") }}</span>
        </label>
      </div>
      <div v-else class="entry-dn-row">
        <p class="entry-dn" :title="dnDraft">{{ dnDraft }}</p>
        <button class="icon-button" :title="t('copyDn')" :aria-label="t('copyDn')" @click="copyText(dnDraft)"><Copy aria-hidden="true" /></button>
      </div>
      <div class="mode-switch-row">
        <div class="mode-switch">
          <button :class="{ 'is-active': editorTab === 'form' }" @click="switchToForm">{{ t("editor.formMode") }}</button>
          <button :class="{ 'is-active': editorTab === 'ldif' }" @click="switchToLdif">{{ t("editor.ldifMode") }}</button>
          <!-- 关联是只读视图，仅查看已有条目（view 态）时可用；新增态没有属性上下文。 -->
          <button v-if="!isAdd && entry" :class="{ 'is-active': editorTab === 'assoc' }" @click="switchToAssoc">{{ t("editor.assocMode") }}</button>
        </div>
        <button v-if="editorTab === 'ldif'" class="icon-button" :title="t('editor.copyLdif')" :aria-label="t('editor.copyLdif')" :disabled="ldifText === ''" @click="copyText(ldifText)"><Copy aria-hidden="true" /></button>
      </div>
      <p v-if="!canWrite" class="hint">{{ t("editor.readonlyHint") }}</p>
      <p v-if="ldifError" class="form-error">{{ t("editor.ldifParseError", { error: ldifError }) }}</p>
      <p v-if="ldifDnChanged" class="hint">{{ t("editor.ldifDnLocked") }}</p>
      <div v-if="missingRequired.length > 0 && editorTab !== 'assoc'" :id="requiredErrorId" class="form-error required-attributes" role="alert">
        <span>{{ t("editor.requiredAttributes") }}</span>
        <button v-for="attribute in missingRequired" :key="attribute" type="button" :aria-label="t('editor.locateAttribute', { attribute })" :disabled="!editable" @click="focusRequiredAttribute(attribute)">{{ attribute }}</button>
      </div>
      <template v-if="editorTab === 'form'">
        <div ref="attrEditor" class="attr-editor">
          <p v-if="rows.length === 0" class="empty" role="status">{{ t("editor.noAttributes") }}</p>
          <div v-for="(row, index) in rows" :key="index" class="attr-row">
            <input v-model="row.name" type="text" name="attr-name" :placeholder="t('editor.attribute')" :aria-label="t('editor.attribute')" :disabled="!editable" spellcheck="false" />
            <span class="attr-value-cell" role="group" :aria-label="row.name || t('editor.values')" :aria-describedby="fieldMissing(row.name) ? requiredErrorId : undefined">
              <PasswordAttributeEditor
                v-if="editorKind(row.name) === 'password'"
                :model-value="row.valuesText"
                :disabled="!editable"
                @update:model-value="row.valuesText = $event"
                @plain-generated="onPlainGenerated"
              />
              <BinaryValueEditor
                v-else-if="editorKind(row.name) === 'binary'"
                :attribute-name="row.name"
                :model-value="rowValues(row)"
                :disabled="!editable"
                @update:model-value="row.valuesText = $event.join('\n')"
              />
              <template v-else>
                <textarea v-model="row.valuesText" rows="2" :placeholder="t('editor.values')" :aria-label="row.name || t('editor.values')" :aria-invalid="fieldMissing(row.name)" :aria-describedby="fieldMissing(row.name) ? requiredErrorId : undefined" :disabled="!editable" spellcheck="false" />
                <small v-if="row.multiline" class="multiline-hint">{{ t("editor.multilineHint") }}</small>
              </template>
            </span>
            <span class="attr-actions">
              <button :title="t('editor.copyValue')" :aria-label="t('editor.copyValue')" :disabled="row.valuesText === ''" @click="copyText(row.valuesText)"><Copy aria-hidden="true" /></button>
              <button :title="t('editor.removeAttribute')" :disabled="!editable" @click="removeRow(index)"><Trash2 /></button>
            </span>
          </div>
        </div>
      </template>
      <AssociationPanel
        v-else-if="editorTab === 'assoc'"
        :dn="dnDraft"
        :attributes="entry?.attributes ?? {}"
        :base-dn="baseDn ?? ''"
        :dn-attributes="dnAttributes"
        :active="open && editorTab === 'assoc'"
        @open-entry="(dn: string) => emit('openEntry', dn)"
        @error="(m: string) => emit('error', m)"
        @notify="(m: string) => emit('notify', m)"
      />
      <textarea v-else v-model="ldifText" class="ldif-editor" :aria-label="t('editor.ldifMode')" :aria-invalid="rdnInvalid || parentInvalid || missingRequired.length > 0 || !!ldifError" :aria-describedby="missingRequired.length > 0 ? requiredErrorId : rdnInvalid ? rdnErrorId : parentInvalid ? parentErrorId : undefined" spellcheck="false" :disabled="!editable" />
      </template>
      <footer>
        <span v-if="dirty && canWrite && !loading && !loadError" class="muted" style="margin-right: auto">{{ t("editor.changed") }}</span>
        <button v-if="editorTab === 'form' && !loading && !loadError" class="toolbar-button" style="margin-right: auto" :disabled="!editable" @click="addRow">
          <Plus aria-hidden="true" />{{ t("editor.addAttribute") }}
        </button>
        <button type="button" @click="emit('close')">{{ t("cancel") }}</button>
        <!-- 关联页签是只读视图：保存等编辑动作一并隐藏，仅保留取消（关闭）。 -->
        <button v-if="canWrite && editorTab !== 'assoc' && !loading && !loadError" type="button" class="primary-button" :disabled="!editable || rdnInvalid || parentInvalid || missingRequired.length > 0" @click="save">
          {{ saving ? "…" : t("save") }}
        </button>
      </footer>
    </div>
  </div>
</template>
