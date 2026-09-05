// 七语完整性守卫：断言 7 个 locale 的 i18n key 集合完全一致（仓库硬性规范，
// 对齐 scripts/check_i18n.py 的语义）。
import { describe, expect, it } from "vitest";
import { messages, resolveWorkbenchLocale, workbenchMessage } from "./i18n";

const LOCALES = ["en", "es", "it", "ja", "pt-BR", "zh-CN", "zh-TW"] as const;

function flattenKeys(source: unknown, prefix = ""): string[] {
  const keys: string[] = [];
  if (source && typeof source === "object") {
    for (const [key, value] of Object.entries(source as Record<string, unknown>)) {
      const path = prefix ? `${prefix}.${key}` : key;
      if (value && typeof value === "object") keys.push(...flattenKeys(value, path));
      else keys.push(path);
    }
  }
  return keys.sort();
}

describe("i18n seven-language completeness", () => {
  it("exposes all seven locales", () => {
    expect(Object.keys(messages).sort()).toEqual([...LOCALES].sort());
  });

  it("has identical key sets across locales", () => {
    const enKeys = flattenKeys(messages.en);
    for (const locale of LOCALES) {
      expect(flattenKeys(messages[locale]), `locale ${locale}`).toEqual(enKeys);
    }
    expect(enKeys.length).toBeGreaterThan(200);
  });

  it("has no empty values", () => {
    for (const locale of LOCALES) {
      const messages_ = messages[locale] as Record<string, unknown>;
      const collect = (node: Record<string, unknown>, prefix: string) => {
        for (const [key, value] of Object.entries(node)) {
          const path = prefix ? `${prefix}.${key}` : key;
          if (value && typeof value === "object") collect(value as Record<string, unknown>, path);
          else expect(typeof value === "string" && value.length > 0, `${locale}:${path}`).toBe(true);
        }
      };
      collect(messages_, "");
    }
  });
});

describe("workbenchMessage", () => {
  it("resolves locale families with fallbacks", () => {
    expect(resolveWorkbenchLocale("zh_CN")).toBe("zh-CN");
    expect(resolveWorkbenchLocale("zh-TW")).toBe("zh-TW");
    expect(resolveWorkbenchLocale("zh-HK")).toBe("zh-TW");
    expect(resolveWorkbenchLocale("pt")).toBe("pt-BR");
    expect(resolveWorkbenchLocale("fr")).toBe("zh-CN");
    expect(resolveWorkbenchLocale("ja-JP")).toBe("ja");
  });

  it("substitutes template values and falls back to en", () => {
    expect(workbenchMessage("zh-CN", "messages.scanned", { count: 7 })).toContain("7");
    expect(workbenchMessage("en", "tree.partitions", { count: 3 })).toBe("3p");
    // missing key falls back to the key itself
    expect(workbenchMessage("en", "definitely.not.a.key")).toBe("definitely.not.a.key");
  });
});
