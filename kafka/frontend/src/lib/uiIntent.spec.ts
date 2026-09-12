// @vitest-environment happy-dom
// 薄 spec：验证 shared/frontend/uiIntent 在本插件工具链下 import 解析与
// 行为成立（事件归一化 + intent 分派 + report 回报），并镜像 mockDbxHost
// 的新事件/方法形状（防单测脱节，AGENTS.md 硬性规则 7）。
import { describe, expect, it, vi } from "vitest";
import { readUiIntentEvent, useUiIntent } from "../../../../shared/frontend/uiIntent";
import { emitKafkaUiIntent } from "../mockDbxHost";
import "../mockDbxHost";

const waitReport = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
};

describe("readUiIntentEvent normalization", () => {
  it("normalizes the sidecar event shape and ignores unrelated events", () => {
    expect(readUiIntentEvent({ type: "env", locale: "en" }, "kafka")).toBeNull();
    expect(readUiIntentEvent({ method: "kafka/audit", params: {} }, "kafka")).toBeNull();
    expect(readUiIntentEvent({ method: "ldap/ui/intent", params: {} }, "kafka")).toBeNull();
    expect(
      readUiIntentEvent({ method: "kafka/ui/intent", params: { intentId: "i-1", action: "search" } }, "kafka"),
    ).toEqual({ intentId: "i-1", action: "search", params: {} });
    expect(
      readUiIntentEvent({ method: "kafka/ui/intent", params: { intentId: "i-2", action: "focus", params: { panel: "groups" } } }, "kafka"),
    ).toEqual({ intentId: "i-2", action: "focus", params: { panel: "groups" } });
    // intentId/action 缺失或非字符串一律拒绝（防宿主形状漂移静默通过）。
    expect(readUiIntentEvent({ method: "kafka/ui/intent", params: { action: "search" } }, "kafka")).toBeNull();
    expect(readUiIntentEvent({ method: "kafka/ui/intent" }, "kafka")).toBeNull();
    expect(readUiIntentEvent(null, "kafka")).toBeNull();
  });
});

describe("useUiIntent dispatch and reporting", () => {
  it("reports applied with the handler summary through kafka/ui/state/report", async () => {
    const invoke = vi.spyOn(window.dbxPlugin, "invoke");
    const uiIntent = useUiIntent("kafka", {
      search: async (params) => ({
        status: "applied",
        summary: { count: Number(params.limit ?? 0), anchor: "order-events-p0-o42", rows: [{ partition: 0, offset: 42 }] },
      }),
    });
    try {
      emitKafkaUiIntent({ intentId: "i-applied", action: "search", params: { topic: "order-events", limit: 100 } });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("kafka/ui/state/report", {
          intentId: "i-applied",
          status: "applied",
          summary: { count: 100, anchor: "order-events-p0-o42", rows: [{ partition: 0, offset: 42 }] },
        });
      });
    } finally {
      uiIntent.stop();
    }
  });

  it("reports rejected when the handler throws or the action has no handler", async () => {
    const invoke = vi.spyOn(window.dbxPlugin, "invoke");
    const uiIntent = useUiIntent("kafka", {
      select: async () => {
        throw new Error("boom");
      },
    });
    try {
      emitKafkaUiIntent({ intentId: "i-thrown", action: "select", params: { partition: 0, offset: 1 } });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("kafka/ui/state/report", {
          intentId: "i-thrown",
          status: "rejected",
          summary: { reason: "boom" },
        });
      });
      emitKafkaUiIntent({ intentId: "i-unknown", action: "teleport", params: {} });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("kafka/ui/state/report", {
          intentId: "i-unknown",
          status: "rejected",
          summary: { reason: 'no handler for action "teleport"' },
        });
      });
    } finally {
      uiIntent.stop();
    }
  });

  it("sends snapshot reports without an intentId", async () => {
    const invoke = vi.spyOn(window.dbxPlugin, "invoke");
    const uiIntent = useUiIntent("kafka", {});
    try {
      uiIntent.reportSnapshot({ panel: "messages", topic: "order-events", count: 3 });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("kafka/ui/state/report", {
          status: "snapshot",
          summary: { panel: "messages", topic: "order-events", count: 3 },
        });
      });
    } finally {
      uiIntent.stop();
    }
  });

  it("mirrors the mock report contract: applied/rejected accepted, snapshot tolerated, bad status refused", async () => {
    await expect(window.dbxPlugin.invoke("kafka/ui/state/report", { intentId: "i-1", status: "applied", summary: { count: 1 } })).resolves.toEqual({ success: true });
    await expect(window.dbxPlugin.invoke("kafka/ui/state/report", { intentId: "i-1", status: "rejected", summary: { reason: "x" } })).resolves.toEqual({ success: true });
    await expect(window.dbxPlugin.invoke("kafka/ui/state/report", { status: "snapshot", summary: {} })).resolves.toEqual({ success: true });
    await expect(window.dbxPlugin.invoke("kafka/ui/state/report", { intentId: "i-1", status: "pending" })).rejects.toThrow("status must be applied or rejected");
  });
});

describe("stop unsubscribes", () => {
  it("no longer reports after stop", async () => {
    const invoke = vi.spyOn(window.dbxPlugin, "invoke");
    const uiIntent = useUiIntent("kafka", { focus: async () => ({ status: "applied" }) });
    uiIntent.stop();
    emitKafkaUiIntent({ intentId: "i-stopped", action: "focus", params: {} });
    await waitReport();
    expect(invoke).not.toHaveBeenCalledWith("kafka/ui/state/report", expect.objectContaining({ intentId: "i-stopped" }));
  });
});
