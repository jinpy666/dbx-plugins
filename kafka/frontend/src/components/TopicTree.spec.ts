// @vitest-environment happy-dom
// TopicTree 组件测试：业务排序 + internal 沉底渲染、过滤框、选中态与 select 事件。
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import TopicTree from "./TopicTree.vue";
import type { KafkaTopic } from "../lib/api";

const topics: KafkaTopic[] = [
  { name: "_schemas", partitionCount: 1, replicationFactor: 1, isInternal: true },
  { name: "users", partitionCount: 3, replicationFactor: 1 },
  { name: "order-events", partitionCount: 2, replicationFactor: 1 },
];

function mountTree(selectedTopic = "") {
  return mount(TopicTree, {
    props: { topics, loading: false, error: "", selectedTopic },
  });
}

describe("TopicTree", () => {
  it("renders business topics first, internal topics sunk to the bottom", () => {
    const wrapper = mountTree();
    const names = wrapper.findAll(".tree-name").map((node) => node.text());
    expect(names).toEqual(["order-events", "users", "_schemas"]);
    expect(wrapper.findAll(".badge-internal")).toHaveLength(1);
  });

  it("shows empty state for empty topic list", () => {
    const wrapper = mount(TopicTree, { props: { topics: [], loading: false, error: "", selectedTopic: "" } });
    expect(wrapper.text()).toContain("集群内暂无 topic");
  });

  it("shows the error text when the backend list fails", () => {
    const wrapper = mount(TopicTree, { props: { topics: [], loading: false, error: "boom", selectedTopic: "" } });
    expect(wrapper.find(".tree-error").text()).toBe("boom");
  });

  it("filters by keyword and emits select with the topic name", async () => {
    const wrapper = mountTree();
    await wrapper.find(".tree-filter input").setValue("user");
    expect(wrapper.findAll(".tree-row")).toHaveLength(1);
    await wrapper.find(".tree-row").trigger("click");
    expect(wrapper.emitted("select")?.[0]).toEqual(["users"]);
  });

  it("marks the selected topic row", () => {
    const wrapper = mountTree("users");
    expect(wrapper.find(".tree-row.selected").text()).toContain("users");
  });
});
