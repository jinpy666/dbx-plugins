// @vitest-environment happy-dom
// ModifyDnDialog workbench UI tests (M5-a UI test track): RDN/parent drafts,
// deleteOldRdn checkbox, confirm payload (trim + undefined parent), Enter
// submit, empty-RDN gating, in-flight state with Esc veto and close paths.
import { afterEach, describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import ModifyDnDialog from "./ModifyDnDialog.vue";

const DN = "cn=old,dc=demo,dc=dbx";
const BASE_DN = "dc=demo,dc=dbx";

interface DialogProps {
  open: boolean;
  dn?: string;
  baseDn?: string;
  submitting?: boolean;
}

function mountDialog(props: DialogProps) {
  return mount(ModifyDnDialog, { props: { baseDn: BASE_DN, ...props } });
}

type DialogWrapper = Awaited<ReturnType<typeof mountDialog>>;

const tracked: DialogWrapper[] = [];

function trackDialog(props: DialogProps): DialogWrapper {
  const wrapper = mountDialog(props);
  tracked.push(wrapper);
  return wrapper;
}

afterEach(() => {
  for (const wrapper of tracked.splice(0)) wrapper.unmount();
});

const textInputs = (wrapper: DialogWrapper) => wrapper.findAll('input[type="text"]');
const rdnInput = (wrapper: DialogWrapper) => textInputs(wrapper)[0];
const parentInput = (wrapper: DialogWrapper) => textInputs(wrapper)[1];
const checkbox = (wrapper: DialogWrapper) => wrapper.find('input[type="checkbox"]');
const confirmButton = (wrapper: DialogWrapper) => wrapper.find("footer .primary-button");
const cancelButton = (wrapper: DialogWrapper) =>
  wrapper.findAll("footer button").find((button) => button.text() === "取消")!;
const pressEscape = () => {
  window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
};

describe("ModifyDnDialog", () => {
  it("renders nothing while closed", () => {
    const wrapper = trackDialog({ open: false, dn: DN });
    expect(wrapper.find(".modal-backdrop").exists()).toBe(false);
  });

  it("seeds the RDN draft from the DN and renders parent placeholder + checkbox", () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    expect(wrapper.find("h2").text()).toBe("修改 DN");
    expect(wrapper.find(".entry-dn").text()).toBe(DN);
    expect((rdnInput(wrapper).element as HTMLInputElement).value).toBe("cn=old");
    expect((parentInput(wrapper).element as HTMLInputElement).value).toBe("");
    expect(parentInput(wrapper).attributes("placeholder")).toBe(BASE_DN);
    expect((checkbox(wrapper).element as HTMLInputElement).checked).toBe(true);
    expect(confirmButton(wrapper).text()).toBe("确认");
  });

  it("emits confirm with the trimmed RDN and no parent when unchanged", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await rdnInput(wrapper).setValue("cn=new");
    await confirmButton(wrapper).trigger("click");
    expect(wrapper.emitted("confirm")?.[0]).toEqual(["cn=new", undefined, true]);
  });

  it("carries an optional new parent DN into the confirm payload", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await rdnInput(wrapper).setValue("cn=new");
    await parentInput(wrapper).setValue("ou=people,dc=demo,dc=dbx");
    await confirmButton(wrapper).trigger("click");
    expect(wrapper.emitted("confirm")?.[0]).toEqual(["cn=new", "ou=people,dc=demo,dc=dbx", true]);
  });

  it("keeps the old RDN when the deleteOldRdn checkbox is unchecked", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await checkbox(wrapper).setValue(false);
    await confirmButton(wrapper).trigger("click");
    expect(wrapper.emitted("confirm")?.[0]).toEqual(["cn=old", undefined, false]);
  });

  it("gates confirm on an empty RDN (including whitespace-only)", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await rdnInput(wrapper).setValue("");
    expect(confirmButton(wrapper).attributes("disabled")).toBeDefined();
    await confirmButton(wrapper).trigger("click");
    expect(wrapper.emitted("confirm")).toBeUndefined();
    await rdnInput(wrapper).setValue("   ");
    expect(confirmButton(wrapper).attributes("disabled")).toBeDefined();
    await rdnInput(wrapper).setValue(" cn=x ");
    expect(confirmButton(wrapper).attributes("disabled")).toBeUndefined();
    await confirmButton(wrapper).trigger("click");
    expect(wrapper.emitted("confirm")?.[0]).toEqual(["cn=x", undefined, true]);
  });

  it("submits on Enter inside the RDN field", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await rdnInput(wrapper).setValue("cn=enter");
    await rdnInput(wrapper).trigger("keydown.enter");
    expect(wrapper.emitted("confirm")?.[0]).toEqual(["cn=enter", undefined, true]);
  });

  it("disables confirm with an ellipsis while submitting and vetoes Esc", async () => {
    const wrapper = trackDialog({ open: false, dn: DN, submitting: true });
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    expect(confirmButton(wrapper).text()).toBe("…");
    expect(confirmButton(wrapper).attributes("disabled")).toBeDefined();
    pressEscape();
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("close")).toBeUndefined();
    await wrapper.find(".icon-button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
  });

  it("closes via ✕, cancel, backdrop self-click and Escape", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await wrapper.find(".icon-button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
    await cancelButton(wrapper).trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(2);
    await wrapper.find(".modal").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(2);
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(3);
    pressEscape();
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("close")).toHaveLength(4);
  });

  it("resets drafts when reopened with the same DN", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await rdnInput(wrapper).setValue("cn=renamed");
    await parentInput(wrapper).setValue("ou=other,dc=demo,dc=dbx");
    await checkbox(wrapper).setValue(false);
    await wrapper.setProps({ open: false });
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    expect((rdnInput(wrapper).element as HTMLInputElement).value).toBe("cn=old");
    expect((parentInput(wrapper).element as HTMLInputElement).value).toBe("");
    expect((checkbox(wrapper).element as HTMLInputElement).checked).toBe(true);
  });

describe("ModifyDnDialog RDN precheck (P2-20)", () => {
  it("shows an inline error and disables confirm for a malformed RDN", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await rdnInput(wrapper).setValue("cn=bad,dn");
    expect(wrapper.find(".form-error").exists()).toBe(true);
    expect(wrapper.find("footer .primary-button").attributes("disabled")).toBeDefined();
  });

  it("keeps confirm enabled for a well-formed RDN", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await rdnInput(wrapper).setValue("cn=renamed");
    expect(wrapper.find(".form-error").exists()).toBe(false);
    expect(wrapper.find("footer .primary-button").attributes("disabled")).toBeUndefined();
  });
});
});
