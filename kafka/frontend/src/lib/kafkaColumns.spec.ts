// lib/kafkaColumns 单测：列定义形状（字段/过滤类型/排序）、VM 映射、
// minimalColumns 窄容器降级、页大小 localStorage 持久化、ag 内置文案七语键齐。
// @vitest-environment happy-dom
import { describe, expect, it } from "vitest";
import {
  AG_GRID_LOCALE_KEYS,
  DEFAULT_PAGE_SIZE,
  agGridLocaleText,
  aclColumns,
  groupColumns,
  groupOffsetColumns,
  lagColumns,
  loadPreferredPageSize,
  memberColumns,
  messageColumns,
  minimalColumns,
  partitionColumns,
  savePreferredPageSize,
  schemaVersionColumns,
  subjectColumns,
  toAclRows,
  toGroupOffsetRows,
  toLagRows,
  toMemberRows,
  toMessageRows,
  toPartitionRows,
  toSchemaVersionRows,
  toSubjectRows,
  toTopicOffsetRows,
  toTopicRows,
  topicColumns,
  topicOffsetColumns,
} from "./kafkaColumns";
import { setWorkbenchLocale } from "./i18n";
import type { KafkaMessage } from "./api";

describe("column builders", () => {
  it("message columns expose sortable/filterable fields", () => {
    setWorkbenchLocale("en");
    const cols = messageColumns();
    expect(cols.map((col) => col.field)).toEqual([
      "partition",
      "offset",
      "timestampText",
      "keyText",
      "valueText",
      "headersText",
      "schemaText",
    ]);
    for (const col of cols) {
      expect(col.sortable).toBe(true);
      expect(["agTextColumnFilter", "agNumberColumnFilter"]).toContain(col.filter);
    }
    expect(cols[0].filter).toBe("agNumberColumnFilter");
  });

  it("every builder produces header names for the current locale", () => {
    setWorkbenchLocale("zh-CN");
    const builders = [messageColumns, groupColumns, groupOffsetColumns, memberColumns, aclColumns, topicColumns, partitionColumns, topicOffsetColumns, subjectColumns, schemaVersionColumns, lagColumns];
    for (const build of builders) {
      for (const col of build()) {
        expect(String(col.headerName).length).toBeGreaterThan(0);
      }
    }
  });

  it("subject/version/lag columns match the Phase 2 contract shape", () => {
    setWorkbenchLocale("en");
    expect(subjectColumns().map((col) => col.field)).toEqual(["subject", "formats", "latestVersion", "compatibilityLevel"]);
    expect(schemaVersionColumns().map((col) => col.field)).toEqual(["version", "id", "format"]);
    expect(lagColumns().map((col) => col.field)).toEqual(["topic", "partition", "committedText", "endOffset", "lag"]);
  });
});

describe("row mappers", () => {
  it("maps messages with previews and schema badge text", () => {
    setWorkbenchLocale("en");
    const message: KafkaMessage = {
      topic: "t",
      partition: 1,
      offset: 2,
      timestamp: 0,
      key: "k",
      valueText: "v",
      headers: { a: "1" },
      schemaId: 3,
      schemaSubject: "s-value",
      schemaVersion: 5,
    };
    const rows = toMessageRows([message]);
    expect(rows[0].id).toBe("1:2");
    expect(rows[0].schemaText).toBe("s-value v5");
    expect(rows[0].raw).toBe(message);
    expect(rows[0].keyText).toBe("k");
  });

  it("marks uncommitted offset rows and null lag", () => {
    setWorkbenchLocale("en");
    const rows = toGroupOffsetRows([
      { topic: "t", partition: 0, hasCommitted: false, lag: undefined },
      { topic: "t", partition: 1, committedOffset: 4, lag: 2 },
    ]);
    expect(rows[0].committedText).toContain("no committed data");
    expect(rows[0].lag).toBeNull();
    expect(rows[1].lag).toBe(2);
    expect(toLagRows(rows.map((row) => row.raw))[0].id).toBe("t:0");
  });

  it("maps partitions health flag and internal topics", () => {
    setWorkbenchLocale("en");
    const partitions = toPartitionRows([{ partition: 0, leader: 1, replicas: [1], isr: [1], offlineReplicas: [], isHealthy: false }]);
    expect(partitions[0].healthy).toBe(false);
    expect(partitions[0].healthyText).toBe("degraded");
    const topics = toTopicRows([{ name: "_internal", partitionCount: 1, replicationFactor: 1, isInternal: true }]);
    expect(topics[0].internalText).toBe("internal");
  });

  it("maps acl / subject / version / topic offset rows", () => {
    setWorkbenchLocale("en");
    const acls = toAclRows([{ resourceType: "TOPIC", resourceName: "r", principal: "User:a", operation: "READ", permission: "ALLOW" }]);
    expect(acls[0].patternType).toBe("LITERAL");
    expect(acls[0].host).toBe("*");
    const subjects = toSubjectRows([{ subject: "s", formats: ["avro"], latestVersion: 2 }]);
    expect(subjects[0].formats).toBe("avro");
    expect(subjects[0].latestVersion).toBe(2);
    const versions = toSchemaVersionRows([{ version: 1, id: 11, format: "avro" }]);
    expect(versions[0].raw.version).toBe(1);
    const offsets = toTopicOffsetRows([{ topic: "t", partition: 0, offset: 9 }]);
    expect(offsets[0].leaderEpoch).toBe("—");
    expect(toMemberRows([{ memberId: "m", assignments: { t: [0, 1] } }])[0].assignments).toBe("t[0,1]");
  });
});

describe("minimalColumns", () => {
  it("keeps only the requested fields in order", () => {
    setWorkbenchLocale("en");
    const defs = messageColumns();
    const compact = minimalColumns(defs, ["partition", "valueText"]);
    expect(compact.map((col) => col.field)).toEqual(["partition", "valueText"]);
    expect(compact).toHaveLength(2);
  });
});

describe("page size persistence", () => {
  it("round-trips a preferred page size with safe fallbacks", () => {
    savePreferredPageSize("spec-table", 123);
    expect(loadPreferredPageSize("spec-table")).toBe(123);
    savePreferredPageSize("spec-table", 50);
    expect(loadPreferredPageSize("spec-table")).toBe(50);
    expect(loadPreferredPageSize("spec-table-never-set")).toBe(DEFAULT_PAGE_SIZE);
  });
});

describe("ag-grid built-in locale text", () => {
  it("covers the same key set across the seven workbench locales", () => {
    const locales = ["en", "zh-CN", "zh-TW", "es", "it", "ja", "pt-BR"];
    for (const locale of locales) {
      setWorkbenchLocale(locale);
      const text = agGridLocaleText();
      for (const key of AG_GRID_LOCALE_KEYS) {
        expect(typeof text[key], `${locale}:${key}`).toBe("string");
        expect(text[key].length, `${locale}:${key}`).toBeGreaterThan(0);
      }
    }
  });
});
