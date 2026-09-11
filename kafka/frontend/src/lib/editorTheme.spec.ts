// shared/frontend 公共适配层薄 spec（README 约定：每个插件保留一条引用断言）。
// kafka 的 CodeEditor 走 CSS 变量驱动，无法把 shared 调色板 import 进样式表，
// `--cm-*` 暗色块按 EDITOR_TOKEN_COLORS.dark 镜像；此处直接断言 CodeEditor.vue
// 源码中的暗色取值与 shared 调色板一致，防止两侧漂移。
import { describe, expect, it } from "vitest";
import CodeEditorSource from "../components/CodeEditor.vue?raw";
import { EDITOR_TOKEN_COLORS } from "../../../../shared/frontend/editorTheme";

// 提取暗色规则块（明暗分支统一双属性匹配 data-theme / data-dbx-theme）。
const darkBlock = /:root\[data-dbx-theme="dark"\] \.dbx-code-editor \{([\s\S]*?)\n\}/.exec(CodeEditorSource)?.[1];
// 浅色规则块：顶层 .dbx-code-editor 声明（历史 VS Code Light+ 取值，未提亮）。
const lightBlock = /\n\.dbx-code-editor \{([\s\S]*?)\n\}/.exec(CodeEditorSource)?.[1];

// --cm-* 与 shared 调色板键的映射（kafka 消费的子集）。
const CSS_VAR_TO_PALETTE_KEY = [
  ["key", "key"],
  ["string", "string"],
  ["number", "number"],
  ["null", "null"],
  ["punct", "punct"],
  ["prop", "prop"],
] as const;

describe("CodeEditor --cm-* 与 shared editorTheme 调色板同源", () => {
  it("dark block mirrors EDITOR_TOKEN_COLORS.dark", () => {
    expect(darkBlock).toBeDefined();
    for (const [cssVar, paletteKey] of CSS_VAR_TO_PALETTE_KEY) {
      const expected = EDITOR_TOKEN_COLORS.dark[paletteKey];
      expect(expected).toBeTruthy();
      expect(darkBlock).toContain(`--cm-${cssVar}: ${expected}`);
    }
  });

  it("keeps the untouched light block values (no accidental palette rewrite)", () => {
    expect(lightBlock).toBeDefined();
    // 浅色历史取值保持不动（prop 为历史 #795e26，未随 shared light 调色板提亮）。
    const lightValues: Record<(typeof CSS_VAR_TO_PALETTE_KEY)[number][0], string> = {
      key: "#0451a5",
      string: "#a31515",
      number: "#098658",
      null: "#795e26",
      punct: "#4b4b4b",
      prop: "#795e26",
    };
    for (const [cssVar] of CSS_VAR_TO_PALETTE_KEY) {
      expect(lightBlock).toContain(`--cm-${cssVar}: ${lightValues[cssVar]}`);
    }
  });
});
