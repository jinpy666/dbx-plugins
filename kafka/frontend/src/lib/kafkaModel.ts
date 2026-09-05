/**
 * Kafka workbench pure functions: message value decode/format pipeline,
 * topic business ranking, group lag aggregation, JSON/CSV export
 * serialization and Confluent properties parsing. No sidecar calls, no
 * framework imports — everything here is unit-testable in isolation.
 * 对标 tinyrdm ConvertValue/kafkaNormalize，重写为纯函数管线。解压支持
 * gzip/lz4/zstd/snappy 全四种：gzip 用浏览器 DecompressionStream，
 * zstd= fzstd、snappy = snappyjs、lz4 = lz4js（轻量纯 JS、MIT/ISC，
 * Phase P 引入补齐 Phase 1 的降级标注）。
 */
import { decompress as fzstdDecompress } from "fzstd";
import { decompress as lz4Decompress } from "lz4js";
import { uncompress as snappyUncompress } from "snappyjs";
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

const DECOMPRESSION_LABELS: Record<string, string> = { gzip: "gzip", lz4: "lz4", zstd: "zstd", snappy: "snappy" };

function decompressError(algorithm: string, cause: unknown): string {
  return `${DECOMPRESSION_LABELS[algorithm] ?? algorithm}: ${cause instanceof Error ? cause.message : String(cause)}`;
}

// zstd 解压：fzstd（纯 JS、MIT；仅解码，与生产端 zstd 帧格式兼容）。
export function inflateZstd(bytes: Uint8Array): { bytes: Uint8Array; error?: string } {
  try {
    return { bytes: fzstdDecompress(bytes) };
  } catch (cause) {
    return { bytes, error: decompressError("zstd", cause) };
  }
}

// snappy 解压：snappyjs（纯 JS、MIT，含 Hadoop 变体外的标准 framing）。
export function inflateSnappy(bytes: Uint8Array): { bytes: Uint8Array; error?: string } {
  try {
    return { bytes: snappyUncompress(bytes) };
  } catch (cause) {
    return { bytes, error: decompressError("snappy", cause) };
  }
}

// lz4 解压：lz4js（纯 JS、ISC，frame 格式，与 Kafka lz4 块兼容）。
export function inflateLz4(bytes: Uint8Array): { bytes: Uint8Array; error?: string } {
  try {
    return { bytes: new Uint8Array(lz4Decompress(bytes)) };
  } catch (cause) {
    return { bytes, error: decompressError("lz4", cause) };
  }
}

/** 单步解压分发（算法不存在时返回原字节 + error，不抛异常）。 */
export function inflateDecompression(algorithm: Decompression, bytes: Uint8Array): { bytes: Uint8Array; error?: string } {
  switch (algorithm) {
    case "zstd":
      return inflateZstd(bytes);
    case "snappy":
      return inflateSnappy(bytes);
    case "lz4":
      return inflateLz4(bytes);
    default:
      return { bytes, error: `decompression "${algorithm}" is not supported` };
  }
}

/**
 * 消息 value 二次解码/格式化（详情抽屉主流程）：
 * valueBase64（保真字节）→ [可选 base64 再解] → [可选 gzip/lz4/zstd/snappy 解压]
 * → 格式化。返回 { text, error? }；error 表示管线中不可恢复的一步（展示而非抛出）。
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
    // gzip 走浏览器 DecompressionStream，其余走纯 JS 解压器（均为同步）。
    const inflated = algorithm === "gzip" ? await inflateGzip(bytes) : inflateDecompression(algorithm, bytes);
    bytes = inflated.bytes;
    error = inflated.error;
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

/** 组级 reset 位点目标：`topic:0=100`（显式 topic）或 `0=100`（套用唯一 topic）→ {topic:{partition:offset}}。 */
export function parseGroupOffsetTargetsText(
  text: string,
  topics: string[],
): { targets: Record<string, Record<string, number>>; invalid: string[] } {
  const targets: Record<string, Record<string, number>> = {};
  const invalid: string[] = [];
  const fallback = topics.length === 1 ? topics[0] : null;
  for (const line of text.split(/[,，\n]+/).map((entry) => entry.trim()).filter(Boolean)) {
    const explicit = line.match(/^(.+?)\s*[:：](\d+)\s*[=:]\s*(\d+)$/);
    const plain = line.match(/^(\d+)\s*[=:]\s*(\d+)$/);
    if (explicit) {
      const topic = explicit[1].trim();
      (targets[topic] ??= {})[explicit[2]] = Number.parseInt(explicit[3], 10);
    } else if (plain && fallback) {
      (targets[fallback] ??= {})[plain[1]] = Number.parseInt(plain[2], 10);
    } else {
      invalid.push(line);
    }
  }
  return { targets, invalid };
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

/**
 * 时间输入 → unix ms 数字（timestampFrom/To 提交用，ConsumeParams 为 number）：
 * 复用 offsetTimeToParam（datetime-local → RFC3339、unix ms 透传），再归一到 ms。
 * 留空或不可解析返回 null。
 */
export function offsetTimeToUnixMs(text: string): number | null {
  const param = offsetTimeToParam(text);
  if (param === null) return null;
  if (typeof param === "number") return param;
  const parsed = Date.parse(param);
  return Number.isNaN(parsed) ? null : parsed;
}

/** unix ms → 本地时区 datetime-local 字符串（秒级精度，`YYYY-MM-DDTHH:mm:ss`）。 */
export function unixMsToDatetimeLocal(ms: number): string {
  if (!Number.isFinite(ms)) return "";
  const date = new Date(ms);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

/** 当前时间 → datetime-local 字符串（「现在」按钮用）。 */
export function nowDatetimeLocal(): string {
  return unixMsToDatetimeLocal(Date.now());
}

/**
 * 时间输入模式切换（datetime-local ↔ unix ms 文本）：能解析就转换，
 * 解析不了原样保留（不打断用户正在输入的草稿）。
 */
export function switchTimeInputMode(text: string, toMode: "datetime" | "unix"): string {
  const trimmed = text.trim();
  if (!trimmed) return "";
  if (toMode === "unix") {
    const ms = offsetTimeToUnixMs(trimmed);
    return ms === null ? text : String(ms);
  }
  const ms = offsetTimeToUnixMs(trimmed);
  return ms === null ? text : unixMsToDatetimeLocal(ms);
}

/** 起止范围校验：两侧都有值且 from > to 时非法（等于合法，后端语义 from ≤ to）。 */
export function isRangeReversed(fromMs: number | null, toMs: number | null): boolean {
  return fromMs !== null && toMs !== null && fromMs > toMs;
}

/**
 * fieldFilters 行级校验：数值比较 operator（gt/gte/lt/lte）要求 value 可转数字。
 * 返回 i18n key 片段（messages.fieldValueNumeric）或 null（通过/不适用）。
 */
export function fieldFilterIssue(row: { operator: string; value: string }): string | null {
  if (!["gt", "gte", "lt", "lte"].includes(row.operator)) return null;
  const trimmed = row.value.trim();
  if (!trimmed) return null; // 空值行不参与载荷，交给「启用 + 有值」过滤
  return Number.isFinite(Number(trimmed)) ? null : "fieldValueNumeric";
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

/**
 * 定位 JSON 首个语法错误所在行（1-based）。合法 JSON、空/纯空白文本返回 null
 * （空态不算错——编辑器 linter 语义）。不解析 JSON.parse 的报错文案：V8 新版
 * 对不少输入不再携带 position、WebKit/Firefox 格式各异，改用内置最小 JSON
 * 扫描器，跨引擎（Electron/Chromium、CI Node）行为一致。
 */
export function jsonErrorLine(text: string): number | null {
  const src = String(text ?? "");
  if (!src.trim()) return null;
  const length = src.length;
  let pos = 0;
  let line = 1;

  function skipWs(): void {
    while (pos < length) {
      const ch = src[pos];
      if (ch === "\n") {
        pos += 1;
        line += 1;
      } else if (ch === " " || ch === "\t" || ch === "\r") {
        pos += 1;
      } else {
        break;
      }
    }
  }

  function matchKeyword(word: string): boolean {
    if (src.startsWith(word, pos)) {
      pos += word.length;
      return true;
    }
    return false;
  }

  function scanString(): boolean {
    pos += 1; // 开引号
    while (pos < length) {
      const ch = src[pos];
      if (ch === '"') {
        pos += 1;
        return true;
      }
      if (ch === "\\") {
        const esc = src[pos + 1];
        if (esc === "u") {
          if (!/^[0-9a-fA-F]{4}$/.test(src.slice(pos + 2, pos + 6))) return false;
          pos += 6;
          continue;
        }
        if (esc === undefined || !"\"\\/bfnrt".includes(esc)) return false;
        pos += 2;
        continue;
      }
      // JSON 字符串内不允许字面控制字符（含裸换行），在此处报错。
      if (ch < " ") return false;
      pos += 1;
    }
    return false; // 未闭合
  }

  function scanNumber(): boolean {
    if (src[pos] === "-") pos += 1;
    if (src[pos] === "0") {
      pos += 1;
    } else if (src[pos]! >= "1" && src[pos]! <= "9") {
      while (pos < length && src[pos] >= "0" && src[pos] <= "9") pos += 1;
    } else {
      return false;
    }
    if (src[pos] === ".") {
      pos += 1;
      if (!(pos < length && src[pos] >= "0" && src[pos] <= "9")) return false;
      while (pos < length && src[pos] >= "0" && src[pos] <= "9") pos += 1;
    }
    if (src[pos] === "e" || src[pos] === "E") {
      pos += 1;
      if (src[pos] === "+" || src[pos] === "-") pos += 1;
      if (!(pos < length && src[pos] >= "0" && src[pos] <= "9")) return false;
      while (pos < length && src[pos] >= "0" && src[pos] <= "9") pos += 1;
    }
    return true;
  }

  function scanObject(): boolean {
    pos += 1; // {
    skipWs();
    if (src[pos] === "}") {
      pos += 1;
      return true;
    }
    for (;;) {
      skipWs();
      if (src[pos] !== '"') return false;
      if (!scanString()) return false;
      skipWs();
      if (src[pos] !== ":") return false;
      pos += 1;
      if (!scanValue()) return false;
      skipWs();
      if (src[pos] === ",") {
        pos += 1;
        continue;
      }
      if (src[pos] === "}") {
        pos += 1;
        return true;
      }
      return false;
    }
  }

  function scanArray(): boolean {
    pos += 1; // [
    skipWs();
    if (src[pos] === "]") {
      pos += 1;
      return true;
    }
    for (;;) {
      if (!scanValue()) return false;
      skipWs();
      if (src[pos] === ",") {
        pos += 1;
        continue;
      }
      if (src[pos] === "]") {
        pos += 1;
        return true;
      }
      return false;
    }
  }

  function scanValue(): boolean {
    skipWs();
    if (pos >= length) return false;
    const ch = src[pos];
    if (ch === "{") return scanObject();
    if (ch === "[") return scanArray();
    if (ch === '"') return scanString();
    if (ch === "-" || (ch >= "0" && ch <= "9")) return scanNumber();
    return matchKeyword("true") || matchKeyword("false") || matchKeyword("null");
  }

  if (!scanValue()) return line;
  skipWs();
  return pos < length ? line : null; // 尾部还有非空白内容 = 多余 token
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

// -- message table cap（消息页大数据量防护）------------------------------------------

/**
 * 消息表内存行上限：一次性 consume 大批量结果只保留最新 N 条，超出裁掉头部
 * （较早的），防大表 ag-grid 全量行 + 深响应爆内存。StreamPanel 有独立的
 * STREAM_ROWS_MAX（流式环形缓冲），两者语义不同不共用。
 */
export const MESSAGE_ROWS_MAX = 10000;

/** 裁剪结果：rows 为保留的最新行、total 为裁剪前行数、dropped 为裁掉数量。 */
export interface CappedRows<T> {
  rows: T[];
  total: number;
  dropped: number;
}

/**
 * 行数据上限裁剪（纯函数）：rows 语义为时间正序（新消息在尾部），保留最新
 * max 条、裁掉头部。未超限返回原数组引用（零拷贝）；超限 slice 裁头部。
 */
export function capRows<T>(rows: T[], max: number = MESSAGE_ROWS_MAX): CappedRows<T> {
  if (rows.length <= max) return { rows, total: rows.length, dropped: 0 };
  const dropped = rows.length - max;
  return { rows: rows.slice(dropped), total: rows.length, dropped };
}

/** 详情抽屉 value 预览上限（字符）：大 value（如 512KB base64）不整段塞 DOM 文本节点。 */
export const DETAIL_VALUE_PREVIEW_MAX = 16384;

/**
 * 大 value 截断预览（纯函数）：保留头 80% + 尾 400 字符（头尾可判别格式/内容），
 * 中间以语言中性的省略标记连接（数字+chars）。未超限原样返回（零拷贝语义）。
 */
export function truncatedValuePreview(text: string, previewMax = DETAIL_VALUE_PREVIEW_MAX): { text: string; truncated: boolean } {
  if (text.length <= previewMax) return { text, truncated: false };
  const head = Math.max(previewMax - 400, 0);
  const hidden = text.length - head - 400;
  return { text: `${text.slice(0, head)}\n⋯ ${hidden} chars ⋯\n${text.slice(-400)}`, truncated: true };
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

// -- Confluent properties 导入助手（Phase 2，只读映射展示，不回填不持久化）---------

export const PROPERTY_MASKED_PLACEHOLDER = "••••••";

export interface PropertyMappingRow {
  /** properties 原始键（或带提取标注的子键）。 */
  property: string;
  /** 解析出的值；敏感值以掩码占位，不携带明文。 */
  value: string;
  masked: boolean;
  /** 对应宿主连接表单字段（manifest 连接字段名）。 */
  formField: string;
}

/**
 * Confluent properties → 只读键值映射（Phase 2 导入助手展示用）：
 * bootstrap.servers / security.protocol / sasl.mechanism（含 GSSAPI 提示）/
 * sasl.jaas.config（username/password/principal/keyTab 提取）/
 * schema.registry.url / schema.registry.basic.auth.user.info。
 * 敏感值（密码/secret）一律以掩码占位；纯函数，不触碰 localStorage。
 */
export function buildPropertyMappings(properties: ConfluentProperties): PropertyMappingRow[] {
  const rows: PropertyMappingRow[] = [];
  const push = (property: string, value: string, formField: string, masked = false) => {
    if (value) rows.push({ property, value, masked, formField });
  };
  push("bootstrap.servers", properties["bootstrap.servers"] ?? "", "bootstrap_servers");
  push("security.protocol", (properties["security.protocol"] ?? "").toUpperCase(), "security_protocol");
  const mechanism = (properties["sasl.mechanism"] ?? "").toUpperCase();
  if (mechanism) {
    push("sasl.mechanism", mechanism, mechanism === "GSSAPI" ? "sasl_mechanism (GSSAPI) + kerberos_*" : "sasl_mechanism");
  }
  const jaas = properties["sasl.jaas.config"] ?? "";
  if (jaas) {
    const username = jaas.match(/(?:^|\s)username\s*=\s*"([^"]*)"/)?.[1] ?? "";
    const password = jaas.match(/(?:^|\s)password\s*=\s*"([^"]*)"/)?.[1] ?? "";
    const principal = jaas.match(/(?:^|\s)principal\s*=\s*"([^"]*)"/)?.[1] ?? "";
    const keytab = jaas.match(/(?:^|\s)keyTab\s*=\s*"([^"]*)"/)?.[1] ?? "";
    if (username) push("sasl.jaas.config → username", username, "sasl_username");
    if (password) push("sasl.jaas.config → password", PROPERTY_MASKED_PLACEHOLDER, "sasl_password", true);
    if (principal) push("sasl.jaas.config → principal", principal, "kerberos_principal");
    if (keytab) push("sasl.jaas.config → keyTab", keytab, "kerberos_keytab_path");
  }
  push("sasl.kerberos.service.name", properties["sasl.kerberos.service.name"] ?? "", "kerberos_service_name");
  push("schema.registry.url", properties["schema.registry.url"] ?? "", "sr_url");
  const srAuth = properties["schema.registry.basic.auth.user.info"] ?? "";
  if (srAuth) {
    const separatorIndex = srAuth.indexOf(":");
    if (separatorIndex > 0) {
      push("schema.registry.basic.auth.user.info → user", srAuth.slice(0, separatorIndex), "sr_username");
      push("schema.registry.basic.auth.user.info → secret", PROPERTY_MASKED_PLACEHOLDER, "sr_password", true);
    }
  }
  return rows;
}

// -- 弹层交互纯逻辑（P1-2/P1-3：Esc 关闭 + Tab 焦点陷阱）------------------------
// DOM 接线在各弹层组件（消息详情抽屉 / 连接弹窗），本节只放可单测的决策逻辑。

/** 容器内可聚焦元素选择器（disabled / hidden input / tabindex=-1 除外）。 */
export const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not([disabled])",
  'input:not([disabled]):not([type="hidden"])',
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(", ");

/** 容器内文档顺序的可聚焦元素列表。 */
export function focusableElements(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
}

/** Tab 焦点陷阱回绕：无焦点/越界时按方向取首/尾，否则循环步进；空容器返回 -1。 */
export function nextFocusIndex(count: number, currentIndex: number, shift: boolean): number {
  if (count <= 0) return -1;
  if (currentIndex < 0 || currentIndex >= count) return shift ? count - 1 : 0;
  return (currentIndex + (shift ? -1 : 1) + count) % count;
}

/** 弹层 keydown 决策：Esc → close；Tab → focus 回绕目标下标；其余 → none。 */
export type ModalKeydownDecision = { kind: "none" } | { kind: "close" } | { kind: "focus"; index: number };

export function decideModalKeydown(
  key: string,
  shiftKey: boolean,
  focusableCount: number,
  currentIndex: number,
): ModalKeydownDecision {
  if (key === "Escape") return { kind: "close" };
  if (key !== "Tab" || focusableCount <= 0) return { kind: "none" };
  return { kind: "focus", index: nextFocusIndex(focusableCount, currentIndex, shiftKey) };
}
