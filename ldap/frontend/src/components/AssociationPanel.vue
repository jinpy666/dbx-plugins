<script setup lang="ts">
// 条目关联视图（对标 ADUC 的 Members / Member Of 两个标签页，扩展为通用 DN 引用）。
// 布局自上而下三段（2026-09 重构：原四个 section 纵向堆叠太拥挤，改为子页签
// 一次只显示一个列表）：
// 1. 本地过滤框——对当前激活列表做客户端子串过滤（大小写不敏感匹配完整 DN，
//    含 RDN），即时生效；dn 变化时清空；
// 2. 子页签行——成员 / DN 引用 / 所属 / 被引用四个页签（复用 .mode-switch /
//    .is-active 样式模式），每个带计数徽标：成员与 DN 引用为同步计数（恒显示），
//    所属与被引用在反查完成（done）前不显示数字；默认激活「成员」，不做自动跳转；
// 3. 激活列表区——一次只渲染一个列表。行表格化固定行高两行式：第一行 RDN
//    （mono、主文本），第二行完整 DN（muted、ellipsis，title 悬停看全量）。
//    行点击 openEntry（保持）、行尾复制按钮（保持）。列表用 VirtualList 虚拟
//    滚动，resetKey 纳入 dn + 当前页签 + 过滤词，切页签 / 换 dn / 改过滤词
//    都回滚到顶部。
//
// 数据来源与既有语义不变：
// - 成员（Members）：直读条目自身 member（回退 uniqueMember）属性值（都是
//   DN）——通用路径，不碰屏蔽属性策略；
// - DN 引用（References）：条目上其余 DN 值属性（managedBy/owner/seeAlso…，
//   member/uniqueMember 除外、已有成员专区）正查。属性识别 schema 驱动
//   （dnAttributes prop，lib/dnAttributes 推导），缺省回退内置核心表。归一为
//   扁平行列表，来源属性名以小字保留在行内（替代旧的分组小标题——固定行高
//   虚拟滚动要求等高行）；
// - 所属（Member Of）：调 ldap/search 发过滤器 (member=<本条目 DN>) 子树反查
//   引用者——过滤器不受屏蔽属性策略影响；
// - 被引用（Referenced by）：单次 OR 过滤器反查经核心引用属性（managedBy/
//   owner 等，固定内置表）指向本条目的条目。两个反查仍由 tab 激活
//   （active=true）惰性加载一次；dn / baseDn 变化时缓存作废，active 下立即
//   重新搜索（请求序号防竞态）。
import { computed, ref, watch } from "vue";
import { Copy } from "@lucide/vue";
import { ldapApi } from "../lib/api";
import { splitFirstDnRdn } from "../lib/dn";
import { escapeLdapFilterValue } from "../lib/ldapFilter";
import { DN_REFERENCE_CORE, buildReferencedByFilter } from "../lib/dnAttributes";
import { writeClipboardText } from "../lib/clipboard";
import { t } from "../lib/i18n";
import VirtualList from "./VirtualList.vue";

const props = withDefaults(
  defineProps<{
    /** 当前条目 DN。 */
    dn: string;
    /** 当前条目属性（ldap/entry/get 结果）。 */
    attributes: Record<string, string[]>;
    /** Member Of 搜索 base（连接 Base DN）。 */
    baseDn: string;
    /** 仅当 true 才发起 Member Of 搜索（tab 激活惰性加载）。 */
    active?: boolean;
    /** schema 推导的 DN 值属性名（缺省或空数组时回退 DN_REFERENCE_CORE）。 */
    dnAttributes?: string[];
  }>(),
  { active: false, dnAttributes: undefined },
);

const emit = defineEmits<{
  (e: "openEntry", dn: string): void;
  (e: "error", message: string): void;
  (e: "notify", message: string): void;
}>();

// 虚拟滚动固定行高：两行式行（第一行 RDN + 第二行完整 DN）取 44px，与行内
// 样式一致（单测中 VirtualList 被 stub 平铺渲染，行高只影响真实布局）。
const ASSOC_ROW_HEIGHT = 44;
// 所属反查上限：截断时用该值提示"到搜索页缩小范围"。
const MEMBER_OF_SIZE_LIMIT = 1000;
// 被引用反查上限（与所属区同值同提示语义）。
const REFERENCED_BY_SIZE_LIMIT = 1000;

// -- 子页签与本地过滤状态 ------------------------------------------------------

// 四个子页签一次只渲染一个列表区；默认「成员」，不做自动跳转。
type AssocTab = "members" | "references" | "memberOf" | "referencedBy";
const activeTab = ref<AssocTab>("members");

// 本地过滤词：只作用于当前激活列表（大小写不敏感子串匹配完整 DN，含 RDN）；
// dn 变化时清空——换条目后旧过滤词对新区没有意义。
const filterText = ref("");
watch(
  () => props.dn,
  () => {
    filterText.value = "";
  },
);

// 统一列表条目：dn 必备；attribute 仅 DN 引用页签的行有值（行内保留来源
// 属性，替代旧的分组小标题——固定行高虚拟滚动要求等高行）。
interface AssocEntry {
  dn: string;
  attribute?: string;
}

// 过滤词归一（小写、去首尾空白）；空词放行全部。
const normalizedFilter = computed(() => filterText.value.trim().toLowerCase());
function passesFilter(dn: string): boolean {
  const keyword = normalizedFilter.value;
  return keyword === "" || dn.toLowerCase().includes(keyword);
}

// 大小写不敏感取属性值：目录返回的属性名大小写不保证与请求一致。
// 属性存在但值为空、或属性不存在 → 同一空态文案（归一为空数组）。
const memberDns = computed<string[]>(() => {
  for (const wanted of ["member", "uniquemember"]) {
    const key = Object.keys(props.attributes).find((name) => name.toLowerCase() === wanted);
    const values = key ? props.attributes[key] : undefined;
    if (Array.isArray(values) && values.length > 0) return values;
  }
  return [];
});

// -- DN 引用（正查：条目上 member/uniqueMember 之外的 DN 值属性） --------------

// 正查识别表：schema 推导结果优先；缺省或空数组回退内置核心表。
// 反查（被引用区）固定用 DN_REFERENCE_CORE——schema 全量 DN 属性可能数百个，
// 不做全量 OR。
const effectiveDnAttributes = computed<string[]>(() =>
  Array.isArray(props.dnAttributes) && props.dnAttributes.length > 0 ? props.dnAttributes : DN_REFERENCE_CORE,
);

interface DnReferenceGroup {
  /** 属性名（保留条目返回的原始大小写，作行内属性小字）。 */
  attribute: string;
  /** 该属性的 DN 值列表。 */
  values: string[];
}

// 按 effectiveDnAttributes 顺序分组（内置表顺序即展示顺序）；member/uniqueMember
// 已有成员专区，不重复出现；值为空的属性与识别表外的属性跳过。
const dnReferenceGroups = computed<DnReferenceGroup[]>(() => {
  const groups: DnReferenceGroup[] = [];
  for (const wanted of effectiveDnAttributes.value) {
    const key = String(wanted ?? "").trim().toLowerCase();
    if (!key || key === "member" || key === "uniquemember") continue;
    const attributeName = Object.keys(props.attributes).find((name) => name.toLowerCase() === key);
    if (!attributeName) continue;
    const values = props.attributes[attributeName];
    if (!Array.isArray(values) || values.length === 0) continue;
    groups.push({ attribute: attributeName, values });
  }
  return groups;
});

// 四个列表统一为 AssocEntry[]，走同一套过滤与两行式行渲染。
const filteredMemberEntries = computed<AssocEntry[]>(() =>
  memberDns.value.filter((dn) => passesFilter(dn)).map((dn) => ({ dn })),
);
const filteredReferenceEntries = computed<AssocEntry[]>(() =>
  dnReferenceGroups.value.flatMap((group) =>
    group.values.filter((dn) => passesFilter(dn)).map((dn) => ({ dn, attribute: group.attribute })),
  ),
);

// -- Member Of（member 过滤器子树反查引用者） ---------------------------------

type MemberOfState = "idle" | "loading" | "done" | "failed";
const memberOfState = ref<MemberOfState>("idle");
const memberOfDns = ref<string[]>([]);
const memberOfTruncated = ref(false);
const memberOfError = ref("");
// 竞态防护：dn/baseDn 变化会让在途响应过期，过期结果直接丢弃不落状态。
let memberOfRequestId = 0;

async function loadMemberOf() {
  const requestId = ++memberOfRequestId;
  memberOfState.value = "loading";
  memberOfError.value = "";
  try {
    const result = await ldapApi.search({
      baseDn: props.baseDn,
      scope: "sub",
      filter: `(member=${escapeLdapFilterValue(props.dn)})`,
      // "1.1" = RFC 4511 noAttributes：只要 DN，不要任何属性。
      attributes: ["1.1"],
      sizeLimit: MEMBER_OF_SIZE_LIMIT,
    });
    if (requestId !== memberOfRequestId) return;
    memberOfDns.value = result.entries.map((entry) => entry.dn);
    memberOfTruncated.value = result.truncated === true;
    memberOfState.value = "done";
  } catch (cause) {
    if (requestId !== memberOfRequestId) return;
    memberOfError.value = cause instanceof Error ? cause.message : String(cause);
    memberOfState.value = "failed";
    // 行内错误态 + 上抛（与编辑器保存失败同风格：宿主横幅兜底）。
    emit("error", t("associations.loadFailed", { error: memberOfError.value }));
  }
}

// dn / baseDn 变化 → 缓存作废（回到 idle）；active 下立即重新搜索。
watch([() => props.dn, () => props.baseDn], () => {
  memberOfRequestId += 1;
  memberOfState.value = "idle";
  memberOfDns.value = [];
  memberOfTruncated.value = false;
  memberOfError.value = "";
  if (props.active) void loadMemberOf();
});

// tab 激活惰性加载：只在尚未加载（idle）时发起；failed 态由重试按钮驱动，
// done 态不重复请求。
watch(
  () => props.active,
  (isActive) => {
    if (isActive && memberOfState.value === "idle") void loadMemberOf();
  },
  { immediate: true },
);

const filteredMemberOfEntries = computed<AssocEntry[]>(() =>
  memberOfDns.value.filter((dn) => passesFilter(dn)).map((dn) => ({ dn })),
);

// -- 被引用（核心引用属性 OR 过滤器子树反查，与所属区同款门控与防竞态） --------

type ReferencedByState = "idle" | "loading" | "done" | "failed";
const referencedByState = ref<ReferencedByState>("idle");
const referencedByDns = ref<string[]>([]);
const referencedByTruncated = ref(false);
const referencedByError = ref("");
// 竞态防护：dn/baseDn 变化会让在途响应过期，过期结果直接丢弃不落状态。
let referencedByRequestId = 0;

async function loadReferencedBy() {
  const requestId = ++referencedByRequestId;
  referencedByState.value = "loading";
  referencedByError.value = "";
  try {
    // 反查只用内置核心属性表（buildReferencedByFilter 内部做 RFC 4515 转义）。
    const result = await ldapApi.search({
      baseDn: props.baseDn,
      scope: "sub",
      filter: buildReferencedByFilter(props.dn, DN_REFERENCE_CORE),
      // "1.1" = RFC 4511 noAttributes：只要 DN，不要任何属性。
      attributes: ["1.1"],
      sizeLimit: REFERENCED_BY_SIZE_LIMIT,
    });
    if (requestId !== referencedByRequestId) return;
    referencedByDns.value = result.entries.map((entry) => entry.dn);
    referencedByTruncated.value = result.truncated === true;
    referencedByState.value = "done";
  } catch (cause) {
    if (requestId !== referencedByRequestId) return;
    referencedByError.value = cause instanceof Error ? cause.message : String(cause);
    referencedByState.value = "failed";
    emit("error", t("associations.loadFailed", { error: referencedByError.value }));
  }
}

// dn / baseDn 变化 → 缓存作废（回到 idle）；active 下立即重新搜索。
watch([() => props.dn, () => props.baseDn], () => {
  referencedByRequestId += 1;
  referencedByState.value = "idle";
  referencedByDns.value = [];
  referencedByTruncated.value = false;
  referencedByError.value = "";
  if (props.active) void loadReferencedBy();
});

// tab 激活惰性加载：只在尚未加载（idle）时发起；failed 态由重试按钮驱动，
// done 态不重复请求。
watch(
  () => props.active,
  (isActive) => {
    if (isActive && referencedByState.value === "idle") void loadReferencedBy();
  },
  { immediate: true },
);

const filteredReferencedByEntries = computed<AssocEntry[]>(() =>
  referencedByDns.value.filter((dn) => passesFilter(dn)).map((dn) => ({ dn })),
);

// -- 页签徽标与列表复位键 ------------------------------------------------------

// 页签元数据：成员 / DN 引用为同步计数（number，恒显示，含 0）；所属 / 被引用
// 只在反查完成（done）后给数字，加载中 / 失败 / 未发起时为 null（不渲染徽标），
// 与 brief「未加载完不显示数字」一致。
const tabs = computed(() => [
  { key: "members" as AssocTab, label: t("associations.members"), count: memberDns.value.length },
  {
    key: "references" as AssocTab,
    label: t("associations.references"),
    count: dnReferenceGroups.value.reduce((total, group) => total + group.values.length, 0),
  },
  {
    key: "memberOf" as AssocTab,
    label: t("associations.memberOf"),
    count: memberOfState.value === "done" ? memberOfDns.value.length : null,
  },
  {
    key: "referencedBy" as AssocTab,
    label: t("associations.referencedBy"),
    count: referencedByState.value === "done" ? referencedByDns.value.length : null,
  },
]);

// 列表滚动复位键：换 dn、切页签、改过滤词都回滚到顶部。
const listResetKey = computed(() => `${props.dn}|${activeTab.value}|${filterText.value}`);

// 复制完整 DN：宿主桥缺失或写入失败时如实通知"复制失败"（与编辑器 copyText 同款）。
async function copyDnValue(dn: string) {
  emit("notify", (await writeClipboardText(dn)) ? t("copied") : t("copyFailed"));
}
</script>

<template>
  <div class="assoc-panel" style="display: flex; min-height: 0; flex: 1; flex-direction: column; gap: 8px; overflow: auto; padding: 8px 10px">
    <!-- 1) 本地过滤框：即时过滤当前激活列表（无按钮、无防抖），dn 变化时清空 -->
    <input
      v-model="filterText"
      type="text"
      class="mono assoc-filter"
      :placeholder="t('associations.filterPlaceholder')"
      spellcheck="false"
      style="flex-shrink: 0; width: 100%; box-sizing: border-box; min-height: 0; border: 1px solid var(--border); border-radius: 4px; padding: 4px 8px; background: var(--background); color: var(--foreground); font-size: 11px"
    />

    <!-- 2) 子页签行：四页签 + 计数徽标（复用 .mode-switch / .is-active 样式模式） -->
    <div class="mode-switch assoc-tabs" role="tablist" style="flex-shrink: 0">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        class="assoc-tab"
        :class="{ 'is-active': activeTab === tab.key }"
        :data-tab="tab.key"
        role="tab"
        :aria-selected="activeTab === tab.key"
        @click="activeTab = tab.key"
      >
        {{ tab.label }}
        <!-- 计数徽标：复用 .tree-badge；null（反查未完成）时不渲染 -->
        <span v-if="tab.count !== null" class="tree-badge" :title="t('associations.count', { count: tab.count })" style="margin-left: 4px">{{ tab.count }}</span>
      </button>
    </div>

    <!-- 3) 激活列表区：一次只渲染一个列表（去拥挤的核心） -->
    <!-- 成员（Members）：条目自身 member / uniqueMember 属性值 -->
    <section v-if="activeTab === 'members'" class="assoc-members" style="display: flex; min-height: 0; flex: 1; flex-direction: column; gap: 4px">
      <div v-if="memberDns.length === 0" class="empty compact">{{ t("associations.emptyMembers") }}</div>
      <div v-else-if="filteredMemberEntries.length === 0" class="empty compact">{{ t("associations.noMatch") }}</div>
      <VirtualList v-else :items="filteredMemberEntries" :row-height="ASSOC_ROW_HEIGHT" :reset-key="listResetKey" style="flex: 1; min-height: 120px">
        <template #default="{ item }">
          <button
            class="tree-row assoc-row"
            :title="item.dn"
            style="cursor: pointer; height: 100%; gap: 6px"
            @click="emit('openEntry', item.dn)"
          >
            <span style="display: flex; min-width: 0; flex: 1; flex-direction: column; justify-content: center; gap: 1px">
              <span style="display: flex; min-width: 0; align-items: baseline; gap: 6px">
                <!-- 第一行：RDN（mono、主文本） -->
                <span class="mono" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ splitFirstDnRdn(item.dn).rdn }}</span>
                <!-- 来源属性小字：仅 DN 引用页签的行有值 -->
                <span v-if="item.attribute" class="muted assoc-attr" style="flex-shrink: 0; font-size: 10px">{{ item.attribute }}</span>
              </span>
              <!-- 第二行：完整 DN（muted、ellipsis；完整内容由行 title 悬停查看） -->
              <span class="muted" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 10px">{{ item.dn }}</span>
            </span>
            <!-- span role=button：button 内不再嵌套 button（与树行 twist 同解法） -->
            <span
              class="tree-twist assoc-copy"
              role="button"
              :title="t('associations.copyDn')"
              :aria-label="t('associations.copyDn')"
              @click.stop="copyDnValue(item.dn)"
            ><Copy aria-hidden="true" /></span>
          </button>
        </template>
      </VirtualList>
    </section>

    <!-- DN 引用（References）：member/uniqueMember 之外的 DN 值属性，扁平行内保留来源属性名 -->
    <section v-else-if="activeTab === 'references'" class="assoc-references" style="display: flex; min-height: 0; flex: 1; flex-direction: column; gap: 4px">
      <div v-if="dnReferenceGroups.length === 0" class="empty compact">{{ t("associations.emptyReferences") }}</div>
      <div v-else-if="filteredReferenceEntries.length === 0" class="empty compact">{{ t("associations.noMatch") }}</div>
      <VirtualList v-else :items="filteredReferenceEntries" :row-height="ASSOC_ROW_HEIGHT" :reset-key="listResetKey" style="flex: 1; min-height: 120px">
        <template #default="{ item }">
          <button
            class="tree-row assoc-row"
            :title="item.dn"
            style="cursor: pointer; height: 100%; gap: 6px"
            @click="emit('openEntry', item.dn)"
          >
            <span style="display: flex; min-width: 0; flex: 1; flex-direction: column; justify-content: center; gap: 1px">
              <span style="display: flex; min-width: 0; align-items: baseline; gap: 6px">
                <span class="mono" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ splitFirstDnRdn(item.dn).rdn }}</span>
                <span v-if="item.attribute" class="muted assoc-attr" style="flex-shrink: 0; font-size: 10px">{{ item.attribute }}</span>
              </span>
              <span class="muted" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 10px">{{ item.dn }}</span>
            </span>
            <span
              class="tree-twist assoc-copy"
              role="button"
              :title="t('associations.copyDn')"
              :aria-label="t('associations.copyDn')"
              @click.stop="copyDnValue(item.dn)"
            ><Copy aria-hidden="true" /></span>
          </button>
        </template>
      </VirtualList>
    </section>

    <!-- 所属（Member Of）：(member=<本条目 DN>) 子树反查，tab 激活后惰性搜索 -->
    <section v-else-if="activeTab === 'memberOf'" class="assoc-member-of" style="display: flex; min-height: 0; flex: 1; flex-direction: column; gap: 4px">
      <div v-if="memberOfState === 'loading'" class="empty compact">{{ t("associations.loading") }}</div>
      <div v-else-if="memberOfState === 'failed'" style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap">
        <p class="form-error" style="flex: 1; min-width: 0">{{ t("associations.loadFailed", { error: memberOfError }) }}</p>
        <button class="toolbar-button assoc-retry" @click="loadMemberOf">{{ t("associations.retry") }}</button>
      </div>
      <div v-else-if="memberOfDns.length === 0" class="empty compact">{{ t("associations.emptyMemberOf") }}</div>
      <div v-else-if="filteredMemberOfEntries.length === 0" class="empty compact">{{ t("associations.noMatch") }}</div>
      <template v-else>
        <VirtualList :items="filteredMemberOfEntries" :row-height="ASSOC_ROW_HEIGHT" :reset-key="listResetKey" style="flex: 1; min-height: 120px">
          <template #default="{ item }">
            <button
              class="tree-row assoc-row"
              :title="item.dn"
              style="cursor: pointer; height: 100%; gap: 6px"
              @click="emit('openEntry', item.dn)"
            >
              <span style="display: flex; min-width: 0; flex: 1; flex-direction: column; justify-content: center; gap: 1px">
                <span style="display: flex; min-width: 0; align-items: baseline; gap: 6px">
                  <span class="mono" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ splitFirstDnRdn(item.dn).rdn }}</span>
                  <span v-if="item.attribute" class="muted assoc-attr" style="flex-shrink: 0; font-size: 10px">{{ item.attribute }}</span>
                </span>
                <span class="muted" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 10px">{{ item.dn }}</span>
              </span>
              <span
                class="tree-twist assoc-copy"
                role="button"
                :title="t('associations.copyDn')"
                :aria-label="t('associations.copyDn')"
                @click.stop="copyDnValue(item.dn)"
              ><Copy aria-hidden="true" /></span>
            </button>
          </template>
        </VirtualList>
        <p v-if="memberOfTruncated" class="hint">{{ t("associations.truncated", { limit: MEMBER_OF_SIZE_LIMIT }) }}</p>
      </template>
    </section>

    <!-- 被引用（Referenced by）：核心引用属性 OR 过滤器子树反查，tab 激活后惰性搜索 -->
    <section v-else class="assoc-referenced-by" style="display: flex; min-height: 0; flex: 1; flex-direction: column; gap: 4px">
      <div v-if="referencedByState === 'loading'" class="empty compact">{{ t("associations.loading") }}</div>
      <div v-else-if="referencedByState === 'failed'" style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap">
        <p class="form-error" style="flex: 1; min-width: 0">{{ t("associations.loadFailed", { error: referencedByError }) }}</p>
        <button class="toolbar-button assoc-retry" @click="loadReferencedBy">{{ t("associations.retry") }}</button>
      </div>
      <div v-else-if="referencedByDns.length === 0" class="empty compact">{{ t("associations.emptyReferencedBy") }}</div>
      <div v-else-if="filteredReferencedByEntries.length === 0" class="empty compact">{{ t("associations.noMatch") }}</div>
      <template v-else>
        <VirtualList :items="filteredReferencedByEntries" :row-height="ASSOC_ROW_HEIGHT" :reset-key="listResetKey" style="flex: 1; min-height: 120px">
          <template #default="{ item }">
            <button
              class="tree-row assoc-row"
              :title="item.dn"
              style="cursor: pointer; height: 100%; gap: 6px"
              @click="emit('openEntry', item.dn)"
            >
              <span style="display: flex; min-width: 0; flex: 1; flex-direction: column; justify-content: center; gap: 1px">
                <span style="display: flex; min-width: 0; align-items: baseline; gap: 6px">
                  <span class="mono" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ splitFirstDnRdn(item.dn).rdn }}</span>
                  <span v-if="item.attribute" class="muted assoc-attr" style="flex-shrink: 0; font-size: 10px">{{ item.attribute }}</span>
                </span>
                <span class="muted" style="min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 10px">{{ item.dn }}</span>
              </span>
              <span
                class="tree-twist assoc-copy"
                role="button"
                :title="t('associations.copyDn')"
                :aria-label="t('associations.copyDn')"
                @click.stop="copyDnValue(item.dn)"
              ><Copy aria-hidden="true" /></span>
            </button>
          </template>
        </VirtualList>
        <p v-if="referencedByTruncated" class="hint">{{ t("associations.truncated", { limit: REFERENCED_BY_SIZE_LIMIT }) }}</p>
      </template>
    </section>
  </div>
</template>
