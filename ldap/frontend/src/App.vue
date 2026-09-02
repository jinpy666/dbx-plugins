<script setup lang="ts">
// LDAP 工作台外壳：布局 + 连接上下文（照 ssh-sftp App.vue 的宿主桥用法）。
// 连接生命周期由宿主驱动（connection/test|connect|disconnect），工作台只持有
// connectionId；所有 ldap/* 调用经 lib/api.ts 注入 connectionId。
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { Database, Download, Info, Loader2, Network, RefreshCw } from "@lucide/vue";
import { DBX_POPOVER, resolveAppearance } from "./lib/appearance";
import { setWorkbenchLocale, t, workbenchLocale } from "./lib/i18n";
import { getLdapConnectionId, ldapApi, setLdapConnectionId, type LdapEntry } from "./lib/api";
import { inferBaseDnFromProfile, pickBaseDnFromRootDse } from "./lib/baseDn";
import { friendlyLdapError } from "./lib/ldapErrors";
import { serializeEntriesToCsv, serializeEntriesToJson, serializeEntriesToLdifText } from "./lib/ldapExporter";
import DnTree from "./components/DnTree.vue";
import SearchForm, { type SearchFormModel } from "./components/SearchForm.vue";
import ResultTable from "./components/ResultTable.vue";
import EntryEditorDialog from "./components/EntryEditorDialog.vue";
import DeleteEntryDialog from "./components/DeleteEntryDialog.vue";
import ModifyDnDialog from "./components/ModifyDnDialog.vue";
import SchemaPanel from "./components/SchemaPanel.vue";
import ConnectionsPanel from "./components/ConnectionsPanel.vue";
import AuditFeedPanel from "./components/AuditFeedPanel.vue";
import { parseAuditEvent, pushAuditItem, type AuditFeedItem } from "./lib/auditFeed";

interface ConnectionSummary {
  name?: string;
  host?: string;
  port?: number;
  username?: string;
  color?: string;
  readOnly?: boolean;
  baseDn?: string;
  external_config?: Record<string, unknown>;
}

const hostContext = ref<Record<string, unknown>>({});
const appearance = ref(resolveAppearance());
const ready = ref(false);
const initError = ref("");
const ldapError = ref("");
const ldapErrorDetail = ref("");
const notice = ref("");
const busy = ref(false);

const treeRef = ref<InstanceType<typeof DnTree>>();
const searchRef = ref<InstanceType<typeof SearchForm>>();

const baseDn = ref("");
const searchModel = ref<SearchFormModel>();
const results = ref<LdapEntry[]>([]);
const resultCount = ref(0);
const resultTruncated = ref(false);
const searching = ref(false);

const editorOpen = ref(false);
const editorEntry = ref<LdapEntry>();
const editorParentDn = ref("");
const deleteOpen = ref(false);
const deleteDn = ref("");
const deleteSubmitting = ref(false);
const modifyDnOpen = ref(false);
const modifyDnSource = ref("");
const modifyDnSubmitting = ref(false);
const schemaOpen = ref(false);
const connectionsOpen = ref(false);

// 审计事件流（ldap/audit → 最近操作面板）；横幅/通知仍保留作为即时反馈。
const auditItems = ref<AuditFeedItem[]>([]);
let auditSeq = 0;

let noticeTimer = 0;
const unsubscribeAppearance: Array<() => void> = [];
const unsubscribeLocale: Array<() => void> = [];
const unsubscribeContext: Array<() => void> = [];
const unsubscribeEvent: Array<() => void> = [];

const connectionId = computed(() => String(hostContext.value.connectionId || ""));
const fallbackWorkbenchId = crypto.randomUUID();
const workbenchId = computed(() => String(hostContext.value.workbenchId || fallbackWorkbenchId));
const connection = computed<ConnectionSummary>(() => {
  const value = hostContext.value.connection;
  return value && typeof value === "object" ? (value as ConnectionSummary) : {};
});
// 写权限 = 宿主 context 未标记只读 且 后端策略层未开启只读门禁
// （ldap/connections/statuses 行的 readOnly：表单 read_only ∥ 宿主 read_only）。
const backendReadOnly = ref(false);
const canWrite = computed(() => !connection.value.readOnly && !backendReadOnly.value);

// 从连接状态表读取生效只读门禁；旧 sidecar 无 readOnly 字段时保持可写回退。
async function refreshBackendReadOnly() {
  try {
    const result = await ldapApi.connectionStatuses();
    const mine = (result.statuses || []).find((row) => row.connectionId === connectionId.value);
    backendReadOnly.value = mine?.readOnly === true;
  } catch {
    backendReadOnly.value = false;
  }
}
const contextBaseDn = computed(() => {
  const direct = connection.value.baseDn;
  if (typeof direct === "string" && direct.trim()) return direct.trim();
  const external = connection.value.external_config;
  if (external && typeof external === "object") {
    const value = (external as Record<string, unknown>)["base_dn"];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
});
const connectionIdentity = computed(() => {
  const host = connection.value.host || connection.value.name || connectionId.value;
  const identity = connection.value.username ? `${connection.value.username}@${host}` : host;
  const port = connection.value.port ? `:${connection.value.port}` : "";
  return `${identity}${port}`;
});
const toolbarStyle = computed(() => {
  const color = connection.value.color;
  if (!color) return undefined;
  return { backgroundColor: colorWithAlpha(color, 0.1), boxShadow: `inset 0 1px 0 ${colorWithAlpha(color, 0.18)}` };
});

function applyAppearance(next?: Partial<DbxPluginAppearance> | null) {
  const resolved = resolveAppearance(next);
  appearance.value = resolved;
  const root = document.documentElement;
  root.dataset.theme = resolved.colorScheme;
  root.style.colorScheme = resolved.colorScheme;
  root.style.setProperty("--background", resolved.colors.background);
  root.style.setProperty("--foreground", resolved.colors.foreground);
  root.style.setProperty("--muted", resolved.colors.muted);
  root.style.setProperty("--muted-foreground", resolved.colors.mutedForeground);
  root.style.setProperty("--accent", resolved.colors.accent);
  root.style.setProperty("--accent-foreground", resolved.colors.accentForeground);
  root.style.setProperty("--border", resolved.colors.border);
  root.style.setProperty("--destructive", resolved.colors.destructive);
  root.style.setProperty("--popover", DBX_POPOVER[resolved.colorScheme]);
  root.style.setProperty("--ui-font-family", resolved.ui.fontFamily);
}

function showNotice(message: string) {
  notice.value = message;
  window.clearTimeout(noticeTimer);
  noticeTimer = window.setTimeout(() => (notice.value = ""), 3500);
}

function showError(cause: unknown, target: "ldap" | "init" = "ldap") {
  const message = cause instanceof Error ? cause.message : String(cause);
  if (target === "init") {
    initError.value = message;
    return;
  }
  // 横幅展示本地化的可行动文案；原始错误串挂在 title 悬停里供排查。
  ldapError.value = friendlyLdapError(message);
  ldapErrorDetail.value = ldapError.value === message ? "" : message;
}

function dismissError() {
  ldapError.value = "";
}

function colorWithAlpha(color: string, alpha: number) {
  const match = color.trim().match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i);
  if (!match) return `color-mix(in srgb, ${color} ${Math.round(alpha * 100)}%, transparent)`;
  const hex = match[1].length === 3 ? [...match[1]].map((part) => `${part}${part}`).join("") : match[1];
  const red = Number.parseInt(hex.slice(0, 2), 16);
  const green = Number.parseInt(hex.slice(2, 4), 16);
  const blue = Number.parseInt(hex.slice(4, 6), 16);
  return `rgb(${red} ${green} ${blue} / ${alpha})`;
}

// -- search flow --------------------------------------------------------------

async function runSearch(model: SearchFormModel) {
  if (busy.value || searching.value) return;
  searching.value = true;
  searchModel.value = model;
  ldapError.value = "";
  try {
    const attributes = model.attributes
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
    const result = await ldapApi.search({
      baseDn: model.baseDn.trim() || undefined,
      filter: model.filter.trim() || "(objectClass=*)",
      scope: model.scope,
      ...(attributes.length > 0 ? { attributes } : {}),
      sizeLimit: positiveInt(model.sizeLimit),
      pageSize: positiveInt(model.pageSize),
      typesOnly: model.typesOnly,
      derefAliases: model.derefAliases,
    });
    results.value = Array.isArray(result.entries) ? result.entries : [];
    resultCount.value = Number.isFinite(result.count) ? result.count : results.value.length;
    resultTruncated.value = result.truncated === true;
  } catch (cause) {
    showError(cause);
  } finally {
    searching.value = false;
  }
}

function positiveInt(value: string): number | undefined {
  const parsed = Number.parseInt(value.trim(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
}

function searchHere(dn: string) {
  // 约束：树根（baseDn）固定为连接 Base DN，「在此搜索」只把搜索面板的
  // base 指到该节点。此前这里直接改 baseDn.value，DnTree watch 到变化会
  // 整树重根，原始根丢失且无还原途径（tiny-rdm 语义：树常驻、搜索联动）。
  selectEntry(dn);
}

function selectEntry(dn: string) {
  // 点选节点只联动搜索面板 base（tiny-rdm 搜索框跟随选中节点语义），
  // 不取数、不动树根；打开编辑器由显式动作触发。
  searchRef.value?.applyBaseDn(dn);
}

async function openEntry(dn: string) {
  ldapError.value = "";
  try {
    const result = await ldapApi.entryGet(dn);
    editorEntry.value = result.entry;
    editorParentDn.value = "";
    editorOpen.value = true;
  } catch (cause) {
    showError(cause);
  }
}

function openAddChild(parentDn: string) {
  editorEntry.value = undefined;
  editorParentDn.value = parentDn;
  editorOpen.value = true;
}

function onEditorSaved(dn: string, mode_: "add" | "edit") {
  editorOpen.value = false;
  showNotice(mode_ === "add" ? t("editor.added") : t("editor.saved"));
  treeRef.value?.invalidate(mode_ === "add" ? parentOf(dn) : dn);
  void openEntryRefresh(dn);
}

async function openEntryRefresh(dn: string) {
  try {
    const result = await ldapApi.entryGet(dn);
    editorEntry.value = result.entry;
  } catch {
    // Entry may have been renamed/moved; keep the dialog closed.
  }
}

function parentOf(dn: string): string {
  const index = dn.indexOf(",");
  return index > 0 ? dn.slice(index + 1) : dn;
}

function askDelete(dn: string) {
  deleteDn.value = dn;
  deleteOpen.value = true;
}

async function confirmDelete() {
  if (!deleteDn.value || deleteSubmitting.value) return;
  deleteSubmitting.value = true;
  ldapError.value = "";
  try {
    await ldapApi.entryDelete(deleteDn.value);
    deleteOpen.value = false;
    showNotice(t("deleteDialog.deleted"));
    treeRef.value?.invalidate(parentOf(deleteDn.value));
  } catch (cause) {
    showError(cause);
  } finally {
    deleteSubmitting.value = false;
  }
}

function askRename(dn: string) {
  modifyDnSource.value = dn;
  modifyDnOpen.value = true;
}

async function confirmRename(newRdn: string, newParentDn: string | undefined, deleteOldRdn: boolean) {
  if (!modifyDnSource.value || modifyDnSubmitting.value) return;
  modifyDnSubmitting.value = true;
  ldapError.value = "";
  try {
    await ldapApi.entryModifyDn(modifyDnSource.value, newRdn, newParentDn, deleteOldRdn);
    modifyDnOpen.value = false;
    showNotice(t("modifyDn.moved"));
    treeRef.value?.invalidate();
  } catch (cause) {
    showError(cause);
  } finally {
    modifyDnSubmitting.value = false;
  }
}

async function exportSubtree(dn: string) {
  ldapError.value = "";
  try {
    const result = await ldapApi.search({ baseDn: dn, filter: "(objectClass=*)", scope: "sub", sizeLimit: 500, pageSize: 500 });
    downloadText(`${rdnOf(dn) || "subtree"}.ldif`, "text/plain", serializeEntriesToLdifText(result.entries));
    showNotice(t("result.exportDone", { name: "LDIF" }));
  } catch (cause) {
    showError(cause);
  }
}

async function exportResults(format: "ldif" | "csv" | "json") {
  if (results.value.length === 0) return;
  try {
    if (format === "ldif") downloadText("ldap-search.ldif", "text/plain", serializeEntriesToLdifText(results.value));
    else if (format === "csv") downloadText("ldap-search.csv", "text/csv", serializeEntriesToCsv(results.value));
    else downloadText("ldap-search.json", "application/json", serializeEntriesToJson(results.value));
    showNotice(t("result.exportDone", { name: format.toUpperCase() }));
  } catch (cause) {
    ldapError.value = t("result.exportFailed");
    void cause;
  }
}

function downloadText(name: string, contentType: string, text: string) {
  // Host API 1.0 has no save-file bridge; Blob URL download is the agreed fallback.
  const blob = new Blob([text], { type: `${contentType};charset=utf-8` });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

function rdnOf(dn: string): string {
  const index = dn.indexOf(",");
  return index > 0 ? dn.slice(0, index) : dn;
}

async function showRootDse() {
  ldapError.value = "";
  try {
    const result = await ldapApi.rootDse();
    const attributes = result.attributes || {};
    const lines = Object.keys(attributes)
      .sort()
      .map((name) => `${name}: ${(attributes[name] ?? []).join(", ")}`);
    downloadText("root-dse.txt", "text/plain", lines.join("\n"));
    showNotice(t("rootDse.title"));
  } catch (cause) {
    showError(cause);
  }
}

async function copyDn(dn: string) {
  try {
    await window.dbxPlugin.clipboard?.writeText(dn);
    showNotice(t("copied"));
  } catch {
    showNotice(t("copied"));
  }
}

function handleEvent(event: { method: string; params: Record<string, unknown> }) {
  if (event.method === "ldap/audit") {
    const params = event.params || {};
    // 数据面：进入最近操作面板（denied/error 高亮）；即时反馈走横幅/通知。
    auditItems.value = pushAuditItem(auditItems.value, parseAuditEvent(params, auditSeq++, Date.now()));
    const result = String(params.result ?? "");
    if (result === "denied" || result === "error") {
      showError(`${params.action ?? "ldap"}: ${result}`);
    } else {
      showNotice(`${params.action ?? "ldap"} ✓`);
    }
  }
}

function clearAuditFeed() {
  auditItems.value = [];
}

async function waitForHostApi(timeoutMs = 8000) {
  const deadline = Date.now() + timeoutMs;
  while (!window.dbxPlugin && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 50));
  if (!window.dbxPlugin) throw new Error(t("hostApiUnavailable"));
  return window.dbxPlugin;
}

async function initialize() {
  const api = await waitForHostApi();
  hostContext.value = await Promise.any([
    api.ready,
    api.request<Record<string, unknown>>("host.getContext"),
  ]);
  setWorkbenchLocale(api.locale || "zh-CN");
  if (api.appearance) applyAppearance(api.appearance);
  if (api.onAppearanceChange) unsubscribeAppearance.push(api.onAppearanceChange(applyAppearance));
  if (api.onLocaleChange) unsubscribeLocale.push(api.onLocaleChange((next) => setWorkbenchLocale(next || "zh-CN")));
  if (api.onContextChange) unsubscribeContext.push(api.onContextChange((context) => {
    hostContext.value = context;
    syncConnectionContext();
  }));
  if (api.onEvent) unsubscribeEvent.push(api.onEvent(handleEvent));
  if (!connectionId.value) throw new Error(t("connectionMissing"));
  setLdapConnectionId(connectionId.value);
  syncConnectionContext();
  void refreshBackendReadOnly();
  ready.value = true;
  await nextTick();
  void treeRef.value?.refresh();
  void searchRef.value?.applyBaseDn(baseDn.value);
}

function syncConnectionContext() {
  setLdapConnectionId(connectionId.value);
  baseDn.value = contextBaseDn.value;
  void resolveAutoBaseDn();
}

// 连接未配置 base_dn 时的兜底定位（tiny-rdm baseDn.js 语义）：
// RootDSE namingContexts（AD 取 defaultNamingContext）→ 主机名推断
// （corp.int.kn → dc=corp,dc=int,dc=kn）。两路都失败则保留空值，
// 由目录树提示用户补配。显式配置永远优先；解析成功后提示实际生效值。
async function resolveAutoBaseDn() {
  if (baseDn.value) return;
  let resolved = "";
  try {
    const result = await ldapApi.rootDse(["namingContexts", "defaultNamingContext"]);
    resolved = pickBaseDnFromRootDse(result.attributes || {});
  } catch {
    // RootDSE 不可读（如配置了 allowed_base_dns）：退回主机名推断。
  }
  if (!resolved) {
    resolved = inferBaseDnFromProfile({ url: connection.value.host, host: connection.value.host });
  }
  if (!resolved || baseDn.value || contextBaseDn.value) return; // 竞态守卫：期间带来显式值则放弃
  baseDn.value = resolved;
  void searchRef.value?.applyBaseDn(resolved);
  showNotice(t("tree.autoBaseDn", { baseDn: resolved }));
}

function refreshTree() {
  void treeRef.value?.refresh();
}

onMounted(() => {
  void initialize().catch((cause) => showError(cause, "init"));
});

onBeforeUnmount(() => {
  window.clearTimeout(noticeTimer);
  for (const dispose of [...unsubscribeAppearance, ...unsubscribeLocale, ...unsubscribeContext, ...unsubscribeEvent]) dispose();
});
</script>

<template>
  <div class="workbench">
    <header class="toolbar" :style="toolbarStyle">
      <div class="identity">
        <span class="connection-color" :style="connection.color ? { background: connection.color } : undefined" />
        <strong :title="connectionIdentity">{{ connectionIdentity }}</strong>
        <span v-if="!canWrite" class="read-only-badge">{{ t("readOnly") }}</span>
        <Loader2 v-if="!ready" class="icon-neutral spinning" aria-hidden="true" />
      </div>
      <div class="toolbar-actions">
        <button class="toolbar-button" :disabled="!ready" :title="t('rootDse.title')" @click="showRootDse">
          <Info aria-hidden="true" /><span>{{ t("rootDse.title") }}</span>
        </button>
        <button class="toolbar-button" :disabled="!ready" :title="t('schema.title')" @click="schemaOpen = true">
          <Database class="icon-violet" aria-hidden="true" /><span>{{ t("schema.title") }}</span>
        </button>
        <button class="toolbar-button" :disabled="!ready" :title="t('connections.title')" @click="connectionsOpen = true">
          <Network class="icon-cyan" aria-hidden="true" /><span>{{ t("connections.title") }}</span>
        </button>
        <span class="toolbar-separator" />
        <button class="icon-button" :disabled="!ready" :title="t('refresh')" @click="refreshTree">
          <RefreshCw aria-hidden="true" />
        </button>
        <button class="icon-button" :disabled="!ready || results.length === 0" :title="t('result.exportLdif')" @click="exportResults('ldif')">
          <Download aria-hidden="true" />
        </button>
      </div>
    </header>

    <div v-if="initError" class="tree-state">{{ initError }}</div>
    <div v-else-if="!ready" class="tree-state">{{ t("tree.loading") }}</div>

    <div v-else class="panes">
      <DnTree
        ref="treeRef"
        :base-dn="baseDn"
        :can-write="canWrite"
        @select="selectEntry"
        @search-here="searchHere"
        @view="openEntry"
        @add="openAddChild"
        @rename="askRename"
        @remove="askDelete"
        @export="exportSubtree"
        @copy-dn="copyDn"
      />
      <div class="divider" />
      <main class="main-pane">
        <SearchForm
          ref="searchRef"
          :base-dn="baseDn"
          :disabled="searching"
          @run="runSearch"
          @notify="showNotice"
          @error="(message) => showError(message)"
        />
        <p v-if="!baseDn" class="hint" style="padding: 0 10px">{{ t("tree.missingBaseDn") }}</p>
        <ResultTable
          :entries="results"
          :count="resultCount"
          :truncated="resultTruncated"
          :disabled="searching"
          @open="openEntry"
          @export="exportResults"
        />
        <AuditFeedPanel :items="auditItems" @clear="clearAuditFeed" />
      </main>
    </div>

    <div v-if="ldapError" class="error-banner">
      <span :title="ldapErrorDetail || ldapError">{{ ldapError }}</span>
      <button type="button" @click="dismissError">✕</button>
    </div>
    <div v-if="notice" class="notice">{{ notice }}</div>

    <EntryEditorDialog
      :open="editorOpen"
      :entry="editorEntry"
      :parent-dn="editorParentDn"
      :can-write="canWrite"
      @close="editorOpen = false"
      @saved="onEditorSaved"
      @error="showError"
      @notify="showNotice"
    />
    <DeleteEntryDialog
      :open="deleteOpen"
      :dn="deleteDn"
      :submitting="deleteSubmitting"
      @close="deleteOpen = false"
      @confirm="confirmDelete"
    />
    <ModifyDnDialog
      :open="modifyDnOpen"
      :dn="modifyDnSource"
      :base-dn="baseDn"
      :submitting="modifyDnSubmitting"
      @close="modifyDnOpen = false"
      @confirm="confirmRename"
    />
    <SchemaPanel :open="schemaOpen" :connection-id="getLdapConnectionId()" @close="schemaOpen = false" @error="showError" />
    <ConnectionsPanel :open="connectionsOpen" :disabled="!ready" @close="connectionsOpen = false" @error="showError" />
  </div>
</template>
