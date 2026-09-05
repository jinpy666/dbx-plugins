// @vitest-environment happy-dom
// SearchForm workbench UI tests (M5-a UI test track): builder → live preview,
// ≠ operator, validation gating, builder↔source mode round-trip. Network
// calls (presets/schema) reject harmlessly without a dbxPlugin host bridge.
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import SearchForm from "./SearchForm.vue";

async function mountForm() {
  const wrapper = mount(SearchForm, { props: { baseDn: "dc=demo,dc=dbx" } });
  await wrapper.vm.$nextTick();
  return wrapper;
}

const previewText = (wrapper: Awaited<ReturnType<typeof mountForm>>) => wrapper.find(".qb-preview").text();

describe("SearchForm (filter builder)", () => {
  it("shows the match-all fallback preview on an empty condition", async () => {
    const wrapper = await mountForm();
    expect(wrapper.find(".filter-builder").exists()).toBe(true);
    expect(previewText(wrapper)).toBe("(objectClass=*)");
  });

  it("generates the live RFC 4515 preview from builder conditions", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    await wrapper.find(".qb-value").setValue("admin");
    expect(previewText(wrapper)).toBe("(uid=admin)");
  });

  it("builds a ≠ clause via the notEquals operator", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    await wrapper.find(".qb-node select").setValue("notEquals");
    await wrapper.find(".qb-value").setValue("admin");
    expect(previewText(wrapper)).toBe("(!(uid=admin))");
  });

  it("gates submit while a clause is half-filled and releases when valid", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    expect(wrapper.find(".form-error").exists()).toBe(true);
    await wrapper.find("form").trigger("submit");
    expect(wrapper.emitted("run")).toBeUndefined();
    await wrapper.find(".qb-value").setValue("admin");
    expect(wrapper.find(".form-error").exists()).toBe(false);
    await wrapper.find("form").trigger("submit");
    expect(wrapper.emitted("run")).toHaveLength(1);
    expect(wrapper.emitted("run")![0][0]).toMatchObject({ baseDn: "dc=demo,dc=dbx", filter: "(uid=admin)" });
  });

  it("carries the generated filter into source mode and back", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    await wrapper.find(".qb-value").setValue("admin");
    const modeButtons = wrapper.findAll(".mode-switch button");
    await modeButtons[1].trigger("click");
    expect(wrapper.find(".filter-source").exists()).toBe(true);
    const sourceInput = wrapper.find(".filter-source input");
    expect((sourceInput.element as HTMLInputElement).value).toBe("(uid=admin)");

    await sourceInput.setValue("(!(uid=admin))");
    await modeButtons[0].trigger("click");
    expect(previewText(wrapper)).toBe("(!(uid=admin))");
  });

  it("flags invalid source filters and keeps source mode authoritative", async () => {
    const wrapper = await mountForm();
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    await wrapper.find(".filter-source input").setValue("(uid");
    expect(wrapper.find(".form-error").exists()).toBe(true);
  });
});
