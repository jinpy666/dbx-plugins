// 审计事件流纯函数单测（App.vue handleEvent 数据面依赖）。
import { describe, expect, it } from "vitest";
import { AUDIT_FEED_MAX, formatAuditTime, parseAuditEvent, pushAuditItem } from "./auditFeed";

describe("parseAuditEvent", () => {
  it("解析完整 AuditRecord params", () => {
    const item = parseAuditEvent({ action: "ldap/search", target: "dc=example,dc=com", result: "ok", detail: "d" }, 1, 1000);
    expect(item).toEqual({ id: 1, action: "ldap/search", target: "dc=example,dc=com", result: "ok", at: 1000, detail: "d" });
  });

  it("缺省字段兜底为安全默认值", () => {
    const item = parseAuditEvent(undefined, 2, 2000);
    expect(item.action).toBe("ldap");
    expect(item.target).toBe("");
    expect(item.result).toBe("error");
    expect(item.detail).toBeUndefined();
  });

  it("未知 result 折算为 error（不静默吞掉）", () => {
    expect(parseAuditEvent({ result: "weird" }, 3, 0).result).toBe("error");
    expect(parseAuditEvent({ result: "denied" }, 3, 0).result).toBe("denied");
  });

  it("非法时间戳回退 Date.now()", () => {
    const item = parseAuditEvent({}, 4, Number.NaN);
    expect(Number.isFinite(item.at)).toBe(true);
  });
});

describe("pushAuditItem", () => {
  it("新事件插入头部", () => {
    const a = parseAuditEvent({ action: "a" }, 1, 0);
    const b = parseAuditEvent({ action: "b" }, 2, 0);
    const next = pushAuditItem([a], b);
    expect(next.map((item) => item.id)).toEqual([2, 1]);
  });

  it("超出上限从尾部裁剪", () => {
    let items = [] as ReturnType<typeof parseAuditEvent>[];
    for (let id = 0; id < AUDIT_FEED_MAX + 5; id += 1) items = pushAuditItem(items, parseAuditEvent({ action: `x${id}` }, id, 0));
    expect(items.length).toBe(AUDIT_FEED_MAX);
    expect(items[0].id).toBe(AUDIT_FEED_MAX + 4);
    expect(items[items.length - 1].id).toBe(5);
  });
});

describe("formatAuditTime", () => {
  it("同一天只显示 HH:MM:SS", () => {
    const now = new Date(2026, 8, 2, 10, 30, 5).getTime();
    expect(formatAuditTime(now, now)).toBe("10:30:05");
  });

  it("跨天事件加 MM-DD 前缀", () => {
    const now = new Date(2026, 8, 2, 0, 5, 0).getTime();
    const yesterday = new Date(2026, 8, 1, 23, 59, 59).getTime();
    expect(formatAuditTime(yesterday, now)).toBe("09-01 23:59:59");
  });
});
