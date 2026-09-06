// 弹窗键盘可访问性（P-LDAP）：Esc 关闭 + Tab 焦点陷阱 + 打开初始聚焦 /
// 关闭焦点归还。决策纯函数与 kafka（kafkaModel.decideModalKeydown）同语义，
// 家族内交互统一；各弹窗组件经 useModalA11y 一行接线。
import { computed, nextTick, onBeforeUnmount, watch, type Ref } from "vue";

export const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not([disabled])",
  'input:not([disabled]):not([type="hidden"])',
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(", ");

export function focusableElements(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
}

/** Tab 焦点陷阱回绕：无焦点/越界时按方向取首/尾，否则循环步进；空容器返回 -1。 */
export function nextFocusIndex(count: number, currentIndex: number, shift: boolean): number {
  if (count <= 0) return -1;
  if (currentIndex < 0 || currentIndex >= count) return shift ? count - 1 : 0;
  return (currentIndex + (shift ? -1 : 1) + count) % count;
}

/** 弹层 keydown 决策：Esc → close；Tab → focus 回绕目标下标；其余 → none。 */
export type ModalKeydownDecision = { kind: "none" } | { kind: "close" } | { kind: "focus"; index: number };

export function decideModalKeydown(
  key: string,
  shiftKey: boolean,
  focusableCount: number,
  currentIndex: number,
): ModalKeydownDecision {
  if (key === "Escape") return { kind: "close" };
  if (key !== "Tab" || focusableCount <= 0) return { kind: "none" };
  return { kind: "focus", index: nextFocusIndex(focusableCount, currentIndex, shiftKey) };
}

/** 遮罩点击关闭决策：与 Esc（decideModalKeydown → allowClose）同一条守卫语义。
 *  否决（未保存修改/提交在途）时保持打开、输入不丢——此前遮罩 @click.self
 *  直接 close 绕过 allowClose，是编辑器 dirty 丢改动的根因（UI 扫描 P1-1）。 */
export type BackdropCloseDecision = { kind: "close" } | { kind: "veto" };

export function decideBackdropClose(allowClose: boolean): BackdropCloseDecision {
  return allowClose ? { kind: "close" } : { kind: "veto" };
}

export interface ModalA11yOptions {
  close: () => void;
  /** 返回 false 否决 Esc 关闭（如编辑器有未保存修改）；Tab 陷阱不受影响。 */
  allowClose?: () => boolean;
  /** 打开时初始聚焦目标（弹层内 CSS 选择器，如删除确认框聚焦"取消"而非
   *  标题栏 ✕，UI 扫描 P2-11）；缺失或不可达时回退首个可交互控件。 */
  initialFocus?: string;
}

/**
 * 弹窗打开期间挂 window keydown（Esc/Tab），打开时焦点进首个可交互控件、
 * 关闭时归还触发元素。同一时刻工作台只有一个弹窗在场，容器取当前
 * `.modal-backdrop .modal`。组件卸载自动摘除监听。
 */
export function useModalA11y(open: Ref<boolean> | (() => boolean), options: ModalA11yOptions): void {
  const isOpen = computed(() => (typeof open === "function" ? open() : open.value));
  let trigger: HTMLElement | null = null;
  let listening = false;

  function modalContainer(): HTMLElement | null {
    return document.querySelector<HTMLElement>(".modal-backdrop .modal");
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "Escape" && event.key !== "Tab") return;
    const root = modalContainer();
    if (!root && event.key !== "Escape") return;
    const focusables = root ? focusableElements(root) : [];
    const currentIndex = focusables.indexOf(document.activeElement as HTMLElement);
    const decision = decideModalKeydown(event.key, event.shiftKey, focusables.length, currentIndex);
    if (decision.kind === "close") {
      if (options.allowClose && !options.allowClose()) return;
      event.preventDefault();
      event.stopPropagation();
      options.close();
    } else if (decision.kind === "focus") {
      event.preventDefault();
      event.stopPropagation();
      focusables[decision.index]?.focus({ preventScroll: true });
    }
  }

  // immediate：以 open=true 直接挂载（父组件先置 open 再挂载/测试直挂）时
  // 也要接上监听并聚焦，否则该弹窗的 Esc/Tab 陷阱整场失效。
  watch(
    isOpen,
    (value) => {
      if (value) {
        trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        window.addEventListener("keydown", onKeydown);
        listening = true;
        void nextTick(() => {
          const root = modalContainer();
          if (!root) return;
          const initial = options.initialFocus ? root.querySelector<HTMLElement>(options.initialFocus) : null;
          if (initial && !initial.matches(":disabled")) initial.focus({ preventScroll: true });
          else focusableElements(root)[0]?.focus({ preventScroll: true });
        });
      } else if (listening) {
        window.removeEventListener("keydown", onKeydown);
        listening = false;
        trigger?.focus({ preventScroll: true });
        trigger = null;
      }
    },
    { immediate: true },
  );

  onBeforeUnmount(() => {
    if (listening) window.removeEventListener("keydown", onKeydown);
  });
}
