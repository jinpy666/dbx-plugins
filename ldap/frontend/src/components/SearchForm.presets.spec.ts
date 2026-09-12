// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { ldapApi, type LdapSearchPreset } from "../lib/api";
import SearchForm from "./SearchForm.vue";

let wrapper: ReturnType<typeof mount<typeof SearchForm>>;
const savedPreset: LdapSearchPreset = { id: "stored-id", name: "People", filter: "(uid=old)", scope: "sub" };

async function mountPresets(presets: LdapSearchPreset[] = [savedPreset]) {
  vi.spyOn(ldapApi, "presetsList").mockResolvedValue({ presets });
  wrapper = mount(SearchForm, { props: { baseDn: "dc=demo,dc=dbx" } });
  await flushPromises();
  return wrapper;
}

const saveButton = () => wrapper.findAll(".search-presets button")[0];
const removeButton = () => wrapper.findAll(".search-presets button")[1];
const presetSelect = () => wrapper.find(".search-presets select");

afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("SearchForm actual preset responses", () => {
  it("updates by the returned id and re-applies the latest filter without duplicate options", async () => {
    const save = vi.spyOn(ldapApi, "presetsSave").mockImplementation(async (preset) => ({ success: true, preset: { ...preset, name: "People updated" } }));
    await mountPresets();
    await presetSelect().setValue(savedPreset.id);
    expect((wrapper.find(".preset-input").element as HTMLInputElement).value).toBe("People");
    await wrapper.find(".qb-value").setValue("new");
    await saveButton().trigger("click");
    await flushPromises();
    expect(save.mock.calls[0][0]).toMatchObject({ id: savedPreset.id, filter: "(uid=new)" });
    expect(save.mock.calls[0][0]).not.toHaveProperty("conditions");
    expect(presetSelect().findAll("option")).toHaveLength(2);
    expect(presetSelect().text()).toContain("People updated");
    await wrapper.find(".qb-value").setValue("unsaved");
    await presetSelect().trigger("change");
    expect(wrapper.find(".qb-preview").text()).toBe("(uid=new)");
  });

  it("uses the server-generated id for the next save", async () => {
    const save = vi.spyOn(ldapApi, "presetsSave").mockImplementation(async (preset) => ({ success: true, preset: { ...preset, id: "server-id" } }));
    await mountPresets([]);
    await wrapper.find(".preset-input").setValue("New preset");
    await saveButton().trigger("click");
    await flushPromises();
    await saveButton().trigger("click");
    await flushPromises();
    expect(save.mock.calls.map(([preset]) => preset.id)).toEqual(["", "server-id"]);
    expect(presetSelect().findAll("option")).toHaveLength(2);
  });

  it("blocks duplicate saves in flight and preserves the draft when saving fails", async () => {
    let reject!: (cause: Error) => void;
    const save = vi.spyOn(ldapApi, "presetsSave").mockImplementation(() => new Promise((_, fail) => { reject = fail; }));
    await mountPresets();
    await presetSelect().setValue(savedPreset.id);
    await saveButton().trigger("click");
    await saveButton().trigger("click");
    expect(save).toHaveBeenCalledOnce();
    expect(presetSelect().attributes("disabled")).toBeDefined();
    reject(new Error("write failed"));
    await flushPromises();
    expect(wrapper.emitted("error")?.at(-1)).toEqual(["write failed"]);
    expect(presetSelect().findAll("option")).toHaveLength(2);
    expect(saveButton().attributes("disabled")).toBeUndefined();
  });

  it("removes only the selected id after the success-only response", async () => {
    vi.stubGlobal("confirm", vi.fn(() => true));
    const remove = vi.spyOn(ldapApi, "presetsRemove").mockResolvedValue({ success: true });
    await mountPresets([savedPreset, { id: "other", name: "Other" }]);
    await presetSelect().setValue(savedPreset.id);
    await removeButton().trigger("click");
    await flushPromises();
    expect(remove).toHaveBeenCalledWith(savedPreset.id);
    expect(presetSelect().findAll("option").map((option) => option.attributes("value"))).toEqual(["", "other"]);
  });

  it("restores protocol defaults and derives the builder from filter, ignoring old fixture conditions", async () => {
    await mountPresets([{ ...savedPreset, conditions: { kind: "clause", attribute: "uid", operator: "equals", value: "stale" } } as LdapSearchPreset]);
    await presetSelect().setValue(savedPreset.id);
    expect(wrapper.find(".qb-preview").text()).toBe("(uid=old)");
    await wrapper.find("form").trigger("submit");
    expect(wrapper.emitted("run")?.[0][0]).toMatchObject({ filter: "(uid=old)", baseDn: "", sizeLimit: "0" });
  });
});
