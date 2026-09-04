<script setup lang="ts">
// 左栏 topic 树：业务 topic 按评分排序、internal（_ 前缀 / 后端标记）沉底，
// 过滤框本地过滤，选中态由父级持有（selectedTopic 单一来源）。
import { computed, ref } from "vue";
import { HardDrive, RefreshCw, Search, X } from "@lucide/vue";
import type { KafkaTopic } from "../lib/api";
import { filterTopics, sortTopics } from "../lib/kafkaModel";
import { t } from "../lib/i18n";

const props = defineProps<{
  topics: KafkaTopic[];
  loading: boolean;
  error: string;
  selectedTopic: string;
}>();

const emit = defineEmits<{
  (e: "refresh"): void;
  (e: "select", topic: string): void;
}>();

const keyword = ref("");

const visible = computed(() => filterTopics(sortTopics(props.topics), keyword.value));

function isInternal(topic: KafkaTopic): boolean {
  return topic.isInternal === true || topic.name.startsWith("_");
}

function clearFilter() {
  keyword.value = "";
}
</script>

<template>
  <aside class="tree-pane">
    <div class="panel-header">
      <span class="panel-title"><HardDrive aria-hidden="true" /> {{ t("tree.title") }}</span>
      <span class="actions">
        <button class="icon-button" :disabled="loading" :title="t('refresh')" @click="emit('refresh')">
          <RefreshCw :class="{ spinning: loading }" />
        </button>
      </span>
    </div>
    <div class="tree-filter">
      <Search aria-hidden="true" class="icon-neutral" style="width: 13px; height: 13px" />
      <input v-model="keyword" :placeholder="t('tree.filterPlaceholder')" type="text" spellcheck="false" />
      <button v-if="keyword" class="icon-button" :title="t('close')" @click="clearFilter"><X /></button>
    </div>
    <div v-if="error" class="tree-error">{{ error }}</div>
    <div v-else-if="loading && topics.length === 0" class="tree-state">{{ t("tree.loading") }}</div>
    <div v-else-if="visible.length === 0" class="tree-state">
      {{ keyword ? t("tree.noMatch", { keyword }) : t("tree.empty") }}
    </div>
    <div v-else class="tree-rows">
      <div class="tree-node">
        <button
          v-for="topic in visible"
          :key="topic.name"
          type="button"
          class="tree-row"
          :class="{ selected: topic.name === selectedTopic }"
          :title="topic.error ? `${topic.name}: ${topic.error}` : topic.name"
          @click="emit('select', topic.name)"
        >
          <span class="tree-label">
            <HardDrive aria-hidden="true" :class="isInternal(topic) ? 'icon-neutral' : 'icon-violet'" style="width: 13px; height: 13px" />
            <span class="tree-name mono">{{ topic.name }}</span>
          </span>
          <span v-if="isInternal(topic)" class="badge badge-internal">{{ t("tree.internal") }}</span>
          <span v-else class="tree-badge">{{ t("tree.partitions", { count: topic.partitionCount }) }}</span>
        </button>
      </div>
    </div>
  </aside>
</template>
