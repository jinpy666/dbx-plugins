<script setup lang="ts">
// 删除条目确认框（破坏性操作红样式，文案走七语）。
import { computed } from "vue";
import { TriangleAlert, X } from "@lucide/vue";
import { splitFirstDnRdn } from "../lib/dn";
import { decideBackdropClose, useModalA11y } from "../lib/modal";
import { t } from "../lib/i18n";

const props = defineProps<{
  open: boolean;
  dn?: string;
  submitting?: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "confirm"): void;
}>();

const label = computed(() => {
  if (!props.dn) return "";
  return splitFirstDnRdn(props.dn).rdn || props.dn;
});

// Esc 关闭 + Tab 焦点陷阱；删除请求在途时否决关闭（防结果不明）。
// 初始聚焦"取消"而非标题栏 ✕（UI 扫描 P2-11）：破坏性确认框的键盘路径
// 不应先经过关闭图标；遮罩点击与 Esc 共用同一条 allowClose 守卫。
useModalA11y(
  () => props.open,
  { close: () => emit("close"), allowClose: () => !props.submitting, initialFocus: "footer button" },
);

function onBackdropClick() {
  if (decideBackdropClose(!props.submitting).kind !== "close") return;
  emit("close");
}
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="onBackdropClick">
    <div class="modal small-modal">
      <header>
        <h2>{{ t("deleteDialog.title") }}</h2>
        <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
      </header>
      <div class="destructive-copy">
        <div class="destructive-icon"><TriangleAlert aria-hidden="true" /></div>
        <div>
          <strong class="mono">{{ label }}</strong>
          <p>{{ t("deleteDialog.message") }}</p>
          <p class="entry-dn">{{ dn }}</p>
        </div>
      </div>
      <footer>
        <button type="button" @click="emit('close')">{{ t("cancel") }}</button>
        <button type="button" class="danger-button" :disabled="submitting" @click="emit('confirm')">
          {{ submitting ? "…" : t("delete") }}
        </button>
      </footer>
    </div>
  </div>
</template>
