// @vitest-environment happy-dom
// SchemasPanel 组件测试（Phase 3 F5 Schema 三件套）：
// - 克隆：版本表行操作「克隆」→ 注册弹窗预填 schema 文本 + subject 原值可改；
// - 模板：注册弹窗 format 选定后「插入模板」（AVRO/JSON/Protobuf 代码常量）；
// - 树视图：详情区 树/文本 toggle（AVRO 递归树；PROTOBUF 保持文本 + 行内提示）。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { defineComponent, h, type PropType } from "vue";
import SchemasPanel from "./SchemasPanel.vue";
import { setKafkaConnectionId, type SchemaSubject } from "../lib/api";
import { SCHEMA_TEMPLATE_AVRO, SCHEMA_TEMPLATE_JSON, SCHEMA_TEMPLATE_PROTOBUF, schemaTemplateFor } from "../lib/kafkaModel";
import { t } from "../lib/i18n";

// DbxAgGrid stub：行渲染 + 把 columnDefs 里的行操作 cellRenderer 真正执行
// （克隆按钮是 kafkaColumns.actionColumn 产出的原生 DOM，需挂到 body 后点击）。
const DbxAgGridStub = defineComponent({
  name: "DbxAgGridStub",
  props: {
    rowData: { type: Array as PropType<unknown[]>, default: () => [] },
    columnDefs: { type: Array as PropType<unknown[]>, default: () => [] },
    tableKey: { type: String, default: "" },
    rowSelection: { type: [String, Boolean] as PropType<"single" | false>, default: "single" as const },
  },
  emits: ["selection-changed", "row-click"],
  setup(props, { emit }) {
    // 行操作列：真正调用 cellRenderer（kafkaColumns.actionColumn 产出原生 button，
    // 拿其文案/aria 校验渲染产物），再用 Vue button 转接点击（Vue 不渲染裸 DOM 节点）。
    const renderAction = (def: Record<string, unknown>, row: Record<string, unknown>) => {
      const renderer = def.cellRenderer as (params: { data?: unknown }) => HTMLElement | undefined;
      if (typeof renderer !== "function") return null;
      const produced = renderer({ data: row });
      const label = produced?.textContent ?? "";
      const aria = produced?.getAttribute("aria-label") ?? "";
      return h(
        "button",
        {
          type: "button",
          class: "grid-action-button",
          "data-table-key": props.tableKey,
          "aria-label": aria,
          // 转发到真实 actionColumn 按钮的监听器（onClone 闭包在真实按钮上）。
          onClick: () => produced?.click(),
        },
        label,
      );
    };
    return () =>
      h(
        "div",
        { class: "grid-stub", "data-key": props.tableKey },
        (props.rowData as Array<Record<string, unknown>>).map((row, index) => {
          const children: Array<ReturnType<typeof h> | null> = [
            h(
              "button",
              {
                type: "button",
                class: "grid-stub-row",
                onClick: () => {
                  if (props.rowSelection) emit("selection-changed", row);
                },
              },
              `${props.tableKey}-row-${index}`,
            ),
          ];
          for (const def of props.columnDefs as Array<Record<string, unknown>>) {
            if (typeof def.cellRenderer === "function") children.push(renderAction(def, row));
          }
          return h("div", { class: "grid-stub-line", key: index }, children);
        }),
      );
  },
});

const invokeMock = vi.fn();

function installBridge(handler: (method: string, params: Record<string, unknown>) => unknown) {
  invokeMock.mockReset();
  invokeMock.mockImplementation(async (method: string, params: Record<string, unknown> = {}) => handler(method, params));
  (window as unknown as { dbxPlugin: unknown }).dbxPlugin = { invoke: invokeMock };
}

const AVRO_V1 = JSON.stringify({ type: "record", name: "Order", fields: [{ name: "orderId", type: "string" }, { name: "amount", type: "double", default: 0 }] });

function confluentBridge() {
  return (method: string, params: Record<string, unknown>) => {
    switch (method) {
      case "kafka/schema/test":
        return { success: true, provider: "confluent" };
      case "kafka/schema/subjects/list":
        return {
          subjects: [
            { subject: "order-events-value", formats: ["avro"], latestVersion: 2, compatibilityLevel: "BACKWARD" } satisfies SchemaSubject,
            { subject: "orders-proto-value", formats: ["protobuf"], latestVersion: 1, compatibilityLevel: "NONE" } satisfies SchemaSubject,
          ],
        };
      case "kafka/schema/versions/list":
        return String(params.subject) === "orders-proto-value"
          ? { versions: [{ version: 1, id: 31, format: "protobuf" }] }
          : { versions: [{ version: 1, id: 10, format: "avro" }, { version: 2, id: 11, format: "avro" }] };
      case "kafka/schema/get":
        return String(params.subject) === "orders-proto-value"
          ? { subject: "orders-proto-value", version: 1, id: 31, schema: 'syntax = "proto3";\nmessage Order {}', format: "protobuf", references: [] }
          : { subject: "order-events-value", version: Number(params.version ?? 2), id: 11, schema: AVRO_V1, format: "avro", references: [] };
      case "kafka/schema/compatibility/get":
        return { level: "BACKWARD", scope: "GLOBAL" };
      default:
        throw new Error(`unhandled method: ${method}`);
    }
  };
}

function mountPanel() {
  return mount(SchemasPanel, {
    props: { canWrite: true, canDelete: true },
    global: { stubs: { DbxAgGrid: DbxAgGridStub, teleport: true } },
  });
}

async function selectFirstSubject(wrapper: ReturnType<typeof mountPanel>) {
  await wrapper.findAll(".grid-stub[data-key='schema-subjects'] .grid-stub-row")[0].trigger("click");
  await flushPromises();
}

beforeEach(() => {
  localStorage.clear();
  setKafkaConnectionId("conn-test");
  document.body.innerHTML = "";
});

describe("SchemasPanel tree view (F5)", () => {
  it("renders a collapsible tree for AVRO and toggles back to text", async () => {
    installBridge(confluentBridge());
    const wrapper = mountPanel();
    await flushPromises();
    await selectFirstSubject(wrapper);
    // 缺省 = 文本（既有行为）。
    expect(wrapper.find('[data-testid="schema-view-text"]').classes()).toContain("is-utc");
    expect(wrapper.find("pre.schema-view").exists()).toBe(true);
    await wrapper.find('[data-testid="schema-view-tree"]').trigger("click");
    const tree = wrapper.find('[data-testid="schema-tree"]');
    expect(tree.exists()).toBe(true);
    expect(tree.text()).toContain("orderId");
    expect(tree.text()).toContain("amount");
    // default 值渲染在树中。
    expect(tree.text()).toContain("0");
    // 回到文本。
    await wrapper.find('[data-testid="schema-view-text"]').trigger("click");
    expect(wrapper.find('[data-testid="schema-tree"]').exists()).toBe(false);
    expect(wrapper.find("pre.schema-view").text()).toContain("orderId");
  });

  it("keeps PROTOBUF as text with the unsupported hint and a disabled tree button", async () => {
    installBridge((method, params) => {
      if (method === "kafka/schema/subjects/list") {
        return confluentBridge()(method, params);
      }
      // 选 proto subject：stub 行序 1。
      return confluentBridge()(method, params);
    });
    const wrapper = mountPanel();
    await flushPromises();
    await wrapper.findAll(".grid-stub[data-key='schema-subjects'] .grid-stub-row")[1].trigger("click");
    await flushPromises();
    const treeButton = wrapper.find('[data-testid="schema-view-tree"]');
    expect(treeButton.attributes("disabled")).toBeDefined();
    expect(wrapper.find('[data-testid="schema-tree-hint"]').text()).toBe(t("schemas.treeUnsupportedHint"));
    // 文本视图仍显示原始 schema 文本。
    expect(wrapper.find("pre.schema-view").text()).toContain("proto3");
  });
});

describe("SchemasPanel template insertion (F5)", () => {
  it("inserts the static template matching the selected format", async () => {
    installBridge(confluentBridge());
    const wrapper = mountPanel();
    await flushPromises();
    await wrapper.findAll(".qb-add")[0].trigger("click"); // 注册按钮
    await flushPromises();
    const modal = wrapper.find(".modal-backdrop .modal, body .modal-backdrop .modal");
    expect(modal.exists()).toBe(true);
    // 每次交互后重新查询（v-if 弹层内元素可能随补丁重建，旧 wrapper 会失联）。
    const body = () => wrapper.find(".modal-backdrop .modal .settings-body");
    // AVRO（缺省）。
    await wrapper.find('[data-testid="insert-template"]').trigger("click");
    await flushPromises();
    expect((body().find("textarea").element as HTMLTextAreaElement).value).toBe(SCHEMA_TEMPLATE_AVRO);
    expect(SCHEMA_TEMPLATE_AVRO).toBe(schemaTemplateFor("avro"));
    // JSON。
    await body().find("select").setValue("json");
    await wrapper.find('[data-testid="insert-template"]').trigger("click");
    await flushPromises();
    expect((body().find("textarea").element as HTMLTextAreaElement).value).toBe(SCHEMA_TEMPLATE_JSON);
    // PROTOBUF：选项存在、模板可插入且不做 JSON 校验。
    await body().find("select").setValue("protobuf");
    await wrapper.find('[data-testid="insert-template"]').trigger("click");
    await flushPromises();
    expect((body().find("textarea").element as HTMLTextAreaElement).value).toBe(SCHEMA_TEMPLATE_PROTOBUF);
    expect(wrapper.find(".modal-backdrop .modal").text()).not.toContain(t("schemas.registerInvalidJson"));
    expect(SCHEMA_TEMPLATE_PROTOBUF).toBe(schemaTemplateFor("protobuf"));
  });
});

describe("SchemasPanel clone (F5)", () => {
  it("prefills the register modal with the cloned schema text and editable subject", async () => {
    installBridge(confluentBridge());
    const wrapper = mountPanel();
    await flushPromises();
    await selectFirstSubject(wrapper);
    // 版本表（v1）行操作「克隆」按钮：actionColumn 渲染的原生 button。
    const cloneButtons = wrapper.findAll("button[data-table-key='schema-versions']");
    expect(cloneButtons).toHaveLength(2); // v1 + v2 各一枚
    await cloneButtons[0].trigger("click");
    await flushPromises();
    const modalText = wrapper.find(".modal-backdrop .modal");
    expect(modalText.exists()).toBe(true);
    // 弹窗标题为克隆语义。
    expect(modalText.text()).toContain(t("schemas.cloneTitle", { version: 1 }));
    // schema 文本已预填（GetSchema 数据），subject 默认原值可改。
    const subjectInput = modalText.find("input").element as HTMLInputElement;
    expect(subjectInput.value).toBe("order-events-value");
    const schemaTextarea = modalText.find("textarea").element as HTMLTextAreaElement;
    expect(schemaTextarea.value).toBe(AVRO_V1);
  });
});
