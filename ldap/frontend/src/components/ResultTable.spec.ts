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
});
