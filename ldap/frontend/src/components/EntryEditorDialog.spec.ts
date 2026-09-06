// @vitest-environment happy-dom
// EntryEditorDialog workbench UI tests (M5-a UI test track): view/add/edit
// forms, attr-row add/remove/edit, LDIF two-way sync + parse errors, save
// gating (readonly / dirty Esc veto / in-flight "…"), close affordances and
// the backdrop-click dirty guard (P1-1: same allowClose path as Esc).
// ldapApi is mocked in-file; no host bridge or network is involved.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { ldapApi, type LdapEntry } from "../lib/api";

vi.mock("../lib/api", () => ({
  ldapApi: {
    entryAdd: vi.fn(),
    entryModify: vi.fn(),
  },
}));

import EntryEditorDialog from "./EntryEditorDialog.vue";

const entryAddMock = vi.mocked(ldapApi.entryAdd);
const entryModifyMock = vi.mocked(ldapApi.entryModify);

// 调用计数跨用例累积会让 not.toHaveBeenCalled 误报：每例前清空 mock。
beforeEach(() => {
  entryAddMock.mockReset();
  entryModifyMock.mockReset();
});

const demoEntry: LdapEntry = {
  dn: "cn=alice,dc=demo,dc=dbx",
  attributes: {
    cn: ["alice"],
    description: ["line1\nline2"],
    objectClass: ["top", "person"],
  },
};

function mountEditor(props: { canWrite: boolean; open: boolean; entry?: LdapEntry; parentDn?: string }) {
  return mount(EntryEditorDialog, { props });
}

type EditorWrapper = Awaited<ReturnType<typeof mountEditor>>;

const tracked: EditorWrapper[] = [];

function trackEditor(props: Parameters<typeof mountEditor>[0]): EditorWrapper {
  const wrapper = mountEditor(props);
  tracked.push(wrapper);
  return wrapper;
}

afterEach(() => {
  for (const wrapper of tracked.splice(0)) wrapper.unmount();
});

const attrRows = (wrapper: EditorWrapper) => wrapper.findAll(".attr-editor .attr-row");
const rdnInput = (wrapper: EditorWrapper) => wrapper.findAll(".attr-row .field input")[0];
const saveButton = (wrapper: EditorWrapper) => wrapper.find("footer .primary-button");
const cancelButton = (wrapper: EditorWrapper) =>
  wrapper.findAll("footer button").find((button) => button.text() === "取消")!;
const pressEscape = () => {
  window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
};

describe("EntryEditorDialog", () => {
  it("renders nothing while closed", () => {
    const wrapper = trackEditor({ canWrite: true, open: false, entry: demoEntry });
    expect(wrapper.find(".modal-backdrop").exists()).toBe(false);
  });

  it("renders view mode with sorted attribute rows, DN, title and multiline hint", () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    expect(wrapper.find(".modal-backdrop").exists()).toBe(true);
    expect(wrapper.find("h2").text()).toBe("条目 · cn=alice");
    expect(wrapper.find(".entry-dn").text()).toBe("cn=alice,dc=demo,dc=dbx");
    expect(attrRows(wrapper)).toHaveLength(3);
    expect((attrRows(wrapper)[0].find("input").element as HTMLInputElement).value).toBe("cn");
    expect((attrRows(wrapper)[0].find("textarea").element as HTMLTextAreaElement).value).toBe("alice");
    expect(wrapper.findAll(".multiline-hint")).toHaveLength(1);
    expect(saveButton(wrapper).text()).toBe("保存");
  });

  it("exposes a read-only mode: hint, disabled controls and no save button", () => {
    const wrapper = trackEditor({ canWrite: false, open: true, entry: demoEntry });
    expect(wrapper.find(".hint").text()).toBe("当前连接为只读");
    for (const input of wrapper.findAll(".attr-editor input")) {
      expect(input.attributes("disabled")).toBeDefined();
    }
    for (const textarea of wrapper.findAll(".attr-editor textarea")) {
      expect(textarea.attributes("disabled")).toBeDefined();
    }
    for (const button of wrapper.findAll(".attr-editor button")) {
      expect(button.attributes("disabled")).toBeDefined();
    }
    expect(wrapper.find(".toolbar-button").attributes("disabled")).toBeDefined();
    expect(wrapper.find("footer .primary-button").exists()).toBe(false);
  });

  it("adds an attribute row and removes a row via the row buttons", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    await wrapper.find(".toolbar-button").trigger("click");
    expect(attrRows(wrapper)).toHaveLength(4);
    const newRow = attrRows(wrapper)[3];
    await newRow.find("input").setValue("mail");
    await newRow.find("textarea").setValue("alice@demo");
    expect((newRow.find("input").element as HTMLInputElement).value).toBe("mail");
    await attrRows(wrapper)[0].find("button").trigger("click");
    expect(attrRows(wrapper)).toHaveLength(3);
    expect((attrRows(wrapper)[0].find("input").element as HTMLInputElement).value).toBe("description");
  });

  it("saves an edited row as a replace change, flags dirty and emits saved", async () => {
    entryModifyMock.mockResolvedValue({ success: true });
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    expect(wrapper.find("footer .muted").exists()).toBe(false);
    await attrRows(wrapper)[1].find("textarea").setValue("new text");
    expect(wrapper.find("footer .muted").text()).toBe("有未保存的修改");
    await saveButton(wrapper).trigger("click");
    await flushPromises();
    expect(entryModifyMock).toHaveBeenCalledWith("cn=alice,dc=demo,dc=dbx", [
      { operation: "replace", attribute: "description", values: ["new text"] },
    ]);
    expect(wrapper.emitted("saved")?.[0]).toEqual(["cn=alice,dc=demo,dc=dbx", "edit"]);
    expect(wrapper.emitted("close")).toBeUndefined();
  });

  it("closes without an API call when nothing changed", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    await saveButton(wrapper).trigger("click");
    await flushPromises();
    expect(entryModifyMock).not.toHaveBeenCalled();
    expect(wrapper.emitted("close")).toHaveLength(1);
    expect(wrapper.emitted("saved")).toBeUndefined();
  });

  it("surfaces API failures via the error event and re-enables the form", async () => {
    entryModifyMock.mockRejectedValue(new Error("modify boom"));
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    await attrRows(wrapper)[0].find("textarea").setValue("alice2");
    await saveButton(wrapper).trigger("click");
    await flushPromises();
    expect(wrapper.emitted("error")?.[0]).toEqual(["modify boom"]);
    expect(saveButton(wrapper).text()).toBe("保存");
    expect(saveButton(wrapper).attributes("disabled")).toBeUndefined();
  });

  it("adds a child entry from the add form with a locked parent DN", async () => {
    entryAddMock.mockResolvedValue({ success: true });
    const wrapper = trackEditor({ canWrite: true, open: true, parentDn: "ou=people,dc=demo,dc=dbx" });
    expect(wrapper.find("h2").text()).toBe("新增子条目");
    const headerInputs = wrapper.findAll(".attr-row .field input");
    expect((headerInputs[1].element as HTMLInputElement).value).toBe("ou=people,dc=demo,dc=dbx");
    expect(headerInputs[1].attributes("disabled")).toBeDefined();
    expect(attrRows(wrapper)).toHaveLength(1);
    expect((attrRows(wrapper)[0].find("input").element as HTMLInputElement).value).toBe("objectClass");
    await rdnInput(wrapper).setValue("cn=bob");
    await saveButton(wrapper).trigger("click");
    await flushPromises();
    expect(entryAddMock).toHaveBeenCalledWith("cn=bob,ou=people,dc=demo,dc=dbx", { objectClass: ["top"] });
    expect(wrapper.emitted("saved")?.[0]).toEqual(["cn=bob,ou=people,dc=demo,dc=dbx", "add"]);
  });

  it("rejects saving an add form without an RDN via the error event", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, parentDn: "ou=people,dc=demo,dc=dbx" });
    await saveButton(wrapper).trigger("click");
    await flushPromises();
    expect(wrapper.emitted("error")?.[0]).toEqual(["新条目 RDN（如 cn=jdoe）"]);
    expect(entryAddMock).not.toHaveBeenCalled();
  });

  it("pre-validates the add-mode RDN client-side and blocks save (P2-6)", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, parentDn: "ou=people,dc=demo,dc=dbx" });
    // 非法 RDN：顶层逗号 / 缺 `=` / 空值段 → 行内红字 + 保存禁用 + 不发请求。
    for (const bad of ["cn=bad,dn", "justname", "cn="]) {
      await rdnInput(wrapper).setValue(bad);
      expect(wrapper.find(".attr-row .form-error").exists()).toBe(true);
      expect(saveButton(wrapper).attributes("disabled")).toBeDefined();
      await saveButton(wrapper).trigger("click");
      await flushPromises();
      expect(entryAddMock).not.toHaveBeenCalled();
    }
    // 合法 RDN 恢复可保存（提示消失）。
    await rdnInput(wrapper).setValue("cn=bob");
    expect(wrapper.find(".attr-row .form-error").exists()).toBe(false);
    expect(saveButton(wrapper).attributes("disabled")).toBeUndefined();
  });

  it("shows the in-flight state (… button, disabled inputs) while saving", async () => {
    entryAddMock.mockImplementation(() => new Promise(() => {}));
    const wrapper = trackEditor({ canWrite: true, open: true, parentDn: "ou=people,dc=demo,dc=dbx" });
    await rdnInput(wrapper).setValue("cn=bob");
    await saveButton(wrapper).trigger("click");
    await wrapper.vm.$nextTick();
    expect(saveButton(wrapper).text()).toBe("…");
    expect(saveButton(wrapper).attributes("disabled")).toBeDefined();
    expect(rdnInput(wrapper).attributes("disabled")).toBeDefined();
  });

  it("switches to LDIF mode and serializes the entry", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    const modeButtons = wrapper.findAll(".mode-switch button");
    expect(modeButtons[0].classes()).toContain("is-active");
    await modeButtons[1].trigger("click");
    const editor = wrapper.find(".ldif-editor");
    expect(editor.exists()).toBe(true);
    const value = (editor.element as HTMLTextAreaElement).value;
    expect(value).toContain("dn: cn=alice,dc=demo,dc=dbx");
    expect(value).toContain("cn: alice");
    expect(wrapper.findAll(".mode-switch button")[1].classes()).toContain("is-active");
    expect(wrapper.find(".attr-editor").exists()).toBe(false);
  });

  it("round-trips an edited LDIF back into the form rows", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    const editor = wrapper.find(".ldif-editor");
    const ldif = (editor.element as HTMLTextAreaElement).value.replace("cn: alice", "cn: alice2");
    await editor.setValue(ldif);
    await wrapper.findAll(".mode-switch button")[0].trigger("click");
    expect(wrapper.find(".ldif-editor").exists()).toBe(false);
    expect(wrapper.find(".form-error").exists()).toBe(false);
    expect((attrRows(wrapper)[0].find("textarea").element as HTMLTextAreaElement).value).toBe("alice2");
  });

  it("blocks the LDIF → form switch on a parse error and keeps LDIF mode", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    await wrapper.find(".ldif-editor").setValue("cn: attribute before any dn");
    await wrapper.findAll(".mode-switch button")[0].trigger("click");
    expect(wrapper.find(".form-error").exists()).toBe(true);
    expect(wrapper.find(".form-error").text()).toContain("LDIF 解析错误");
    expect(wrapper.find(".ldif-editor").exists()).toBe(true);
  });

  it("veto Esc while dirty but still allows explicit ✕ / cancel discard", async () => {
    const wrapper = trackEditor({ canWrite: true, open: false, entry: demoEntry });
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    pressEscape();
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("close")).toHaveLength(1);
    await attrRows(wrapper)[0].find("textarea").setValue("dirty value");
    pressEscape();
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("close")).toHaveLength(1);
    await wrapper.find(".icon-button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(2);
    await cancelButton(wrapper).trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(3);
  });

  it("closes on backdrop self-click while clean, not on clicks inside the modal", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    await wrapper.find(".modal").trigger("click");
    expect(wrapper.emitted("close")).toBeUndefined();
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
  });

  it("vetoes backdrop self-click while dirty so edits survive (same guard as Esc)", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, entry: demoEntry });
    await attrRows(wrapper)[1].find("textarea").setValue("dirty value");
    await wrapper.find(".modal-backdrop").trigger("click");
    // P1-1：遮罩点击与 Esc 走同一条 allowClose 守卫——dirty 否决，弹窗保持
    // 打开且修改原样保留；撤回修改后遮罩点击恢复关闭。
    expect(wrapper.emitted("close")).toBeUndefined();
    expect((attrRows(wrapper)[1].find("textarea").element as HTMLTextAreaElement).value).toBe("dirty value");
    await attrRows(wrapper)[1].find("textarea").setValue("line1\nline2");
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
  });

  it("veto backdrop click in add mode with a half-typed RDN", async () => {
    const wrapper = trackEditor({ canWrite: true, open: true, parentDn: "ou=people,dc=demo,dc=dbx" });
    await rdnInput(wrapper).setValue("cn=bo");
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toBeUndefined();
    expect((rdnInput(wrapper).element as HTMLInputElement).value).toBe("cn=bo");
  });
});
