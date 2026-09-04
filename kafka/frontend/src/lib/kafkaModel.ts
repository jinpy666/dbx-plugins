/**
 * Kafka workbench pure functions: message value decode/format pipeline,
 * topic business ranking, group lag aggregation, JSON/CSV export
 * serialization and Confluent properties parsing. No sidecar calls, no
 * framework imports — everything here is unit-testable in isolation.
 * 对标 tinyrdm ConvertValue/kafkaNormalize，重写为无重依赖版本
 * （GZip 用浏览器 DecompressionStream；lz4/zstd/snappy 标注降级）。
 */
import type { KafkaMessage, MatchMode } from "./api";

// -- byte helpers -------------------------------------------------------------

export function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value.replace(/-/g, "+").replace(/_/g, "/"));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

export function bytesToHex(bytes: Uint8Array): string {
  let text = "";
  for (const byte of bytes) text += byte.toString(16).padStart(2, "0");
  return text;
}

export function bytesToUtf8(bytes: Uint8Array): string {
  // fatal:false → 非法字节替换为 U+FFFD（valueText 预览语义一致）。
  return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
}

// GZip 解压：浏览器 DecompressionStream（Node 18+ 同名全局，测试可用）。
// 缺失或非 gzip 算法时返回带 error 的降级结果，不抛异常。
export function isGzipSupported(): boolean {
  return typeof DecompressionStream !== "undefined";
}

export async function inflateGzip(bytes: Uint8Array): Promise<{ bytes: Uint8Array; error?: string }> {
  if (!isGzipSupported()) return { bytes, error: "gzip: DecompressionStream unsupported" };
  try {
    const stream = new Blob([bytes as BlobPart]).stream().pipeThrough(new DecompressionStream("gzip"));
    const buffer = await new Response(stream).arrayBuffer();
    return { bytes: new Uint8Array(buffer) };
  } catch (cause) {
    return { bytes, error: `gzip: ${cause instanceof Error ? cause.message : String(cause)}` };
  }
}

// -- value format pipeline ------------------------------------------------------

export type ValueFormat = "raw" | "json" | "hex" | "bitset";
export type DecodeMode = "none" | "base64";
export type Decompression = "none" | "gzip" | "lz4" | "zstd" | "snappy";

export interface ValueFormatOptions {
  decode: DecodeMode;
  decompression: Decompression;
  format: ValueFormat;
}

export interface DecodedValue {
  text: string;
  error?: string;
}

// JSON pretty：可解析才格式化，否则原样返回（不视为错误，raw 兜底）。
export function prettyJson(text: string): string {
  try {
    const parsed = JSON.parse(text);
    return JSON.stringify(parsed, null, 2);
  } catch {
    return text;
  }
}

export function looksLikeJson(text: string): boolean {
  const trimmed = text.trim();
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return false;
  try {
    JSON.parse(trimmed);
    return true;
  } catch {
    return false;
  }
}

// BitSet 展示：把整数字符串（十进制/0x 十六进制/二进制字面量）转成从
// 高位到低位的 0/1 串，按 8 位分组（tinyrdm BitSet 语义的纯函数版）。
export function formatBitSet(text: string): string | null {
  const trimmed = text.trim();
  if (!/^(0x[0-9a-f]+|0b[01]+|\d+)$/i.test(trimmed)) return null;
  let value = BigInt(trimmed);
  if (value === 0n) return "0";
  const bits: string[] = [];
  while (value > 0n) {
    bits.unshift(value & 1n ? "1" : "0");
    value >>= 1n;
  }
  return bits.join("").replace(/\B(?=(\d{4})+(?!\d))/g, " ");
}

const UNSUPPORTED_DECOMPRESSION: Record<string, string> = {
  lz4: "lz4 decompression is not available in the web UI",
  zstd: "zstd decompression is not available in the web UI",
  snappy: "snappy decompression is not available in the web UI",
};

/**
 * 消息 value 二次解码/格式化（详情抽屉主流程）：
 * valueBase64（保真字节）→ [可选 base64 再解] → [可选 gzip 解压] → 格式化。
 * 返回 { text, error? }；error 表示管线中不可恢复的一步（展示而非抛出）。
 */
export async function formatMessageValue(message: KafkaMessage, options: ValueFormatOptions): Promise<DecodedValue> {
  const encoded = message.valueBase64 ?? "";
  let bytes: Uint8Array;
  try {
    bytes = base64ToBytes(encoded);
  } catch {
    return { text: message.valueText ?? "", error: "invalid value base64" };
  }
  let error: string | undefined;
  if (options.decode === "base64") {
    const inner = bytesToUtf8(bytes).trim();
    try {
      bytes = base64ToBytes(inner);
    } catch {
      error = "invalid inner base64 (decode=base64)";
    }
  }
  if (options.decompression !== "none" && !error) {
    const algorithm = options.decompression;
    if (algorithm === "gzip") {
      const inflated = await inflateGzip(bytes);
      bytes = inflated.bytes;
      error = inflated.error;
    } else {
      error = UNSUPPORTED_DECOMPRESSION[algorithm];
    }
  }
  const text = bytesToUtf8(bytes);
  switch (options.format) {
    case "json":
      return { text: prettyJson(text), error };
    case "hex":
      return { text: bytesToHex(bytes).replace(/(..)(?=.)/g, "$1 "), error };
    case "bitset": {
      const bitSet = formatBitSet(text);
      return bitSet === null ? { text, error: error ?? "value is not an integer (bitset)" } : { text: bitSet, error };
    }
    default:
      return { text, error };
  }
}

// 下载用完整 value（始终 base64 → bytes → UTF-8 保真文本，不受预览替换影响）。
export function messageFullValueText(message: KafkaMessage): string {
  if (!message.valueBase64) return message.valueText ?? "";
  try {
    return bytesToUtf8(base64ToBytes(message.valueBase64));
  } catch {
    return message.valueText ?? "";
  }
}

// -- topic ranking（对标 tinyrdm kafkaNormalize.scoreBusinessTopicName）---------

const BUSINESS_TOKENS = new Set([
  "account", "activity", "audit", "cart", "customer", "email", "event", "events", "invoice", "item",
  "message", "messages", "notification", "order", "orders", "payment", "product", "profile", "session",
  "transaction", "user", "users",
]);
const INFRA_TOKENS = new Set([
  "changelog", "command", "config", "connect", "dlq", "heartbeat", "offset", "offsets", "repartition", "retry",
]);

export function scoreTopicName(name: string): number {
  const normalized = String(name || "").toLowerCase();
  const tokens = normalized.split(/[._-]+/).filter(Boolean);
  const business = tokens.reduce((acc, token) => acc + (BUSINESS_TOKENS.has(token) ? 24 : 0), 0);
  const infra = tokens.reduce((acc, token) => acc + (INFRA_TOKENS.has(token) ? 18 : 0), 0);
  const structure = tokens.length > 1 ? 8 : 0;
  const readable = normalized.length >= 6 && /[a-z]/.test(normalized) ? 4 : 0;
  return business + structure + readable - infra;
}

export function isInternalTopicName(name: string): boolean {
  return String(name || "").startsWith("_");
}

export interface TopicSortItem {
  name: string;
  isInternal?: boolean;
}

/**
 * topic 排序：internal 沉底（_ 开头或后端标记），业务 topic 按业务评分
 * 降序、同分按名称字典序。返回新数组，不改入参。
 */
export function sortTopics<T extends TopicSortItem>(topics: T[]): T[] {
  return [...topics].sort((left, right) => {
    const leftInternal = left.isInternal === true || isInternalTopicName(left.name);
    const rightInternal = right.isInternal === true || isInternalTopicName(right.name);
    if (leftInternal !== rightInternal) return leftInternal ? 1 : -1;
    const scoreDelta = scoreTopicName(right.name) - scoreTopicName(left.name);
    if (scoreDelta !== 0) return scoreDelta;
    return left.name.localeCompare(right.name);
  });
}

export function filterTopics<T extends TopicSortItem>(topics: T[], keyword: string): T[] {
  const needle = keyword.trim().toLowerCase();
  if (!needle) return topics;
  return topics.filter((topic) => topic.name.toLowerCase().includes(needle));
}

// -- lag aggregation ------------------------------------------------------------

export interface LagRow {
  lag?: number | null;
}

/** 分区 lag 求和；缺失/负值按 0 计（Option 语义的展示兜底在后端）。 */
export function sumLag(rows: LagRow[]): number {
  return rows.reduce((acc, row) => acc + Math.max(0, Number(row.lag ?? 0) || 0), 0);
}

// -- export serialization --------------------------------------------------------

function csvEscape(value: string): string {
  if (/[",\n\r]/.test(value)) return `"${value.replace(/"/g, '""')}"`;
  return value;
}

const CSV_COLUMNS = ["topic", "partition", "offset", "timestamp", "key", "value", "headers"] as const;

/** 消息数组 → CSV 文本（RFC 4180 转义；headers 序列化为 k=v;… JSON 兜底）。 */
export function serializeMessagesToCsv(messages: KafkaMessage[]): string {
  const lines = [CSV_COLUMNS.join(",")];
  for (const message of messages) {
    const headers = message.headers && Object.keys(message.headers).length > 0
      ? Object.entries(message.headers)
          .map(([key, value]) => `${key}=${value}`)
          .join("; ")
      : "";
    lines.push(
      [
        message.topic,
        String(message.partition),
        String(message.offset),
        String(message.timestamp),
        message.key ?? "",
        messageFullValueText(message),
        headers,
      ]
        .map(csvEscape)
        .join(","),
    );
  }
  return lines.join("\r\n");
}

/** 消息数组 → JSON 文本（稳定键序，value 用保真文本）。 */
export function serializeMessagesToJson(messages: KafkaMessage[]): string {
  const payload = messages.map((message) => ({
    topic: message.topic,
    partition: message.partition,
    offset: message.offset,
    timestamp: message.timestamp,
    ...(message.leaderEpoch !== undefined ? { leaderEpoch: message.leaderEpoch } : {}),
    ...(message.key !== undefined ? { key: message.key } : {}),
    value: messageFullValueText(message),
    headers: message.headers ?? {},
    ...(message.committed !== undefined ? { committed: message.committed } : {}),
    ...(message.truncated ? { truncated: true } : {}),
    ...(message.decodeError ? { decodeError: message.decodeError } : {}),
  }));
  return JSON.stringify(payload, null, 2);
}

// -- consume form helpers ---------------------------------------------------------

/** "0,1,2" / "0 2"（含全角逗号）→ 去重分区号列表；非法片段忽略。 */
export function parsePartitionList(text: string): number[] {
  const seen = new Set<number>();
  for (const part of text.split(/[,，\s]+/).filter(Boolean)) {
    const value = Number.parseInt(part, 10);
    if (Number.isInteger(value) && value >= 0) seen.add(value);
  }
  return [...seen].sort((left, right) => left - right);
}

/** "0=100,1:200"（= 或 :，逗号/换行分隔）→ {partition:offset}；非法片段忽略。 */
export function parsePartitionOffsetsText(text: string): Record<string, number> {
  const result: Record<string, number> = {};
  for (const line of text.split(/[,，\n]+/).map((entry) => entry.trim()).filter(Boolean)) {
    const match = line.match(/^(\d+)\s*[=:]\s*(\d+)$/);
    if (match) result[match[1]] = Number.parseInt(match[2], 10);
  }
  return result;
}

export function partitionOffsetsToText(offsets: Record<string, number>): string {
  return Object.keys(offsets)
    .sort((left, right) => Number(left) - Number(right))
    .map((partition) => `${partition}=${offsets[partition]}`)
    .join(",");
}

/** datetime-local（或留空）→ RFC3339（本地时区偏移）；unix ms 数字原样透传。 */
export function offsetTimeToParam(text: string): string | number | null {
  const trimmed = text.trim();
  if (!trimmed) return null;
  if (/^\d{10,}$/.test(trimmed)) return Number(trimmed);
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(trimmed)) {
    const withSeconds = trimmed.length === 16 ? `${trimmed}:00` : trimmed;
    const parsed = new Date(withSeconds);
    if (Number.isNaN(parsed.getTime())) return null;
    return parsed.toISOString();
  }
  // 已是 RFC3339 / 其他可解析时间串 → 校验后透传。
  const parsed = new Date(trimmed);
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString();
}

export interface ConsumeFormValidationIssue {
  field: "commit" | "strategy" | "partitions";
  key: string; // i18n key 后缀（messages.errXxx）
}

/** consume 表单互斥/必填校验（§5.3：commit×过滤互斥、partitions×groupId 互斥、
 *  strategy=offset 必填 partitionOffsets、strategy=timestamp 必填 offsetTime）。 */
export function validateConsumeForm(form: {
  commit: boolean;
  groupId: string;
  partitionsText: string;
  offsetStrategy: string;
  offsetTimeText: string;
  partitionOffsetsText: string;
  hasFilters: boolean;
}): ConsumeFormValidationIssue[] {
  const issues: ConsumeFormValidationIssue[] = [];
  if (form.commit && form.hasFilters) {
    issues.push({ field: "commit", key: "commitFilterConflict" });
  }
  if (form.commit && !form.groupId.trim()) {
    issues.push({ field: "commit", key: "commitNeedsGroup" });
  }
  const partitions = parsePartitionList(form.partitionsText);
  if (partitions.length > 0 && form.groupId.trim()) {
    issues.push({ field: "partitions", key: "partitionsGroupConflict" });
  }
  if (form.offsetStrategy === "timestamp" && !offsetTimeToParam(form.offsetTimeText)) {
    issues.push({ field: "strategy", key: "timestampRequired" });
  }
  if (form.offsetStrategy === "offset") {
    const offsets = parsePartitionOffsetsText(form.partitionOffsetsText);
    if (Object.keys(offsets).length === 0) issues.push({ field: "strategy", key: "offsetsRequired" });
  }
  return issues;
}

// -- matchMode 本地预览（后端为准，前端仅做详情过滤提示/导出前二次确认用）-----

export function matchText(candidate: string | undefined, pattern: string, mode: MatchMode): boolean {
  const haystack = candidate ?? "";
  switch (mode) {
    case "prefix":
      return haystack.startsWith(pattern);
    case "exact":
      return haystack === pattern;
    case "regex":
      try {
        return new RegExp(pattern).test(haystack);
      } catch {
        return false;
      }
    default:
      return haystack.includes(pattern);
  }
}

// -- headers JSON / Confluent properties ------------------------------------------

/** headers 编辑框 JSON 对象校验（值必须是 string；其余键值报错）。 */
export function parseHeadersJson(text: string): { headers: Record<string, string> } | { error: string } {
  const trimmed = text.trim();
  if (!trimmed) return { headers: {} };
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch (cause) {
    return { error: cause instanceof Error ? cause.message : String(cause) };
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { error: "headers must be a JSON object" };
  }
  const headers: Record<string, string> = {};
  for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
    if (typeof value !== "string") return { error: `header "${key}" must be a string` };
    headers[key] = value;
  }
  return { headers };
}

export interface ConfluentProperties {
  [key: string]: string;
}

/**
 * Confluent properties 文本解析（`key=value` 行，# / ! 注释，`\` 续行，
 * 值内 `\:=` 等反斜杠转义）。纯解析，不做语义校验。
 */
export function parsePropertiesText(text: string): ConfluentProperties {
  const result: ConfluentProperties = {};
  // 续行：行尾奇数个反斜杠才生效（偶数个是转义的字面反斜杠）。
  const logical: string[] = [];
  let pending = "";
  for (const rawLine of text.split(/\r?\n/)) {
    const line = pending + rawLine;
    const trailing = /\\+$/.exec(line)?.[0].length ?? 0;
    if (trailing % 2 === 1) {
      pending = line.slice(0, -1);
      continue;
    }
    pending = "";
    logical.push(line);
  }
  if (pending) logical.push(pending);
  for (const line of logical) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("!")) continue;
    // 第一个未被转义的 = 或 : 为分隔符（\ 后跳过一个字符）。
    let separatorIndex = -1;
    for (let index = 0; index < trimmed.length; index += 1) {
      const character = trimmed[index];
      if (character === "\\") {
        index += 1;
        continue;
      }
      if (character === "=" || character === ":") {
        separatorIndex = index;
        break;
      }
    }
    if (separatorIndex <= 0) continue;
    const key = unescapeProperties(trimmed.slice(0, separatorIndex)).trim();
    const value = unescapeProperties(trimmed.slice(separatorIndex + 1).replace(/^[ \t]+/, "")).trimEnd();
    if (key) result[key] = value;
  }
  return result;
}

function unescapeProperties(input: string): string {
  let output = "";
  for (let index = 0; index < input.length; index += 1) {
    const character = input[index];
    if (character === "\\" && index + 1 < input.length) {
      output += input[index + 1];
      index += 1;
    } else {
      output += character;
    }
  }
  return output;
}

export interface ConnectionFormHints {
  bootstrapServers: string;
  securityProtocol: string;
  saslMechanism: string;
  saslUsername: string;
  saslPassword: string;
  tlsInsecureSkipVerify: boolean;
}

/**
 * Confluent properties → manifest 连接表单字段映射（§4）：
 * bootstrap.servers / security.protocol / sasl.mechanism /
 * sasl.jaas.config（提取 username/password）/ ssl.endpoint.identification.algorithm。
 * 跳过校验仅在 key 显式置空或 none 时为 true；key 缺省保持宿主默认校验。
 */
export function propertiesToConnectionForm(properties: ConfluentProperties): ConnectionFormHints {
  const bootstrap = properties["bootstrap.servers"] ?? "";
  const securityProtocol = (properties["security.protocol"] ?? "").toUpperCase();
  const saslMechanism = (properties["sasl.mechanism"] ?? "").toUpperCase();
  const jaas = properties["sasl.jaas.config"] ?? "";
  const username = jaas.match(/(?:^|\s)username\s*=\s*"([^"]*)"/)?.[1] ?? "";
  const password = jaas.match(/(?:^|\s)password\s*=\s*"([^"]*)"/)?.[1] ?? "";
  const endpointAlgorithm = properties["ssl.endpoint.identification.algorithm"];
  const skipVerify = endpointAlgorithm !== undefined && /^(|none)$/i.test(endpointAlgorithm.trim());
  return {
    bootstrapServers: bootstrap,
    securityProtocol,
    saslMechanism,
    saslUsername: username,
    saslPassword: password,
    tlsInsecureSkipVerify: skipVerify,
  };
}

// -- display helpers ---------------------------------------------------------------

export function formatTimestamp(ms: number | undefined): string {
  if (!ms || !Number.isFinite(ms)) return "—";
  const date = new Date(ms);
  if (Number.isNaN(date.getTime())) return String(ms);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

/** 消息表格 value 预览：单行化 + 截断（详情抽屉看全文）。 */
export function previewText(text: string | undefined, maxLength = 120): string {
  const singleLine = (text ?? "").replace(/\s+/g, " ").trim();
  return singleLine.length > maxLength ? `${singleLine.slice(0, maxLength)}…` : singleLine;
}

// -- stream buffer ----------------------------------------------------------------

/** 流式面板展示上限（超出丢最旧并累计 dropped，防长会话 OOM）。 */
export const STREAM_ROWS_MAX = 1000;

/** 追加流式消息行并按上限裁剪；返回新数组与累计丢弃数（纯函数）。 */
export function appendStreamRows(
  current: KafkaMessage[],
  incoming: KafkaMessage[],
  max = STREAM_ROWS_MAX,
  droppedSoFar = 0,
): { rows: KafkaMessage[]; dropped: number } {
  const combined = [...current, ...incoming];
  if (combined.length <= max) return { rows: combined, dropped: droppedSoFar };
  const overflow = combined.length - max;
  return { rows: combined.slice(overflow), dropped: droppedSoFar + overflow };
}

/** 表格 headers 摘要：k=v, k2=v2（最多 2 个）。 */
export function headersPreview(headers: Record<string, string> | undefined): string {
  if (!headers) return "";
  const entries = Object.entries(headers);
  const head = entries.slice(0, 2).map(([key, value]) => `${key}=${value}`).join(", ");
  return entries.length > 2 ? `${head}, …` : head;
}
