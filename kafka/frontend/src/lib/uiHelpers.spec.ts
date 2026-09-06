// @vitest-environment happy-dom
// Phase 3 H 路 UI 纯助手（F6）：copyTextToClipboard 的 clipboard API / execCommand
// 兜底双路径 + debounce 防抖语义（quickFilter 150ms 共用）。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { copyTextToClipboard, debounce } from "./kafkaModel";

// navigator.clipboard 是 getter-only（happy-dom 同浏览器），测试用 defineProperty 注入。
function stubClipboard(clipboard: unknown) {
  Object.defineProperty(navigator, "clipboard", { value: clipboard, configurable: true });
}
function stubExecCommand(execCommand: unknown) {
  Object.defineProperty(document, "execCommand", { value: execCommand, configurable: true });
}

describe("copyTextToClipboard (F6-2)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("prefers navigator.clipboard.writeText", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    stubClipboard({ writeText });
    await expect(copyTextToClipboard("hello")).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith("hello");
  });

  it("falls back to execCommand when the clipboard API rejects", async () => {
    stubClipboard({ writeText: vi.fn().mockRejectedValue(new Error("denied")) });
    const execCommand = vi.fn().mockReturnValue(true);
    stubExecCommand(execCommand);
    await expect(copyTextToClipboard("fallback")).resolves.toBe(true);
    expect(execCommand).toHaveBeenCalledWith("copy");
  });

  it("uses execCommand when the clipboard API is unavailable", async () => {
    stubClipboard(undefined);
    const execCommand = vi.fn().mockReturnValue(true);
    stubExecCommand(execCommand);
    await expect(copyTextToClipboard("legacy")).resolves.toBe(true);
    expect(execCommand).toHaveBeenCalledWith("copy");
  });

  it("reports failure when both paths fail and ignores empty input", async () => {
    stubClipboard(undefined);
    stubExecCommand(vi.fn().mockReturnValue(false));
    await expect(copyTextToClipboard("nope")).resolves.toBe(false);
    const writeText = vi.fn();
    stubClipboard({ writeText });
    await expect(copyTextToClipboard("")).resolves.toBe(false);
    expect(writeText).not.toHaveBeenCalled();
  });
});

describe("debounce (F6-1 quick filter timing)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("collapses bursts into a single trailing call", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 150);
    debounced("a");
    vi.advanceTimersByTime(100);
    debounced("ab");
    vi.advanceTimersByTime(100);
    debounced("abc");
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(150);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith("abc");
  });

  it("cancel drops the pending call", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 150);
    debounced("x");
    debounced.cancel();
    vi.advanceTimersByTime(500);
    expect(fn).not.toHaveBeenCalled();
  });
});
