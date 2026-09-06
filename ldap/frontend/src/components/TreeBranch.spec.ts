// @vitest-environment happy-dom
// TreeBranch badge tests (P1-2 truncation visibility): a truncated node shows
// a "loaded+" badge (never the exact total) that emits loadMore on click;
// a fully loaded node keeps the exact-count span badge.
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import TreeBranch from "./TreeBranch.vue";
import type { DnTreeNode } from "../lib/dnTree";

function makeNode(overrides: Partial<DnTreeNode> = {}): DnTreeNode {
  return {
    dn: "ou=people,dc=demo,dc=dbx",
    label: "people",
    expanded: true,
    loaded: true,
    loading: false,
    children: [],
    ...overrides,
  };
}

function mountBranch(node: DnTreeNode, disabled = false) {
  return mount(TreeBranch, { props: { node, depth: 1, selectedDn: "", disabled } });
}

describe("TreeBranch child-count badge", () => {
  it("shows the exact count span badge with a title when fully loaded", () => {
    const wrapper = mountBranch(makeNode({ childCount: 1000, truncated: false }));
    const badge = wrapper.find(".tree-badge");
    expect(badge.exists()).toBe(true);
    expect(badge.element.tagName).toBe("SPAN");
    expect(badge.text()).toBe("1000");
    expect(badge.attributes("title")).toBe("1000 个子条目");
  });

  it("shows a truncated 'loaded+' badge whose title carries loaded/total, not the badge", async () => {
    const children = Array.from({ length: 500 }, (_, index) => makeNode({ dn: `uid=user${index},ou=people,dc=demo,dc=dbx`, loaded: false }));
    const wrapper = mountBranch(makeNode({ children, childCount: 1000, truncated: true }));
    const badge = wrapper.find(".tree-badge--truncated");
    expect(badge.exists()).toBe(true);
    // 徽标文案只背书已加载数，精确总数只在悬停提示里。
    expect(badge.text()).toBe("500+");
    expect(badge.attributes("title")).toContain("已加载 500 / 共 1000");
    await badge.trigger("click");
    expect(wrapper.emitted("loadMore")?.[0]).toEqual([wrapper.props("node")]);
  });

  it("falls back to an unknown-total title when count did not return", () => {
    const children = Array.from({ length: 500 }, (_, index) => makeNode({ dn: `uid=user${index},ou=people,dc=demo,dc=dbx`, loaded: false }));
    const wrapper = mountBranch(makeNode({ children, truncated: true }));
    const badge = wrapper.find(".tree-badge--truncated");
    expect(badge.text()).toBe("500+");
    expect(badge.attributes("title")).toContain("已加载前 500 个子条目（已截断）");
  });

  it("does not emit loadMore while disabled", async () => {
    const children = Array.from({ length: 500 }, (_, index) => makeNode({ dn: `uid=user${index},ou=people,dc=demo,dc=dbx`, loaded: false }));
    const wrapper = mountBranch(makeNode({ children, childCount: 1000, truncated: true }), true);
    await wrapper.find(".tree-badge--truncated").trigger("click");
    expect(wrapper.emitted("loadMore")).toBeUndefined();
  });
});
