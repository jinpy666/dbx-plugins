// kafkaModel 纯函数单测：解码/格式化管线、topic 排序、lag 聚合、CSV/JSON
// 导出、properties 解析与 consume 表单校验。不连网、不依赖组件；
// 不引 node 专有模块（zlib/Buffer）：gzip/zstd 用预生成 base64 常量，
// snappy/lz4 用各库自身 compress API 构造 roundtrip 样本。
import { describe, expect, it } from "vitest";
import { compress as lz4Compress } from "lz4js";
import { compress as snappyCompress } from "snappyjs";
import {
  appendStreamRows,
  base64ToBytes,
  bytesToHex,
  bytesToUtf8,
  capRows,
  decideModalKeydown,
  filterTopics,
  formatBitSet,
  formatMessageValue,
  headersPreview,
  isInternalTopicName,
  fieldFilterIssue,
  isRangeReversed,
  jsonErrorLine,
  matchText,
  messageFullValueText,
  nowDatetimeLocal,
  offsetTimeToParam,
  offsetTimeToUnixMs,
  parseHeadersJson,
  parsePartitionList,
  parseGroupOffsetTargetsText,
  parsePartitionOffsetsText,
  partitionOffsetsToText,
  parsePropertiesText,
  prettyJson,
  previewText,
  propertiesToConnectionForm,
  serializeMessagesToCsv,
  serializeMessagesToJson,
  sortTopics,
  scoreTopicName,
  sumLag,
  switchTimeInputMode,
  unixMsToDatetimeLocal,
  validateConsumeForm,
} from "./kafkaModel";
import type { KafkaMessage } from "./api";
import { MESSAGE_ROWS_MAX, truncatedValuePreview } from "./kafkaModel";

// gzipSync('{"n":7}') 的 base64 常量（生成命令见仓库 PROGRESS 文档）。
const GZIP_JSON_N7_BASE64 = "H4sIAAAAAAAAE6tWylOyMq8FAPicEYIHAAAA";
// `zstd -q`('{"orderId":"A-1001","amount":42,"currency":"USD"}') 的 base64 常量
// （fzstd 仅提供解码器，无法库内 roundtrip，故用 CLI 预生成固定向量）。
const ZSTD_ORDER_JSON_BASE64 =
  "KLUv/SQxiQEAeyJvcmRlcklkIjoiQS0xMDAxIiwiYW1vdW50Ijo0MiwiY3VycmVuY3kiOiJVU0QifbQxJQg=";

function b64(text: string): string {
  return utf8ToBase64(text);
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function utf8ToBase64(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function msg(overrides: Partial<KafkaMessage>): KafkaMessage {
  return { topic: "orders", partition: 0, offset: 1, timestamp: 1_700_000_000_000, ...overrides };
}

describe("byte helpers", () => {
  it("round-trips base64/utf8/hex", () => {
    expect(bytesToUtf8(base64ToBytes(b64("hello")))).toBe("hello");
    expect(b64("hello")).toBe("aGVsbG8=");
    expect(bytesToHex(base64ToBytes(b64("\n\x1f")))).toBe("0a1f");
  });

  it("replaces invalid utf-8 bytes instead of throwing", () => {
    const text = bytesToUtf8(new Uint8Array([0x61, 0xff, 0x62]));
    expect(text).toContain("a");
    expect(text).toContain("b");
    expect(text.length).toBe(3);
  });
});

describe("format pipeline", () => {
  it("pretty-prints JSON only when parseable", () => {
    expect(prettyJson('{"a":1}')).toBe('{\n  "a": 1\n}');
    expect(prettyJson("plain text")).toBe("plain text");
  });

  it("formats bitsets from decimal/hex/binary literals", () => {
    expect(formatBitSet("5")).toBe("101");
    expect(formatBitSet("0xff")).toBe("1111 1111");
    expect(formatBitSet("0b1010")).toBe("1010");
    expect(formatBitSet("nope")).toBeNull();
  });

  it("passes raw value through by default", async () => {
    const result = await formatMessageValue(msg({ valueBase64: b64("hello") }), {
      decode: "none",
      decompression: "none",
      format: "raw",
    });
    expect(result).toEqual({ text: "hello" });
  });

  it("applies inner base64 decode then format", async () => {
    const result = await formatMessageValue(msg({ valueBase64: b64(b64("payload")) }), {
      decode: "base64",
      decompression: "none",
      format: "json",
    });
    expect(result.text).toBe("payload");
  });

  it("inflates gzip payloads via DecompressionStream", async () => {
    const result = await formatMessageValue(msg({ valueBase64: GZIP_JSON_N7_BASE64 }), {
      decode: "none",
      decompression: "gzip",
      format: "json",
    });
    expect(result.error).toBeUndefined();
    expect(result.text).toBe('{\n  "n": 7\n}');
  });

  it("inflates zstd payloads (fzstd, fixed CLI vector)", async () => {
    const result = await formatMessageValue(msg({ valueBase64: ZSTD_ORDER_JSON_BASE64 }), {
      decode: "none",
      decompression: "zstd",
      format: "json",
    });
    expect(result.error).toBeUndefined();
    expect(result.text).toBe('{\n  "orderId": "A-1001",\n  "amount": 42,\n  "currency": "USD"\n}');
  });

  it("inflates snappy payloads (roundtrip with snappyjs compress)", async () => {
    const payload = new TextEncoder().encode('{"n":7,"ok":true}');
    const result = await formatMessageValue(msg({ valueBase64: bytesToBase64(snappyCompress(payload)) }), {
      decode: "none",
      decompression: "snappy",
      format: "raw",
    });
    expect(result.error).toBeUndefined();
    expect(result.text).toBe('{"n":7,"ok":true}');
  });

  it("inflates lz4 payloads (roundtrip with lz4js compress)", async () => {
    const payload = new TextEncoder().encode('{"n":7,"ok":true}');
    const result = await formatMessageValue(msg({ valueBase64: bytesToBase64(lz4Compress(payload)) }), {
      decode: "none",
      decompression: "lz4",
      format: "raw",
    });
    expect(result.error).toBeUndefined();
    expect(result.text).toBe('{"n":7,"ok":true}');
  });

  it("reports decompression failures per algorithm instead of crashing", async () => {
    // 非 zstd 字节流喂给 zstd：fzstd 抛错 → 管线转 error 展示（原值透传）。
    const result = await formatMessageValue(msg({ valueBase64: b64("data") }), {
      decode: "none",
      decompression: "zstd",
      format: "raw",
    });
    expect(result.error).toContain("zstd");
    expect(result.text).toBe("data");
    const snappy = await formatMessageValue(msg({ valueBase64: b64("data") }), {
      decode: "none",
      decompression: "snappy",
      format: "raw",
    });
    expect(snappy.error).toContain("snappy");
    const lz4 = await formatMessageValue(msg({ valueBase64: b64("data") }), {
      decode: "none",
      decompression: "lz4",
      format: "raw",
    });
    expect(lz4.error).toContain("lz4");
  });

  it("hex-formats and reports invalid inner base64", async () => {
    const hex = await formatMessageValue(msg({ valueBase64: b64("A") }), {
      decode: "none",
      decompression: "none",
      format: "hex",
    });
    expect(hex.text).toBe("41");
    const bad = await formatMessageValue(msg({ valueBase64: b64("!!!") }), {
      decode: "base64",
      decompression: "none",
      format: "raw",
    });
    expect(bad.error).toContain("inner base64");
  });

  it("falls back to full value text for preview/download", () => {
    const message = msg({ valueText: "safe", valueBase64: b64("line1\nline2") });
    expect(messageFullValueText(message)).toBe("line1\nline2");
    expect(previewText("a  b\nc", 2)).toBe("a …");
    expect(headersPreview({ h1: "v1", h2: "v2", h3: "v3" })).toBe("h1=v1, h2=v2, …");
  });
});

describe("topic ranking", () => {
  it("scores business-like names above infra names", () => {
    expect(scoreTopicName("order-events")).toBeGreaterThan(scoreTopicName("connect-offsets"));
  });

  it("detects internal topics and sinks them to the bottom", () => {
    expect(isInternalTopicName("_schemas")).toBe(true);
    const sorted = sortTopics([
      { name: "_internal.state" },
      { name: "zzz-raw" },
      { name: "order-events", isInternal: false },
      { name: "users" },
    ]);
    expect(sorted.map((topic) => topic.name)).toEqual(["order-events", "users", "zzz-raw", "_internal.state"]);
  });

  it("filters topics by keyword", () => {
    const topics = [{ name: "orders" }, { name: "users" }];
    expect(filterTopics(topics, "ORD")).toHaveLength(1);
    expect(filterTopics(topics, "  ")).toHaveLength(2);
  });
});

describe("lag aggregation", () => {
  it("sums non-negative lags and treats missing as zero", () => {
    expect(sumLag([{ lag: 5 }, { lag: 0 }, {}, { lag: -3 }, { lag: null }])).toBe(5);
  });
});

describe("stream buffer", () => {
  it("appends rows, caps at max and counts dropped oldest rows", () => {
    const seed = [{ topic: "t", partition: 0, offset: 0, timestamp: 1 }, { topic: "t", partition: 0, offset: 1, timestamp: 2 }];
    const keep = appendStreamRows(seed, [], 10);
    expect(keep).toEqual({ rows: seed, dropped: 0 });
    const grown = appendStreamRows(seed, [{ topic: "t", partition: 0, offset: 2, timestamp: 3 }], 3);
    expect(grown.rows.map((row) => row.offset)).toEqual([0, 1, 2]);
    expect(grown.dropped).toBe(0);
    const overflow = appendStreamRows(grown.rows, [{ topic: "t", partition: 0, offset: 3, timestamp: 4 }], 3, 0);
    expect(overflow.rows.map((row) => row.offset)).toEqual([1, 2, 3]);
    expect(overflow.dropped).toBe(1);
    const stacked = appendStreamRows(overflow.rows, overflow.rows.slice(), 3, 1);
    expect(stacked.dropped).toBe(1 + 3);
    expect(stacked.rows).toHaveLength(3);
  });
});

describe("message table cap", () => {
  it("returns the same reference and zero dropped when under the cap", () => {
    const rows = [msg({ partition: 0, offset: 0 }), msg({ partition: 0, offset: 1 })];
    const capped = capRows(rows);
    expect(capped.rows).toBe(rows); // 零拷贝语义
    expect(capped).toEqual({ rows, total: 2, dropped: 0 });
  });

  it("trims the head and keeps the newest tail when over the cap", () => {
    const rows = Array.from({ length: 5 }, (_unused, index) => msg({ partition: 0, offset: index }));
    const capped = capRows(rows, 3);
    expect(capped.rows.map((row) => row.offset)).toEqual([2, 3, 4]); // 最新在尾部
    expect(capped.total).toBe(5);
    expect(capped.dropped).toBe(2);
    expect(capped.rows).not.toBe(rows); // 裁剪产出新数组
  });

  it("caps to a single row and handles an empty list", () => {
    const rows = [msg({ partition: 0, offset: 7 }), msg({ partition: 0, offset: 8 })];
    expect(capRows(rows, 1).rows.map((row) => row.offset)).toEqual([8]);
    expect(capRows([], 10)).toEqual({ rows: [], total: 0, dropped: 0 });
    expect(MESSAGE_ROWS_MAX).toBeGreaterThan(0);
  });

  it("keeps short value previews untouched", () => {
    const short = "hello world";
    expect(truncatedValuePreview(short)).toEqual({ text: short, truncated: false });
  });

  it("truncates long values keeping head and tail with a hidden-char marker", () => {
    const text = "A".repeat(20000) + "B".repeat(100) + "C".repeat(20000);
    const preview = truncatedValuePreview(text);
    expect(preview.truncated).toBe(true);
    expect(preview.text.length).toBeLessThan(text.length);
    expect(preview.text.startsWith("A".repeat(100))).toBe(true); // 头部保留
    expect(preview.text.endsWith("C".repeat(100))).toBe(true); // 尾部保留
    expect(preview.text).toMatch(/⋯ \d+ chars ⋯/); // 中段省略标记
  });
});

describe("export serialization", () => {
  it("serializes CSV with RFC 4180 escaping", () => {
    const csv = serializeMessagesToCsv([
      msg({ key: "k,1", valueText: 'say "hi"', headers: { trace: "abc" } }),
    ]);
    expect(csv.split("\r\n")[0]).toBe("topic,partition,offset,timestamp,key,value,headers");
    expect(csv).toContain('"k,1"');
    expect(csv).toContain('"say ""hi"""');
    expect(csv).toContain("trace=abc");
  });

  it("serializes stable JSON with full value text", () => {
    const json = JSON.parse(serializeMessagesToJson([msg({ key: "k", valueBase64: b64("body") })]));
    expect(json).toHaveLength(1);
    expect(json[0]).toMatchObject({ topic: "orders", partition: 0, offset: 1, key: "k", value: "body" });
  });
});

describe("consume form helpers", () => {
  it("parses partition lists and offset maps", () => {
    expect(parsePartitionList("2, 0，0 1")).toEqual([0, 1, 2]);
    expect(parsePartitionOffsetsText("0=100, 1:200\n2=300")).toEqual({ "0": 100, "1": 200, "2": 300 });
    expect(partitionOffsetsToText({ "1": 200, "0": 100 })).toBe("0=100,1=200");
  });

  it("parses group reset offset targets (explicit topic and single-topic fallback)", () => {
    const multi = parseGroupOffsetTargetsText("t1:0=100, t1:1=200, t2:0=5", ["t1", "t2"]);
    expect(multi.invalid).toEqual([]);
    expect(multi.targets).toEqual({ t1: { "0": 100, "1": 200 }, t2: { "0": 5 } });
    const single = parseGroupOffsetTargetsText("0=100, 1:200", ["only"]);
    expect(single.targets).toEqual({ only: { "0": 100, "1": 200 } });
    const ambiguous = parseGroupOffsetTargetsText("0=100", ["a", "b"]);
    expect(ambiguous.invalid).toEqual(["0=100"]);
  });

  it("converts offset time inputs (unix ms, datetime-local, RFC3339)", () => {
    expect(offsetTimeToParam("1700000000000")).toBe(1700000000000);
    expect(offsetTimeToParam("2026-09-05T08:30")).toBe(new Date("2026-09-05T08:30:00").toISOString());
    expect(offsetTimeToParam("2026-09-05T08:30:00Z")).toBe("2026-09-05T08:30:00.000Z");
    expect(offsetTimeToParam("junk")).toBeNull();
    expect(offsetTimeToParam("")).toBeNull();
  });

  it("validates consume form mutex rules", () => {
    const base = {
      commit: false,
      groupId: "",
      partitionsText: "",
      offsetStrategy: "latest",
      offsetTimeText: "",
      partitionOffsetsText: "",
      hasFilters: false,
    };
    expect(validateConsumeForm(base)).toEqual([]);
    expect(validateConsumeForm({ ...base, commit: true, hasFilters: true })).toEqual([
      { field: "commit", key: "commitFilterConflict" },
      { field: "commit", key: "commitNeedsGroup" },
    ]);
    expect(validateConsumeForm({ ...base, commit: true })).toEqual([{ field: "commit", key: "commitNeedsGroup" }]);
    expect(validateConsumeForm({ ...base, partitionsText: "0", groupId: "g1" })).toEqual([
      { field: "partitions", key: "partitionsGroupConflict" },
    ]);
    expect(validateConsumeForm({ ...base, offsetStrategy: "timestamp" })).toEqual([
      { field: "strategy", key: "timestampRequired" },
    ]);
    expect(validateConsumeForm({ ...base, offsetStrategy: "offset" })).toEqual([
      { field: "strategy", key: "offsetsRequired" },
    ]);
  });

  it("matches text in all four modes", () => {
    expect(matchText("orders-eu", "orders", "prefix")).toBe(true);
    expect(matchText("orders", "orders", "exact")).toBe(true);
    expect(matchText("payload", "OA", "contains")).toBe(false);
    expect(matchText("error-42", "error-\\d+", "regex")).toBe(true);
    expect(matchText(undefined, "x", "contains")).toBe(false);
  });
});

describe("time range inputs", () => {
  it("normalizes datetime-local / unix ms / RFC3339 inputs to unix ms", () => {
    expect(offsetTimeToUnixMs("1700000000000")).toBe(1700000000000);
    expect(offsetTimeToUnixMs("2026-09-05T08:30:00")).toBe(new Date("2026-09-05T08:30:00").getTime());
    expect(offsetTimeToUnixMs("2026-09-05T08:30:00Z")).toBe(Date.parse("2026-09-05T08:30:00.000Z"));
    expect(offsetTimeToUnixMs("junk")).toBeNull();
    expect(offsetTimeToUnixMs("  ")).toBeNull();
  });

  it("round-trips unix ms through datetime-local with second precision", () => {
    const ms = Date.UTC(2026, 8, 5, 0, 30, 15);
    const text = unixMsToDatetimeLocal(ms);
    expect(text).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/);
    expect(offsetTimeToUnixMs(text)).toBe(ms);
    expect(unixMsToDatetimeLocal(Number.NaN)).toBe("");
  });

  it("builds now() as a parseable datetime-local string", () => {
    const now = nowDatetimeLocal();
    expect(now).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/);
    expect(offsetTimeToUnixMs(now)).not.toBeNull();
  });

  it("switches input modes without losing unparseable drafts", () => {
    expect(switchTimeInputMode("2026-09-05T08:30:00", "unix")).toBe(String(new Date("2026-09-05T08:30:00").getTime()));
    const ms = 1_700_000_000_000;
    const back = switchTimeInputMode(String(ms), "datetime");
    expect(offsetTimeToUnixMs(back)).toBe(ms);
    expect(switchTimeInputMode("junk", "unix")).toBe("junk");
    expect(switchTimeInputMode("junk", "datetime")).toBe("junk");
    expect(switchTimeInputMode("", "datetime")).toBe("");
  });

  it("flags only concrete from>to ranges as reversed", () => {
    expect(isRangeReversed(200, 100)).toBe(true);
    expect(isRangeReversed(100, 100)).toBe(false);
    expect(isRangeReversed(null, 100)).toBe(false);
    expect(isRangeReversed(100, null)).toBe(false);
  });

  it("requires numeric values only for numeric-comparison operators", () => {
    expect(fieldFilterIssue({ operator: "gt", value: "42" })).toBeNull();
    expect(fieldFilterIssue({ operator: "lte", value: "abc" })).toBe("fieldValueNumeric");
    expect(fieldFilterIssue({ operator: "gt", value: "" })).toBeNull();
    expect(fieldFilterIssue({ operator: "contains", value: "abc" })).toBeNull();
  });
});

describe("headers json", () => {
  it("accepts string-valued objects only", () => {
    expect(parseHeadersJson('{"trace":"abc"}')).toEqual({ headers: { trace: "abc" } });
    expect(parseHeadersJson("")).toEqual({ headers: {} });
    expect(parseHeadersJson("[1]")).toHaveProperty("error");
    expect(parseHeadersJson('{"n":1}')).toHaveProperty("error");
    expect(parseHeadersJson("{bad")).toHaveProperty("error");
  });
});

describe("json error line (editor linter)", () => {
  it("returns null for valid, empty and whitespace-only text", () => {
    expect(jsonErrorLine('{"a": [1, 2, {"b": null}], "c": "x"}')).toBeNull();
    expect(jsonErrorLine("")).toBeNull();
    expect(jsonErrorLine("   \n\t ")).toBeNull();
  });

  it("locates the first syntax error line across value kinds", () => {
    expect(jsonErrorLine("{bad}")).toBe(1);
    expect(jsonErrorLine('{\n  "a": 1,\n  "b": tru\n}')).toBe(3); // 非法字面量
    expect(jsonErrorLine('{\n  "a": 1\n  "b": 2\n}')).toBe(3); // 缺逗号
    expect(jsonErrorLine("[1, 2,\n3,\n]")).toBe(3); // 数组尾逗号
    expect(jsonErrorLine('{"k": "v"')).toBe(1); // 未闭合
  });

  it("tracks multi-line strings/objects and trailing junk", () => {
    expect(jsonErrorLine('{\n  "a": "line1\nbroken"}')).toBe(2); // 字符串内裸换行
    expect(jsonErrorLine('{"a": 1} extra')).toBe(1); // 尾部多余 token
    expect(jsonErrorLine("123")).toBeNull(); // 顶层标量合法
    expect(jsonErrorLine("1.5e-3")).toBeNull();
    expect(jsonErrorLine("01")).toBe(1); // 前导零 → 多余 token
    expect(jsonErrorLine('{"\\u00zz": 1}')).toBe(1); // 非法 \\u 转义
  });
});

describe("confluent properties", () => {
  it("parses key=value lines with comments, continuations and escapes", () => {
    const properties = parsePropertiesText(`
# comment line
! another comment
bootstrap.servers=dbx-kafka-test\\
:9092
security.protocol=SASL_SSL
sasl.mechanism=SCRAM\\-SHA-256
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kafka" password="pass=1";
`);
    expect(properties["bootstrap.servers"]).toBe("dbx-kafka-test:9092");
    expect(properties["security.protocol"]).toBe("SASL_SSL");
    expect(properties["sasl.mechanism"]).toBe("SCRAM-SHA-256");
    expect(properties["sasl.jaas.config"]).toContain('password="pass=1"');
  });

  it("maps properties onto the connection form fields", () => {
    const form = propertiesToConnectionForm(
      parsePropertiesText(`
bootstrap.servers=broker1:9092,broker2:9092
security.protocol=SASL_SSL
sasl.mechanism=SCRAM-SHA-256
sasl.jaas.config=ScramLoginModule required username="app" password="secret";
`),
    );
    expect(form).toEqual({
      bootstrapServers: "broker1:9092,broker2:9092",
      securityProtocol: "SASL_SSL",
      saslMechanism: "SCRAM-SHA-256",
      saslUsername: "app",
      saslPassword: "secret",
      tlsInsecureSkipVerify: false,
    });
    const open = propertiesToConnectionForm(parsePropertiesText("bootstrap.servers=b:9092"));
    // key 缺省 = 保持默认校验（不跳过）
    expect(open.tlsInsecureSkipVerify).toBe(false);
    expect(open.saslUsername).toBe("");
    const skipped = propertiesToConnectionForm(parsePropertiesText("ssl.endpoint.identification.algorithm="));
    expect(skipped.tlsInsecureSkipVerify).toBe(true);
    const skippedNone = propertiesToConnectionForm(parsePropertiesText("ssl.endpoint.identification.algorithm=NONE"));
    expect(skippedNone.tlsInsecureSkipVerify).toBe(true);
  });
});

describe("modal keydown decision (Esc close + Tab focus trap)", () => {
  it("closes on Escape regardless of Tab state", () => {
    expect(decideModalKeydown("Escape", false, 0, -1)).toEqual({ kind: "close" });
    expect(decideModalKeydown("Escape", true, 5, 2)).toEqual({ kind: "close" });
  });

  it("ignores non-Esc/Tab keys and empty containers", () => {
    expect(decideModalKeydown("Enter", false, 5, 0)).toEqual({ kind: "none" });
    expect(decideModalKeydown("Tab", false, 0, -1)).toEqual({ kind: "none" });
    expect(decideModalKeydown("Tab", true, 0, 3)).toEqual({ kind: "none" });
  });

  it("cycles forward with wrap-around", () => {
    expect(decideModalKeydown("Tab", false, 3, 0)).toEqual({ kind: "focus", index: 1 });
    expect(decideModalKeydown("Tab", false, 3, 2)).toEqual({ kind: "focus", index: 0 });
  });

  it("cycles backward with wrap-around", () => {
    expect(decideModalKeydown("Tab", true, 3, 2)).toEqual({ kind: "focus", index: 1 });
    expect(decideModalKeydown("Tab", true, 3, 0)).toEqual({ kind: "focus", index: 2 });
  });

  it("enters at the start/end when focus is outside the container", () => {
    // 焦点尚未进容器（如打开瞬间）：Tab 进首个控件，Shift+Tab 进最后一个。
    expect(decideModalKeydown("Tab", false, 4, -1)).toEqual({ kind: "focus", index: 0 });
    expect(decideModalKeydown("Tab", true, 4, -1)).toEqual({ kind: "focus", index: 3 });
    // 越界（焦点被容器外逻辑移走）同样按方向兜底。
    expect(decideModalKeydown("Tab", false, 4, 99)).toEqual({ kind: "focus", index: 0 });
    expect(decideModalKeydown("Tab", true, 4, 99)).toEqual({ kind: "focus", index: 3 });
  });
});
