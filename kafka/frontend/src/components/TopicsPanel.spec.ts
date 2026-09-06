// @vitest-environment happy-dom
// TopicsPanel 组件测试（UI 扫描第 4 轮防回归）：
// P2-20 扩分区填更小/相等值时使用专用文案（不再错位复用 err.partition），
// 且仍不发出 topics/partitions/update 请求。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { defineComponent, h, type PropType } from "vue";
import TopicsPanel from "./TopicsPanel.vue";
import { setKafkaConnectionId, type KafkaTopic } from "../lib/api";
import { t } from "../lib/i18n";

// -- DbxAgGrid 轻量 stub（镜像真实桥形状，见 GroupsPanel.spec 同款） -----------------
const DbxAgGridStub = defineComponent({
  name: "DbxAgGridStub",
  props: {
    rowData: { type: Array as PropType<unknown[]>, default: () => [] },
    tableKey: { type: String, default: "" },
    rowSelection: { type: [String, Boolean] as PropType<"single" | false>, default: "single" as const },
    emitRowClick: { type: Boolean, default: true },
  },
  emits: ["selection-changed", "row-click"],
  setup(props, { emit }) {
    return () =>
      h(
        "div",
        { class: "grid-stub", "data-key": props.tableKey },
        (props.rowData ?? []).map((row, index) =>
          h("button", {
            type: "button",
            class: "grid-stub-row",
            onClick: () => {
              if (props.rowSelection) emit("selection-changed", row);
              if (props.emitRowClick) emit("row-click", row);
            },
          }),
        ),
      );
  },
});

const invokeMock = vi.fn();

function installBridge(routes: Record<string, unknown>) {
  invokeMock.mockReset();
  invokeMock.mockImplementation(async (method: string) => {
    if (method in routes) return routes[method];
    throw new Error(`unhandled method: ${method}`);
  });
  (window as unknown as { dbxPlugin: unknown }).dbxPlugin = { invoke: invokeMock };
}

const topics: KafkaTopic[] = [{ name: "order-events", partitionCount: 2, replicationFactor: 1 }];

function mountPanel(props: Record<string, unknown> = {}) {
  return mount(TopicsPanel, {
    props: { topics, loading: false, canWrite: true, canDelete: true, ...props },
    global: { stubs: { DbxAgGrid: DbxAgGridStub, teleport: true } },
  });
}

/** 选中 topic 网格首行（selection-changed → selected）。 */
async function selectFirstTopic(wrapper: ReturnType<typeof mountPanel>) {
  await wrapper.find('.grid-stub[data-key="topics"] .grid-stub-row').trigger("click");
  await flushPromises();
}

/** 打开扩分区弹窗（工具栏第 5 个按钮：TrendingUp 扩分区）。 */
async function openExpandDialog(wrapper: ReturnType<typeof mountPanel>) {
  await wrapper.findAll(".result-meta .qb-add")[3].trigger("click");
  await flushPromises();
}

beforeEach(() => {
  localStorage.clear();
  setKafkaConnectionId("conn-test");
});

describe("TopicsPanel expand partitions", () => {
  it("rejects a smaller new count with the dedicated message and no request (P2-20)", async () => {
    installBridge({});
    const wrapper = mountPanel();
    await selectFirstTopic(wrapper);
    await openExpandDialog(wrapper);
    const modal = wrapper.find(".modal-backdrop .modal");
    expect(modal.exists()).toBe(true);
    // 默认值为当前 + 1；填更小值（1 ≤ 当前 2）确认
    await modal.find('input[type="number"]').setValue("1");
    await modal.find(".primary-button").trigger("click");
    await flushPromises();
    expect(wrapper.emitted("error")?.at(-1)).toEqual([t("topics.expandCountInvalid", { count: 2 })]);
    expect(invokeMock.mock.calls.filter(([method]) => method === "kafka/topics/partitions/update")).toHaveLength(0);
  });

  it("rejects an equal new count with the dedicated message as well", async () => {
    installBridge({});
    const wrapper = mountPanel();
    await selectFirstTopic(wrapper);
    await openExpandDialog(wrapper);
    const modal = wrapper.find(".modal-backdrop .modal");
    await modal.find('input[type="number"]').setValue("2");
    await modal.find(".primary-button").trigger("click");
    await flushPromises();
    expect(wrapper.emitted("error")?.at(-1)).toEqual([t("topics.expandCountInvalid", { count: 2 })]);
  });

  it("still submits a valid larger count", async () => {
    installBridge({ "kafka/topics/partitions/update": { success: true } });
    const wrapper = mountPanel();
    await selectFirstTopic(wrapper);
    await openExpandDialog(wrapper);
    const modal = wrapper.find(".modal-backdrop .modal");
    await modal.find('input[type="number"]').setValue("4");
    await modal.find(".primary-button").trigger("click");
    await flushPromises();
    expect(wrapper.emitted("notify")?.at(-1)).toEqual([t("topics.expanded")]);
    const call = invokeMock.mock.calls.find(([method]) => method === "kafka/topics/partitions/update");
    // api 层把 partitions 包在 params 里并注入 connectionId
    expect(call?.[1]).toMatchObject({ partitions: { "order-events": 4 }, connectionId: "conn-test" });
  });
});
