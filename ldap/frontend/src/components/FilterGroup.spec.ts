// @vitest-environment happy-dom
// Component tests for the visual filter builder rows (M5-a UI test track):
// operator coverage (incl. ≠), row add/remove, empty-group state and the
// disabled gate — all mutating the caller-owned BuilderGroup in place.
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import FilterGroup from "./FilterGroup.vue";
import { createBuilderClause, createBuilderGroup, type BuilderGroup } from "../lib/ldapFilter";

function mountGroup(children = [createBuilderClause({ attribute: "uid", op: "equals", value: "admin" })]) {
  const group: BuilderGroup = createBuilderGroup({ children });
  const wrapper = mount(FilterGroup, { props: { group, depth: 0, listId: "test-datalist" } });
  return { wrapper, group };
}

describe("FilterGroup", () => {
  it("renders clause rows with the full operator set (incl. notEquals)", () => {
    const { wrapper } = mountGroup();
    expect(wrapper.find(".qb-attr").exists()).toBe(true);
    expect(wrapper.find(".qb-value").exists()).toBe(true);
    const optionValues = wrapper.findAll("select option").map((option) => (option.element as HTMLOptionElement).value);
    expect(optionValues).toEqual(["equals", "notEquals", "contains", "startsWith", "endsWith", "present", "gte", "lte", "approx"]);
  });

  it("keeps two-way binding with the caller-owned node (v-model)", async () => {
    const { wrapper, group } = mountGroup();
    await wrapper.find(".qb-attr").setValue("mail");
    await wrapper.find("select").setValue("notEquals");
    await wrapper.find(".qb-value").setValue("a@b");
    expect(group.children[0]).toMatchObject({ attribute: "mail", op: "notEquals", value: "a@b" });
  });

  it("adds and removes clause rows", async () => {
    const { wrapper, group } = mountGroup();
    await wrapper.findAll(".qb-add")[0].trigger("click");
    expect(group.children).toHaveLength(2);
    await wrapper.findAll(".qb-remove")[0].trigger("click");
    expect(group.children).toHaveLength(1);
    await wrapper.findAll(".qb-remove")[0].trigger("click");
    expect(group.children).toHaveLength(0);
    expect(wrapper.find(".qb-empty").exists()).toBe(true);
  });

  it("hides the value input for presence clauses", async () => {
    const { wrapper } = mountGroup();
    await wrapper.find("select").setValue("present");
    expect(wrapper.find(".qb-value--empty").exists()).toBe(true);
  });

  it("disables every control when disabled is set", async () => {
    const group: BuilderGroup = createBuilderGroup({ children: [createBuilderClause()] });
    const wrapper = mount(FilterGroup, { props: { group, depth: 0, listId: "d", disabled: true } });
    for (const button of wrapper.findAll("button")) {
      expect(button.attributes("disabled")).toBeDefined();
    }
    await wrapper.find(".qb-attr").setValue("x");
    expect(group.children[0].kind === "clause" && group.children[0].attribute).toBe("");
  });

  it("caps nesting: depth 1 hides the add-group action", () => {
    const group: BuilderGroup = createBuilderGroup({ children: [createBuilderClause()] });
    const wrapper = mount(FilterGroup, { props: { group, depth: 1, listId: "d" } });
    expect(wrapper.findAll(".qb-add")).toHaveLength(1);
  });
});
