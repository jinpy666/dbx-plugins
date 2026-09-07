<script setup lang="ts">
// 树节点种类图标：按 dnNodeKind（root / dc / ou / cn / uid / o / other）区分
// 图形 + 专属配色（颜色类复用 style.css 的 icon-* 体系，含暗色变体），
// ou 展开时换开文件夹。树行（TreeBranch）与过滤结果行（DnTree）共用；
// 纯装饰（aria-hidden），行语义仍在行文本上。
import { computed } from "vue";
import { Building2, Database, Folder, FolderOpen, Globe, Tag, User } from "@lucide/vue";
import { dnNodeKind, type DnNodeKind } from "../lib/dnTree";

const props = withDefaults(
  defineProps<{
    dn: string;
    baseDn?: string;
    expanded?: boolean;
  }>(),
  { baseDn: "", expanded: false },
);

const kind = computed(() => dnNodeKind(props.dn, props.baseDn));

const icon = computed(() => {
  switch (kind.value) {
    case "root":
      return Database;
    case "dc":
      return Globe;
    case "ou":
      return props.expanded ? FolderOpen : Folder;
    case "cn":
    case "uid":
      return User;
    case "o":
      return Building2;
    default:
      return Tag;
  }
});

// 逐类配色（DC=青 / OU=琥珀 / 人=蓝 / 组织=翠绿 / 根=紫 / 其他=灰），
// 选中态不清色——颜色本身就是识别手段。
const KIND_CLASS: Record<DnNodeKind, string> = {
  root: "icon-violet",
  dc: "icon-cyan",
  ou: "icon-amber",
  cn: "icon-blue",
  uid: "icon-blue",
  o: "icon-emerald",
  other: "icon-neutral",
};
</script>

<template>
  <component :is="icon" class="tree-kind-icon" :class="KIND_CLASS[kind]" aria-hidden="true" />
</template>
