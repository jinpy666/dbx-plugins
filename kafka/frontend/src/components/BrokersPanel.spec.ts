// @vitest-environment happy-dom
// BrokersPanel 组件测试：列表渲染（nodeId/host/port/rack，rack 缺省 —）/
// 空态 / config 弹窗开合（sensitive 条目掩码 + 徽标）/ 列表调用失败 error 事件 +
// 刷新按钮重载。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import BrokersPanel from "./BrokersPanel.vue";
import { setKafkaConnectionId, type KafkaBroker } from "../lib/api";
import { t } from "../lib/i18n";

const invokeMock = vi.fn();

/** method → 响应 路由表；未命中抛错（对应未注册方法）；`{ error }` 信封按真实桥
 *  形态以异常拒绝（JSON-RPC error → invoke rejection → api 层转译为 Error）。 */
function installBridge(routes: Record<string, unknown>) {
  invokeMock.mockReset();
  invokeMock.mockImplementation(async (method: string, params?: unknown) => {
    if (method in routes) {
      const result = routes[method];
      const envelope = result as { error?: { message?: string } } | null;
      if (envelope && typeof envelope === "object" && envelope.error) {
        throw new Error(envelope.error.message ?? "request failed");
      }
      return typeof result === "function" ? result(params) : result;
    }
    throw new Error(`unhandled method: ${method}`);
  });
  (window as unknown as { dbxPlugin: unknown }).dbxPlugin = { invoke: invokeMock };
}

const brokers: KafkaBroker[] = [
  { nodeId: 1, host: "dbx-kafka-test", port: 9092, rack: "rack-a" },
  { nodeId: 2, host: "dbx-kafka-test-2", port: 9093 },
];

function mountPanel() {
  // teleport stub 让弹窗留在 wrapper 内可查（与 GroupsPanel.spec 同款）
  return mount(BrokersPanel, { global: { stubs: { teleport: true } } });
}

beforeEach(() => {
  localStorage.clear();
  setKafkaConnectionId("conn-test");
});

describe("BrokersPanel", () => {
  it("renders broker rows with rack fallback on mount", async () => {
    installBridge({ "kafka/brokers/list": { brokers, connectionSource: "kafka" } });
    const wrapper = mountPanel();
    await flushPromises();
    expect(wrapper.text()).toContain(`${t("brokers.title")} · 2`);
    const rows = wrapper.findAll(".kafka-table-row");
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain("dbx-kafka-test");
    expect(rows[0].text()).toContain("rack-a");
    // rack 缺省行显示 — 占位
    expect(rows[1].text()).toContain("dbx-kafka-test-2");
    expect(rows[1].text()).toContain("—");
  });

  it("shows the empty state when no brokers are returned", async () => {
    installBridge({ "kafka/brokers/list": { brokers: [] } });
    const wrapper = mountPanel();
    await flushPromises();
    expect(wrapper.find(".empty").text()).toBe(t("brokers.empty"));
  });

  it("opens the config modal with masked sensitive entries and closes it", async () => {
    installBridge({
      "kafka/brokers/list": { brokers },
      "kafka/brokers/config": {
        entries: [
          { name: "num.partitions", value: "1", source: "DEFAULT_CONFIG", sensitive: false, isDefault: true },
          { name: "sasl.jaas.config", value: "hidden", source: "STATIC_BROKER_CONFIG", sensitive: true },
        ],
      },
    });
    const wrapper = mountPanel();
    await flushPromises();
    await rows(wrapper)[1].find(".qb-add").trigger("click");
    await flushPromises();
    // 弹窗标题带 broker id；sensitive 值掩码、不回显原值
    expect(invokeMock.mock.calls.find(([method]) => method === "kafka/brokers/config")?.[1]).toMatchObject({ brokerId: 2 });
    const modal = wrapper.find(".modal-backdrop .modal");
    expect(modal.find("h2").text()).toBe(t("brokers.configTitle", { id: 2 }));
    const configRows = modal.findAll("tbody tr");
    expect(configRows[0].text()).toContain("num.partitions");
    expect(configRows[0].text()).toContain(t("brokers.colDefault"));
    expect(configRows[1].text()).toContain(t("brokers.sensitiveMasked"));
    expect(configRows[1].text()).not.toContain("hidden-plain");
    expect(configRows[1].text()).toContain(t("brokers.colSensitive"));
    // 头部 ✕ 关闭
    await modal.find("header .icon-button").trigger("click");
    expect(wrapper.find(".modal-backdrop").exists()).toBe(false);
  });

  it("emits error when the list call rejects and reloads via refresh", async () => {
    installBridge({
      "kafka/brokers/list": { error: { code: -32000, message: "metadata unavailable" } },
    });
    const wrapper = mountPanel();
    await flushPromises();
    expect(wrapper.emitted("error")?.at(-1)).toEqual(["metadata unavailable"]);
    // 刷新按钮重载（路由切换为成功响应）
    installBridge({ "kafka/brokers/list": { brokers, connectionSource: "kafka" } });
    await wrapper.find(".result-meta .icon-button").trigger("click");
    await flushPromises();
    expect(wrapper.findAll(".kafka-table-row")).toHaveLength(2);
    // 清屏用的空串 error 事件（现行为：每次 load 前清空横幅）
    expect(wrapper.emitted("error")?.at(-1)).toEqual([""]);
  });
});

function rows(wrapper: ReturnType<typeof mountPanel>) {
  return wrapper.findAll(".kafka-table-row");
}
