/**
 * ag-grid column definitions for every Kafka workbench table (Phase 2).
 *
 * Centralised here so sorting/filter types stay consistent across panels and
 * stay unit-testable: builders are pure functions over the locale-aware `t()`
 * (components rebuild them inside `computed` so a locale switch re-renders
 * headers). Row data is passed through small `to*Rows()` view-model mappers so
 * display text (previews, timestamps) is formatted once, not per-cell.
 *
 * Pagination page size persists per table key in localStorage (commercial
 * parity with tinyrdm KafkaGrid). `minimalColumns()` powers the narrow
 * container degradation to the minimal column set.
 */
import type { ColDef, ValueFormatterParams } from "ag-grid-community";
import type {
  GroupMember,
  GroupOffsetRow,
  KafkaAcl,
  KafkaGroup,
  KafkaMessage,
  KafkaTopic,
  SchemaSubject,
  SchemaVersionRow,
  TopicOffsetRow,
  TopicPartitionInfo,
} from "./api";
import { formatTimestamp, headersPreview, previewText } from "./kafkaModel";
import { t, workbenchLocale } from "./i18n";

// -- view models ----------------------------------------------------------------

export interface MessageRow {
  id: string;
  partition: number;
  offset: number;
  timestampText: string;
  keyText: string;
  valueText: string;
  headersText: string;
  schemaText: string;
  raw: KafkaMessage;
}

export interface GroupRow {
  group: string;
  state: string;
  protocolType: string;
  coordinator: string;
  raw: KafkaGroup;
}

export interface GroupOffsetVm {
  id: string;
  topic: string;
  partition: number;
  startOffset: string;
  endOffset: string;
  committedText: string;
  lag: number | null;
  raw: GroupOffsetRow;
}

export interface MemberVm {
  memberId: string;
  instanceId: string;
  clientId: string;
  clientHost: string;
  assignments: string;
  raw: GroupMember;
}

export interface AclVm {
  resourceType: string;
  resourceName: string;
  patternType: string;
  principal: string;
  host: string;
  operation: string;
  permission: string;
  raw: KafkaAcl;
}

export interface TopicVm {
  name: string;
  partitionCount: number;
  replicationFactor: number;
  internalText: string;
  raw: KafkaTopic;
}

export interface PartitionVm {
  partition: number;
  leader: number;
  replicas: string;
  isr: string;
  offline: string;
  healthyText: string;
  healthy: boolean;
  raw: TopicPartitionInfo;
}

export interface TopicOffsetVm {
  partition: number;
  offset: number;
  timestampText: string;
  leaderEpoch: string;
  raw: TopicOffsetRow;
}

export interface SubjectVm {
  subject: string;
  formats: string;
  latestVersion: number | null;
  compatibilityLevel: string;
  raw: SchemaSubject;
}

export interface SchemaVersionVm {
  version: number;
  id: number;
  format: string;
  raw: SchemaVersionRow;
}

export interface LagVm {
  id: string;
  topic: string;
  partition: number;
  committedText: string;
  endOffset: string;
  lag: number | null;
  raw: GroupOffsetRow;
}

// -- row mappers ------------------------------------------------------------------

export function toMessageRows(messages: KafkaMessage[]): MessageRow[] {
  return messages.map((message) => ({
    id: `${message.partition}:${message.offset}`,
    partition: message.partition,
    offset: message.offset,
    timestampText: formatTimestamp(message.timestamp),
    keyText: previewText(message.key, 60),
    valueText: previewText(message.valueText, 160),
    headersText: headersPreview(message.headers),
    schemaText: message.schemaSubject ? `${message.schemaSubject} v${message.schemaVersion ?? "?"}` : "",
    raw: message,
  }));
}

export function toGroupRows(groups: KafkaGroup[]): GroupRow[] {
  return groups.map((group) => ({
    group: group.group,
    state: group.state ?? "—",
    protocolType: group.protocolType ?? "—",
    coordinator: group.coordinator === undefined || group.coordinator === null ? "—" : String(group.coordinator),
    raw: group,
  }));
}

export function toGroupOffsetRows(rows: GroupOffsetRow[]): GroupOffsetVm[] {
  return rows.map((row) => ({
    id: `${row.topic}:${row.partition}`,
    topic: row.topic,
    partition: row.partition,
    startOffset: row.startOffset === undefined || row.startOffset === null ? "—" : String(row.startOffset),
    endOffset: row.endOffset === undefined || row.endOffset === null ? "—" : String(row.endOffset),
    committedText:
      row.hasCommitted === false ? t("groups.hasCommittedFalse") : row.committedOffset === undefined || row.committedOffset === null ? "—" : String(row.committedOffset),
    lag: row.lag === undefined || row.lag === null ? null : Number(row.lag),
    raw: row,
  }));
}

export function toMemberRows(members: GroupMember[]): MemberVm[] {
  return members.map((member) => ({
    memberId: member.memberId,
    instanceId: member.instanceId ?? "—",
    clientId: member.clientId ?? "—",
    clientHost: member.clientHost ?? "—",
    assignments: Object.entries(member.assignments ?? {})
      .map(([topic, partitions]) => `${topic}[${partitions.join(",")}]`)
      .join("; "),
    raw: member,
  }));
}

export function toAclRows(acls: KafkaAcl[]): AclVm[] {
  return acls.map((acl) => ({
    resourceType: acl.resourceType,
    resourceName: acl.resourceName,
    patternType: acl.patternType || "LITERAL",
    principal: acl.principal,
    host: acl.host || "*",
    operation: acl.operation,
    permission: acl.permission,
    raw: acl,
  }));
}

export function toTopicRows(topics: KafkaTopic[]): TopicVm[] {
  return topics.map((topic) => ({
    name: topic.name,
    partitionCount: topic.partitionCount,
    replicationFactor: topic.replicationFactor,
    internalText: topic.isInternal || topic.name.startsWith("_") ? t("topics.colInternal") : "",
    raw: topic,
  }));
}

export function toPartitionRows(partitions: TopicPartitionInfo[]): PartitionVm[] {
  return partitions.map((partition) => ({
    partition: partition.partition,
    leader: partition.leader,
    replicas: partition.replicas.join(","),
    isr: partition.isr.join(","),
    offline: partition.offlineReplicas.length > 0 ? partition.offlineReplicas.join(",") : "—",
    healthyText: partition.isHealthy === false ? t("topics.unhealthy") : t("topics.healthy"),
    healthy: partition.isHealthy !== false,
    raw: partition,
  }));
}

export function toTopicOffsetRows(rows: TopicOffsetRow[]): TopicOffsetVm[] {
  return rows.map((row) => ({
    partition: row.partition,
    offset: row.offset,
    timestampText: formatTimestamp(row.timestamp),
    leaderEpoch: row.leaderEpoch === undefined || row.leaderEpoch === null ? "—" : String(row.leaderEpoch),
    raw: row,
  }));
}

export function toSubjectRows(subjects: SchemaSubject[]): SubjectVm[] {
  return subjects.map((subject) => ({
    subject: subject.subject,
    formats: (subject.formats ?? []).join(", "),
    latestVersion: subject.latestVersion === undefined || subject.latestVersion === null ? null : Number(subject.latestVersion),
    compatibilityLevel: subject.compatibilityLevel ?? "",
    raw: subject,
  }));
}

export function toSchemaVersionRows(rows: SchemaVersionRow[]): SchemaVersionVm[] {
  return rows.map((row) => ({ version: row.version, id: row.id, format: row.format, raw: row }));
}

export function toLagRows(rows: GroupOffsetRow[]): LagVm[] {
  return rows.map((row) => ({
    id: `${row.topic}:${row.partition}`,
    topic: row.topic,
    partition: row.partition,
    committedText: row.hasCommitted === false ? t("groups.hasCommittedFalse") : row.committedOffset === undefined || row.committedOffset === null ? "—" : String(row.committedOffset),
    endOffset: row.endOffset === undefined || row.endOffset === null ? "—" : String(row.endOffset),
    lag: row.lag === undefined || row.lag === null ? null : Number(row.lag),
    raw: row,
  }));
}

// -- column builders ---------------------------------------------------------------

function textColumn(field: string, headerKey: string, extra: Partial<ColDef> = {}): ColDef {
  return { field, headerName: t(headerKey), sortable: true, resizable: true, filter: "agTextColumnFilter", ...extra };
}

function numberColumn(field: string, headerKey: string, extra: Partial<ColDef> = {}): ColDef {
  return {
    field,
    headerName: t(headerKey),
    sortable: true,
    resizable: true,
    filter: "agNumberColumnFilter",
    cellClass: "numeric",
    ...extra,
  };
}

export function messageColumns(): ColDef<MessageRow>[] {
  return [
    numberColumn("partition", "messages.colPartition", { maxWidth: 90 }),
    numberColumn("offset", "messages.colOffset", { maxWidth: 120 }),
    textColumn("timestampText", "messages.colTimestamp", { minWidth: 150, cellClass: "mono-s" }),
    textColumn("keyText", "messages.colKey", { cellClass: "mono-s" }),
    textColumn("valueText", "messages.colValue", { flex: 2, minWidth: 180, tooltipField: "valueText" }),
    textColumn("headersText", "messages.colHeaders", { cellClass: "mono-s", tooltipField: "headersText" }),
    textColumn("schemaText", "messages.colSchema", { cellClass: "mono-s", minWidth: 120 }),
  ];
}

export function groupColumns(): ColDef<GroupRow>[] {
  return [
    textColumn("group", "groups.colGroup", { flex: 1.4, cellClass: "mono-s" }),
    {
      ...textColumn("state", "groups.colState", { maxWidth: 150 }),
      valueFormatter: (params: ValueFormatterParams<GroupRow>) => {
        const raw = String(params.value ?? "").trim();
        if (!raw || raw === "—") return raw || "—";
        const pascal = raw.replace(/(^|[-_])([a-z])/g, (_m, _s, c) => c.toUpperCase());
        const key = `groups.state${pascal}`;
        const localized = t(key);
        return localized === key ? raw : localized;
      },
    } as ColDef<GroupRow>,
    textColumn("protocolType", "groups.colProtocol", { maxWidth: 120 }),
    textColumn("coordinator", "groups.colCoordinator", { maxWidth: 110, cellClass: "mono-s" }),
  ];
}

export function groupOffsetColumns(): ColDef<GroupOffsetVm>[] {
  return [
    textColumn("topic", "groups.colTopic", { flex: 1.4, cellClass: "mono-s" }),
    numberColumn("partition", "groups.colPartition", { maxWidth: 90 }),
    numberColumn("startOffset", "groups.colStart", { maxWidth: 110 }),
    numberColumn("endOffset", "groups.colEnd", { maxWidth: 110 }),
    textColumn("committedText", "groups.colCommitted", { maxWidth: 130, cellClass: "mono-s" }),
    numberColumn("lag", "groups.colLag", { maxWidth: 110 }),
  ];
}

export function memberColumns(): ColDef<MemberVm>[] {
  return [
    textColumn("memberId", "groups.memberId", { flex: 1.2, cellClass: "mono-s" }),
    textColumn("instanceId", "groups.instanceId", { cellClass: "mono-s" }),
    textColumn("clientId", "groups.clientId", { cellClass: "mono-s" }),
    textColumn("clientHost", "groups.clientHost", { cellClass: "mono-s" }),
    textColumn("assignments", "groups.assignments", { flex: 1.4, cellClass: "mono-s" }),
  ];
}

export function aclColumns(): ColDef<AclVm>[] {
  return [
    textColumn("resourceType", "acls.resourceType", { maxWidth: 130, cellClass: "mono-s" }),
    textColumn("resourceName", "acls.resourceName", { flex: 1.2, cellClass: "mono-s" }),
    textColumn("patternType", "acls.patternType", { maxWidth: 110, cellClass: "mono-s" }),
    textColumn("principal", "acls.principal", { flex: 1, cellClass: "mono-s" }),
    textColumn("host", "acls.host", { maxWidth: 110, cellClass: "mono-s" }),
    textColumn("operation", "acls.operation", { maxWidth: 130, cellClass: "mono-s" }),
    textColumn("permission", "acls.permission", { maxWidth: 110, cellClass: "mono-s" }),
  ];
}

export function topicColumns(): ColDef<TopicVm>[] {
  return [
    textColumn("name", "topics.colTopic", { flex: 2, cellClass: "mono-s" }),
    textColumn("internalText", "topics.colInternal", { maxWidth: 90, filter: false }),
    numberColumn("partitionCount", "topics.colPartitions", { maxWidth: 100 }),
    numberColumn("replicationFactor", "topics.colReplication", { maxWidth: 80 }),
  ];
}

export function partitionColumns(): ColDef<PartitionVm>[] {
  return [
    numberColumn("partition", "topics.colPartition", { maxWidth: 90 }),
    numberColumn("leader", "topics.colLeader", { maxWidth: 90 }),
    textColumn("replicas", "topics.colReplicas", { maxWidth: 120, cellClass: "mono-s" }),
    textColumn("isr", "topics.colIsr", { maxWidth: 120, cellClass: "mono-s" }),
    textColumn("offline", "topics.colOffline", { maxWidth: 110, cellClass: "mono-s" }),
    {
      field: "healthyText",
      headerName: t("topics.colHealthy"),
      sortable: true,
      resizable: true,
      filter: false,
      maxWidth: 110,
      cellClass: (params) => (params.data?.healthy ? "dbx-cell-ok" : "dbx-cell-bad"),
    },
  ];
}

export function topicOffsetColumns(): ColDef<TopicOffsetVm>[] {
  return [
    numberColumn("partition", "topics.colPartition", { maxWidth: 90 }),
    numberColumn("offset", "topics.colOffset", { maxWidth: 130 }),
    textColumn("timestampText", "messages.colTimestamp", { minWidth: 150, cellClass: "mono-s" }),
    textColumn("leaderEpoch", "Epoch", { maxWidth: 90, cellClass: "mono-s", headerName: "Epoch" }),
  ];
}

export function subjectColumns(): ColDef<SubjectVm>[] {
  return [
    textColumn("subject", "schemas.colSubject", { flex: 2, cellClass: "mono-s" }),
    textColumn("formats", "schemas.colFormats", { maxWidth: 120, cellClass: "mono-s" }),
    numberColumn("latestVersion", "schemas.colLatestVersion", { maxWidth: 110 }),
    textColumn("compatibilityLevel", "schemas.colCompatibility", { maxWidth: 170, cellClass: "mono-s" }),
  ];
}

export function schemaVersionColumns(): ColDef<SchemaVersionVm>[] {
  return [
    numberColumn("version", "schemas.colVersion", { maxWidth: 100 }),
    numberColumn("id", "schemas.colId", { maxWidth: 110 }),
    textColumn("format", "schemas.colFormat", { maxWidth: 100, cellClass: "mono-s" }),
  ];
}

export function lagColumns(): ColDef<LagVm>[] {
  return [
    textColumn("topic", "groups.colTopic", { flex: 1.4, cellClass: "mono-s" }),
    numberColumn("partition", "groups.colPartition", { maxWidth: 90 }),
    textColumn("committedText", "monitor.colCommitted", { maxWidth: 130, cellClass: "mono-s" }),
    numberColumn("endOffset", "monitor.colEnd", { maxWidth: 120 }),
    numberColumn("lag", "groups.colLag", { maxWidth: 110 }),
  ];
}

// -- narrow-container degradation ---------------------------------------------------

/**
 * Keep only the columns whose `field` is listed (used below the responsive
 * threshold). Pure so panels can unit-test their minimal sets.
 */
export function minimalColumns<T>(defs: ColDef<T>[], fields: string[]): ColDef<T>[] {
  const wanted = new Set(fields);
  return defs.filter((def) => typeof def.field === "string" && wanted.has(def.field));
}

/** 窄容器阈值（px）：低于此宽度表格降级到 minimal 列集。 */
export const GRID_COMPACT_WIDTH = 560;

export const MINIMAL_MESSAGE_FIELDS = ["partition", "offset", "valueText"];
export const MINIMAL_GROUP_FIELDS = ["group", "state"];
export const MINIMAL_GROUP_OFFSET_FIELDS = ["topic", "partition", "lag"];
export const MINIMAL_MEMBER_FIELDS = ["memberId", "assignments"];
export const MINIMAL_ACL_FIELDS = ["resourceType", "resourceName", "principal"];
export const MINIMAL_TOPIC_FIELDS = ["name", "partitionCount"];
export const MINIMAL_PARTITION_FIELDS = ["partition", "leader", "healthyText"];
export const MINIMAL_TOPIC_OFFSET_FIELDS = ["partition", "offset"];
export const MINIMAL_SUBJECT_FIELDS = ["subject", "latestVersion"];
export const MINIMAL_VERSION_FIELDS = ["version", "id"];
export const MINIMAL_LAG_FIELDS = ["topic", "partition", "lag"];

// -- page size persistence -----------------------------------------------------------

const PAGE_SIZE_STORAGE_PREFIX = "dbx-kafka-grid-pagesize-";
export const PAGE_SIZE_OPTIONS = [20, 50, 100, 200];
export const DEFAULT_PAGE_SIZE = 50;

function storage(): Storage | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null; // 宿主 webview 禁用 localStorage 时的静默兜底
  }
}

export function loadPreferredPageSize(tableKey: string): number {
  const raw = storage()?.getItem(PAGE_SIZE_STORAGE_PREFIX + tableKey) ?? "";
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_PAGE_SIZE;
}

export function savePreferredPageSize(tableKey: string, size: number): void {
  const store = storage();
  if (!store) return;
  try {
    store.setItem(PAGE_SIZE_STORAGE_PREFIX + tableKey, String(size));
  } catch {
    // quota/private mode → 分页偏好放弃持久化即可
  }
}

// -- ag-grid built-in chrome locale（七语；键集对齐 AG_GRID_LOCALE_KEYS 守卫） --------

export const AG_GRID_LOCALE_KEYS = [
  "searchOoo",
  "blanks",
  "noRowsToShow",
  "page",
  "pageSizeSelectorLabel",
  "filterOoo",
  "equals",
  "notEqual",
  "contains",
  "notContains",
  "startsWith",
  "endsWith",
  "greaterThan",
  "lessThan",
  "inRange",
  "andCondition",
  "orCondition",
  "applyFilter",
  "resetFilter",
  "cancelFilter",
] as const;

type AgLocaleText = Record<(typeof AG_GRID_LOCALE_KEYS)[number], string>;

const AG_LOCALE_TEXT: Record<string, AgLocaleText> = {
  en: {
    searchOoo: "Search…",
    blanks: "(Blanks)",
    noRowsToShow: "No rows",
    page: "Page",
    pageSizeSelectorLabel: "Page size:",
    filterOoo: "Filter…",
    equals: "Equals",
    notEqual: "Not equal",
    contains: "Contains",
    notContains: "Not contains",
    startsWith: "Starts with",
    endsWith: "Ends with",
    greaterThan: "Greater than",
    lessThan: "Less than",
    inRange: "In range",
    andCondition: "AND",
    orCondition: "OR",
    applyFilter: "Apply",
    resetFilter: "Reset",
    cancelFilter: "Cancel",
  },
  "zh-CN": {
    searchOoo: "搜索…",
    blanks: "（空）",
    noRowsToShow: "暂无数据",
    page: "页",
    pageSizeSelectorLabel: "每页条数：",
    filterOoo: "过滤…",
    equals: "等于",
    notEqual: "不等于",
    contains: "包含",
    notContains: "不包含",
    startsWith: "开头为",
    endsWith: "结尾为",
    greaterThan: "大于",
    lessThan: "小于",
    inRange: "介于",
    andCondition: "且",
    orCondition: "或",
    applyFilter: "应用",
    resetFilter: "重置",
    cancelFilter: "取消",
  },
  "zh-TW": {
    searchOoo: "搜尋…",
    blanks: "（空）",
    noRowsToShow: "尚無資料",
    page: "頁",
    pageSizeSelectorLabel: "每頁筆數：",
    filterOoo: "過濾…",
    equals: "等於",
    notEqual: "不等於",
    contains: "包含",
    notContains: "不包含",
    startsWith: "開頭為",
    endsWith: "結尾為",
    greaterThan: "大於",
    lessThan: "小於",
    inRange: "介於",
    andCondition: "且",
    orCondition: "或",
    applyFilter: "套用",
    resetFilter: "重設",
    cancelFilter: "取消",
  },
  es: {
    searchOoo: "Buscar…",
    blanks: "(Vacíos)",
    noRowsToShow: "Sin filas",
    page: "Página",
    pageSizeSelectorLabel: "Tamaño de página:",
    filterOoo: "Filtrar…",
    equals: "Igual a",
    notEqual: "Distinto de",
    contains: "Contiene",
    notContains: "No contiene",
    startsWith: "Empieza por",
    endsWith: "Termina en",
    greaterThan: "Mayor que",
    lessThan: "Menor que",
    inRange: "Entre",
    andCondition: "Y",
    orCondition: "O",
    applyFilter: "Aplicar",
    resetFilter: "Restablecer",
    cancelFilter: "Cancelar",
  },
  it: {
    searchOoo: "Cerca…",
    blanks: "(Vuote)",
    noRowsToShow: "Nessuna riga",
    page: "Pagina",
    pageSizeSelectorLabel: "Dimensione pagina:",
    filterOoo: "Filtra…",
    equals: "Uguale a",
    notEqual: "Diverso da",
    contains: "Contiene",
    notContains: "Non contiene",
    startsWith: "Inizia con",
    endsWith: "Termina con",
    greaterThan: "Maggiore di",
    lessThan: "Minore di",
    inRange: "Nell'intervallo",
    andCondition: "E",
    orCondition: "O",
    applyFilter: "Applica",
    resetFilter: "Reimposta",
    cancelFilter: "Annulla",
  },
  ja: {
    searchOoo: "検索…",
    blanks: "（空）",
    noRowsToShow: "データがありません",
    page: "ページ",
    pageSizeSelectorLabel: "ページサイズ：",
    filterOoo: "フィルター…",
    equals: "一致",
    notEqual: "不一致",
    contains: "含む",
    notContains: "含まない",
    startsWith: "前方一致",
    endsWith: "後方一致",
    greaterThan: "より大きい",
    lessThan: "より小さい",
    inRange: "範囲内",
    andCondition: "かつ",
    orCondition: "または",
    applyFilter: "適用",
    resetFilter: "リセット",
    cancelFilter: "キャンセル",
  },
  "pt-BR": {
    searchOoo: "Pesquisar…",
    blanks: "(Vazios)",
    noRowsToShow: "Sem linhas",
    page: "Página",
    pageSizeSelectorLabel: "Tamanho da página:",
    filterOoo: "Filtrar…",
    equals: "Igual a",
    notEqual: "Diferente de",
    contains: "Contém",
    notContains: "Não contém",
    startsWith: "Começa com",
    endsWith: "Termina com",
    greaterThan: "Maior que",
    lessThan: "Menor que",
    inRange: "No intervalo",
    andCondition: "E",
    orCondition: "OU",
    applyFilter: "Aplicar",
    resetFilter: "Redefinir",
    cancelFilter: "Cancelar",
  },
};

/** 当前工作台 locale 对应的 ag-grid 内置文案（组件每次建表时读取，随 locale 切换重建）。 */
export function agGridLocaleText(): AgLocaleText {
  return AG_LOCALE_TEXT[workbenchLocale.value] ?? AG_LOCALE_TEXT["zh-CN"];
}
