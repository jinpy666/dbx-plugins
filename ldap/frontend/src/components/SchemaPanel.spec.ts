// @vitest-environment happy-dom
// SchemaPanel workbench UI tests (M5-a UI test track): load-on-open via the
// schema cache, keyword filtering over both columns, empty/error/loading
// states, forced refresh (refresh=true bypass) and close affordances.
// ldapApi is mocked in-file; no host bridge or network is involved.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { ldapApi, type SchemaResult } from "../lib/api";

vi.mock("../lib/api", () => ({
  ldapApi: {
    schema: vi.fn(),
  },
}));

import SchemaPanel from "./SchemaPanel.vue";

const schemaMock = vi.mocked(ldapApi.schema);

const SCHEMA_PAYLOAD: SchemaResult = {
  attributeTypes: ["( 1.2.3 NAME 'cn' DESC 'common name' )", "( 1.2.4 NAME ( 'uid' 'userId' ) )"],
  objectClasses: ["( 1.2.5 NAME 'person' MUST ( cn $ sn ) MAY ( userPassword $ telephoneNumber ) )"],
};

interface PanelProps {
  open: boolean;
  connectionId?: string;
}

function mountPanel(props: PanelProps) {
  return mount(SchemaPanel, { props: { connectionId: "conn-1", ...props } });
}

type PanelWrapper = Awaited<ReturnType<typeof mountPanel>>;

const tracked: PanelWrapper[] = [];

function trackPanel(props: PanelProps): PanelWrapper {
  const wrapper = mountPanel(props);
  tracked.push(wrapper);
  return wrapper;
}

afterEach(() => {
  for (const wrapper of tracked.splice(0)) wrapper.unmount();
});

beforeEach(() => {
  schemaMock.mockReset();
  schemaMock.mockResolvedValue(SCHEMA_PAYLOAD);
});

const searchInput = (wrapper: PanelWrapper) => wrapper.find(".settings-field input");
const refreshButton = (wrapper: PanelWrapper) => wrapper.find(".settings-field .toolbar-button");
const columns = (wrapper: PanelWrapper) => wrapper.findAll(".schema-col");
const pressEscape = () => {
  window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
};

describe("SchemaPanel", () => {
  it("renders nothing and loads nothing while closed", () => {
    const wrapper = trackPanel({ open: false });
    expect(wrapper.find(".modal-backdrop").exists()).toBe(false);
    expect(schemaMock).not.toHaveBeenCalled();
  });

  it("loads the schema on open and renders both columns", async () => {
    const wrapper = trackPanel({ open: true });
    await flushPromises();
    expect(schemaMock).toHaveBeenCalledTimes(1);
    expect(schemaMock).toHaveBeenNthCalledWith(1, false);
    expect(wrapper.find("h2").text()).toBe("Schema");
    expect(columns(wrapper)[0].find("h3").text()).toBe("属性类型 (3)");
    expect(columns(wrapper)[0].findAll("li").map((li) => li.text())).toEqual(["cn", "uid", "userId"]);
    expect(columns(wrapper)[1].find("h3").text()).toBe("对象类 (1)");
    expect(columns(wrapper)[1].find("strong").text()).toBe("person");
    expect(columns(wrapper)[1].findAll(".schema-def")[0].text()).toBe("必填（MUST）: cn, sn");
    expect(columns(wrapper)[1].findAll(".schema-def")[1].text()).toBe("可选（MAY）: userPassword, telephoneNumber");
  });

  it("filters both columns by keyword and shows the no-match fallback for zero hits (P2-4)", async () => {
    const wrapper = trackPanel({ open: true });
    await flushPromises();
    await searchInput(wrapper).setValue("uid");
    // 关键字按属性名（含别名逐个列出）做子串匹配："userid" 不含 "uid"，
    // 只有 uid 本名命中；userId 用 "user" 前缀可查到。
    expect(columns(wrapper)[0].find("h3").text()).toBe("属性类型 (1)");
    expect(columns(wrapper)[0].findAll("li").map((li) => li.text())).toEqual(["uid"]);
    // 对象类列 0 命中时不再误显"Schema 为空"，而是"无匹配"（P2-4）。
    expect(columns(wrapper)[1].text()).toContain("没有匹配“uid”的定义");
    await searchInput(wrapper).setValue("");
    expect(columns(wrapper)[0].findAll("li")).toHaveLength(3);
    expect(columns(wrapper)[1].find("strong").text()).toBe("person");
  });

  it("distinguishes no-match from an empty schema (P2-4)", async () => {
    const wrapper = trackPanel({ open: true });
    await flushPromises();
    await searchInput(wrapper).setValue("zzz");
    expect(columns(wrapper)[0].text()).toContain("没有匹配“zzz”的定义");
    expect(columns(wrapper)[1].text()).toContain("没有匹配“zzz”的定义");
    expect(columns(wrapper)[0].text()).not.toContain("Schema 为空");
  });

  it("shows the empty state in both columns when the server has no definitions", async () => {
    schemaMock.mockResolvedValue({ attributeTypes: [], objectClasses: [] });
    const wrapper = trackPanel({ open: true });
    await flushPromises();
    expect(columns(wrapper)[0].text()).toContain("Schema 为空");
    expect(columns(wrapper)[1].text()).toContain("Schema 为空");
    expect(columns(wrapper)[0].find("h3").text()).toBe("属性类型 (0)");
    expect(columns(wrapper)[1].find("h3").text()).toBe("对象类 (0)");
  });

  it("emits error when the initial load fails", async () => {
    schemaMock.mockRejectedValue(new Error("schema boom"));
    const wrapper = trackPanel({ open: true });
    await flushPromises();
    expect(wrapper.emitted("error")?.[0]).toEqual(["schema boom"]);
    expect(wrapper.find(".modal-backdrop").exists()).toBe(true);
  });

  it("marks the refresh icon while a load is in flight", async () => {
    let resolveSchema!: (value: SchemaResult) => void;
    schemaMock.mockImplementation(() => new Promise<SchemaResult>((resolve) => (resolveSchema = resolve)));
    const wrapper = trackPanel({ open: true });
    // loader 经动态 import 调 ldapApi.schema：先 flushPromises 让请求真正
    // 发起（promise 仍 pending，loading 保持 true），再解析。
    await flushPromises();
    expect(wrapper.find(".spinning").exists()).toBe(true);
    resolveSchema(SCHEMA_PAYLOAD);
    await flushPromises();
    expect(wrapper.find(".spinning").exists()).toBe(false);
  });

  it("forces a server round-trip on the refresh button", async () => {
    const wrapper = trackPanel({ open: true });
    await flushPromises();
    expect(schemaMock).toHaveBeenCalledTimes(1);
    await refreshButton(wrapper).trigger("click");
    await flushPromises();
    expect(schemaMock).toHaveBeenNthCalledWith(2, true);
    expect(schemaMock).toHaveBeenNthCalledWith(3, false);
    expect(columns(wrapper)[0].findAll("li")).toHaveLength(3);
  });

  it("emits error when the forced refresh fails", async () => {
    const wrapper = trackPanel({ open: true });
    await flushPromises();
    schemaMock.mockRejectedValueOnce(new Error("refresh boom"));
    await refreshButton(wrapper).trigger("click");
    await flushPromises();
    expect(wrapper.emitted("error")?.[0]).toEqual(["refresh boom"]);
  });

  it("closes via ✕, footer close and backdrop self-click, not via clicks inside", async () => {
    const wrapper = trackPanel({ open: true });
    await wrapper.find(".modal").trigger("click");
    expect(wrapper.emitted("close")).toBeUndefined();
    await wrapper.find(".icon-button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
    await wrapper.find("footer button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(2);
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(3);
  });

  it("closes via Escape once open", async () => {
    const wrapper = trackPanel({ open: false });
    await wrapper.setProps({ open: true });
    await wrapper.vm.$nextTick();
    pressEscape();
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("close")).toHaveLength(1);
  });
});
