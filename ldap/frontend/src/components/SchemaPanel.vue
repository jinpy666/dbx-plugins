<script setup lang="ts">
// Schema 面板：attributeTypes / objectClasses 浏览 + 关键字过滤 + 强制刷新。
// 数据经 useLdapSchemaCache（进程内 TTL 30min）→ sidecar ldap/schema（服务端
// 缓存 cache/schema-<hash>.json，refresh=true 绕过）。
import { computed, ref, watch } from "vue";
import { Database, RefreshCw, X } from "@lucide/vue";
import { deriveSchemaMetadata, useLdapSchemaCache } from "../lib/schemaCache";
import { useModalA11y } from "../lib/modal";
import { t } from "../lib/i18n";

interface SchemaClassRow {
  name: string;
  must: string[];
  may: string[];
  definition: string;
}

const props = defineProps<{
  open: boolean;
  connectionId: string;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "error", message: string): void;
}>();

const cache = useLdapSchemaCache({
  loader: async (connectionId) => {
    const { ldapApi } = await import("../lib/api");
    const result = await ldapApi.schema(false);
    return deriveSchemaMetadata(result.attributeTypes, result.objectClasses);
  },
});

const keyword = ref("");
const rawDefinition = ref<SchemaClassRow>();
// 空列两态（UI 扫描 P2-4）：有过滤字时是"无匹配"，不是"Schema 为空"。
const hasKeyword = computed(() => keyword.value.trim() !== "");
const attributeEmptyText = computed(() => (hasKeyword.value ? t("schema.noMatch", { keyword: keyword.value.trim() }) : t("schema.empty")));
const objectClassEmptyText = computed(() => (hasKeyword.value ? t("schema.noMatch", { keyword: keyword.value.trim() }) : t("schema.empty")));

const attributeRows = computed(() => {
  const needle = keyword.value.trim().toLowerCase();
  return cache.attributeNames.value.filter((name) => !needle || name.toLowerCase().includes(needle));
});

const objectClassRows = computed<SchemaClassRow[]>(() => {
  const needle = keyword.value.trim().toLowerCase();
  const source = cache.objectClassAttributes.value;
  const names = Object.keys(source);
  const seen = new Set<string>();
  const rows: SchemaClassRow[] = [];
  for (const name of names) {
    if (seen.has(name.toLowerCase())) continue;
    seen.add(name.toLowerCase());
    if (needle && !name.toLowerCase().includes(needle)) continue;
    const entry = source[name];
    rows.push({ name, must: entry.must ?? [], may: entry.may ?? [], definition: "" });
  }
  return rows.sort((left, right) => left.name.localeCompare(right.name));
});

watch(
  () => [props.open, props.connectionId] as const,
  ([open, connectionId]) => {
    if (!open || !connectionId) return;
    void cache.ensureLoaded(connectionId).catch((cause) => emit("error", cause instanceof Error ? cause.message : String(cause)));
  },
  { immediate: true },
);

async function refresh() {
  try {
    const { ldapApi } = await import("../lib/api");
    // Bypass the server-side cache first, then drop the in-process entry so
    // the loader refetches the fresh payload.
    await ldapApi.schema(true);
    cache.invalidate(props.connectionId);
    await cache.ensureLoaded(props.connectionId);
  } catch (cause) {
    emit("error", cause instanceof Error ? cause.message : String(cause));
  }
}

// Esc 关闭 + Tab 焦点陷阱（useModalA11y 统一接线）。
useModalA11y(
  () => props.open,
  { close: () => emit("close") },
);
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="emit('close')">
    <div class="modal panel-modal" role="dialog" aria-modal="true" :aria-label="t('schema.title')">
      <header>
        <h2><Database aria-hidden="true" style="width: 14px; height: 14px" /> {{ t("schema.title") }}</h2>
        <button class="icon-button" :title="t('close')" @click="emit('close')"><X /></button>
      </header>
      <div class="settings-field" style="flex-direction: row; align-items: center; gap: 8px">
        <input v-model="keyword" type="text" :placeholder="t('schema.searchPlaceholder')" spellcheck="false" style="flex: 1" />
        <button type="button" class="toolbar-button" :title="t('schema.refresh')" @click="refresh">
          <RefreshCw :class="{ spinning: cache.loading.value }" /><span>{{ t("schema.refresh") }}</span>
        </button>
      </div>
      <p class="hint">{{ t("schema.refreshHint") }}</p>
      <div class="settings-body">
        <div class="schema-cols">
          <div class="schema-col">
            <h3>{{ t("schema.attributeTypes") }} <span class="muted">({{ attributeRows.length }})</span></h3>
            <ul>
              <li v-for="name in attributeRows" :key="name" class="mono">{{ name }}</li>
              <li v-if="attributeRows.length === 0" class="muted">{{ attributeEmptyText }}</li>
            </ul>
          </div>
          <div class="schema-col">
            <h3>{{ t("schema.objectClasses") }} <span class="muted">({{ objectClassRows.length }})</span></h3>
            <ul>
              <li v-for="row in objectClassRows" :key="row.name" :title="row.definition">
                <strong class="mono">{{ row.name }}</strong>
                <div v-if="row.must.length" class="schema-def">{{ t("schema.must") }}: {{ row.must.join(", ") }}</div>
                <div v-if="row.may.length" class="schema-def">{{ t("schema.may") }}: {{ row.may.join(", ") }}</div>
              </li>
              <li v-if="objectClassRows.length === 0" class="muted">{{ objectClassEmptyText }}</li>
            </ul>
          </div>
        </div>
      </div>
      <footer>
        <button type="button" @click="emit('close')">{{ t("close") }}</button>
      </footer>
    </div>
  </div>
</template>
