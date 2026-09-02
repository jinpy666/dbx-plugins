<script setup lang="ts" generic="T">
// Zero-dependency fixed-row-height virtual scroller. Only rows inside the
// viewport (plus an overscan buffer) are mounted; scroll-away rows are
// recycled. Window math lives in lib/virtualScroll.ts (unit-tested).
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { computeWindow } from "../lib/virtualScroll";

const props = withDefaults(
  defineProps<{
    items: T[];
    rowHeight: number;
    /** Rows rendered above/below the viewport so slow scrolls don't flash. */
    overscan?: number;
    /** Changing this value scrolls the list back to the top (e.g. new filter keyword). */
    resetKey?: string | number;
  }>(),
  { overscan: 8, resetKey: undefined },
);

const scroller = ref<HTMLElement>();
const scrollTop = ref(0);
const viewportHeight = ref(0);
let observer: ResizeObserver | undefined;

function syncScroll() {
  scrollTop.value = scroller.value?.scrollTop ?? 0;
}

const totalHeight = computed(() => props.items.length * props.rowHeight);
const window_ = computed(() =>
  computeWindow(scrollTop.value, viewportHeight.value, props.items.length, props.rowHeight, props.overscan),
);
const visibleRows = computed(() => {
  const rows: Array<{ index: number; item: T }> = [];
  for (let index = window_.value.start; index < window_.value.end; index += 1) {
    rows.push({ index, item: props.items[index] as T });
  }
  return rows;
});

onMounted(() => {
  viewportHeight.value = scroller.value?.clientHeight ?? 0;
  observer = new ResizeObserver((entries) => {
    viewportHeight.value = entries[0]?.contentRect.height ?? scroller.value?.clientHeight ?? 0;
  });
  if (scroller.value) observer.observe(scroller.value);
});
onBeforeUnmount(() => observer?.disconnect());

// New result set (filter keyword change, base DN reload) restarts at the top.
watch(
  () => props.resetKey,
  () => {
    if (scroller.value) scroller.value.scrollTop = 0;
    scrollTop.value = 0;
  },
);

// When the list shrinks the browser clamps scrollTop on its own; sync the
// state so the window follows instead of pointing past the last row.
watch(
  () => props.items.length,
  (next, prev) => {
    if (next < prev) requestAnimationFrame(syncScroll);
  },
);
</script>

<template>
  <div ref="scroller" class="vlist" @scroll.passive="syncScroll">
    <div class="vlist-inner" :style="{ height: `${totalHeight}px` }">
      <div
        v-for="row in visibleRows"
        :key="row.index"
        class="vlist-row"
        :style="{ top: `${row.index * rowHeight}px`, height: `${rowHeight}px` }"
      >
        <slot :item="row.item" :index="row.index" />
      </div>
    </div>
  </div>
</template>
