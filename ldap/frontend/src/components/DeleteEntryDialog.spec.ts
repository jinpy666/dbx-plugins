// @vitest-environment happy-dom
// DeleteEntryDialog workbench UI tests (M5-a UI test track): destructive
// confirm copy, in-flight disable ("…") with Esc veto, and every close
// affordance (✕ / cancel / backdrop self-click / Escape).
import { afterEach, describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import DeleteEntryDialog from "./DeleteEntryDialog.vue";

const DN = "cn=alice,dc=demo,dc=dbx";

function mountDialog(props: { open: boolean; dn?: string; submitting?: boolean }) {
  return mount(DeleteEntryDialog, { props });
}

type DialogWrapper = Awaited<ReturnType<typeof mountDialog>>;

const tracked: DialogWrapper[] = [];

function trackDialog(props: Parameters<typeof mountDialog>[0]): DialogWrapper {
  const wrapper = mountDialog(props);
  tracked.push(wrapper);
  return wrapper;
}

afterEach(() => {
  for (const wrapper of tracked.splice(0)) wrapper.unmount();
});

const pressEscape = () => {
  window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
};

describe("DeleteEntryDialog", () => {
  it("renders nothing while closed", () => {
    const wrapper = trackDialog({ open: false, dn: DN });
    expect(wrapper.find(".modal-backdrop").exists()).toBe(false);
  });

  it("renders title, RDN label, destructive copy and full DN", () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    expect(wrapper.find("h2").text()).toBe("删除 LDAP 条目");
    expect(wrapper.find("strong.mono").text()).toBe("cn=alice");
    expect(wrapper.find(".destructive-copy p").text()).toBe("确认删除以下条目？此操作不可撤销。");
    expect(wrapper.find(".entry-dn").text()).toBe(DN);
    expect(wrapper.find(".danger-button").text()).toBe("删除");
  });

  it("falls back to an empty label when no DN is given", () => {
    const wrapper = trackDialog({ open: true });
    expect(wrapper.find("strong.mono").text()).toBe("");
    expect(wrapper.find(".entry-dn").text()).toBe("");
  });

  it("emits confirm from the danger button", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await wrapper.find(".danger-button").trigger("click");
    expect(wrapper.emitted("confirm")).toHaveLength(1);
    expect(wrapper.find(".danger-button").attributes("disabled")).toBeUndefined();
  });

  it("closes via ✕, cancel and backdrop self-click, not via clicks inside", async () => {
    const wrapper = trackDialog({ open: true, dn: DN });
    await wrapper.find(".modal").trigger("click");
    expect(wrapper.emitted("close")).toBeUndefined();
    await wrapper.find(".icon-button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
    await wrapper.findAll("footer button").find((button) => button.text() === "取消")!.trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(2);
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(3);
  });

  it("closes via Escape once open", async () => {
    const wrapper = trackDialog({ open: false, dn: DN });
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    pressEscape();
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("close")).toHaveLength(1);
  });

  it("disables the danger button with an ellipsis while submitting and vetoes Esc", async () => {
    const wrapper = trackDialog({ open: false, dn: DN, submitting: true });
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    const danger = wrapper.find(".danger-button");
    expect(danger.text()).toBe("…");
    expect(danger.attributes("disabled")).toBeDefined();
    pressEscape();
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("close")).toBeUndefined();
    // Explicit close affordances stay available while the request is in flight.
    await wrapper.find(".icon-button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
  });

  it("focuses the cancel button on open, not the header ✕ (P2-11)", async () => {
    // initialFocus 走 useModalA11y 的 document 级容器查找：必须真实挂载。
    const wrapper = mount(DeleteEntryDialog, { attachTo: document.body, props: { open: false, dn: DN } });
    tracked.push(wrapper);
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const active = document.activeElement;
    expect(active).not.toBeNull();
    expect(active!.textContent).toContain("取消");
    expect(active!.className).not.toContain("icon-button");
  });

  it("vetoes the backdrop click while submitting (shared guard with Esc)", async () => {
    const wrapper = trackDialog({ open: false, dn: DN, submitting: true });
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toBeUndefined();
  });
});
