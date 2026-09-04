// kafkaModel 纯函数单测：解码/格式化管线、topic 排序、lag 聚合、CSV/JSON
// 导出、properties 解析与 consume 表单校验。不连网、不依赖组件；
// 不引 node 专有模块（zlib/Buffer），gzip 样本用预生成常量，保持 tsconfig
// types=["vite/client"] 与 ldap 基线一致（零新依赖）。
import { describe, expect, it } from "vitest";
import {
  appendStreamRows,
  base64ToBytes,
  bytesToHex,
  bytesToUtf8,
  filterTopics,
  formatBitSet,
  formatMessageValue,
  headersPreview,
  isInternalTopicName,
  matchText,
  messageFullValueText,
  offsetTimeToParam,
  parseHeadersJson,
  parsePartitionList,
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
  validateConsumeForm,
} from "./kafkaModel";
import type { KafkaMessage } from "./api";

// gzipSync('{"n":7}') 的 base64 常量（生成命令见仓库 PROGRESS 文档）。
const GZIP_JSON_N7_BASE64 = "H4sIAAAAAAAAE6tWylOyMq8FAPicEYIHAAAA";

function b64(text: string): string {
  return utf8ToBase64(text);
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

  it("flags unsupported decompression algorithms instead of crashing", async () => {
    const result = await formatMessageValue(msg({ valueBase64: b64("data") }), {
      decode: "none",
      decompression: "zstd",
      format: "raw",
    });
    expect(result.error).toContain("zstd");
    expect(result.text).toBe("data");
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

describe("headers json", () => {
  it("accepts string-valued objects only", () => {
    expect(parseHeadersJson('{"trace":"abc"}')).toEqual({ headers: { trace: "abc" } });
    expect(parseHeadersJson("")).toEqual({ headers: {} });
    expect(parseHeadersJson("[1]")).toHaveProperty("error");
    expect(parseHeadersJson('{"n":1}')).toHaveProperty("error");
    expect(parseHeadersJson("{bad")).toHaveProperty("error");
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
