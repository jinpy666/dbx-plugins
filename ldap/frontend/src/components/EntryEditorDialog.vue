<script setup lang="ts">
// 条目编辑器：查看/编辑/新增三形态。
// 表单模式 = 属性行（属性名 + 多行值）；LDIF 模式 = RFC 2849 文本双向同步
// （serializeEntriesToLdif / parseLdif）。新增走 ldap/entry/add，修改走
// ldap/entry/modify（按行 diff 生成 add/replace/delete changes）。
import { computed, ref, watch } from "vue";
import { Plus, Trash2, X } from "@lucide/vue";
import { ldapApi, type LdapEntry } from "../lib/api";
import { parseLdif, serializeEntriesToLdif } from "../lib/ldif";
import { attrRowsToAttributes, diffChanges, type AttrRowDraftSource } from "../lib/ldapDiff";
import { joinRdnAndParent, isLikelyRdn, splitFirstDnRdn } from "../lib/dn";
import { useModalA11y, decideBackdropClose } from "../lib/modal";
import { t } from "../lib/i18n";

export interface AttrRowDraft extends AttrRowDraftSource {
  /** 源值自身含换行：编辑后按行重切分为多值（歧义提示用）。 */
  multiline?: boolean;
}

type EditorMode = "view" | "edit" | "add";

const props = defineProps<{
  canWrite: boolean;
  open: boolean;
  /** view/edit: an existing entry; add: the parent DN to create under. */
  entry?: LdapEntry;
  parentDn?: string;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "saved", dn: string, mode: "add" | "edit"): void;
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
}>();

const mode = ref<EditorMode>("view");
const dnDraft = ref("");
const rdnDraft = ref("");
const rows = ref<AttrRowDraft[]>([]);
const ldifText = ref("");
const ldifMode = ref(false);
const ldifError = ref("");
// LDIF 模式 DN 行锁定信号（UI 扫描 P2-13）：LDIF 里的 dn 与原条目不一致时提示。
const ldifDnChanged = ref(false);
const saving = ref(false);
let suppressLdifSync = false;

const isAdd = computed(() => mode.value === "add");
const editable = computed(() => props.canWrite && !saving.value);
// 新增态 RDN 客户端预检（UI 扫描 P2-6）：逗号/空段/缺 `=` 提前拦截，
// 不等服务器报 invalid DN。空值仍走原有"DN 为空"保存守卫，不在此提示。
const rdnInvalid = computed(() => {
  if (!isAdd.value) return false;
  const rdn = rdnDraft.value.trim();
  return rdn !== "" && !isLikelyRdn(rdn);
});
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
    const dn = joinRdnAndParent(rdnDraft.value, props.parentDn || dnDraft.value);
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
    if (parentDn) dnDraft.value = parentDn;
  } else {
    // 编辑态忽略 LDIF 中的 DN 变更（P2-13）：LDIF 的 dn 行不是改名入口
    // （改名走 Modify DN），照单全收会把 modify 发往不存在的 DN。
    ldifDnChanged.value = props.entry != null && entry.dn !== props.entry.dn;
    dnDraft.value = props.entry ? props.entry.dn : entry.dn;
  }
  return true;
}

// LDIF 模式内实时提示 dn 变更（UI 扫描 P2-23）：无需切回表单即可看到
// 「dn 行不能用于重命名」。编辑态仅在 ldif 模式下解析比对，add 态 dn 合法可编辑。
watch([ldifText, ldifMode], ([text, active]) => {
  if (!active || isAdd.value) return;
  const result = parseLdif(text);
  const dn = result.entries[0]?.dn;
  ldifDnChanged.value = dn != null && props.entry != null && dn !== props.entry.dn;
});

function initFor(mode_: EditorMode, entry?: LdapEntry, parentDn?: string) {
  mode.value = mode_;
  ldifMode.value = false;
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
  () => [props.open, props.entry, props.parentDn] as const,
  ([open]) => {
    if (!open) return;
    initFor(props.entry ? "view" : "add", props.entry, props.parentDn);
  },
  { immediate: true },
);

watch([rows, rdnDraft], () => {
  if (ldifMode.value) return;
  syncLdifFromRows();
}, { deep: true });

function switchToLdif() {
  if (!ldifMode.value) syncLdifFromRows();
  ldifMode.value = true;
}

function switchToForm() {
  if (ldifMode.value) {
    suppressLdifSync = true;
    const ok = syncRowsFromLdif();
    suppressLdifSync = false;
    if (!ok) return;
  }
  ldifMode.value = false;
}

function addRow() {
  rows.value.push({ name: "", valuesText: "" });
}

function removeRow(index: number) {
  rows.value.splice(index, 1);
}

async function save() {
  if (!editable.value || rdnInvalid.value) return;
  if (ldifMode.value && !syncRowsFromLdif()) return;
  saving.value = true;
  try {
    if (isAdd.value) {
      const dn = joinRdnAndParent(rdnDraft.value, dnDraft.value);
      if (!dn) {
        emit("error", t("editor.rdn"));
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

const title = computed(() => (isAdd.value ? t("editor.addTitle") : `${t("editor.viewTitle")} · ${rdnDraft.value || dnDraft.value}`));

// 关闭守卫：提交在途 / 可写且有未保存修改时否决。Esc 与遮罩点击共用同一条
// 判定（UI 扫描 P1-1：此前遮罩 @click.self 直接 close 绕过保护丢改动）；
// ✕/取消仍为显式放弃入口。只读态无改动可做，始终放行。
function canRequestClose(): boolean {
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
    <div class="modal editor-modal" role="dialog" aria-modal="true" :aria-label="title">
      <header>
        <h2>{{ title }}</h2>
        <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
      </header>
      <div v-if="isAdd" class="attr-row">
        <label class="field">
          <span class="muted">{{ t("editor.rdn") }}</span>
          <input v-model="rdnDraft" type="text" name="attr-name" class="mono" :disabled="!editable" spellcheck="false" />
          <span v-if="rdnInvalid" class="form-error">{{ t("editor.rdnInvalid") }}</span>
        </label>
        <label class="field">
          <span class="muted">{{ t("editor.parentDn") }}</span>
          <input :value="dnDraft" type="text" class="mono" :disabled="true" spellcheck="false" />
        </label>
      </div>
      <p v-else class="entry-dn">{{ dnDraft }}</p>
      <div class="mode-switch">
        <button :class="{ 'is-active': !ldifMode }" @click="switchToForm">{{ t("editor.formMode") }}</button>
        <button :class="{ 'is-active': ldifMode }" @click="switchToLdif">{{ t("editor.ldifMode") }}</button>
      </div>
      <p v-if="!canWrite" class="hint">{{ t("editor.readonlyHint") }}</p>
      <p v-if="ldifError" class="form-error">{{ t("editor.ldifParseError", { error: ldifError }) }}</p>
      <p v-if="ldifDnChanged" class="hint">{{ t("editor.ldifDnLocked") }}</p>
      <template v-if="!ldifMode">
        <div class="attr-editor">
          <div v-for="(row, index) in rows" :key="index" class="attr-row">
            <input v-model="row.name" type="text" name="attr-name" :placeholder="t('editor.attribute')" :disabled="!editable" spellcheck="false" />
            <span class="attr-value-cell">
              <textarea v-model="row.valuesText" rows="2" :placeholder="t('editor.values')" :disabled="!editable" spellcheck="false" />
              <small v-if="row.multiline" class="multiline-hint">{{ t("editor.multilineHint") }}</small>
            </span>
            <button :title="t('editor.removeAttribute')" :disabled="!editable" @click="removeRow(index)"><Trash2 /></button>
          </div>
        </div>
      </template>
      <textarea v-else v-model="ldifText" class="ldif-editor" spellcheck="false" :disabled="!editable" />
      <footer>
        <span v-if="dirty && canWrite" class="muted" style="margin-right: auto">{{ t("editor.changed") }}</span>
        <button v-if="!ldifMode" class="toolbar-button" style="margin-right: auto" :disabled="!editable" @click="addRow">
          <Plus aria-hidden="true" />{{ t("editor.addAttribute") }}
        </button>
        <button type="button" @click="emit('close')">{{ t("cancel") }}</button>
        <button v-if="canWrite" type="button" class="primary-button" :disabled="!editable || rdnInvalid" @click="save">
          {{ saving ? "…" : t("save") }}
        </button>
      </footer>
    </div>
  </div>
</template>
