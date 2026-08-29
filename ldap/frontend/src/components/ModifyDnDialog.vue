<script setup lang="ts">
// Modify DN 对话框：新 RDN + 可选新父 DN + deleteOldRdn（tiny-rdm 语义）。
import { ref, watch } from "vue";
import { X } from "@lucide/vue";
import { splitFirstDnRdn } from "../lib/dn";
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

function confirm() {
  if (!rdnDraft.value.trim()) return;
  const parent = parentDraft.value.trim();
  emit("confirm", rdnDraft.value.trim(), parent || undefined, deleteOldRdn.value);
}
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="emit('close')">
    <div class="modal small-modal">
      <header>
        <h2>{{ t("modifyDn.title") }}</h2>
        <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
      </header>
      <p class="entry-dn">{{ dn }}</p>
      <label class="settings-field">
        <span>{{ t("modifyDn.newRdn") }}</span>
        <input v-model="rdnDraft" type="text" class="mono" spellcheck="false" />
      </label>
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
        <button type="button" class="primary-button" :disabled="submitting || !rdnDraft.trim()" @click="confirm">
          {{ submitting ? "…" : t("confirm") }}
        </button>
      </footer>
    </div>
  </div>
</template>
