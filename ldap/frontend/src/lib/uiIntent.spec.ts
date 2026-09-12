// @vitest-environment happy-dom
// 薄 spec：验证 shared/frontend/uiIntent 在本插件工具链下 import 解析与
// 行为成立（事件归一化 + intent 分派 + report 回报），并镜像 mockDbxHost
// 的新事件/方法形状（防单测脱节，AGENTS.md 硬性规则 7）。
import { describe, expect, it, vi } from "vitest";
import { readUiIntentEvent, useUiIntent } from "../../../../shared/frontend/uiIntent";
import { emitUiIntent } from "../mockDbxHost";
import "../mockDbxHost";

const waitReport = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
};

describe("readUiIntentEvent normalization", () => {
  it("normalizes the sidecar event shape and ignores unrelated events", () => {
    expect(readUiIntentEvent({ type: "env", locale: "en" }, "ldap")).toBeNull();
    expect(readUiIntentEvent({ method: "ldap/audit", params: {} }, "ldap")).toBeNull();
    expect(readUiIntentEvent({ method: "kafka/ui/intent", params: {} }, "ldap")).toBeNull();
    expect(
      readUiIntentEvent({ method: "ldap/ui/intent", params: { intentId: "i-1", action: "search" } }, "ldap"),
    ).toEqual({ intentId: "i-1", action: "search", params: {} });
    expect(
      readUiIntentEvent({ method: "ldap/ui/intent", params: { intentId: "i-2", action: "focus", params: { panel: "schema" } } }, "ldap"),
    ).toEqual({ intentId: "i-2", action: "focus", params: { panel: "schema" } });
    // intentId/action 缺失或非字符串一律拒绝（防宿主形状漂移静默通过）。
    expect(readUiIntentEvent({ method: "ldap/ui/intent", params: { action: "search" } }, "ldap")).toBeNull();
    expect(readUiIntentEvent({ method: "ldap/ui/intent" }, "ldap")).toBeNull();
    expect(readUiIntentEvent(null, "ldap")).toBeNull();
  });
});

describe("useUiIntent dispatch and reporting", () => {
  it("reports applied with the handler summary through ldap/ui/state/report", async () => {
    const invoke = vi.spyOn(window.dbxPlugin, "invoke");
    const uiIntent = useUiIntent("ldap", {
      search: async (params) => ({ status: "applied", summary: { count: Number(params.sizeLimit ?? 0), anchor: "uid=a,dc=demo,dc=dbx" } }),
    });
    try {
      emitUiIntent({ intentId: "i-applied", action: "search", params: { filter: "(uid=a)", sizeLimit: 500 } });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("ldap/ui/state/report", {
          intentId: "i-applied",
          status: "applied",
          summary: { count: 500, anchor: "uid=a,dc=demo,dc=dbx" },
        });
      });
    } finally {
      uiIntent.stop();
    }
  });

  it("reports rejected when the handler throws or the action has no handler", async () => {
    const invoke = vi.spyOn(window.dbxPlugin, "invoke");
    const uiIntent = useUiIntent("ldap", {
      select: async () => {
        throw new Error("boom");
      },
    });
    try {
      emitUiIntent({ intentId: "i-thrown", action: "select", params: { dn: "x" } });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("ldap/ui/state/report", {
          intentId: "i-thrown",
          status: "rejected",
          summary: { reason: "boom" },
        });
      });
      emitUiIntent({ intentId: "i-unknown", action: "teleport", params: {} });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("ldap/ui/state/report", {
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
    const uiIntent = useUiIntent("ldap", {});
    try {
      uiIntent.reportSnapshot({ panel: "search", count: 3 });
      await vi.waitFor(() => {
        expect(invoke).toHaveBeenCalledWith("ldap/ui/state/report", {
          status: "snapshot",
          summary: { panel: "search", count: 3 },
        });
      });
    } finally {
      uiIntent.stop();
    }
  });

  it("mirrors the mock report contract: applied/rejected accepted, snapshot tolerated, bad status refused", async () => {
    await expect(window.dbxPlugin.invoke("ldap/ui/state/report", { intentId: "i-1", status: "applied", summary: { count: 1 } })).resolves.toEqual({ success: true });
    await expect(window.dbxPlugin.invoke("ldap/ui/state/report", { intentId: "i-1", status: "rejected", summary: { reason: "x" } })).resolves.toEqual({ success: true });
    await expect(window.dbxPlugin.invoke("ldap/ui/state/report", { status: "snapshot", summary: {} })).resolves.toEqual({ success: true });
    await expect(window.dbxPlugin.invoke("ldap/ui/state/report", { intentId: "i-1", status: "pending" })).rejects.toThrow("status must be applied or rejected");
  });
});

describe("stop unsubscribes", () => {
  it("no longer reports after stop", async () => {
    const invoke = vi.spyOn(window.dbxPlugin, "invoke");
    const uiIntent = useUiIntent("ldap", { search: async () => ({ status: "applied" }) });
    uiIntent.stop();
    emitUiIntent({ intentId: "i-stopped", action: "search", params: {} });
    await waitReport();
    expect(invoke).not.toHaveBeenCalledWith("ldap/ui/state/report", expect.objectContaining({ intentId: "i-stopped" }));
  });
});
