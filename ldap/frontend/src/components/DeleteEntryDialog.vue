<script setup lang="ts">
// 删除条目确认框（破坏性操作红样式，文案走七语）。
// M6 N1：打开时 best-effort 拉取子条目数（ldap/entry/childrenCount），
// 有子条目时提供「递归删除」勾选；旧 sidecar / 无桥环境拿不到计数时
// 静默降级为单条删除语义（与既有行为一致）。
import { computed, ref, watch } from "vue";
import { TriangleAlert, X } from "@lucide/vue";
import { splitFirstDnRdn } from "../lib/dn";
import { ldapApi } from "../lib/api";
import { decideBackdropClose, useModalA11y } from "../lib/modal";
import { t } from "../lib/i18n";

const props = defineProps<{
  open: boolean;
  dn?: string;
  submitting?: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "confirm", options: { recursive: boolean }): void;
}>();

const label = computed(() => {
  if (!props.dn) return "";
  return splitFirstDnRdn(props.dn).rdn || props.dn;
});

// 子条目计数：仅用于展示与递归勾选，拿不到（方法未注册/无桥）即隐藏勾选，
// 不阻塞确认框打开。
const childCount = ref<number>();
const recursive = ref(false);
watch(
  () => [props.open, props.dn] as const,
  ([open]) => {
    recursive.value = false;
    childCount.value = undefined;
    if (!open || !props.dn) return;
    ldapApi
      .childrenCount(props.dn)
      .then((result) => {
        childCount.value = result.count;
      })
      .catch(() => {
        childCount.value = undefined;
      });
  },
  { immediate: true },
);

function onConfirm() {
  emit("confirm", { recursive: recursive.value && (childCount.value ?? 0) > 0 });
}

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
    <div class="modal small-modal" role="dialog" aria-modal="true" :aria-label="t('deleteDialog.title')">
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
          <p v-if="childCount !== undefined && childCount > 0" class="hint">
            {{ t("tree.childCount", { count: childCount }) }}
          </p>
          <label v-if="childCount !== undefined && childCount > 0" class="recursive-row">
            <input v-model="recursive" type="checkbox" :disabled="submitting" />
            <span>{{ t("deleteDialog.recursive") }}</span>
          </label>
        </div>
      </div>
      <footer>
        <button type="button" @click="emit('close')">{{ t("cancel") }}</button>
        <button type="button" class="danger-button" :disabled="submitting" @click="onConfirm">
          {{ submitting ? "…" : t("delete") }}
        </button>
      </footer>
    </div>
  </div>
</template>
