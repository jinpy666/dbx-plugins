// @vitest-environment happy-dom
// 弹窗键盘决策纯函数（Esc close + Tab 焦点陷阱）：与 kafka 同语义的家族
// 统一测试；focusableElements 用真实 DOM 断言选择器覆盖面。遮罩点击守卫
// （decideBackdropClose）与 Esc 共用 allowClose，防止 dirty 丢改动（P1-1）。
import { describe, expect, it } from "vitest";
import { decideBackdropClose, decideModalKeydown, focusableElements, nextFocusIndex } from "./modal";

describe("modal keydown decision (Esc close + Tab focus trap)", () => {
  it("closes on Escape regardless of Tab state", () => {
    expect(decideModalKeydown("Escape", false, 0, -1)).toEqual({ kind: "close" });
    expect(decideModalKeydown("Escape", true, 5, 2)).toEqual({ kind: "close" });
  });

  it("ignores non-Esc/Tab keys and empty containers", () => {
    expect(decideModalKeydown("Enter", false, 5, 0)).toEqual({ kind: "none" });
    expect(decideModalKeydown("Tab", false, 0, -1)).toEqual({ kind: "none" });
    expect(decideModalKeydown("Tab", true, 0, 3)).toEqual({ kind: "none" });
  });

  it("cycles forward with wrap-around", () => {
    expect(decideModalKeydown("Tab", false, 3, 0)).toEqual({ kind: "focus", index: 1 });
    expect(decideModalKeydown("Tab", false, 3, 2)).toEqual({ kind: "focus", index: 0 });
  });

  it("cycles backward with wrap-around", () => {
    expect(decideModalKeydown("Tab", true, 3, 2)).toEqual({ kind: "focus", index: 1 });
    expect(decideModalKeydown("Tab", true, 3, 0)).toEqual({ kind: "focus", index: 2 });
  });

  it("enters at the start/end when focus is outside the container", () => {
    // 焦点尚未进容器（如打开瞬间）：Tab 进首个控件，Shift+Tab 进最后一个。
    expect(decideModalKeydown("Tab", false, 4, -1)).toEqual({ kind: "focus", index: 0 });
    expect(decideModalKeydown("Tab", true, 4, -1)).toEqual({ kind: "focus", index: 3 });
  });
});

describe("decideBackdropClose (P1-1 backdrop dirty guard)", () => {
  it("closes when the shared allowClose guard passes", () => {
    expect(decideBackdropClose(true)).toEqual({ kind: "close" });
  });

  it("vetoes when the guard fails, matching the Esc path", () => {
    // dirty/提交在途：遮罩点击与 Esc 同被否决——关闭途径语义一致，输入不丢。
    expect(decideBackdropClose(false)).toEqual({ kind: "veto" });
  });
});

describe("nextFocusIndex", () => {
  it("returns -1 for an empty container", () => {
    expect(nextFocusIndex(0, -1, false)).toBe(-1);
    expect(nextFocusIndex(0, 2, true)).toBe(-1);
  });
});

describe("focusableElements", () => {
  it("collects interactive controls in DOM order and skips disabled/hidden", () => {
    const root = document.createElement("div");
    root.innerHTML = [
      '<button id="a">a</button>',
      '<button id="b" disabled>b</button>',
      '<input id="c" type="hidden" />',
      '<input id="d" />',
      '<select id="e"><option>1</option></select>',
      '<textarea id="f"></textarea>',
      '<a id="g" href="#g">g</a>',
      '<a id="h">h</a>',
      '<span id="i" tabindex="-1">i</span>',
      '<span id="j" tabindex="0">j</span>',
    ].join("");
    document.body.appendChild(root);
    try {
      expect(focusableElements(root).map((element) => element.id)).toEqual(["a", "d", "e", "f", "g", "j"]);
    } finally {
      root.remove();
    }
  });
});
