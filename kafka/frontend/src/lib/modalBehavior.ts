/**
 * 弹层行为下沉（UI 扫描第 2 轮 P1-5）：modal/drawer 通用的
 * 「Esc 关闭 + Tab 焦点陷阱 + 打开聚焦 + 关闭归还触发元素」组合式函数。
 *
 * 决策逻辑复用 kafkaModel.decideModalKeydown（纯函数，有单测）；本模块只做
 * DOM 接线（与 MessagesPanel 抽屉 / ConnectionsPanel 主弹窗的已验证实现同源）。
 * 层级语义照连接弹窗导入助手子弹层的捕获态参照：后打开的层在上，keydown 只由
 * 栈顶层响应——子弹层在场时 Esc 只关子弹层、不透传给下层（2→1→0 逐层关闭）。
 */
import { nextTick, onBeforeUnmount, watch, type Ref } from "vue";
import { decideModalKeydown, focusableElements } from "./kafkaModel";

interface ModalLayer {
  open: () => boolean;
  container: () => HTMLElement | null;
  close: () => void;
}

/** 模块级层栈：按打开顺序压栈，仅栈顶层响应 Esc/Tab。 */
const layerStack: ModalLayer[] = [];

export interface ModalBehaviorOptions {
  /** 弹层开合状态（驱动 v-if 的同一 ref）。 */
  open: Ref<boolean>;
  /** 弹层容器元素 ref（`.modal` / `.drawer` 根；建议补 tabindex="-1" 作聚焦兜底）。 */
  container: Ref<HTMLElement | null>;
  /** 关闭回调（如 `() => (createOpen.value = false)`）。 */
  close: () => void;
}

/**
 * 把一套弹层接入统一 Esc/焦点行为。在 setup 顶层调用：
 *
 * ```ts
 * const createOpen = ref(false);
 * const createModalEl = ref<HTMLElement | null>(null);
 * useModalBehavior({ open: createOpen, container: createModalEl, close: () => (createOpen.value = false) });
 * ```
 */
export function useModalBehavior(options: ModalBehaviorOptions): void {
  let trigger: HTMLElement | null = null;

  const layer: ModalLayer = {
    open: () => options.open.value,
    container: () => options.container.value,
    close: options.close,
  };

  function onKeydown(event: KeyboardEvent) {
    if (!options.open.value) return;
    // 只有栈顶层响应：上层弹层（如子弹层）在场时本层让位，Esc 不跨层透传。
    if (layerStack[layerStack.length - 1] !== layer) return;
    if (event.key !== "Escape" && event.key !== "Tab") return;
    const container = options.container.value;
    if (!container && event.key !== "Escape") return;
    const focusables = container ? focusableElements(container) : [];
    const currentIndex = focusables.indexOf(document.activeElement as HTMLElement);
    const decision = decideModalKeydown(event.key, event.shiftKey, focusables.length, currentIndex);
    if (decision.kind === "close") {
      event.preventDefault();
      event.stopPropagation();
      options.close();
    } else if (decision.kind === "focus") {
      event.preventDefault();
      event.stopPropagation();
      focusables[decision.index]?.focus();
    }
  }

  const stopWatch = watch(options.open, (open, previous) => {
    if (open && !previous) {
      // 打开：记住触发元素（供归还），入栈并监听；下一帧焦点进容器首个
      // 可交互控件（无控件时兜底容器自身，需 tabindex="-1"）。
      trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      layerStack.push(layer);
      window.addEventListener("keydown", onKeydown);
      void nextTick(() => {
        const root = options.container.value;
        if (!root || !options.open.value) return;
        const first = focusableElements(root)[0];
        (first ?? root).focus({ preventScroll: true });
      });
    } else if (!open && previous) {
      // 关闭（Esc/✕/footer/遮罩）：出栈、摘监听、焦点归还触发元素。
      const index = layerStack.lastIndexOf(layer);
      if (index >= 0) layerStack.splice(index, 1);
      window.removeEventListener("keydown", onKeydown);
      trigger?.focus({ preventScroll: true });
      trigger = null;
    }
  });

  onBeforeUnmount(() => {
    stopWatch();
    const index = layerStack.lastIndexOf(layer);
    if (index >= 0) layerStack.splice(index, 1);
    window.removeEventListener("keydown", onKeydown);
  });
}
