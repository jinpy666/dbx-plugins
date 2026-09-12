// @vitest-environment happy-dom
// Exercise current host callbacks, not the legacy callbacks exposed by the visual fixture.
import { afterEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { defineComponent, h } from "vue";
import App from "./App.vue";
import { getLdapConnectionId, ldapApi, setLdapConnectionId } from "./lib/api";
import { setWorkbenchLocale, workbenchLocale } from "./lib/i18n";

const context = (id: string) => ({ connectionId: id, connection: { host: id, baseDn: "dc=" + id } });
let wrapper: ReturnType<typeof mount<typeof App>> | undefined;
const treeStub = defineComponent({
  setup(_, { expose }) {
    expose({ refresh: vi.fn(), invalidate: vi.fn() });
    return () => h("div");
  },
});
const searchStub = defineComponent({
  name: "SearchForm",
  props: ["baseDn", "disabled", "running"],
  emits: ["run"],
  setup(_, { expose }) {
    expose({ applyBaseDn: vi.fn() });
    return () => h("div");
  },
});

async function mountWithHost(legacy = false, both = false) {
  let receiveContext: ((next: Record<string, unknown>) => void) | undefined;
  let receiveEvent: ((event: DbxPluginEvent) => void) | undefined;
  const offContext = vi.fn();
  const offEvent = vi.fn();
  const subscribe = vi.fn((listener) => { receiveContext = listener; return offContext; });
  const legacySubscribe = legacy ? subscribe : vi.fn(() => () => undefined);
  const invoke = vi.fn(async (_method: string, _params?: Record<string, unknown>): Promise<unknown> => ({ statuses: [], entries: [], count: 0, truncated: false }));
  window.dbxPlugin = {
    ready: Promise.resolve(context("first")),
    request: vi.fn(async () => context("first")),
    locale: "en",
    invoke,
    ...(legacy ? { onContextChange: subscribe } : { onContext: subscribe }),
    ...(both ? { onContextChange: legacySubscribe } : {}),
    onEvent: (listener: (event: DbxPluginEvent) => void) => { receiveEvent = listener; return offEvent; },
  } as unknown as DbxPluginApi;
  wrapper = mount(App, { global: { stubs: {
    DnTree: treeStub, SearchForm: searchStub, ResultTable: true, EntryEditorDialog: true,
    DeleteEntryDialog: true, ModifyDnDialog: true, NewEntryWizard: true,
    SchemaPanel: true, ConnectionsPanel: true, AuditFeedPanel: true,
  } } });
  await flushPromises();
  return { subscribe, legacySubscribe, offContext, offEvent, invoke,
    updateContext: () => {
      expect(receiveContext).toBeTypeOf("function");
      receiveContext?.(context("second"));
    },
    sendEvent: (event: DbxPluginEvent) => receiveEvent?.(event),
  };
}

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  delete (window as unknown as { dbxPlugin?: unknown }).dbxPlugin;
  setLdapConnectionId("");
  setWorkbenchLocale("zh-CN");
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (cause: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

const searchModel = { baseDn: "dc=first", filter: "(uid=alice)", scope: "sub", attributes: "", sizeLimit: "500", pageSize: "500", typesOnly: false, derefAliases: "never" };
const resultsPane = () => wrapper!.findComponent({ name: "ResultTable" });
const editor = () => wrapper!.findComponent({ name: "EntryEditorDialog" });

describe("App request feedback and recovery", () => {
  it("connects the pending state to form and results, then marks only a successful search as searched", async () => {
    const host = await mountWithHost();
    const pending = deferred<unknown>();
    host.invoke.mockImplementation(() => pending.promise);
    wrapper!.findComponent(searchStub).vm.$emit("run", searchModel);
    await flushPromises();
    expect(wrapper!.findComponent(searchStub).props("running")).toBe(true);
    expect(resultsPane().props()).toMatchObject({ loading: true, searched: false });
    pending.resolve({ entries: [], count: 0, truncated: false });
    await flushPromises();
    expect(resultsPane().props()).toMatchObject({ loading: false, searched: true, error: "" });
  });

  it("retries the same failed search after an actionable error", async () => {
    const host = await mountWithHost();
    host.invoke.mockRejectedValueOnce(new Error("connection refused"));
    wrapper!.findComponent(searchStub).vm.$emit("run", searchModel);
    await flushPromises();
    expect(resultsPane().props("error")).toContain("Cannot reach");
    expect(resultsPane().props("errorDetail")).toBe("connection refused");
    expect(resultsPane().props("searched")).toBe(false);
    resultsPane().vm.$emit("retry");
    await flushPromises();
    expect(host.invoke.mock.calls.at(-1)?.[1]).toMatchObject({ connectionId: "first", filter: searchModel.filter, baseDn: searchModel.baseDn });
    expect(resultsPane().props()).toMatchObject({ error: "", searched: true, loading: false });
  });

  it("ignores a search response from the previous connection and releases its loading state", async () => {
    const host = await mountWithHost();
    const pending = deferred<unknown>();
    host.invoke.mockImplementation(() => pending.promise);
    wrapper!.findComponent(searchStub).vm.$emit("run", searchModel);
    await flushPromises();
    host.updateContext();
    await flushPromises();
    expect(resultsPane().props()).toMatchObject({ entries: [], loading: false, searched: false });
    pending.resolve({ entries: [{ dn: "uid=old,dc=first", attributes: {} }], count: 1, truncated: false });
    await flushPromises();
    expect(resultsPane().props()).toMatchObject({ entries: [], count: 0, searched: false });
  });

  it("opens detail loading/error states immediately and retries the same DN and tab", async () => {
    const host = await mountWithHost();
    const pending = deferred<unknown>();
    const rawObjectClasses = ["( 1.2.3 NAME 'custom' SUP person STRUCTURAL MUST employeeNumber )"];
    host.invoke.mockImplementation(async (method) => method === "ldap/schema"
      ? { attributeTypes: [], objectClasses: rawObjectClasses }
      : pending.promise);
    editor().vm.$emit("openEntry", "cn=alice,dc=first");
    await flushPromises();
    expect(editor().props()).toMatchObject({ open: true, loading: true, requestedDn: "cn=alice,dc=first", initialTab: "assoc" });
    expect(editor().props("schema")).toMatchObject({ rawObjectClasses });
    pending.reject(new Error("connection refused"));
    await flushPromises();
    expect(editor().props("loadError")).toContain("Cannot reach");
    expect(editor().props("loadErrorDetail")).toBe("connection refused");
    expect(editor().props("loading")).toBe(false);
    host.invoke.mockResolvedValue({ entry: { dn: "cn=alice,dc=first", attributes: {} } });
    editor().vm.$emit("retry");
    await flushPromises();
    expect(host.invoke.mock.calls.at(-1)?.[1]).toMatchObject({ dn: "cn=alice,dc=first" });
    expect(editor().props()).toMatchObject({ open: true, loading: false, loadError: "", initialTab: "assoc", entry: { dn: "cn=alice,dc=first" } });
  });

  it.each([false, true])("does not reopen a pending detail after close or connection switch (switch=%s)", async (switchConnection) => {
    const host = await mountWithHost();
    const pending = deferred<unknown>();
    host.invoke.mockImplementation(async (method) => method === "ldap/schema" ? { attributeTypes: [], objectClasses: [] } : pending.promise);
    resultsPane().vm.$emit("open", "cn=alice,dc=first");
    await flushPromises();
    if (switchConnection) host.updateContext();
    else editor().vm.$emit("close");
    await flushPromises();
    pending.resolve({ entry: { dn: "cn=alice,dc=first", attributes: {} } });
    await flushPromises();
    expect(editor().props()).toMatchObject({ open: false, loading: false });
    expect(editor().props("entry")).toBeUndefined();
  });
});

describe("App current host bridge contract", () => {
  it.each([false, true])("follows context changes and sends the updated connectionId (legacy=%s)", async (legacy) => {
    const host = await mountWithHost(legacy);
    expect(getLdapConnectionId()).toBe("first");
    host.updateContext();
    await flushPromises();
    expect(getLdapConnectionId()).toBe("second");
    await ldapApi.search({ filter: "(objectClass=*)", scope: "sub" });
    expect(host.invoke).toHaveBeenLastCalledWith("ldap/search", { connectionId: "second", filter: "(objectClass=*)", scope: "sub" }, undefined);
  });

  it("prefers onContext when both callbacks exist and disposes subscriptions", async () => {
    const host = await mountWithHost(false, true);
    expect(host.subscribe).toHaveBeenCalledOnce();
    expect(host.legacySubscribe).not.toHaveBeenCalled();
    wrapper?.unmount();
    wrapper = undefined;
    expect(host.offContext).toHaveBeenCalledOnce();
    expect(host.offEvent).toHaveBeenCalledOnce();
  });

  it("accepts the native env locale envelope alongside backend audit events", async () => {
    const host = await mountWithHost();
    host.sendEvent({ type: "env", locale: "ja" });
    await flushPromises();
    expect(workbenchLocale.value).toBe("ja");
    host.sendEvent({ method: "ldap/audit", params: { connectionId: "first", action: "modify", target: "cn=a,dc=first", result: "ok" } });
    await flushPromises();
    expect(wrapper?.findComponent({ name: "AuditFeedPanel" }).props("items")).toHaveLength(1);
  });
});
