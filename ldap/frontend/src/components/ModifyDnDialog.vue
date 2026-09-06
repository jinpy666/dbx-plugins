<script setup lang="ts">
// Modify DN 对话框：新 RDN + 可选新父 DN + deleteOldRdn（tiny-rdm 语义）。
import { computed, ref, watch } from "vue";
import { X } from "@lucide/vue";
import { isLikelyRdn, splitFirstDnRdn } from "../lib/dn";
import { decideBackdropClose, useModalA11y } from "../lib/modal";
import { t } from "../lib/i18n";

const props = defineProps<{
  open: boolean;
  dn?: string;
  baseDn: string;
  submitting?: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "confirm", newRdn: string, newParentDn: string | undefined, deleteOldRdn: boolean): void;
}>();

const rdnDraft = ref("");
const parentDraft = ref("");
const deleteOldRdn = ref(true);

watch(
  () => [props.open, props.dn] as const,
  ([open, dn]) => {
    if (!open || !dn) return;
    rdnDraft.value = splitFirstDnRdn(dn).rdn;
    parentDraft.value = "";
    deleteOldRdn.value = true;
  },
  { immediate: true },
);

// 新 RDN 客户端预检（UI 扫描 P2-20）：与新增态（P2-6）同一判定，
// 非法 RDN 行内提示 + 禁用确认，不等服务器报 invalid DN。
const rdnInvalid = computed(() => {
  const rdn = rdnDraft.value.trim();
  return rdn !== "" && !isLikelyRdn(rdn);
});

function confirm() {
  if (!rdnDraft.value.trim() || rdnInvalid.value) return;
  const parent = parentDraft.value.trim();
  emit("confirm", rdnDraft.value.trim(), parent || undefined, deleteOldRdn.value);
}

// Esc 关闭 + Tab 焦点陷阱；改名请求在途时否决关闭。遮罩点击与 Esc 共用
// 同一条 allowClose 守卫（P1-1 家族收口；本弹窗无 dirty 态，窗口仅提交在途）。
useModalA11y(
  () => props.open,
  { close: () => emit("close"), allowClose: () => !props.submitting },
);

function onBackdropClick() {
  if (decideBackdropClose(!props.submitting).kind !== "close") return;
  emit("close");
}
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="onBackdropClick">
    <div class="modal small-modal" role="dialog" aria-modal="true" :aria-label="t('modifyDn.title')">
      <header>
        <h2>{{ t("modifyDn.title") }}</h2>
        <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
      </header>
      <p class="entry-dn">{{ dn }}</p>
      <label class="settings-field">
        <span>{{ t("modifyDn.newRdn") }}</span>
        <input v-model="rdnDraft" type="text" class="mono" spellcheck="false" :aria-invalid="rdnInvalid" @keydown.enter.prevent="confirm" />
      </label>
      <p v-if="rdnInvalid" class="form-error">{{ t("editor.rdnInvalid") }}</p>
      <label class="settings-field">
        <span>{{ t("modifyDn.newParentDn") }}</span>
        <input v-model="parentDraft" type="text" class="mono" :placeholder="baseDn" spellcheck="false" />
      </label>
      <label class="settings-field" style="flex-direction: row; align-items: center; gap: 7px">
        <input v-model="deleteOldRdn" type="checkbox" style="width: 13px; height: 13px; margin: 0; accent-color: var(--primary)" />
        <span>{{ t("modifyDn.deleteOldRdn") }}</span>
      </label>
      <footer>
        <button type="button" @click="emit('close')">{{ t("cancel") }}</button>
        <button type="button" class="primary-button" :disabled="submitting || !rdnDraft.trim() || rdnInvalid" @click="confirm">
          {{ submitting ? "…" : t("confirm") }}
        </button>
      </footer>
    </div>
  </div>
</template>
