// @vitest-environment happy-dom
// PasswordAttributeEditor tests (M6 N2): scheme dropdown (default {SSHA}),
// show/hide toggle, random generation feeding the hash emit, apply emitting a
// parseable RFC 2307 value + one-shot plainGenerated, disabled gating and
// existing-value recognition. i18n keys are placeholders (merged later), and
// t() passes unknown keys through verbatim, so assertions match the raw keys.
import { afterEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import PasswordAttributeEditor from "./PasswordAttributeEditor.vue";
import { parsePasswordHash } from "../lib/passwordHash";

const SCHEME_SELECT = ".password-editor select";
const PLAIN_INPUT = ".password-editor input";
const TOGGLE_BUTTON = ".password-editor .icon-button";
const GENERATE_BUTTON = ".password-editor button:not(.icon-button):not(.primary-button)";
const APPLY_BUTTON = ".password-editor .primary-button";

const mountEditor = (props: { modelValue?: string; disabled?: boolean } = {}) =>
  mount(PasswordAttributeEditor, { props: { modelValue: "", ...props } });

const tracked: Awaited<ReturnType<typeof mountEditor>>[] = [];

afterEach(() => {
  for (const wrapper of tracked.splice(0)) wrapper.unmount();
});

const track = (props?: { modelValue?: string; disabled?: boolean }) => {
  const wrapper = mountEditor(props);
  tracked.push(wrapper);
  return wrapper;
};

const setScheme = async (wrapper: Awaited<ReturnType<typeof mountEditor>>, scheme: string) => {
  await wrapper.find(SCHEME_SELECT).setValue(scheme);
};

// apply 内部先 await Web Crypto digest 再 emit；subtle.digest 可能跨事件
// 循环轮次，单个 flushPromises 不保证等到，用 vi.waitFor 轮询 emit 落地。
const applyAndWait = async (wrapper: Awaited<ReturnType<typeof mountEditor>>) => {
  await wrapper.find(APPLY_BUTTON).trigger("click");
  await vi.waitFor(() => expect(wrapper.emitted("update:modelValue")).toBeDefined());
  await flushPromises();
};

describe("PasswordAttributeEditor", () => {
  it("defaults the scheme dropdown to {SSHA} and starts with an empty password", () => {
    const wrapper = track();
    expect((wrapper.find(SCHEME_SELECT).element as HTMLSelectElement).value).toBe("{SSHA}");
    expect(wrapper.find(SCHEME_SELECT).findAll("option")).toHaveLength(7);
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).value).toBe("");
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).type).toBe("password");
    // 空存储值：不显示任何"现有值"提示。
    expect(wrapper.find(".hint").exists()).toBe(false);
    expect(wrapper.find(".form-error").exists()).toBe(false);
    expect(wrapper.find(".password-editor").attributes("data-scheme")).toBe("");
  });

  it("keeps the apply button disabled until a password is entered", async () => {
    const wrapper = track();
    expect((wrapper.find(APPLY_BUTTON).element as HTMLButtonElement).disabled).toBe(true);
    await wrapper.find(PLAIN_INPUT).setValue("password");
    expect((wrapper.find(APPLY_BUTTON).element as HTMLButtonElement).disabled).toBe(false);
  });

  it("toggles the password input between hidden and visible", async () => {
    const wrapper = track();
    await wrapper.find(PLAIN_INPUT).setValue("password");
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).type).toBe("password");
    expect((wrapper.find(TOGGLE_BUTTON).element as HTMLButtonElement).disabled).toBe(false);
    await wrapper.find(TOGGLE_BUTTON).trigger("click");
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).type).toBe("text");
    await wrapper.find(TOGGLE_BUTTON).trigger("click");
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).type).toBe("password");
  });

  it("hashes on apply with the selected scheme and emits a parseable value", async () => {
    const wrapper = track();
    await setScheme(wrapper, "{SHA512}");
    await wrapper.find(PLAIN_INPUT).setValue("password");
    await applyAndWait(wrapper);
    const events = wrapper.emitted("update:modelValue");
    expect(events).toHaveLength(1);
    const stored = events![0][0] as string;
    expect(stored.startsWith("{SHA512}")).toBe(true);
    expect(parsePasswordHash(stored)?.scheme).toBe("{SHA512}");
    // 非随机生成路径：不发出一次性明文事件；输入框随即清空。
    expect(wrapper.emitted("plainGenerated")).toBeUndefined();
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).value).toBe("");
  });

  it("emits plainGenerated only when the password came from the random generator", async () => {
    const wrapper = track();
    await wrapper.find(GENERATE_BUTTON).trigger("click");
    const generated = (wrapper.find(PLAIN_INPUT).element as HTMLInputElement).value;
    expect(generated).not.toBe("");
    // 随机生成后明文自动回显，便于管理员确认一次性密码。
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).type).toBe("text");
    await applyAndWait(wrapper);
    const events = wrapper.emitted("update:modelValue");
    expect(events).toHaveLength(1);
    const stored = events![0][0] as string;
    expect(stored.startsWith("{SSHA}")).toBe(true);
    expect(parsePasswordHash(stored)?.scheme).toBe("{SSHA}");
    expect(wrapper.emitted("plainGenerated")![0][0]).toBe(generated);
    // 提交后明文不再留在组件状态。
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).value).toBe("");
  });

  it("regenerates a different password on every click of the random button", async () => {
    const wrapper = track();
    await wrapper.find(GENERATE_BUTTON).trigger("click");
    const first = (wrapper.find(PLAIN_INPUT).element as HTMLInputElement).value;
    await wrapper.find(GENERATE_BUTTON).trigger("click");
    const second = (wrapper.find(PLAIN_INPUT).element as HTMLInputElement).value;
    expect(first).toHaveLength(16);
    expect(second).toHaveLength(16);
    expect(first).not.toBe(second);
  });

  it("recognises an existing hash and surfaces its scheme without plaintext", () => {
    const stored = "{SSHA}bCvrAbvqRheNTx76n4jgXHj3a0MBAgMEBQYHCAkKCwwNDg8Q";
    const wrapper = track({ modelValue: stored });
    expect(wrapper.find(".password-editor").attributes("data-scheme")).toBe("{SSHA}");
    expect(wrapper.find(".hint").text()).toContain("{SSHA}");
    expect(wrapper.find(".hint").text()).not.toContain(stored);
    expect(wrapper.find(".form-error").exists()).toBe(false);
  });

  it("warns on plaintext storage and on unknown scheme labels instead of guessing", () => {
    const plaintext = track({ modelValue: "clear-secret" });
    expect(plaintext.find(".password-editor").attributes("data-scheme")).toBe("{CLEARTEXT}");
    expect(plaintext.find(".form-error").exists()).toBe(true);

    const unknown = track({ modelValue: "{MD5}abc=" });
    expect(unknown.find(".password-editor").attributes("data-scheme")).toBe("");
    expect(unknown.find(".form-error").exists()).toBe(true);
  });

  it("gates every control in the disabled state and swallows generate/apply", async () => {
    const wrapper = track({ modelValue: "{SHA}W6ph5Mm5Pz8GgiULbPgzG37mj9g=", disabled: true });
    expect((wrapper.find(SCHEME_SELECT).element as HTMLSelectElement).disabled).toBe(true);
    expect((wrapper.find(PLAIN_INPUT).element as HTMLInputElement).disabled).toBe(true);
    for (const button of wrapper.findAll("button")) {
      expect((button.element as HTMLButtonElement).disabled).toBe(true);
    }
    await wrapper.find(GENERATE_BUTTON).trigger("click");
    await wrapper.find(APPLY_BUTTON).trigger("click");
    expect(wrapper.emitted("update:modelValue")).toBeUndefined();
    expect(wrapper.emitted("plainGenerated")).toBeUndefined();
  });
});
