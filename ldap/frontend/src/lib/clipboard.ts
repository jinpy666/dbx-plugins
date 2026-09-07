// 剪贴板写入：宿主桥优先（Host API clipboard.writeText），桥缺失或写入失败
// 时回退 execCommand（覆盖 mock / Host API 1.0 等无剪贴板桥的环境）。
// 自 App.vue 内联实现抽出，供树 copyDn 与详情弹窗复制按钮共用。
export async function writeClipboardText(text: string): Promise<boolean> {
  const bridge = window.dbxPlugin?.clipboard?.writeText;
  if (typeof bridge === "function") {
    try {
      await bridge(text);
      return true;
    } catch {
      // 宿主桥写入失败 → 走 execCommand 兜底
    }
  }
  try {
    const helper = document.createElement("textarea");
    helper.value = text;
    helper.setAttribute("readonly", "");
    helper.style.position = "fixed";
    helper.style.opacity = "0";
    document.body.appendChild(helper);
    helper.select();
    const ok = document.execCommand("copy");
    helper.remove();
    return ok;
  } catch {
    return false;
  }
}
