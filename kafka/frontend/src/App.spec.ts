// @vitest-environment happy-dom
// App 壳层 a11y 测试（UI 扫描第 4 轮 P2-24 防回归）：
// 错误横幅 role="alert"、成功通知 role="status" + aria-live="polite"——
// 异步到达的错误/成功反馈对读屏可感知（与 ProducePanel 成功条约定收敛）。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import App from "./App.vue";
import MessagesPanel from "./components/MessagesPanel.vue";
import { setKafkaConnectionId } from "./lib/api";

const invokeMock = vi.fn();
let eventListener: ((event: { method: string; params: Record<string, unknown> }) => void) | null = null;

function installHostBridge() {
  invokeMock.mockReset();
  invokeMock.mockImplementation(async (method: string) => {
    if (method === "kafka/topics/list") return { topics: [] };
    if (method === "kafka/connections/statuses") return { statuses: [] };
    if (method === "kafka/presets/list") return { presets: [] };
    throw new Error(`unhandled method: ${method}`);
  });
  eventListener = null;
  (window as unknown as { dbxPlugin: unknown }).dbxPlugin = {
    // ready 永不 resolve：走 Promise.any 兜底的 host.getContext（与真实宿主慢启动同型）
    ready: new Promise(() => {}),
    request: async (method: string) => {
      if (method === "host.getContext") return { connectionId: "conn-test" };
      return {};
    },
    invoke: invokeMock,
    locale: "zh-CN",
    onEvent: (listener: (event: { method: string; params: Record<string, unknown> }) => void) => {
      eventListener = listener;
      return () => {};
    },
  };
}

async function mountApp() {
  const wrapper = mount(App);
  await flushPromises();
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  localStorage.clear();
  setKafkaConnectionId("conn-test");
  installHostBridge();
});

describe("App a11y live regions (P2-24)", () => {
  it("announces async errors via role=alert on the error banner", async () => {
    const wrapper = await mountApp();
    expect(wrapper.find(".error-banner").exists()).toBe(false);
    // 异步到达的错误（审计 denied/error 事件走 showError）
    eventListener?.({ method: "kafka/audit", params: { action: "kafka/test", result: "denied", detail: "boom" } });
    await flushPromises();
    const banner = wrapper.find(".error-banner");
    expect(banner.exists()).toBe(true);
    expect(banner.attributes("role")).toBe("alert");
    wrapper.unmount();
  });

  it("announces success notices via role=status with aria-live=polite", async () => {
    const wrapper = await mountApp();
    expect(wrapper.find(".notice").exists()).toBe(false);
    // 面板成功通知（@notify → showNotice），经 MessagesPanel 子组件实例触发
    wrapper.findComponent(MessagesPanel).vm.$emit("notify", "已保存");
    await flushPromises();
    const notice = wrapper.find(".notice");
    expect(notice.exists()).toBe(true);
    expect(notice.text()).toBe("已保存");
    expect(notice.attributes("role")).toBe("status");
    expect(notice.attributes("aria-live")).toBe("polite");
    wrapper.unmount();
  });
});
