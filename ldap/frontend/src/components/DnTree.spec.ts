// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { defineComponent, h } from "vue";
import { ldapApi, type LdapSearchResult } from "../lib/api";
import DnTree from "./DnTree.vue";

vi.mock("../lib/api", () => ({ ldapApi: { search: vi.fn(), count: vi.fn() } }));

// Only replace viewport measurement; exercise the real tree rows and API flow.
const ListStub = defineComponent({
  props: ["items"],
  setup: (props, { slots }) => () => h("div", props.items.map((item: unknown, index: number) => slots.default?.({ item, index }))),
});
const search = vi.mocked(ldapApi.search);
const count = vi.mocked(ldapApi.count);
const baseDn = "dc=demo,dc=dbx";
const hit = { dn: "cn=alice," + baseDn, attributes: { cn: ["alice"] } };
let wrapper: ReturnType<typeof mount<typeof DnTree>>;

beforeEach(() => {
  vi.useFakeTimers();
  search.mockReset();
  count.mockReset().mockResolvedValue({ count: 0 });
  wrapper = mount(DnTree, {
    props: { baseDn, canWrite: true },
    global: { stubs: { VirtualList: ListStub } },
  });
});
afterEach(() => {
  wrapper.unmount();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("DnTree keyboard navigation through virtual rows", () => {
  async function useRealList() {
    wrapper.unmount();
    vi.stubGlobal("ResizeObserver", class {
      constructor(private callback: ResizeObserverCallback) {}
      observe() { this.callback([{ contentRect: { height: 84 } } as ResizeObserverEntry], this as unknown as ResizeObserver); }
      disconnect() {}
    });
    wrapper = mount(DnTree, { props: { baseDn, canWrite: true }, attachTo: document.body });
    await wrapper.vm.refresh();
    await flushPromises();
  }

  async function press(key: string) {
    const row = wrapper.findAll(".tree-node").find((item) => item.element === document.activeElement);
    // Vue's trigger also advances the event timestamp past listeners attached
    // at the frozen fake-timer time, allowing child -> parent bubbling.
    await row?.trigger("keydown", { key });
    await flushPromises();
  }

  const focusedDn = () => document.activeElement?.getAttribute("title");

  it("enters children and returns to parents without toggling an already requested state", async () => {
    const parent = { dn: "ou=people," + baseDn, attributes: {} };
    const child = { dn: "cn=alice," + parent.dn, attributes: {} };
    search.mockResolvedValueOnce({ entries: [parent], count: 1, truncated: false });
    search.mockResolvedValueOnce({ entries: [child], count: 1, truncated: false });
    count.mockResolvedValue({ count: 1 });
    await useRealList();
    (wrapper.find(".tree-node").element as HTMLElement).focus();
    await press("ArrowRight");
    expect(focusedDn()).toBe(parent.dn);
    await press("ArrowRight"); // load children, retaining parent focus
    expect(focusedDn()).toBe(parent.dn);
    await press("ArrowRight");
    expect(focusedDn()).toBe(child.dn);
    await press("ArrowLeft");
    expect(focusedDn()).toBe(parent.dn);
    await press("ArrowLeft"); // collapse
    expect(focusedDn()).toBe(parent.dn);
    expect(wrapper.findAll(".tree-node")).toHaveLength(2);
    await press("ArrowLeft");
    expect(focusedDn()).toBe(baseDn);
    await press("Enter");
    await press(" ");
    expect(wrapper.emitted("select")?.slice(-2)).toEqual([[baseDn], [baseDn]]);
  });

  it("uses Home/End and adjacent arrows across unmounted rows while keeping a bounded DOM", async () => {
    const entries = Array.from({ length: 100 }, (_, index) => ({ dn: `cn=user${index},${baseDn}`, attributes: {} }));
    search.mockResolvedValue({ entries, count: entries.length, truncated: false });
    count.mockResolvedValue({ count: entries.length });
    await useRealList();
    (wrapper.find(".tree-node").element as HTMLElement).focus();
    await press("End");
    expect(focusedDn()).toBe(entries[99].dn);
    await press("ArrowUp");
    expect(focusedDn()).toBe(entries[98].dn);
    await press("Home");
    expect(focusedDn()).toBe(baseDn);
    for (let index = 0; index < 30; index++) await press("ArrowDown");
    expect(focusedDn()).toBe(entries[29].dn);
    expect(wrapper.findAll(".tree-node").length).toBeLessThan(25);
  });

  it("navigates the full filtered list and ignores hierarchy keys there", async () => {
    const entries = Array.from({ length: 100 }, (_, index) => ({ dn: `cn=user${index},${baseDn}`, attributes: {} }));
    search.mockResolvedValue({ entries, count: entries.length, truncated: false });
    await useRealList();
    await filter("user");
    (wrapper.find(".tree-node").element as HTMLElement).focus();
    await press("End");
    expect(focusedDn()).toBe(entries[99].dn);
    await press("ArrowLeft");
    expect(focusedDn()).toBe(entries[99].dn);
    await press("Home");
    expect(focusedDn()).toBe(entries[0].dn);
    expect(wrapper.findAll(".tree-node").length).toBeLessThan(25);
  });
});

async function filter(keyword: string) {
  await wrapper.find(".tree-filter input").setValue(keyword);
  await vi.advanceTimersByTimeAsync(300);
  await flushPromises();
}

describe("DnTree recoverable states", () => {
  it("retries a failed remote filter without losing the keyword", async () => {
    search.mockRejectedValueOnce(new Error("connection refused"));
    search.mockResolvedValueOnce({ entries: [hit], count: 1, truncated: false });
    await filter("alice");
    expect(wrapper.find(".tree-error").attributes("role")).toBe("alert");
    expect(wrapper.find(".tree-error").text()).toContain("无法连接");
    await wrapper.find(".tree-error button").trigger("click");
    await flushPromises();
    expect(search).toHaveBeenCalledTimes(2);
    expect(search.mock.calls[1][0]).toEqual(search.mock.calls[0][0]);
    expect((wrapper.find(".tree-filter input").element as HTMLInputElement).value).toBe("alice");
    expect(wrapper.find(".tree-error").exists()).toBe(false);
    expect(wrapper.find(".tree-node").attributes("title")).toBe(hit.dn);
  });

  it("refreshes the active filter instead of reloading the hidden root tree", async () => {
    search.mockRejectedValueOnce(new Error("connection refused"));
    search.mockResolvedValueOnce({ entries: [], count: 0, truncated: false });
    await filter("alice");
    await wrapper.find(".panel-header button").trigger("click");
    await flushPromises();
    expect(search.mock.calls[1][0]).toEqual(search.mock.calls[0][0]);
    expect(wrapper.find(".tree-error").exists()).toBe(false);
  });

  it("announces loading separately from no matches", async () => {
    let resolve!: (value: LdapSearchResult) => void;
    search.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    await filter("nobody");
    expect(wrapper.find("[role='tree']").attributes("aria-busy")).toBe("true");
    expect(wrapper.find("[role='status']").text()).toContain("加载");
    resolve({ entries: [], count: 0, truncated: false });
    await flushPromises();
    expect(wrapper.find("[role='tree']").attributes("aria-busy")).toBe("false");
    expect(wrapper.find("[role='status']").text()).toContain("nobody");
  });

  it("keeps treeitem semantics and selected state in the filtered view", async () => {
    search.mockResolvedValueOnce({ entries: [hit], count: 1, truncated: false });
    await filter("alice");
    const row = wrapper.find(".tree-node");
    expect(row.attributes("role")).toBe("treeitem");
    expect(row.attributes("aria-level")).toBe("1");
    expect(row.attributes("aria-selected")).toBe("false");
    await row.trigger("click");
    expect(row.attributes("aria-selected")).toBe("true");
  });

  it("does not show an older response as the new keyword's result during debounce", async () => {
    let resolve!: (value: LdapSearchResult) => void;
    search.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    await filter("alice");
    await wrapper.find(".tree-filter input").setValue("bob");
    resolve({ entries: [hit], count: 1, truncated: false });
    await flushPromises();
    expect(wrapper.find("[role='tree']").attributes("aria-busy")).toBe("true");
    expect(wrapper.find(".tree-node").exists()).toBe(false);
    search.mockResolvedValueOnce({ entries: [], count: 0, truncated: false });
    await vi.advanceTimersByTimeAsync(300);
    await flushPromises();
    expect(wrapper.find("[role='status']").text()).toContain("bob");
  });

  it("cancels a pending debounced search when the tree unmounts", async () => {
    await wrapper.find(".tree-filter input").setValue("alice");
    wrapper.unmount();
    await vi.advanceTimersByTimeAsync(300);
    expect(search).not.toHaveBeenCalled();
  });
});
