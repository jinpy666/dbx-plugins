// @vitest-environment happy-dom
// SearchForm workbench UI tests (M5-a UI test track): builder → live preview,
// ≠ operator, validation gating, builder↔source mode round-trip. Network
// calls (presets/schema) reject harmlessly without a dbxPlugin host bridge.
import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import SearchForm from "./SearchForm.vue";

async function mountForm() {
  const wrapper = mount(SearchForm, { props: { baseDn: "dc=demo,dc=dbx" } });
  await wrapper.vm.$nextTick();
  return wrapper;
}

const previewText = (wrapper: Awaited<ReturnType<typeof mountForm>>) => wrapper.find(".qb-preview").text();

describe("SearchForm (filter builder)", () => {
  it("shows the match-all fallback preview on an empty condition", async () => {
    const wrapper = await mountForm();
    expect(wrapper.find(".filter-builder").exists()).toBe(true);
    expect(previewText(wrapper)).toBe("(objectClass=*)");
  });

  it("generates the live RFC 4515 preview from builder conditions", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    await wrapper.find(".qb-value").setValue("admin");
    expect(previewText(wrapper)).toBe("(uid=admin)");
  });

  it("builds a ≠ clause via the notEquals operator", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    await wrapper.find(".qb-node select").setValue("notEquals");
    await wrapper.find(".qb-value").setValue("admin");
    expect(previewText(wrapper)).toBe("(!(uid=admin))");
  });

  it("gates submit while a clause is half-filled and releases when valid", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    expect(wrapper.find(".form-error").exists()).toBe(true);
    await wrapper.find("form").trigger("submit");
    expect(wrapper.emitted("run")).toBeUndefined();
    await wrapper.find(".qb-value").setValue("admin");
    expect(wrapper.find(".form-error").exists()).toBe(false);
    await wrapper.find("form").trigger("submit");
    expect(wrapper.emitted("run")).toHaveLength(1);
    expect(wrapper.emitted("run")![0][0]).toMatchObject({ baseDn: "dc=demo,dc=dbx", filter: "(uid=admin)" });
  });

  it("carries the generated filter into source mode and back", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    await wrapper.find(".qb-value").setValue("admin");
    const modeButtons = wrapper.findAll(".mode-switch button");
    await modeButtons[1].trigger("click");
    expect(wrapper.find(".filter-source").exists()).toBe(true);
    const sourceInput = wrapper.find(".filter-source input");
    expect((sourceInput.element as HTMLInputElement).value).toBe("(uid=admin)");

    await sourceInput.setValue("(!(uid=admin))");
    await modeButtons[0].trigger("click");
    expect(previewText(wrapper)).toBe("(!(uid=admin))");
  });

  it("flags invalid source filters and keeps source mode authoritative", async () => {
    const wrapper = await mountForm();
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    await wrapper.find(".filter-source input").setValue("(uid");
    expect(wrapper.find(".form-error").exists()).toBe(true);
  });

  it("disables the search button while the filter is invalid, with a hint (P2-5)", async () => {
    const wrapper = await mountForm();
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    await wrapper.find(".filter-source input").setValue("(uid=*0001");
    const runButton = wrapper.find(".primary-button.compact");
    expect(runButton.attributes("disabled")).toBeDefined();
    expect(runButton.attributes("title")).toBe("LDAP 过滤器不合法");
    await wrapper.find("form").trigger("submit");
    expect(wrapper.emitted("run")).toBeUndefined();
    // 恢复合法后按钮解锁。
    await wrapper.find(".filter-source input").setValue("(uid=*0001)");
    expect(runButton.attributes("disabled")).toBeUndefined();
    await wrapper.find("form").trigger("submit");
    expect(wrapper.emitted("run")).toHaveLength(1);
  });

  it("flashes the Base DN input when a tree selection rewrites it (P2-7)", async () => {
    const wrapper = await mountForm();
    const baseInput = wrapper.find(".field input.mono");
    expect(baseInput.classes()).not.toContain("base-dn-flash");
    // 模拟树节点联动（App.selectEntry → applyBaseDn）。
    (wrapper.vm as unknown as { applyBaseDn: (dn: string) => void }).applyBaseDn("ou=people,dc=demo,dc=dbx");
    await wrapper.vm.$nextTick();
    expect((baseInput.element as HTMLInputElement).value).toBe("ou=people,dc=demo,dc=dbx");
    expect(baseInput.classes()).toContain("base-dn-flash");
    expect(baseInput.attributes("title")).toBe("Base DN 已跟随选中的树节点");
    // 初始化回填（highlight=false）与同值回填不触发高亮。
    (wrapper.vm as unknown as { applyBaseDn: (dn: string, highlight?: boolean) => void }).applyBaseDn("dc=other,dc=dbx", false);
    await wrapper.vm.$nextTick();
    expect(baseInput.classes()).not.toContain("base-dn-flash");
  });

  it("runs a subtree search at a tree node immediately, falling back to match-all on a fresh form", async () => {
    const wrapper = await mountForm();
    // 新表单构建器是空子句（表单本身不可运行）：右键子树搜索仍必须出结果，
    // 过滤回退匹配全部；范围强制 sub；Base DN 同步改写并带跟随高亮。
    (wrapper.vm as unknown as { runSubtreeAt: (dn: string) => void }).runSubtreeAt("ou=people,dc=demo,dc=dbx");
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("run")?.[0][0]).toMatchObject({
      baseDn: "ou=people,dc=demo,dc=dbx",
      scope: "sub",
      filter: "(objectClass=*)",
    });
    const baseInput = wrapper.find(".field input.mono");
    expect((baseInput.element as HTMLInputElement).value).toBe("ou=people,dc=demo,dc=dbx");
    expect(baseInput.classes()).toContain("base-dn-flash");
  });

  it("keeps a configured filter (and forces scope=sub) when the tree requests a subtree search", async () => {
    const wrapper = await mountForm();
    await wrapper.find(".qb-attr").setValue("uid");
    await wrapper.find(".qb-value").setValue("admin");
    // 用户此前把范围切成 base：右键子树搜索要强制回 sub，过滤器照常沿用。
    await wrapper.find("select").setValue("base");
    (wrapper.vm as unknown as { runSubtreeAt: (dn: string) => void }).runSubtreeAt("ou=people,dc=demo,dc=dbx");
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("run")?.[0][0]).toMatchObject({
      baseDn: "ou=people,dc=demo,dc=dbx",
      scope: "sub",
      filter: "(uid=admin)",
    });
  });

  it("runs a filtered search at a base immediately when the tree requests members (right-click)", async () => {
    const wrapper = await mountForm();
    // 先把范围切成 base：成员直达要强制回 sub（语义同 runSubtreeAt）。
    await wrapper.find("select").setValue("base");
    (wrapper.vm as unknown as { runFilterAt: (baseDn: string, filter: string) => void }).runFilterAt("dc=x", "(memberOf=cn=g,dc=x)");
    await wrapper.vm.$nextTick();
    // 源码模式激活且承载调用方组织的过滤器（构建器不逆向解析）。
    expect(wrapper.find(".filter-source").exists()).toBe(true);
    const sourceInput = wrapper.find(".filter-source input");
    expect((sourceInput.element as HTMLInputElement).value).toBe("(memberOf=cn=g,dc=x)");
    expect(wrapper.find(".filter-builder").exists()).toBe(false);
    // Base DN 同步改写（draft.baseDn）并立即发出运行请求。
    const baseInput = wrapper.find(".field input.mono");
    expect((baseInput.element as HTMLInputElement).value).toBe("dc=x");
    expect(wrapper.emitted("run")?.[0][0]).toMatchObject({
      baseDn: "dc=x",
      filter: "(memberOf=cn=g,dc=x)",
      scope: "sub",
    });
  });

  it("skips the right-click members run while the form is disabled", async () => {
    const wrapper = mount(SearchForm, { props: { baseDn: "dc=demo,dc=dbx", disabled: true } });
    await wrapper.vm.$nextTick();
    (wrapper.vm as unknown as { runFilterAt: (baseDn: string, filter: string) => void }).runFilterAt("dc=x", "(memberOf=cn=g,dc=x)");
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("run")).toBeUndefined();
  });

  it("falls back to match-all when the right-click members filter arrives empty", async () => {
    const wrapper = await mountForm();
    (wrapper.vm as unknown as { runFilterAt: (baseDn: string, filter: string) => void }).runFilterAt("dc=x", "  ");
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("run")?.[0][0]).toMatchObject({
      baseDn: "dc=x",
      scope: "sub",
      filter: "(objectClass=*)",
    });
    // 源码模式同样回填兜底值，与实际发出的过滤器一致。
    const sourceInput = wrapper.find(".filter-source input");
    expect((sourceInput.element as HTMLInputElement).value).toBe("(objectClass=*)");
  });

  it("flags non-numeric size/page inputs instead of silently treating them as unlimited (P2-16)", async () => {
    const wrapper = await mountForm();
    const numeric = wrapper.findAll("input.numeric");
    await numeric[0].setValue("abc");
    await numeric[1].setValue("-5");
    const errors = wrapper.findAll(".form-error");
    expect(errors).toHaveLength(2);
    expect(errors[0].text()).toBe("请输入正整数（留空或 0 = 不限制）");
    // 0 与留空 = 不限制，不算非法。
    await numeric[0].setValue("0");
    await numeric[1].setValue("");
    expect(wrapper.findAll(".form-error")).toHaveLength(0);
  });
});

describe("SearchForm validation feedback (fresh review)", () => {
  it.each(["abc", "12px", "1.5", "1e3", "-1", "9007199254740992"])("blocks all search entry points for an invalid numeric value: %s", async (value) => {
    const wrapper = await mountForm();
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    const numeric = wrapper.findAll("input.numeric");
    for (const index of [0, 1]) {
      await numeric[0].setValue("500");
      await numeric[1].setValue("500");
      await numeric[index].setValue(value);
      expect(numeric[index].attributes("aria-invalid")).toBe("true");
      const description = numeric[index].attributes("aria-describedby");
      expect(wrapper.find('[id="' + description + '"]').text()).toContain("请输入正整数");
      expect(wrapper.find("button[type='submit']").attributes("disabled")).toBeDefined();
      await wrapper.find("form").trigger("submit");
      const actions = wrapper.vm as unknown as {
        runSubtreeAt(dn: string): void;
        runFilterAt(dn: string, filter: string): void;
      };
      actions.runSubtreeAt("dc=demo,dc=dbx");
      actions.runFilterAt("dc=demo,dc=dbx", "(cn=alice)");
      expect(wrapper.emitted("run")).toBeUndefined();
    }
    wrapper.unmount();
  });

  it("accepts blank, zero and whole numbers, and clears field feedback after correction", async () => {
    const wrapper = await mountForm();
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    const numeric = wrapper.findAll("input.numeric");
    await numeric[0].setValue("bad");
    for (const value of ["", "0", "0012", "500"]) {
      await numeric[0].setValue(value);
      await numeric[1].setValue(value);
      expect(numeric[0].attributes("aria-invalid")).toBe("false");
      expect(numeric[0].attributes("aria-describedby")).toBeUndefined();
      expect(wrapper.find("button[type='submit']").attributes("disabled")).toBeUndefined();
      await wrapper.find("form").trigger("submit");
    }
    expect(wrapper.emitted("run")).toHaveLength(4);
    wrapper.unmount();
  });

  it("links the syntax error to the source field and removes it immediately after correction", async () => {
    const wrapper = await mountForm();
    await wrapper.findAll(".mode-switch button")[1].trigger("click");
    const input = wrapper.find(".filter-source input");
    await input.setValue("(uid");
    expect(input.attributes("aria-label")).toContain("RFC 4515");
    expect(input.attributes("aria-invalid")).toBe("true");
    expect(wrapper.find('[id="' + input.attributes("aria-describedby") + '"]').text()).toBe("LDAP 过滤器不合法");
    await wrapper.findAll(".mode-switch button")[0].trigger("click");
    await input.setValue("(uid=alice)");
    expect(input.attributes("aria-invalid")).toBe("false");
    expect(input.attributes("aria-describedby")).toBeUndefined();
    expect(wrapper.find(".filter-source .form-error").exists()).toBe(false);
    wrapper.unmount();
  });
});
