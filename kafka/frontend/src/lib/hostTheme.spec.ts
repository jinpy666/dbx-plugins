// @vitest-environment happy-dom
// hostTheme 行为测试（覆盖完善轮 §10.3 低覆盖项补测）：宿主 1.1 主题通道
// 的类型守卫 / env detail 归一 / token → appearance 映射 / 事件订阅退订。
// 宿主契约：init 与 env 消息推送 { appearance, tokens }；旧宿主（1.0）缺失
// 时调用方降级本地色板——守卫必须对非法形状返回 false 而非抛错。
import { describe, expect, it, vi } from "vitest";
import { isDbxPluginTheme, onHostThemeChange, themeFromEnvDetail, themeToAppearance } from "./hostTheme";

describe("isDbxPluginTheme", () => {
  it("accepts light/dark themes with optional tokens", () => {
    expect(isDbxPluginTheme({ appearance: "dark" })).toBe(true);
    expect(isDbxPluginTheme({ appearance: "light", tokens: {} })).toBe(true);
    expect(isDbxPluginTheme({ appearance: "light", tokens: { "--color-accent": "#3b82f6" } })).toBe(true);
  });

  it("rejects non-objects, unknown appearances and non-object tokens", () => {
    expect(isDbxPluginTheme(null)).toBe(false);
    expect(isDbxPluginTheme("dark")).toBe(false);
    expect(isDbxPluginTheme({})).toBe(false);
    expect(isDbxPluginTheme({ appearance: "system" })).toBe(false);
    expect(isDbxPluginTheme({ appearance: "dark", tokens: "nope" })).toBe(false);
    expect(isDbxPluginTheme({ appearance: "dark", tokens: null })).toBe(false);
  });
});

describe("themeFromEnvDetail", () => {
  it("normalizes a valid env detail and defaults missing tokens to {}", () => {
    expect(themeFromEnvDetail({ theme: { appearance: "dark" } })).toEqual({ appearance: "dark", tokens: {} });
    expect(themeFromEnvDetail({ theme: { appearance: "light", tokens: { "--color-border": "#eee" } } })).toEqual({
      appearance: "light",
      tokens: { "--color-border": "#eee" },
    });
  });

  it("returns null for missing/invalid payloads without throwing", () => {
    expect(themeFromEnvDetail(undefined)).toBeNull();
    expect(themeFromEnvDetail(null)).toBeNull();
    expect(themeFromEnvDetail({})).toBeNull();
    expect(themeFromEnvDetail({ theme: { appearance: "nope" } })).toBeNull();
  });
});

describe("themeToAppearance", () => {
  it("maps known host tokens onto appearance colors and keeps the scheme", () => {
    const result = themeToAppearance({
      appearance: "dark",
      tokens: {
        "--color-background": "#131416",
        "--color-foreground": "#d7d7db",
        "--color-accent": "#3b82f6",
        "--color-destructive": "#ef4444",
      },
    });
    expect(result.colorScheme).toBe("dark");
    expect(result.colors).toMatchObject({
      background: "#131416",
      foreground: "#d7d7db",
      accent: "#3b82f6",
      destructive: "#ef4444",
    });
  });

  it("drops blank/non-string tokens and ignores unknown ones", () => {
    const result = themeToAppearance({
      appearance: "light",
      tokens: {
        "--color-muted": "   ",
        "--color-border": 42 as unknown as string,
        "--not-a-token": "#fff",
      },
    });
    expect(result.colorScheme).toBe("light");
    expect(result.colors).toEqual({});
  });
});

describe("onHostThemeChange", () => {
  it("delivers only valid themes from dbx-plugin-env events and stops after unsubscribe", () => {
    const listener = vi.fn();
    const unsubscribe = onHostThemeChange(listener);

    const dispatch = (detail: unknown) =>
      document.dispatchEvent(new CustomEvent("dbx-plugin-env", { detail }));

    dispatch({ theme: { appearance: "dark", tokens: { "--color-accent": "#abc" } } });
    expect(listener).toHaveBeenCalledTimes(1);
    expect(listener).toHaveBeenLastCalledWith({ appearance: "dark", tokens: { "--color-accent": "#abc" } });

    // 非法主题（旧宿主字段缺失 / 形状错误）静默忽略
    dispatch({});
    dispatch({ theme: "dark" });
    expect(listener).toHaveBeenCalledTimes(1);

    unsubscribe();
    dispatch({ theme: { appearance: "light" } });
    expect(listener).toHaveBeenCalledTimes(1);
  });
});
