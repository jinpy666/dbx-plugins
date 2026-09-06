// @vitest-environment happy-dom
// MessagesPanel 组件测试（UI 扫描第 4 轮防回归）：
// P1-6 offset 范围输入（number v-model → optionalNumber String 归一）真正发请求；
// P1-7 慢响应竞态——在途切换 topic 后旧响应丢弃、新 topic 可立即重发且结果落地；
// P2-21 空态两态——未选 topic 与已选 topic 的文案区分。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { defineComponent, h, type PropType } from "vue";
import MessagesPanel from "./MessagesPanel.vue";
import { setKafkaConnectionId, type ConsumeResult, type KafkaMessage } from "../lib/api";
import { t } from "../lib/i18n";

// -- DbxAgGrid 轻量 stub（镜像真实桥形状，见 GroupsPanel.spec 同款） -----------------
let goToLatestCalls = 0;
const DbxAgGridStub = defineComponent({
  name: "DbxAgGridStub",
  props: {
    rowData: { type: Array as PropType<unknown[]>, default: () => [] },
    tableKey: { type: String, default: "" },
    rowSelection: { type: [String, Boolean] as PropType<"single" | false>, default: "single" as const },
    emitRowClick: { type: Boolean, default: true },
  },
  emits: ["selection-changed", "row-click"],
  setup(props, { emit, expose }) {
    expose({
      goToLatest: () => {
        goToLatestCalls += 1;
      },
    });
    return () =>
      h(
        "div",
        { class: "grid-stub", "data-key": props.tableKey },
        (props.rowData ?? []).map((row, index) =>
          h(
            "button",
            {
              type: "button",
              class: "grid-stub-row",
              onClick: () => {
                if (props.rowSelection) emit("selection-changed", row);
                if (props.emitRowClick) emit("row-click", row);
              },
            },
            `${props.tableKey}-row-${index}`,
          ),
        ),
      );
  },
});

const invokeMock = vi.fn();

function installBridge(routes: Record<string, unknown>) {
  invokeMock.mockReset();
  invokeMock.mockImplementation(async (method: string) => {
    if (method in routes) {
      const result = routes[method];
      const envelope = result as { error?: { message?: string } } | null;
      if (envelope && typeof envelope === "object" && envelope.error) {
        throw new Error(envelope.error.message ?? "request failed");
      }
      return result;
    }
    throw new Error(`unhandled method: ${method}`);
  });
  (window as unknown as { dbxPlugin: unknown }).dbxPlugin = { invoke: invokeMock };
}

function mountPanel(props: Record<string, unknown> = {}) {
  return mount(MessagesPanel, {
    props: { topic: "order-events", canWrite: true, ...props },
    global: { stubs: { DbxAgGrid: DbxAgGridStub, teleport: true } },
  });
}

function consumeResult(topic: string, offset: number): ConsumeResult {
  const message: KafkaMessage = { topic, partition: 0, offset, timestamp: 1_700_000_000_000, valueText: `{"v":"${topic}-${offset}"}` };
  return { messages: [message], scanned: 3, matched: 3, limited: false, hasMore: false };
}

beforeEach(() => {
  localStorage.clear();
  setKafkaConnectionId("conn-test");
  goToLatestCalls = 0;
});

describe("MessagesPanel", () => {
  // P1-6：<input type="number"> 的 v-model 赋 number，optionalNumber 直接 .trim()
  // 曾抛 TypeError（「value.trim is not a function」）导致消费请求根本不发出。
  it("consumes with an offset range and forwards offsetFrom/offsetTo as numbers (P1-6)", async () => {
    installBridge({
      "kafka/presets/list": { presets: [] },
      "kafka/messages/consume": { messages: [], scanned: 0, matched: 0, limited: false, hasMore: false },
    });
    const wrapper = mountPanel();
    await flushPromises();
    const numberInputs = wrapper.findAll('input[type="number"]');
    // 基础组 limit/timeout/maxScanRecords + 时间与范围组 offsetFrom/offsetTo
    expect(numberInputs).toHaveLength(5);
    await numberInputs[3].setValue(1);
    await numberInputs[4].setValue(2);
    await wrapper.find(".form-footer .primary-button").trigger("click");
    await flushPromises();
    const consumeCall = invokeMock.mock.calls.find(([method]) => method === "kafka/messages/consume");
    expect(consumeCall?.[1]).toMatchObject({ topic: "order-events", offsetFrom: 1, offsetTo: 2 });
    // 不再有内部异常透传到错误横幅（error 事件仅允许出现清屏用的空串）
    expect((wrapper.emitted("error") ?? []).every(([message]) => message === "")).toBe(true);
  });

  it("consumes with only offsetTo filled (partial range also sends the request)", async () => {
    installBridge({
      "kafka/presets/list": { presets: [] },
      "kafka/messages/consume": { messages: [], scanned: 0, matched: 0, limited: false, hasMore: false },
    });
    const wrapper = mountPanel();
    await flushPromises();
    const numberInputs = wrapper.findAll('input[type="number"]');
    await numberInputs[4].setValue(2);
    await wrapper.find(".form-footer .primary-button").trigger("click");
    await flushPromises();
    const consumeCall = invokeMock.mock.calls.find(([method]) => method === "kafka/messages/consume");
    expect(consumeCall?.[1]).toMatchObject({ offsetTo: 2 });
    expect(consumeCall?.[1]).not.toHaveProperty("offsetFrom");
    expect((wrapper.emitted("error") ?? []).every(([message]) => message === "")).toBe(true);
  });

  // P1-7：在途消费切换 topic 后，晚到的旧 topic 响应必须丢弃（不串台），且新
  // topic 立即可重新消费（consuming 复位），新响应正常落地。
  it("drops a stale consume response when the topic changes mid-flight (P1-7)", async () => {
    let resolveConsume!: (value: ConsumeResult) => void;
    invokeMock.mockReset();
    invokeMock.mockImplementation(async (method: string) => {
      if (method === "kafka/presets/list") return { presets: [] };
      if (method === "kafka/messages/consume") {
        return new Promise<ConsumeResult>((resolve) => {
          resolveConsume = resolve;
        });
      }
      throw new Error(`unhandled method: ${method}`);
    });
    (window as unknown as { dbxPlugin: unknown }).dbxPlugin = { invoke: invokeMock };

    const wrapper = mountPanel({ topic: "order-events" });
    await flushPromises();
    await wrapper.find(".form-footer .primary-button").trigger("click");
    // 在途切换 topic：旧结果清空、consuming 复位、请求序号自增
    await wrapper.setProps({ topic: "payment-gateway" });
    await flushPromises();
    // 晚到的 order-events 响应落地 → 必须被丢弃，不呈现在 payment-gateway 名下
    resolveConsume(consumeResult("order-events", 1));
    await flushPromises();
    expect(wrapper.find(".result-meta").exists()).toBe(false);
    expect(wrapper.find(".grid-stub").exists()).toBe(false);
    // 新 topic 立即可重新消费，且新响应正常落地
    await wrapper.find(".form-footer .primary-button").trigger("click");
    resolveConsume(consumeResult("payment-gateway", 7));
    await flushPromises();
    expect(wrapper.find(".result-meta").exists()).toBe(true);
    expect(wrapper.text()).toContain(t("messages.matched", { count: 3 }));
    const rows = wrapper.findAll(".grid-stub .grid-stub-row");
    expect(rows).toHaveLength(1);
    // 详情抽屉证实落地的是 payment-gateway 的消息（topic/offset 对得上，非串台）
    await rows[0]?.trigger("click");
    await flushPromises();
    expect(wrapper.find(".drawer").text()).toContain("payment-gateway");
    expect(wrapper.find(".drawer").text()).toContain("7");
    expect((wrapper.emitted("error") ?? []).every(([message]) => message === "")).toBe(true);
  });

  // P2-21：未选 topic 时引导先在左侧树选择；已选 topic 才是「调整条件重新消费」。
  it("distinguishes the no-topic empty state from the no-match empty state (P2-21)", async () => {
    installBridge({ "kafka/presets/list": { presets: [] } });
    const wrapper = mountPanel({ topic: "" });
    await flushPromises();
    expect(wrapper.find(".empty").text()).toBe(t("messages.uiNoTopicSelected"));
    await wrapper.setProps({ topic: "order-events" });
    expect(wrapper.find(".empty").text()).toBe(t("messages.noMessages"));
  });
});
