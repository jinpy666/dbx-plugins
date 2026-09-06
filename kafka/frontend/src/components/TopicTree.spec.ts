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

  // P2-18：树错误区与错误横幅同源 friendlyKafkaError——夹具/网络类错误本地化，
  // 原始串留在 title 悬停；未覆盖错误原文透传且无 title。
  it("friendly-maps connection errors and keeps the raw string in the title (P2-18)", () => {
    const wrapper = mount(TopicTree, { props: { topics: [], loading: false, error: "connection lost (fixture error injection)", selectedTopic: "" } });
    const node = wrapper.find(".tree-error");
    expect(node.text()).toBe("无法连接 Kafka broker：请检查 bootstrap servers 与网络");
    expect(node.attributes("title")).toBe("connection lost (fixture error injection)");
  });

  it("passes unknown errors through unchanged without a hover title (P2-18)", () => {
    const wrapper = mount(TopicTree, { props: { topics: [], loading: false, error: "boom", selectedTopic: "" } });
    const node = wrapper.find(".tree-error");
    expect(node.text()).toBe("boom");
    expect(node.attributes("title")).toBeFalsy();
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
