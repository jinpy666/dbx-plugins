<script setup lang="ts">
// 密码属性编辑器（M6 N2）：`userPassword` 专用行组件。scheme 下拉（缺省
// {SSHA}，随机盐）+ 新密码输入（type=password，可切换明文回显）+ 随机生成
// （无歧义字符集）+ 提交时前端哈希（RFC 2307 形态，见 lib/passwordHash）。
// 已有值只展示 scheme 识别结果（哈希不可逆，不回显明文）；随机生成的明文
// 通过 `plainGenerated` 一次性交给父层提示（组件自身不留存、不落日志）。
// 纯前端哈希，值仍走 entry/modify，sidecar 零改动。
import { computed, ref } from "vue";
import { Eye, EyeOff, RefreshCw } from "@lucide/vue";
import {
    PASSWORD_SCHEMES,
    generateRandomPassword,
    hashPassword,
    parsePasswordHash,
    type PasswordScheme,
} from "../lib/passwordHash";
import { t } from "../lib/i18n";

const props = defineProps<{
  /** 当前存储值（可空；哈希或明文均可能）。 */
  modelValue: string;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  /** 哈希后的最终存储值（RFC 2307 字符串）。 */
  (e: "update:modelValue", value: string): void;
  /** 仅随机生成路径发出：一次性明文，父层负责展示，不得持久化。 */
  (e: "plainGenerated", plain: string): void;
}>();

// 占位 key（lib/i18n.ts 由主控合并，见任务收口说明）：t() 对缺失 key 直通
// 返回 key 本身，测试因此不依赖具体语言文案。

const scheme = ref<PasswordScheme>("{SSHA}");
const newPlain = ref("");
const showPlain = ref(false);
const applying = ref(false);
// 最近一次新密码是否来自随机生成：只有该路径才把明文交给父层提示。
const wasGenerated = ref(false);

const inputType = computed(() => (showPlain.value ? "text" : "password"));
const canApply = computed(() => !props.disabled && !applying.value && newPlain.value !== "");

// 已有值识别：空值不提示；不认识的 scheme 前缀（{MD5}/{CRYPT}…）明确告警
// 而非静默当作明文。data-scheme 供测试与父层嗅探，不含任何值内容。
const existing = computed(() => parsePasswordHash(props.modelValue));
const existingNote = computed(() => {
  if (!props.modelValue.trim()) return "";
  if (!existing.value) return t("ldap.passwordEditor.existingUnknown");
  return existing.value.scheme === "{CLEARTEXT}" ? t("ldap.passwordEditor.cleartextWarning") : "";
});

// 随机生成：填入输入框并切换明文回显，让管理员确认一次性明文后再提交。
const generate = () => {
  if (props.disabled) return;
  newPlain.value = generateRandomPassword(16);
  showPlain.value = true;
  wasGenerated.value = true;
};

const toggleShow = () => {
  showPlain.value = !showPlain.value;
};

// 提交：哈希后回写存储值；输入即清空，明文不留在组件状态里。
const apply = async () => {
  if (!canApply.value) return;
  const plain = newPlain.value;
  applying.value = true;
  try {
    const hashed = await hashPassword(plain, scheme.value);
    emit("update:modelValue", hashed);
    if (wasGenerated.value) emit("plainGenerated", plain);
  } finally {
    applying.value = false;
    newPlain.value = "";
    showPlain.value = false;
    wasGenerated.value = false;
  }
};
</script>

<template>
  <div class="password-editor" :data-scheme="existing?.scheme ?? ''">
    <label class="field">
      <span class="muted">{{ t("ldap.passwordEditor.scheme") }}</span>
      <select v-model="scheme" :disabled="disabled">
        <option v-for="option in PASSWORD_SCHEMES" :key="option" :value="option">{{ option }}</option>
      </select>
    </label>
    <label class="field">
      <span class="muted">{{ t("ldap.passwordEditor.fieldLabel") }}</span>
      <span class="password-input-row">
        <input
          v-model="newPlain"
          :type="inputType"
          autocomplete="new-password"
          spellcheck="false"
          :disabled="disabled"
          @keyup.enter="apply"
        />
        <button
          type="button"
          class="icon-button"
          :title="showPlain ? t('ldap.passwordEditor.hide') : t('ldap.passwordEditor.show')"
          :aria-label="showPlain ? t('ldap.passwordEditor.hide') : t('ldap.passwordEditor.show')"
          :disabled="disabled || !newPlain"
          @click="toggleShow"
        >
          <EyeOff v-if="showPlain" aria-hidden="true" /><Eye v-else aria-hidden="true" />
        </button>
      </span>
    </label>
    <span class="password-actions">
      <button type="button" :disabled="disabled" @click="generate">
        <RefreshCw aria-hidden="true" />{{ t("ldap.passwordEditor.generate") }}
      </button>
      <button type="button" class="primary-button" :disabled="!canApply" @click="apply">
        {{ applying ? "…" : t("ldap.passwordEditor.apply") }}
      </button>
    </span>
    <p v-if="modelValue && existing && !existingNote" class="hint">
      {{ t("ldap.passwordEditor.existing") }}<span class="mono">{{ existing.scheme }}</span>
    </p>
    <p v-if="existingNote" class="form-error">{{ existingNote }}</p>
  </div>
</template>
