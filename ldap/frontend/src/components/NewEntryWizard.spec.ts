// @vitest-environment happy-dom
// NewEntryWizard workbench UI tests（M6 N4）：模板选择 → must 表单铺开、
// objectClass 增删引起 must/may 重算（补加引导）、must 未填提交被拦、
// submit payload 的 dn/attributes、cancel 出口与 schema 缺失回退。
// 组件不直接调 api（提交只 emit），i18n 占位 key 未落 i18n.ts 时 t()
// 回退为 key 本身，断言统一走 t()。
import { afterEach, describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import NewEntryWizard from "./NewEntryWizard.vue";
import { t } from "../lib/i18n";
import type { LdapSchema } from "../lib/newEntryTemplates";

const PARENT = "ou=people,dc=demo,dc=dbx";

const SCHEMA: LdapSchema = {
  objectClassAttributes: {
    top: { must: ["objectClass"], may: [] },
    person: { must: ["cn", "sn"], may: ["telephoneNumber", "description"] },
    organizationalPerson: { must: [], may: ["l", "o", "ou", "title", "street"] },
    inetOrgPerson: { must: [], may: ["mail", "uid", "displayName"] },
    groupOfNames: { must: ["cn", "member"], may: ["description", "owner", "seeAlso"] },
    organizationalUnit: { must: ["ou"], may: ["description", "l"] },
    simpleSecurityObject: { must: ["userPassword"], may: [] },
  },
};

type WizardProps = { open?: boolean; parentDn?: string; schema?: LdapSchema };

function mountWizard(props: WizardProps = {}) {
  return mount(NewEntryWizard, { props: { open: true, ...props } });
}

type WizardWrapper = Awaited<ReturnType<typeof mountWizard>>;

const tracked: WizardWrapper[] = [];

function trackWizard(props: WizardProps = {}) {
  const wrapper = mountWizard(props);
  tracked.push(wrapper);
  return wrapper;
}

afterEach(() => {
  for (const wrapper of tracked.splice(0)) wrapper.unmount();
});

const footerButtons = (wrapper: WizardWrapper) => wrapper.findAll("footer button");
const buttonByText = (wrapper: WizardWrapper, text: string) =>
  footerButtons(wrapper).find((button) => button.text() === text)!;
const anyButtonByText = (wrapper: WizardWrapper, text: string) =>
  wrapper.findAll("button").find((button) => button.text() === text)!;
const nextButton = (wrapper: WizardWrapper) => buttonByText(wrapper, t("ldap.wizard.next"));
const prevButton = (wrapper: WizardWrapper) => buttonByText(wrapper, t("ldap.wizard.prev"));
const submitButton = (wrapper: WizardWrapper) => buttonByText(wrapper, t("ldap.wizard.submit"));
const activeStep = (wrapper: WizardWrapper) =>
  wrapper.findAll(".wizard-steps span").find((span) => span.classes().includes("is-active"))!;
const classChips = (wrapper: WizardWrapper) => wrapper.findAll(".class-chip");
const mustFields = (wrapper: WizardWrapper) => wrapper.findAll(".must-field");
const mustAttrNames = (wrapper: WizardWrapper) =>
  mustFields(wrapper).map((field) => field.attributes("data-attr"));
const mustFieldInput = (wrapper: WizardWrapper, attr: string) =>
  mustFields(wrapper).find((field) => field.attributes("data-attr") === attr)!.find("input");

// 步骤 ③ 的三个 field 输入：[0] 父 DN、[1] RDN 属性、[2] RDN 值。
const stepInputs = (wrapper: WizardWrapper) => wrapper.findAll(".wizard-step .field input");

async function chooseTemplate(wrapper: WizardWrapper, id: string) {
  await wrapper.find(`.template-card[data-template="${id}"]`).trigger("click");
}

async function gotoStep(wrapper: WizardWrapper, target: number) {
  while (Number(activeStep(wrapper).attributes("data-step")) < target) {
    await nextButton(wrapper).trigger("click");
  }
}

async function fillRdn(wrapper: WizardWrapper, attr: string, value: string) {
  await stepInputs(wrapper)[1]!.setValue(attr);
  await stepInputs(wrapper)[2]!.setValue(value);
}

describe("NewEntryWizard", () => {
  it("renders nothing while closed", () => {
    const wrapper = trackWizard({ open: false, parentDn: PARENT });
    expect(wrapper.find(".modal-backdrop").exists()).toBe(false);
  });

  it("starts on the template step with all five cards", () => {
    const wrapper = trackWizard({ parentDn: PARENT, schema: SCHEMA });
    expect(activeStep(wrapper).text()).toBe(t("ldap.wizard.stepTemplate"));
    const ids = wrapper.findAll(".template-card").map((card) => card.attributes("data-template"));
    expect(ids).toEqual(["user", "group", "ou", "simpleObject", "blank"]);
  });

  it("seeds the user template chain, then lays out the must form with the RDN-synced field", async () => {
    const wrapper = trackWizard({ parentDn: PARENT, schema: SCHEMA });
    await chooseTemplate(wrapper, "user");
    expect(activeStep(wrapper).text()).toBe(t("ldap.wizard.stepObjectClass"));
    expect(classChips(wrapper).map((chip) => chip.find(".mono").text())).toEqual([
      "top",
      "person",
      "organizationalPerson",
      "inetOrgPerson",
    ]);

    await nextButton(wrapper).trigger("click");
    expect(activeStep(wrapper).text()).toBe(t("ldap.wizard.stepDn"));
    const parentInput = wrapper.findAll(".wizard-step .field")[0]!.find("input");
    expect((parentInput.element as HTMLInputElement).value).toBe(PARENT);
    expect((stepInputs(wrapper)[1]!.element as HTMLInputElement).value).toBe("cn");
    await fillRdn(wrapper, "cn", "Alice");

    await nextButton(wrapper).trigger("click");
    expect(activeStep(wrapper).text()).toBe(t("ldap.wizard.stepAttributes"));
    expect(mustAttrNames(wrapper)).toEqual(["cn", "sn"]);
    // RDN 同名字段只读并带 RDN 值；sn 为可编辑必填。
    expect(mustFieldInput(wrapper, "cn").attributes("disabled")).toBeDefined();
    expect((mustFieldInput(wrapper, "cn").element as HTMLInputElement).value).toBe("Alice");
    expect(mustFieldInput(wrapper, "sn").attributes("disabled")).toBeUndefined();
    expect(wrapper.find(".form-error").text()).toContain("sn");
  });

  it("recomputes must attributes when objectClasses are added and removed (guidance)", async () => {
    const wrapper = trackWizard({ parentDn: PARENT, schema: SCHEMA });
    await chooseTemplate(wrapper, "user");
    // 补加 simpleSecurityObject → 引导带出 must userPassword。
    await wrapper.find(".class-adder input").setValue("simpleSecurityObject");
    await anyButtonByText(wrapper, t("ldap.wizard.addClass")).trigger("click");
    expect(classChips(wrapper)).toHaveLength(5);
    expect(wrapper.find(".class-hint").text()).toContain("userPassword");

    await gotoStep(wrapper, 4);
    expect(mustAttrNames(wrapper)).toEqual(["cn", "sn", "userPassword"]);
    expect(mustFieldInput(wrapper, "userPassword").exists()).toBe(true);

    // 回到 ② 移除 simpleSecurityObject → userPassword 退出 must，表单即时重算。
    // （移除 person 不会去掉 sn：organizationalPerson 经 SUP 继承 person 的
    // must，父类归并语义由 newEntryTemplates.spec 覆盖。）
    for (let index = 0; index < 2; index++) await prevButton(wrapper).trigger("click");
    expect(activeStep(wrapper).text()).toBe(t("ldap.wizard.stepObjectClass"));
    const removeSso = classChips(wrapper)
      .find((chip) => chip.find(".mono").text() === "simpleSecurityObject")!
      .find(".chip-remove");
    await removeSso.trigger("click");
    expect(classChips(wrapper)).toHaveLength(4);
    expect(wrapper.find(".class-hint").text()).not.toContain("userPassword");

    await gotoStep(wrapper, 4);
    expect(mustAttrNames(wrapper)).toEqual(["cn", "sn"]);
  });

  it("blocks submit while a must attribute is empty and lists the missing names", async () => {
    const wrapper = trackWizard({ parentDn: PARENT, schema: SCHEMA });
    await chooseTemplate(wrapper, "user");
    await gotoStep(wrapper, 3);
    await fillRdn(wrapper, "cn", "Alice");
    await gotoStep(wrapper, 4);
    expect(submitButton(wrapper).attributes("disabled")).toBeDefined();
    await submitButton(wrapper).trigger("click");
    expect(wrapper.emitted("submit")).toBeUndefined();
    expect(wrapper.find(".form-error").text()).toContain("sn");

    await mustFieldInput(wrapper, "sn").setValue("Doe");
    expect(wrapper.find(".form-error").exists()).toBe(false);
    expect(submitButton(wrapper).attributes("disabled")).toBeUndefined();
  });

  it("emits the submit payload with dn and attributes for the parent to persist", async () => {
    const wrapper = trackWizard({ parentDn: PARENT, schema: SCHEMA });
    await chooseTemplate(wrapper, "user");
    await gotoStep(wrapper, 3);
    await fillRdn(wrapper, "cn", "Alice");
    await gotoStep(wrapper, 4);
    await mustFieldInput(wrapper, "sn").setValue("Doe");
    await wrapper.find(".optional-toggle").trigger("click");
    const mailInput = wrapper.findAll(".may-field").find((field) => field.attributes("data-attr") === "mail")!;
    await mailInput.find("input").setValue("alice@demo");
    // 未填的 may 属性不进入 payload。
    await submitButton(wrapper).trigger("click");
    expect(wrapper.emitted("submit")).toHaveLength(1);
    expect(wrapper.emitted("submit")![0]![0]).toEqual({
      dn: `cn=Alice,${PARENT}`,
      attributes: {
        objectClass: ["top", "person", "organizationalPerson", "inetOrgPerson"],
        cn: ["Alice"],
        sn: ["Doe"],
        mail: ["alice@demo"],
      },
    });
  });

  it("guides the blank template through manual objectClass addition", async () => {
    const wrapper = trackWizard({ parentDn: PARENT, schema: SCHEMA });
    await chooseTemplate(wrapper, "blank");
    expect(classChips(wrapper)).toHaveLength(0);
    expect(wrapper.find(".class-chips .muted").text()).toBe(t("ldap.wizard.noObjectClass"));
    // 无 objectClass 时第 ④ 步给出必填提示并拦截提交。
    await gotoStep(wrapper, 4);
    expect(wrapper.find(".form-error").text()).toBe(t("ldap.wizard.objectClassRequired"));
    expect(submitButton(wrapper).attributes("disabled")).toBeDefined();

    for (let index = 0; index < 2; index++) await prevButton(wrapper).trigger("click");
    await wrapper.find(".class-adder input").setValue("organizationalUnit");
    await anyButtonByText(wrapper, t("ldap.wizard.addClass")).trigger("click");
    expect(classChips(wrapper)).toHaveLength(1);
    await nextButton(wrapper).trigger("click");
    await fillRdn(wrapper, "ou", "people");
    await nextButton(wrapper).trigger("click");
    expect(mustAttrNames(wrapper)).toEqual(["ou"]);
    // ou 即 RDN 属性：must 字段随 RDN 值同步。
    expect((mustFieldInput(wrapper, "ou").element as HTMLInputElement).value).toBe("people");
    await submitButton(wrapper).trigger("click");
    expect(wrapper.emitted("submit")![0]![0]).toEqual({
      dn: `ou=people,${PARENT}`,
      attributes: { objectClass: ["organizationalUnit"], ou: ["people"] },
    });
  });

  it("falls back to the built-in must table when no schema is provided", async () => {
    const wrapper = trackWizard({ parentDn: PARENT });
    await chooseTemplate(wrapper, "user");
    expect(wrapper.find(".wizard-step .hint").text()).toBe(t("ldap.wizard.schemaUnavailable"));
    expect(wrapper.find(".class-hint").text()).toContain("cn, sn");
    await gotoStep(wrapper, 4);
    expect(mustAttrNames(wrapper)).toEqual(["cn", "sn"]);
  });

  it("emits cancel from the footer button, the ✕ icon and Escape", async () => {
    const wrapper = trackWizard({ parentDn: PARENT });
    await buttonByText(wrapper, t("cancel")).trigger("click");
    await wrapper.find(".icon-button").trigger("click");
    expect(wrapper.emitted("cancel")).toHaveLength(2);
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await wrapper.vm.$nextTick();
    expect(wrapper.emitted("cancel")).toHaveLength(3);
  });
});
