<script setup lang="ts">
// CodeMirror 6 薄封装（CodeEditor）：v-model、行号、自动换行开关（默认开）、
// JSON 模式（lang-json 高亮 + 内置最小扫描器 linter 标红错误行）、invalid 外部
// 驱动红框、disabled 只读态。主题全部引用 style.css 的 DBX 令牌 CSS 变量
// （--background/--foreground/--muted/--border/--primary/--destructive…），
// 宿主 appearance 管线改 documentElement 变量/data-theme 时自动跟随（明暗
// 两套无需重建编辑器）；语法 token 色走 --cm-* 变量（见底部 style 块）。
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Compartment, EditorState, StateEffect, StateField, type Extension } from "@codemirror/state";
import {
  Decoration,
  type DecorationSet,
  drawSelection,
  EditorView,
  GutterMarker,
  gutter,
  keymap,
  lineNumbers,
  placeholder as cmPlaceholder,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { bracketMatching, HighlightStyle, indentOnInput, syntaxHighlighting } from "@codemirror/language";
import { json } from "@codemirror/lang-json";
import { tags as lezerTags } from "@lezer/highlight";
import { jsonErrorLine } from "../lib/kafkaModel";
import { t } from "../lib/i18n";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    language?: "json" | "text";
    placeholder?: string;
    disabled?: boolean;
    minHeight?: string;
    maxHeight?: string;
    invalid?: boolean;
    /** 自动换行初始状态（默认开）；编辑器右下角提供开关。 */
    wrap?: boolean;
  }>(),
  { language: "text", placeholder: "", disabled: false, minHeight: "96px", maxHeight: "", invalid: false, wrap: true },
);

const emit = defineEmits<{
  (e: "update:modelValue", value: string): void;
}>();

const hostEl = ref<HTMLDivElement | null>(null);
const wrapOn = ref(props.wrap);

let view: EditorView | null = null;
const readOnlyCompartment = new Compartment();
const languageCompartment = new Compartment();
const placeholderCompartment = new Compartment();
const wrapCompartment = new Compartment();

// -- JSON 错误行（StateField + 行装饰 + 侧边 gutter 标记） ----------------------

const setErrorLine = StateEffect.define<number | null>();
const errorLineField = StateField.define<number | null>({
  create: () => null,
  update(value, transaction) {
    for (const effect of transaction.effects) if (effect.is(setErrorLine)) return effect.value;
    return value;
  },
});
const errorLineDecoration = Decoration.line({ class: "cm-errorLine" });
const errorDecorations: Extension = EditorView.decorations.compute([errorLineField], (state): DecorationSet => {
  const line = state.field(errorLineField);
  if (line === null) return Decoration.none;
  return Decoration.set([errorLineDecoration.range(state.doc.line(line).from)]);
});

class JsonErrorMarker extends GutterMarker {
  toDOM(): HTMLElement {
    const marker = document.createElement("span");
    marker.className = "cm-errorMarker";
    marker.textContent = "!";
    marker.setAttribute("aria-label", t("codeEditor.jsonError"));
    marker.title = t("codeEditor.jsonError");
    return marker;
  }
}
class JsonErrorSpacer extends GutterMarker {
  toDOM(): HTMLElement {
    const marker = document.createElement("span");
    marker.className = "cm-errorMarker cm-errorMarker--idle";
    marker.textContent = "·";
    return marker;
  }
}
const jsonErrorMarker = new JsonErrorMarker();
const jsonErrorSpacer = new JsonErrorSpacer();

const errorGutter = gutter({
  class: "cm-error-gutter",
  // BlockInfo.from 是偏移量；errorLineField 存 1-based 行号，此处换算比较。
  lineMarker(editorView, lineBlock) {
    return editorView.state.doc.lineAt(lineBlock.from).number === editorView.state.field(errorLineField)
      ? jsonErrorMarker
      : null;
  },
  // setErrorLine 是 docChanged 之后的独立事务（不触发 docChanged/viewportChanged
  // 重渲染），必须显式声明 field 变化时重绘 gutter，否则修正 JSON 后 "!" 标记残留。
  lineMarkerChange: (update) => update.startState.field(errorLineField) !== update.state.field(errorLineField),
  initialSpacer: () => jsonErrorSpacer,
});

function computeErrorLine(text: string): number | null {
  return props.language === "json" ? jsonErrorLine(text) : null;
}

function syncErrorLine(nextView: EditorView): void {
  const found = computeErrorLine(nextView.state.doc.toString());
  if (found !== nextView.state.field(errorLineField)) {
    nextView.dispatch({ effects: setErrorLine.of(found) });
  }
}

// -- 主题（DBX 令牌变量；明暗跟随宿主 data-theme/--* 变量更新） -----------------

const dbxEditorTheme = EditorView.theme({
  "&": { height: "100%", fontSize: "12px", backgroundColor: "transparent", color: "var(--foreground)" },
  ".cm-scroller": { fontFamily: "var(--mono-font-family)", lineHeight: "1.55", overflow: "auto" },
  ".cm-content": { caretColor: "var(--primary)", paddingBottom: "10px" },
  "&.cm-focused": { outline: "none" },
  ".cm-gutters": { backgroundColor: "var(--muted)", color: "var(--muted-foreground)", border: "none", borderRight: "1px solid var(--border)" },
  ".cm-lineNumbers .cm-gutterElement": { padding: "0 6px 0 8px", minWidth: "26px" },
  ".cm-error-gutter": { minWidth: "18px", textAlign: "center" },
  ".cm-activeLine": { backgroundColor: "color-mix(in srgb, var(--accent) 38%, transparent)" },
  ".cm-activeLineGutter": { backgroundColor: "color-mix(in srgb, var(--accent) 62%, transparent)", color: "var(--foreground)" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": { backgroundColor: "color-mix(in srgb, var(--primary) 26%, transparent) !important" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--primary)" },
  ".cm-errorLine": { backgroundColor: "color-mix(in srgb, var(--destructive) 10%, transparent)", boxShadow: "inset 2px 0 0 var(--destructive)" },
  ".cm-errorMarker": { color: "var(--destructive)", fontWeight: "700", fontSize: "11px" },
  ".cm-errorMarker--idle": { color: "color-mix(in srgb, var(--muted-foreground) 45%, transparent)", fontWeight: "400" },
  ".cm-placeholder": { color: "var(--muted-foreground)" },
});

// JSON token 色：VS Code Light+/Dark+ 同源，经 --cm-* 变量适配明暗两套。
const jsonHighlightStyle = HighlightStyle.define([
  { tag: lezerTags.propertyName, color: "var(--cm-prop)" },
  { tag: lezerTags.string, color: "var(--cm-string)" },
  { tag: lezerTags.number, color: "var(--cm-number)" },
  { tag: lezerTags.bool, color: "var(--cm-key)" },
  { tag: lezerTags.null, color: "var(--cm-null)" },
  { tag: [lezerTags.punctuation, lezerTags.separator], color: "var(--cm-punct)" },
]);

function languageExtensions(): Extension[] {
  if (props.language !== "json") return [];
  return [json(), syntaxHighlighting(jsonHighlightStyle)];
}

function readOnlyExtensions(): Extension[] {
  return [EditorState.readOnly.of(props.disabled), EditorView.editable.of(!props.disabled)];
}

onMounted(() => {
  if (!hostEl.value) return;
  view = new EditorView({
    parent: hostEl.value,
    state: EditorState.create({
      doc: props.modelValue ?? "",
      extensions: [
        lineNumbers(),
        errorGutter,
        errorDecorations,
        errorLineField,
        history(),
        indentOnInput(),
        bracketMatching(),
        drawSelection(),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        languageCompartment.of(languageExtensions()),
        readOnlyCompartment.of(readOnlyExtensions()),
        placeholderCompartment.of(props.placeholder ? cmPlaceholder(props.placeholder) : []),
        wrapCompartment.of(wrapOn.value ? EditorView.lineWrapping : []),
        dbxEditorTheme,
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            emit("update:modelValue", update.state.doc.toString());
            syncErrorLine(update.view);
          }
        }),
      ],
    }),
  });
  syncErrorLine(view);
});

onBeforeUnmount(() => {
  view?.destroy();
  view = null;
});

// 外部注入（清空/样例/回填）：仅值不同才 dispatch，防止编辑器回 emit 成环。
watch(
  () => props.modelValue,
  (next) => {
    if (!view) return;
    const current = view.state.doc.toString();
    if (next !== current) view.dispatch({ changes: { from: 0, to: current.length, insert: next ?? "" } });
    syncErrorLine(view);
  },
);

watch(
  () => props.language,
  () => {
    if (!view) return;
    view.dispatch({ effects: languageCompartment.reconfigure(languageExtensions()) });
    syncErrorLine(view);
  },
);

watch(
  () => props.disabled,
  () => view?.dispatch({ effects: readOnlyCompartment.reconfigure(readOnlyExtensions()) }),
);

watch(
  () => props.placeholder,
  (next) => view?.dispatch({ effects: placeholderCompartment.reconfigure(next ? [cmPlaceholder(next)] : []) }),
);

function toggleWrap(): void {
  wrapOn.value = !wrapOn.value;
  view?.dispatch({ effects: wrapCompartment.reconfigure(wrapOn.value ? [EditorView.lineWrapping] : []) });
}

// 测试/宿主扩展点：读当前编辑器文档与视图（不进 props/emits 契约）。
defineExpose({
  focus: () => view?.focus(),
  getDoc: () => view?.state.doc.toString() ?? "",
  getView: () => view,
});
</script>

<template>
  <div
    ref="hostEl"
    class="dbx-code-editor"
    :class="{ 'is-disabled': disabled, 'is-invalid': invalid }"
    :style="{
      '--ce-min-height': minHeight || undefined,
      '--ce-max-height': maxHeight || undefined,
    }"
  >
    <button
      class="dbx-code-editor__wrap-toggle"
      type="button"
      :class="{ 'is-active': wrapOn }"
      :disabled="disabled"
      :title="t('codeEditor.wrapToggle')"
      @click="toggleWrap"
    >
      {{ wrapOn ? t("codeEditor.wrap") : t("codeEditor.unwrap") }}
    </button>
  </div>
</template>

<!-- 全局样式（非 scoped）：所有规则收敛在 .dbx-code-editor 前缀下，配色走
     DBX 令牌变量；明暗跟随宿主 appearance 管线设置的 data-theme。 -->
<style>
.dbx-code-editor {
  --cm-key: #0451a5;
  --cm-string: #a31515;
  --cm-number: #098658;
  --cm-null: #795e26;
  --cm-punct: #4b4b4b;
  --cm-prop: #795e26;
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: var(--ce-min-height, 96px);
  max-height: var(--ce-max-height, none);
  height: 100%;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: color-mix(in srgb, var(--background) 95%, var(--foreground));
}
.dbx-code-editor .cm-editor {
  min-height: 0;
  flex: 1 1 auto;
}
.dbx-code-editor:focus-within {
  border-color: color-mix(in srgb, var(--primary) 70%, var(--border));
}
.dbx-code-editor.is-invalid {
  border-color: color-mix(in srgb, var(--destructive) 65%, var(--border));
}
.dbx-code-editor.is-disabled {
  background: var(--muted);
  opacity: 0.72;
}
.dbx-code-editor.is-disabled .cm-content {
  color: var(--muted-foreground);
}
.dbx-code-editor__wrap-toggle {
  position: absolute;
  right: 6px;
  bottom: 6px;
  z-index: 2;
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 1px 8px;
  background: color-mix(in srgb, var(--background) 82%, transparent);
  color: var(--muted-foreground);
  font-size: 10px;
  cursor: pointer;
}
.dbx-code-editor__wrap-toggle:hover:not(:disabled),
.dbx-code-editor__wrap-toggle.is-active {
  color: var(--primary);
  border-color: color-mix(in srgb, var(--primary) 55%, var(--border));
}
:root[data-theme="dark"] .dbx-code-editor {
  --cm-key: #569cd6;
  --cm-string: #ce9178;
  --cm-number: #b5cea8;
  --cm-null: #dcdcaa;
  --cm-punct: #d4d4d4;
  --cm-prop: #9cdcfe;
}
</style>
