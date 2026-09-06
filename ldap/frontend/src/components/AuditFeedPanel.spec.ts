// @vitest-environment happy-dom
// AuditFeedPanel workbench UI tests (M5-a UI test track): collapsed summary,
// denied badge highlighting, manual toggle, auto-expand on denied/error
// arrivals, per-item badge/time/target rendering, clear emit and the
// empty-but-expanded state. Pure presentational component — no mocks needed.
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import { formatAuditTime, type AuditFeedItem } from "../lib/auditFeed";
import AuditFeedPanel from "./AuditFeedPanel.vue";

let seq = 0;

function feedItem(result: AuditFeedItem["result"], overrides: Partial<AuditFeedItem> = {}): AuditFeedItem {
  seq += 1;
  return { id: seq, action: "ldap/search", target: "dc=demo,dc=dbx", result, at: Date.now(), ...overrides };
}

function localClock(at: number): string {
  const date = new Date(at);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function localDayClock(at: number): string {
  const date = new Date(at);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${localClock(at)}`;
}

function mountPanel(items: AuditFeedItem[]) {
  return mount(AuditFeedPanel, { props: { items } });
}

const toggle = (wrapper: ReturnType<typeof mountPanel>) => wrapper.find(".audit-toggle");

describe("AuditFeedPanel", () => {
  it("renders nothing with no events while collapsed", () => {
    const wrapper = mountPanel([]);
    expect(wrapper.find(".audit-feed").exists()).toBe(false);
  });

  it("shows a collapsed summary for ok-only events without the denied styling", () => {
    const wrapper = mountPanel([feedItem("ok")]);
    expect(wrapper.find(".audit-feed").exists()).toBe(true);
    expect(wrapper.find(".audit-feed").classes()).not.toContain("audit-feed-denied");
    expect(wrapper.find(".audit-summary").text()).toBe("1 条事件");
    expect(wrapper.find(".audit-summary").classes()).not.toContain("audit-summary-denied");
    expect(toggle(wrapper).attributes("aria-expanded")).toBe("false");
    expect(toggle(wrapper).attributes("title")).toBe("展开审计记录");
    expect(wrapper.find(".audit-list").exists()).toBe(false);
    expect(wrapper.find(".audit-clear").exists()).toBe(true);
  });

  it("toggles the item list open and closed via the header button", async () => {
    const item = feedItem("ok");
    const wrapper = mountPanel([item]);
    await toggle(wrapper).trigger("click");
    expect(toggle(wrapper).attributes("aria-expanded")).toBe("true");
    expect(toggle(wrapper).attributes("title")).toBe("收起审计记录");
    const rows = wrapper.findAll(".audit-item");
    expect(rows).toHaveLength(1);
    expect(rows[0].find(".audit-time").text()).toBe(formatAuditTime(item.at));
    expect(localClock(item.at)).toMatch(/^\d{2}:\d{2}:\d{2}$/);
    expect(rows[0].find(".audit-badge").text()).toBe("成功");
    expect(rows[0].find(".audit-badge").classes()).toContain("audit-badge-ok");
    expect(rows[0].find(".audit-action").text()).toBe("ldap/search");
    expect(rows[0].find(".audit-target").text()).toBe("dc=demo,dc=dbx");
    await toggle(wrapper).trigger("click");
    expect(wrapper.find(".audit-list").exists()).toBe(false);
  });

  it("prefixes cross-day timestamps and falls back to an em dash for empty targets", async () => {
    const stale = feedItem("error", { at: Date.now() - 48 * 60 * 60 * 1000, target: "" });
    const wrapper = mountPanel([stale]);
    await toggle(wrapper).trigger("click");
    const row = wrapper.findAll(".audit-item")[0];
    expect(row.find(".audit-time").text()).toBe(localDayClock(stale.at));
    expect(row.find(".audit-target").text()).toBe("—");
    expect(row.attributes("title")).toBe(stale.target);
    expect(row.find(".audit-badge").text()).toBe("错误");
    expect(row.find(".audit-badge").classes()).toContain("audit-badge-error");
  });

  it("keeps the feed collapsed when further ok events arrive", async () => {
    const first = feedItem("ok");
    const wrapper = mountPanel([first]);
    await wrapper.setProps({ items: [feedItem("ok"), first] });
    expect(wrapper.find(".audit-list").exists()).toBe(false);
    expect(wrapper.find(".audit-summary").text()).toBe("2 条事件");
  });

  it("auto-expands once when a denied event arrives and highlights the count", async () => {
    const ok = feedItem("ok");
    const wrapper = mountPanel([ok]);
    const denied = feedItem("denied", { detail: "read-only connection" });
    await wrapper.setProps({ items: [denied, ok] });
    expect(wrapper.find(".audit-list").exists()).toBe(true);
    expect(wrapper.find(".audit-feed").classes()).toContain("audit-feed-denied");
    expect(wrapper.find(".audit-summary").text()).toBe("2 条事件 · 1 条被拒绝");
    expect(wrapper.find(".audit-summary").classes()).toContain("audit-summary-denied");
    const row = wrapper.findAll(".audit-item")[0];
    expect(row.find(".audit-badge").text()).toBe("已拒绝");
    expect(row.find(".audit-badge").classes()).toContain("audit-badge-denied");
    expect(row.attributes("title")).toBe("read-only connection");
  });

  it("emits clear from the trash button", async () => {
    const wrapper = mountPanel([feedItem("ok")]);
    await wrapper.find(".audit-clear").trigger("click");
    expect(wrapper.emitted("clear")).toHaveLength(1);
  });

  it("survives an emptied feed while expanded: disabled toggle, no clear, no list", async () => {
    const denied = feedItem("denied");
    const wrapper = mountPanel([denied]);
    await wrapper.setProps({ items: [] });
    expect(wrapper.find(".audit-feed").exists()).toBe(true);
    expect(toggle(wrapper).attributes("disabled")).toBeDefined();
    expect(wrapper.find(".audit-clear").exists()).toBe(false);
    expect(wrapper.find(".audit-list").exists()).toBe(false);
    expect(wrapper.find(".audit-summary").text()).toBe("暂无审计事件");
    // A fresh ok event restores the (still expanded) list without the denied badge.
    const ok = feedItem("ok");
    await wrapper.setProps({ items: [ok] });
    expect(wrapper.findAll(".audit-item")).toHaveLength(1);
    expect(wrapper.find(".audit-feed").classes()).not.toContain("audit-feed-denied");
    expect(toggle(wrapper).attributes("disabled")).toBeUndefined();
  });

  it("does not auto-expand for ok arrivals after a denied-triggered expansion stays manual", async () => {
    const denied = feedItem("denied");
    const wrapper = mountPanel([denied]);
    await toggle(wrapper).trigger("click");
    await wrapper.setProps({ items: [feedItem("ok"), denied] });
    // Already expanded manually; the ok arrival neither collapses nor re-expands.
    expect(wrapper.find(".audit-list").exists()).toBe(true);
    expect(wrapper.find(".audit-summary").text()).toBe("2 条事件 · 1 条被拒绝");
  });
});
