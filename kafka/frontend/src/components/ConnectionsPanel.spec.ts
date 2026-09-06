// @vitest-environment happy-dom
// ConnectionsPanel 组件测试：状态列表（三态圆点/SR/Kerberos/ZK/门禁徽标）、
// 刷新按钮与 disabled 门禁、Esc 关闭 + Tab 焦点陷阱、Confluent properties
// 导入助手（粘贴 → 解析表 → 掩码列 → 清空/Esc 仅关子弹层）。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import ConnectionsPanel from "./ConnectionsPanel.vue";
import { setKafkaConnectionId, type KafkaConnectionStatus } from "../lib/api";
import { t } from "../lib/i18n";

const invokeMock = vi.fn();

/** 安装镜像真实桥形状的 window.dbxPlugin（invoke 按 method 路由）。 */
function installBridge(handler?: (method: string) => unknown) {
  invokeMock.mockReset();
  invokeMock.mockImplementation(async (method: string) => {
    if (handler) return handler(method);
    throw new Error(`unhandled method: ${method}`);
  });
  (window as unknown as { dbxPlugin: unknown }).dbxPlugin = { invoke: invokeMock };
}

function mountPanel(props: Record<string, unknown> = {}, attach = false) {
  return mount(ConnectionsPanel, {
    props: { open: true, ...props },
    attachTo: attach ? document.body : undefined,
  });
}

const baseStatus: KafkaConnectionStatus = {
  connectionId: "conn-test",
  status: "connected",
  lastUsedAt: 1757000000000,
  schemaRegistry: { enabled: true, provider: "confluent" },
};

beforeEach(() => {
  localStorage.clear();
  setKafkaConnectionId("conn-test");
  document.body.innerHTML = "";
});

describe("ConnectionsPanel", () => {
  it("open=false renders nothing", () => {
    const wrapper = mountPanel({ open: false });
    expect(wrapper.find(".modal-backdrop").exists()).toBe(false);
  });

  it("loads and renders status list with state dots and SR/Kerberos badges", async () => {
    installBridge(() => ({
      statuses: [
        { ...baseStatus },
        { ...baseStatus, connectionId: "other", status: "error", lastError: "boom", allowDelete: false, readOnly: true, kerberos: { enabled: true }, connectionSource: "zookeeper", schemaRegistry: { enabled: false } },
      ],
    }));
    const wrapper = mountPanel();
    await flushPromises();
    const items = wrapper.findAll(".settings-list li");
    expect(items).toHaveLength(2);
    // 三态圆点：connected/error/idle
    expect(items[0].find(".state-dot.connected").exists()).toBe(true);
    expect(items[1].find(".state-dot.error").exists()).toBe(true);
    // SR 开关徽标
    expect(items[0].text()).toContain(t("connections.srOn"));
    expect(items[1].text()).toContain(t("connections.srOff"));
    expect(items[1].text()).toContain(t("connections.kerberosOn"));
    expect(items[1].text()).toContain(t("connections.zkSource"));
    // 门禁徽标：readOnly / allowDelete=false
    expect(items[1].text()).toContain(t("readOnly"));
    expect(items[1].find(".badge-danger").text()).toBe(t("connections.noDelete"));
    // lastError 经 friendlyKafkaError 原样兜底
    expect(items[1].text()).toContain("boom");
  });

  it("shows empty state when statuses list is empty", async () => {
    installBridge(() => ({ statuses: [] }));
    const wrapper = mountPanel();
    await flushPromises();
    expect(wrapper.find(".empty").text()).toBe(t("connections.empty"));
  });

  it("emits error when the statuses call rejects", async () => {
    installBridge(() => {
      throw new Error("sidecar gone");
    });
    const wrapper = mountPanel();
    await flushPromises();
    expect(wrapper.emitted("error")?.at(-1)).toEqual(["sidecar gone"]);
  });

  it("refresh button reloads statuses and spins while loading", async () => {
    let release: ((value: unknown) => void) | undefined;
    invokeMock.mockReset();
    invokeMock.mockImplementation(
      () =>
        new Promise((resolve) => {
          release = resolve;
        }),
    );
    (window as unknown as { dbxPlugin: unknown }).dbxPlugin = { invoke: invokeMock };
    const wrapper = mountPanel();
    // 挂载即 load：此时 promise 未决 → loading=true → 图标 spinning、按钮禁用刷新仍可点
    expect(wrapper.find(".icon-button svg.spinning").exists()).toBe(true);
    release?.({ statuses: [baseStatus] });
    await flushPromises();
    expect(wrapper.find(".icon-button svg.spinning").exists()).toBe(false);
    await wrapper.find(".icon-button").trigger("click");
    await flushPromises();
    expect(invokeMock).toHaveBeenCalledTimes(2);
  });

  it("disabled prop blocks loading", async () => {
    installBridge(() => ({ statuses: [baseStatus] }));
    mountPanel({ disabled: true });
    await flushPromises();
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it("close button, footer button and backdrop click emit close", async () => {
    installBridge(() => ({ statuses: [] }));
    const wrapper = mountPanel();
    await flushPromises();
    const closeButtons = wrapper.findAll("button[title='" + t("close") + "']");
    expect(closeButtons.length).toBeGreaterThanOrEqual(1);
    await closeButtons[0].trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(1);
    await wrapper.find("footer button").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(2);
    await wrapper.find(".modal-backdrop").trigger("click");
    expect(wrapper.emitted("close")).toHaveLength(3);
  });

  it("Escape closes the modal and Tab wraps focus within it", async () => {
    installBridge(() => ({ statuses: [] }));
    const wrapper = mountPanel({}, true);
    await flushPromises();
    // Esc → close
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(wrapper.emitted("close")).toHaveLength(1);
    // Tab 焦点陷阱：聚焦最后一个可交互元素后 Tab 回绕到首个（刷新按钮）
    const modal = wrapper.find(".modal").element;
    const focusables = Array.from(modal.querySelectorAll<HTMLElement>("button:not([disabled])"));
    focusables[focusables.length - 1].focus();
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab" }));
    expect((document.activeElement as HTMLElement).getAttribute("title")).toBe(t("refresh"));
  });

  it("renders Glue badges and detail only for the current connection", async () => {
    installBridge(() => ({
      statuses: [
        {
          ...baseStatus,
          schemaRegistry: { enabled: true, provider: "glue" },
          connection: undefined,
        },
        { ...baseStatus, connectionId: "glue-other", schemaRegistry: { enabled: true, provider: "glue" } },
      ],
    }));
    const wrapper = mountPanel({ connection: { glue_region: "us-east-1", glue_registry_name: "reg-1", glue_auth_mode: "IAM", glue_access_key_id: "AKIA01", glue_secret_access_key: "SUPERSECRET" } });
    await flushPromises();
    const items = wrapper.findAll(".settings-list li");
    expect(items[0].text()).toContain(t("connections.glueBadge"));
    expect(items[0].text()).toContain("us-east-1");
    expect(items[0].text()).toContain("reg-1");
    // secret 类字段只显示「已配置/未配置」，不展示值
    expect(items[0].text()).toContain(t("connections.glueConfigured"));
    expect(items[0].text()).toContain(t("connections.glueNotConfigured"));
    expect(items[0].text()).not.toContain("SUPERSECRET");
    // 非当前连接不展示 Glue 详情
    expect(items[1].text()).toContain(t("connections.glueBadge"));
    expect(items[1].text()).not.toContain("us-east-1");
  });

  it("import assistant parses pasted properties into a masked mapping table", async () => {
    installBridge(() => ({ statuses: [] }));
    const wrapper = mountPanel();
    await flushPromises();
    // 打开助手子弹层（teleport 到 body）
    await wrapper.find("button[title='" + t("connections.importAssistant") + "']").trigger("click");
    const assistant = document.querySelector("body > .modal-backdrop .modal") as HTMLElement;
    expect(assistant).not.toBeNull();
    expect(assistant.textContent).toContain(t("connections.importTitle"));
    const textarea = assistant.querySelector("textarea") as HTMLTextAreaElement;
    textarea.value = [
      "bootstrap.servers=localhost:9092",
      "security.protocol=SASL_SSL",
      "sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"u1\" password=\"p1\";",
      "schema.registry.url=http://sr:8081",
    ].join("\n");
    textarea.dispatchEvent(new Event("input"));
    await flushPromises();
    await (assistant.querySelector(".primary-button") as HTMLButtonElement).click();
    await flushPromises();
    const rows = assistant.querySelectorAll("tbody tr");
    expect(rows.length).toBeGreaterThanOrEqual(4);
    // 密码行掩码展示
    const tableText = assistant.querySelector("table")?.textContent ?? "";
    expect(tableText).toContain(t("connections.masked"));
    expect(tableText).toContain("u1");
    // 清空按钮：回到提示态
    await (Array.from(assistant.querySelectorAll("button")).find((b) => b.textContent?.trim() === t("connections.importClear")) as HTMLButtonElement).click();
    await flushPromises();
    expect(assistant.querySelector("table")).toBeNull();
    // 子弹层 Esc 只关子弹层、不透传给连接弹窗
    textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushPromises();
    expect(document.querySelector("body > .modal-backdrop .modal")).toBeNull();
    expect(wrapper.emitted("close")).toBeUndefined();
    // 关闭连接弹窗同时卸载子弹层监听（无残留报错）
    wrapper.unmount();
  });

  it("assistant footer close button closes only the assistant", async () => {
    installBridge(() => ({ statuses: [] }));
    const wrapper = mountPanel();
    await flushPromises();
    await wrapper.find("button[title='" + t("connections.importAssistant") + "']").trigger("click");
    const assistant = document.querySelector("body > .modal-backdrop") as HTMLElement;
    await (assistant.querySelector("footer button") as HTMLButtonElement).click();
    await flushPromises();
    expect(document.querySelector("body > .modal-backdrop")).toBeNull();
    expect(wrapper.emitted("close")).toBeUndefined();
  });
});
