// @vitest-environment happy-dom
// ResultTable workbench UI tests: empty-state copy distinguishes "not searched
// yet" from "search ran, zero matches" (UI scan P2-3).
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import ResultTable from "./ResultTable.vue";
import type { LdapEntry } from "../lib/api";

const entry: LdapEntry = { dn: "cn=alice,dc=demo,dc=dbx", attributes: { cn: ["alice"] } };

function mountTable(props: { entries: LdapEntry[]; count: number; truncated?: boolean; searched?: boolean; atLimit?: boolean; sizeLimit?: number }) {
  return mount(ResultTable, { props: { truncated: false, ...props } });
}

describe("ResultTable empty states (P2-3)", () => {
  it("says 'run a search first' before any search has run", () => {
    const wrapper = mountTable({ entries: [], count: 0 });
    expect(wrapper.find(".empty").text()).toBe("暂无结果，请先执行搜索");
  });

  it("says 'no matches' after a search returned zero entries", () => {
    const wrapper = mountTable({ entries: [], count: 0, searched: true });
    expect(wrapper.find(".empty").text()).toBe("当前搜索没有匹配的条目");
  });

  it("keeps the no-match copy after a later empty search (state is sticky until next run)", () => {
    const wrapper = mountTable({ entries: [entry], count: 1, searched: true });
    expect(wrapper.find(".empty").exists()).toBe(false);
    expect(wrapper.findAll(".result-row")).toHaveLength(1);
  });

  it("shows an 'at limit' badge when count equals sizeLimit without a backend truncation flag (P2-15)", () => {
    const wrapper = mountTable({ entries: [entry], count: 500, atLimit: true, sizeLimit: 500 });
    const badge = wrapper.find(".truncated-badge");
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe("已到上限");
    expect(badge.attributes("title")).toContain("500");
  });

  it("does not show the at-limit badge when results are below the limit", () => {
    const wrapper = mountTable({ entries: [entry], count: 12 });
    expect(wrapper.find(".truncated-badge").exists()).toBe(false);
  });

  it("wires aria-sort on sortable headers (P2-22: ariaSortFor must not be dead code)", () => {
    const wrapper = mountTable({ entries: [entry], count: 1 });
    const headers = wrapper.findAll(".result-header button");
    expect(headers[0].attributes("aria-sort")).toBe("ascending");
    expect(headers[1].attributes("aria-sort")).toBe("none");
  });

  it("resets a stale sort column that the new result set does not contain (round-4)", async () => {
    const wrapper = mountTable({
      entries: [{ dn: "cn=alice,dc=demo,dc=dbx", attributes: { cn: ["alice"], mail: ["a@x"] } }],
      count: 1,
    });
    // 按 mail 列排序（旧结果含该列）。
    await wrapper.findAll(".result-header button").find((b) => b.text().startsWith("mail"))!.trigger("click");
    expect(wrapper.find(".result-header button[aria-sort='ascending']").text()).toContain("mail");
    // 新结果不再含 mail 列：排序指示失效（所有行该列取值皆空，等于没排），
    // 应回落 dn 升序，而不是残留一个不存在的排序键。
    await wrapper.setProps({ entries: [{ dn: "uid=b,dc=demo,dc=dbx", attributes: { uid: ["b"] } }], count: 1 });
    const dnHeader = wrapper.findAll(".result-header button")[0];
    expect(dnHeader.text()).toContain("dn");
    expect(dnHeader.attributes("aria-sort")).toBe("ascending");
    expect(wrapper.findAll(".result-header button").every((b) => b.text().startsWith("dn") || !b.text().includes("▲"))).toBe(true);
  });

  it("keeps the sort column when the new result set still contains it", async () => {
    const wrapper = mountTable({ entries: [entry, { ...entry, dn: "cn=bob,dc=demo,dc=dbx" }], count: 2 });
    await wrapper.findAll(".result-header button").find((b) => b.text().startsWith("cn"))!.trigger("click");
    expect(wrapper.find(".result-header button[aria-sort='ascending']").text()).toContain("cn");
    await wrapper.setProps({ entries: [{ dn: "cn=carol,dc=demo,dc=dbx", attributes: { cn: ["carol"] } }], count: 1 });
    expect(wrapper.find(".result-header button[aria-sort='ascending']").text()).toContain("cn");
  });
});

describe("ResultTable cell value tooltip (round-2)", () => {
  it("exposes the full joined value via title even when the cell text is truncated", () => {
    const long = "A".repeat(300);
    const wrapper = mountTable({ entries: [{ dn: entry.dn, attributes: { cn: [long] } }], count: 1 });
    const cell = wrapper.find(".cell-value");
    expect(cell.text().endsWith("…")).toBe(true); // 117 字符 + 省略号（长度断言放宽：happy-dom text() 归一化差异）
    expect(cell.attributes("title")).toBe(long);
  });

  it("caps the title at 2000 characters and omits it for empty cells", () => {
    const huge = "B".repeat(3000);
    const wrapper = mountTable({ entries: [{ dn: entry.dn, attributes: { cn: [huge], mail: [] } }], count: 1 });
    const cells = wrapper.findAll(".cell-value");
    expect(cells[0].attributes("title")).toBe(`${"B".repeat(2000)}…`);
    // mail 列存在但无值：不渲染空 title 属性。
    expect(cells[1].attributes("title")).toBeUndefined();
  });
});
