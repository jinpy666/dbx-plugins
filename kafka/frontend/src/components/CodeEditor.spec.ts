// @vitest-environment happy-dom
// CodeEditor 组件测试：CodeMirror 轻量挂载下断言 v-model 双向、disabled 只读、
// JSON invalid（外部红框 + 内置 linter 错误行）、wrap 开关与语言切换。
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import { nextTick } from "vue";
import { EditorView } from "@codemirror/view";
import CodeEditor from "./CodeEditor.vue";

function mountEditor(props: Record<string, unknown> = {}) {
  return mount(CodeEditor, {
    props: { modelValue: "", ...props },
  });
}

function exposedView(wrapper: ReturnType<typeof mountEditor>): EditorView {
  return (wrapper.vm as unknown as { getView: () => EditorView }).getView();
}

describe("CodeEditor", () => {
  it("mounts CodeMirror with line numbers, placeholder and wrap toggle", () => {
    const wrapper = mountEditor({ language: "json", placeholder: "ph-text" });
    expect(wrapper.find(".cm-lineNumbers").exists()).toBe(true);
    expect(wrapper.find(".cm-error-gutter").exists()).toBe(true);
    expect(wrapper.find(".cm-placeholder").text()).toBe("ph-text");
    const toggle = wrapper.find(".dbx-code-editor__wrap-toggle");
    expect(toggle.exists()).toBe(true);
    expect(toggle.classes()).toContain("is-active");
  });

  it("syncs external modelValue changes into the editor without echo loops", async () => {
    const wrapper = mountEditor({ modelValue: "a" });
    expect(wrapper.emitted("update:modelValue")).toBeUndefined();
    await wrapper.setProps({ modelValue: "line1\nline2" });
    expect((wrapper.vm as unknown as { getDoc: () => string }).getDoc()).toBe("line1\nline2");
    // 外部注入引发至多一次等值回传即收敛；相同值重复下发不再产生事件（防循环）。
    await wrapper.setProps({ modelValue: "line1\nline2" });
    const events = wrapper.emitted("update:modelValue") ?? [];
    expect(events).toHaveLength(1);
    expect(events[0]).toEqual(["line1\nline2"]);
  });

  it("emits update:modelValue when the user edits the document", () => {
    const wrapper = mountEditor({ modelValue: "" });
    exposedView(wrapper).dispatch({ changes: { from: 0, insert: "typed by user" } });
    const events = wrapper.emitted("update:modelValue");
    expect(events?.at(-1)).toEqual(["typed by user"]);
  });

  it("becomes read-only when disabled", async () => {
    const wrapper = mountEditor({ modelValue: "x", disabled: true });
    expect(wrapper.find(".dbx-code-editor").classes()).toContain("is-disabled");
    expect(wrapper.find(".dbx-code-editor__wrap-toggle").attributes("disabled")).toBeDefined();
    const view = exposedView(wrapper);
    expect(view.state.facet(EditorView.editable)).toBe(false);
    expect(view.state.readOnly).toBe(true);
    // disabled 变化时 compartment 动态重配置
    await wrapper.setProps({ disabled: false });
    expect(exposedView(wrapper).state.facet(EditorView.editable)).toBe(true);
  });

  it("marks invalid JSON lines via the built-in linter in json mode", async () => {
    const wrapper = mountEditor({ modelValue: "{bad}", language: "json" });
    await nextTick();
    expect(wrapper.find(".cm-errorLine").exists()).toBe(true);
    expect(wrapper.find(".cm-errorMarker").exists()).toBe(true);
    // 修正为合法 JSON 后错误行消失，gutter "!" 标记同步清除（lineMarkerChange 回归）
    await wrapper.setProps({ modelValue: '{"a": 1}' });
    await nextTick();
    expect(wrapper.find(".cm-errorLine").exists()).toBe(false);
    expect(wrapper.find(".cm-errorMarker:not(.cm-errorMarker--idle)").exists()).toBe(false);
  });

  it("does not lint in text mode and reacts to language switches", async () => {
    const wrapper = mountEditor({ modelValue: "{bad}", language: "text" });
    await nextTick();
    expect(wrapper.find(".cm-errorLine").exists()).toBe(false);
    await wrapper.setProps({ language: "json" });
    await nextTick();
    expect(wrapper.find(".cm-errorLine").exists()).toBe(true);
  });

  it("drives the invalid red border from the prop", async () => {
    const wrapper = mountEditor({ modelValue: "anything" });
    expect(wrapper.find(".dbx-code-editor").classes()).not.toContain("is-invalid");
    await wrapper.setProps({ invalid: true });
    expect(wrapper.find(".dbx-code-editor").classes()).toContain("is-invalid");
  });

  it("toggles line wrapping from the corner switch", async () => {
    const wrapper = mountEditor({ modelValue: "" });
    const toggle = wrapper.find(".dbx-code-editor__wrap-toggle");
    expect(toggle.classes()).toContain("is-active");
    await toggle.trigger("click");
    expect(wrapper.find(".dbx-code-editor__wrap-toggle").classes()).not.toContain("is-active");
  });
});
